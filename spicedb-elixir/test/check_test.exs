defmodule SpiceDB.CheckTest do
  # grpc-elixir's Mint adapter is unreliable under many concurrent short-lived
  # connections; serialize this file rather than risk flaky failures.
  use ExUnit.Case, async: false

  alias Authzed.Api.V1
  alias SpiceDB.{CheckResult, Consistency, Relationship}
  alias SpiceDB.Test.Forwarder

  @rel Relationship.from_triple("document", "d", "viewer", "user", "alice")

  defp check_response(permissionship, missing \\ nil) do
    %V1.CheckPermissionResponse{
      permissionship: permissionship,
      checked_at: %V1.ZedToken{token: "rev1"},
      partial_caveat_info: missing && %V1.PartialCaveatInfo{missing_required_context: missing}
    }
  end

  defp client(handler) do
    Forwarder.client!(V1.PermissionsService.Service, %{CheckPermission: :unary}, handler)
  end

  defp bulk_client(handler) do
    Forwarder.client!(V1.PermissionsService.Service, %{CheckBulkPermissions: :unary}, handler)
  end

  describe "check_permission/5" do
    test "maps each permissionship, and only :has_permission is a grant" do
      for {wire, mapped, granted} <- [
            {:PERMISSIONSHIP_HAS_PERMISSION, :has_permission, true},
            {:PERMISSIONSHIP_NO_PERMISSION, :no_permission, false},
            {:PERMISSIONSHIP_CONDITIONAL_PERMISSION, :conditional_permission, false},
            {:PERMISSIONSHIP_UNSPECIFIED, :unspecified, false},
            {4242, :unspecified, false}
          ] do
        client = client(fn :CheckPermission, _ -> {:ok, check_response(wire)} end)
        {:ok, result} = SpiceDB.check_permission(client, Consistency.full(), "view", @rel)
        assert result.permissionship == mapped
        assert CheckResult.has_permission?(result) == granted
        assert result.checked_at == "rev1"
      end
    end

    test "reports missing caveat context" do
      client =
        client(fn _, _ ->
          {:ok, check_response(:PERMISSIONSHIP_CONDITIONAL_PERMISSION, ["now"])}
        end)

      assert {:ok, %CheckResult{missing_context: ["now"]}} =
               SpiceDB.check_permission(client, Consistency.full(), "view", @rel)
    end

    test "sends the relationship's resource and subject, and the permission" do
      client = client(fn _, _ -> {:ok, check_response(:PERMISSIONSHIP_NO_PERMISSION)} end)

      rel = Relationship.from_triple("document", "d", "viewer", "group", "eng", "member")
      SpiceDB.check_permission(client, Consistency.min_latency(), "edit", rel)

      assert_received {:rpc, :CheckPermission, req, _}
      assert req.permission == "edit"
      assert req.resource == %V1.ObjectReference{object_type: "document", object_id: "d"}
      assert req.subject.object == %V1.ObjectReference{object_type: "group", object_id: "eng"}
      assert req.subject.optional_relation == "member"
      assert req.context == nil
      assert req.consistency.requirement == {:minimize_latency, true}
    end

    test "merges call and relationship context, the relationship winning per key" do
      client = client(fn _, _ -> {:ok, check_response(:PERMISSIONSHIP_NO_PERMISSION)} end)

      rel = Relationship.with_check_context(@rel, %{"a" => 1, b: "item"})

      SpiceDB.check_permission(client, Consistency.full(), "view", rel,
        context: %{"b" => "call", c: true}
      )

      assert_received {:rpc, :CheckPermission, req, _}

      assert SpiceDB.CaveatContext.from_struct(req.context) == %{
               "a" => 1.0,
               "b" => "item",
               "c" => true
             }
    end

    test "rejects a non-Consistency value without sending" do
      client = client(fn _, _ -> flunk("sent") end)

      assert {:error, %SpiceDB.InvalidArgumentError{}} =
               SpiceDB.check_permission(client, :full, "view", @rel)
    end

    test "the bang variant raises the typed error" do
      client =
        client(fn _, _ ->
          {:error, %GRPC.RPCError{status: 5, message: "gone"}}
        end)

      assert_raise SpiceDB.NotFoundError, "gone", fn ->
        SpiceDB.check_permission!(client, Consistency.full(), "view", @rel)
      end
    end
  end

  describe "check_permissions/5" do
    defp bulk_handler(permissionship_for) do
      fn :CheckBulkPermissions, req ->
        pairs =
          Enum.map(req.items, fn item ->
            %V1.CheckBulkPermissionsPair{
              request: item,
              response:
                {:item,
                 %V1.CheckBulkPermissionsResponseItem{permissionship: permissionship_for.(item)}}
            }
          end)

        {:ok,
         %V1.CheckBulkPermissionsResponse{pairs: pairs, checked_at: %V1.ZedToken{token: "r"}}}
      end
    end

    test "batches by 1000 and keeps input order" do
      rels =
        for i <- 1..2500, do: Relationship.from_triple("document", "#{i}", "viewer", "user", "u")

      client =
        bulk_client(
          bulk_handler(fn item ->
            if rem(String.to_integer(item.resource.object_id), 2) == 0,
              do: :PERMISSIONSHIP_HAS_PERMISSION,
              else: :PERMISSIONSHIP_NO_PERMISSION
          end)
        )

      {:ok, results} = SpiceDB.check_permissions(client, Consistency.full(), "view", rels)

      sizes =
        for _ <- 1..3 do
          assert_received {:rpc, :CheckBulkPermissions, req, _}
          length(req.items)
        end

      refute_received {:rpc, _, _, _}
      assert sizes == [1000, 1000, 500]
      assert length(results) == 2500

      assert Enum.map(results, & &1.permissionship) ==
               Enum.map(1..2500, &if(rem(&1, 2) == 0, do: :has_permission, else: :no_permission))

      assert Enum.all?(results, &(&1.checked_at == "r"))
    end

    test "a per-item error fails the call with that item's typed error" do
      client =
        bulk_client(fn _, _ ->
          {:ok,
           %V1.CheckBulkPermissionsResponse{
             pairs: [
               %V1.CheckBulkPermissionsPair{
                 response: {:error, %Google.Rpc.Status{code: 9, message: "bad"}}
               }
             ]
           }}
        end)

      assert {:error, %SpiceDB.FailedPreconditionError{code: 9, message: "bad"}} =
               SpiceDB.check_permissions(client, Consistency.full(), "view", [@rel])
    end

    test "a pair count that does not match the request is an error" do
      client =
        bulk_client(fn _, _ -> {:ok, %V1.CheckBulkPermissionsResponse{pairs: []}} end)

      assert {:error, %SpiceDB.Error{message: message}} =
               SpiceDB.check_permissions(client, Consistency.full(), "view", [@rel])

      assert message =~ "0 result(s) for 1 item(s)"
    end

    test "a pair with neither item nor error is an error" do
      client =
        bulk_client(fn _, _ ->
          {:ok, %V1.CheckBulkPermissionsResponse{pairs: [%V1.CheckBulkPermissionsPair{}]}}
        end)

      assert {:error, %SpiceDB.Error{}} =
               SpiceDB.check_permissions(client, Consistency.full(), "view", [@rel])
    end

    test "an empty list sends nothing" do
      client = bulk_client(fn _, _ -> flunk("sent") end)
      assert {:ok, []} = SpiceDB.check_permissions(client, Consistency.full(), "view", [])
    end
  end

  describe "check_any/5 and check_all/5" do
    test "an empty list is false for both, and sends nothing" do
      client = bulk_client(fn _, _ -> flunk("sent") end)
      assert {:ok, false} = SpiceDB.check_any(client, Consistency.full(), "view", [])
      assert {:ok, false} = SpiceDB.check_all(client, Consistency.full(), "view", [])
    end

    test "a conditional result counts as no grant" do
      client = bulk_client(bulk_handler(fn _ -> :PERMISSIONSHIP_CONDITIONAL_PERMISSION end))

      assert {:ok, false} = SpiceDB.check_any(client, Consistency.full(), "view", [@rel])
      assert {:ok, false} = SpiceDB.check_all(client, Consistency.full(), "view", [@rel])
    end

    test "any and all over mixed results" do
      rels = [@rel, Relationship.from_triple("document", "e", "viewer", "user", "bob")]

      client =
        bulk_client(
          bulk_handler(fn item ->
            if item.resource.object_id == "d",
              do: :PERMISSIONSHIP_HAS_PERMISSION,
              else: :PERMISSIONSHIP_NO_PERMISSION
          end)
        )

      assert SpiceDB.check_any!(client, Consistency.full(), "view", rels)
      refute SpiceDB.check_all!(client, Consistency.full(), "view", rels)
    end
  end
end
