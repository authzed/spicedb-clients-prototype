defmodule SpiceDB.Examples.ErrorMappingTest do
  use SpiceDB.ExampleCase, async: false

  alias Authzed.Api.V1
  alias Google.Protobuf.Any
  alias Google.Rpc.ErrorInfo
  alias SpiceDB.Examples.StandIn

  @stale_token "stale-zedtoken"

  defmodule StandInService do
    use GRPC.Server, service: Authzed.Api.V1.PermissionsService.Service

    def check_permission(
          %{consistency: %{requirement: {:at_least_as_fresh, %{token: "stale-zedtoken"}}}},
          _stream
        ) do
      raise GRPC.RPCError.exception(
              status: :out_of_range,
              message: "the specified revision has expired or been garbage collected",
              details: [
                %Any{
                  type_url: "type.googleapis.com/google.rpc.ErrorInfo",
                  value:
                    ErrorInfo.encode(%ErrorInfo{
                      reason: "ERROR_REASON_INVALID_REVISION",
                      domain: "authzed.com",
                      metadata: %{"revision" => "stale-zedtoken"}
                    })
                }
              ]
            )
    end

    def check_permission(%{permission: "rotated"}, _stream) do
      raise GRPC.RPCError.exception(status: :unauthenticated, message: "invalid token")
    end

    def check_permission(%{permission: "internal"}, _stream) do
      raise GRPC.RPCError.exception(status: :internal, message: "boom")
    end

    def check_permission(_request, _stream) do
      %V1.CheckPermissionResponse{
        permissionship: :PERMISSIONSHIP_HAS_PERMISSION,
        checked_at: %V1.ZedToken{token: "fresh"}
      }
    end
  end

  defp stand_in_client do
    port = StandIn.start!([StandInService])
    client = SpiceDB.new_plaintext!("127.0.0.1:#{port}", "some-token")
    on_exit(fn -> SpiceDB.close(client) end)
    client
  end

  defp rel, do: Relationship.from_tuple!("document:readme#viewer@user:alice")

  @tag :no_spicedb
  test "recovers from a stale ZedToken by matching the kind, not the message" do
    client = stand_in_client()

    assert {:error, %SpiceDB.OutOfRangeError{} = error} =
             SpiceDB.check_permission(client, Consistency.at_least(@stale_token), "view", rel())

    assert error.code == 11
    assert error.reason == "ERROR_REASON_INVALID_REVISION"
    assert error.reason_domain == "authzed.com"
    assert error.reason_metadata == %{"revision" => @stale_token}

    assert_raise SpiceDB.OutOfRangeError, fn ->
      SpiceDB.check_permission!(client, Consistency.at_least(@stale_token), "view", rel())
    end

    assert {:ok, result} = SpiceDB.check_permission(client, Consistency.full(), "view", rel())
    assert CheckResult.has_permission?(result)
  end

  @tag :no_spicedb
  test "reports a rotated token as UNAUTHENTICATED, distinct from a transport fault" do
    client = stand_in_client()

    assert {:error, error} =
             SpiceDB.check_permission(client, Consistency.full(), "rotated", rel())

    assert %SpiceDB.UnauthenticatedError{code: 16, message: "invalid token"} = error
    refute is_struct(error, SpiceDB.UnavailableError)
  end

  @tag :no_spicedb
  test "keeps the code of a status with no dedicated kind" do
    client = stand_in_client()

    assert {:error, %SpiceDB.Error{code: 13, message: "boom"}} =
             SpiceDB.check_permission(client, Consistency.full(), "internal", rel())
  end

  test "maps a schema parse error to InvalidArgumentError with its ErrorInfo reason",
       %{client: client} do
    assert {:error, %SpiceDB.InvalidArgumentError{} = error} =
             SpiceDB.write_schema(client, "definition user { relation viewer: user }")

    assert error.code == 3
    assert error.reason == "ERROR_REASON_SCHEMA_PARSE_ERROR"
    assert error.reason_domain == "authzed.com"
  end

  test "maps creating an existing relationship to AlreadyExistsError", %{client: client} do
    txn = Transaction.create(Transaction.new(), rel())
    SpiceDB.write_relationships!(client, txn)

    assert {:error, %SpiceDB.AlreadyExistsError{code: 6}} =
             SpiceDB.write_relationships(client, txn)

    assert_raise SpiceDB.AlreadyExistsError, fn -> SpiceDB.write_relationships!(client, txn) end
  end

  test "maps a failed precondition to FailedPreconditionError", %{client: client} do
    txn =
      Transaction.new()
      |> Transaction.touch(rel())
      |> Transaction.must_match(Filter.new("document") |> Filter.with_resource_id("absent"))

    assert {:error, %SpiceDB.FailedPreconditionError{code: 9}} =
             SpiceDB.write_relationships(client, txn)
  end

  test "gets PERMISSION_DENIED, not UNAUTHENTICATED, from a real SpiceDB with a bad key" do
    bad = SpiceDB.new_plaintext!(endpoint(), "definitely-the-wrong-key")
    on_exit(fn -> SpiceDB.close(bad) end)

    assert {:error, %SpiceDB.PermissionDeniedError{code: 7}} = SpiceDB.read_schema(bad)
  end
end
