defmodule SpiceDB.RelationshipsTest do
  # grpc-elixir's Mint adapter is unreliable under many concurrent short-lived
  # connections; serialize this file rather than risk flaky failures.
  use ExUnit.Case, async: false

  alias Authzed.Api.V1
  alias SpiceDB.{Filter, Relationship, Transaction}
  alias SpiceDB.Test.Forwarder

  @rel Relationship.from_triple("document", "d", "viewer", "user", "alice")

  defp write_client(handler) do
    Forwarder.client!(V1.PermissionsService.Service, %{WriteRelationships: :unary}, handler)
  end

  defp delete_client(handler) do
    Forwarder.client!(V1.PermissionsService.Service, %{DeleteRelationships: :unary}, handler)
  end

  defp import_client(handler) do
    Forwarder.client!(
      V1.PermissionsService.Service,
      %{ImportBulkRelationships: :client_stream},
      handler
    )
  end

  test "write_relationships sends updates and preconditions and returns the revision" do
    client =
      write_client(fn :WriteRelationships, _ ->
        {:ok, %V1.WriteRelationshipsResponse{written_at: %V1.ZedToken{token: "w"}}}
      end)

    txn =
      Transaction.new()
      |> Transaction.create(@rel)
      |> Transaction.delete(@rel)
      |> Transaction.must_not_match(Filter.new("document") |> Filter.with_resource_id("x"))

    assert {:ok, "w"} = SpiceDB.write_relationships(client, txn)
    assert_received {:rpc, :WriteRelationships, req, _}
    assert Enum.map(req.updates, & &1.operation) == [:OPERATION_CREATE, :OPERATION_DELETE]
    assert [%V1.Precondition{operation: :OPERATION_MUST_NOT_MATCH}] = req.optional_preconditions
  end

  test "caveats and expiration go over the wire" do
    client = write_client(fn _, _ -> {:ok, %V1.WriteRelationshipsResponse{}} end)
    at = ~U[2030-01-01 00:00:00.000000Z]
    rel = @rel |> Relationship.with_caveat("c", %{"n" => 1}) |> Relationship.with_expiration(at)
    SpiceDB.write_relationships(client, Transaction.touch(Transaction.new(), rel))

    assert_received {:rpc, :WriteRelationships, %{updates: [update]}, _}
    assert update.relationship.optional_caveat.caveat_name == "c"
    assert update.relationship.optional_expires_at.seconds == DateTime.to_unix(at)
    assert Relationship.from_triple("a", "b", "c", "d", "e") |> Map.get(:caveat_name) == nil
  end

  test "delete_relationships pages until complete, re-sending preconditions" do
    counter = :counters.new(1, [])

    client =
      delete_client(fn :DeleteRelationships, _ ->
        :counters.add(counter, 1, 1)
        n = :counters.get(counter, 1)

        {:ok,
         %V1.DeleteRelationshipsResponse{
           deleted_at: %V1.ZedToken{token: "d#{n}"},
           deletion_progress:
             if(n < 3, do: :DELETION_PROGRESS_PARTIAL, else: :DELETION_PROGRESS_COMPLETE),
           after_result_cursor: if(n == 1, do: %V1.Cursor{token: "c1"})
         }}
      end)

    assert {:ok, "d3"} =
             SpiceDB.delete_relationships(client, Filter.new("document"),
               must_match: [Filter.new("document")],
               limit: 7
             )

    reqs =
      for _ <- 1..3 do
        assert_received {:rpc, :DeleteRelationships, req, _}
        req
      end

    assert Enum.all?(reqs, &(&1.optional_limit == 7 and &1.optional_allow_partial_deletions))

    assert Enum.all?(
             reqs,
             &match?(
               [%V1.Precondition{operation: :OPERATION_MUST_MATCH}],
               &1.optional_preconditions
             )
           )

    assert Enum.map(reqs, &(&1.optional_cursor && &1.optional_cursor.token)) == [nil, "c1", nil]
  end

  test "a filter with subject parts but no subject type is refused before sending" do
    client = delete_client(fn _, _ -> flunk("sent") end)
    filter = Filter.new("document") |> Filter.with_subject_id("alice")

    assert {:error, %SpiceDB.InvalidArgumentError{message: message}} =
             SpiceDB.delete_relationships(client, filter)

    assert message =~ "with_subject_type"
  end

  test "import_relationships consumes a lazy enumerable in batches of 1000" do
    client =
      import_client(fn :ImportBulkRelationships, reqs ->
        {:ok,
         %V1.ImportBulkRelationshipsResponse{
           num_loaded: Enum.sum(Enum.map(reqs, &length(&1.relationships)))
         }}
      end)

    rels =
      Stream.map(1..2500, &Relationship.from_triple("document", "#{&1}", "viewer", "user", "u"))

    assert {:ok, 2500} = SpiceDB.import_relationships(client, rels)
    assert_received {:client_stream, :ImportBulkRelationships, reqs, nil}
    assert Enum.map(reqs, &length(&1.relationships)) == [1000, 1000, 500]
  end

  test "from_tuple parses and rejects" do
    assert {:ok, %Relationship{subject_relation: "member"}} =
             Relationship.from_tuple("document:d#viewer@group:eng#member")

    assert to_string(Relationship.from_tuple!("document:d#viewer@user:a")) ==
             "document:d#viewer@user:a"

    for bad <- [
          "document:d#viewer",
          "document#viewer@user:a",
          "document:d#viewer@user",
          "document:#viewer@user:a"
        ] do
      assert {:error, %SpiceDB.InvalidArgumentError{}} = Relationship.from_tuple(bad)
    end
  end
end
