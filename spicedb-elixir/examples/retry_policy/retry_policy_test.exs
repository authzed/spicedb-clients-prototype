defmodule SpiceDB.Examples.RetryPolicyTest do
  use SpiceDB.ExampleCase, async: false

  alias Authzed.Api.V1
  alias SpiceDB.Examples.StandIn

  @moduletag :no_spicedb

  defmodule CountingService do
    use GRPC.Server, service: Authzed.Api.V1.PermissionsService.Service

    def configure(failures, status) do
      :persistent_term.put(__MODULE__, %{
        attempts: :counters.new(2, []),
        failures: failures,
        status: status
      })
    end

    def attempts(rpc) do
      %{attempts: attempts} = :persistent_term.get(__MODULE__)
      :counters.get(attempts, index(rpc))
    end

    def check_permission(_request, _stream) do
      %{attempts: attempts, failures: failures, status: status} = :persistent_term.get(__MODULE__)
      :counters.add(attempts, index(:check), 1)

      if :counters.get(attempts, index(:check)) <= failures do
        raise GRPC.RPCError.exception(status: status, message: "transient, from the stand-in")
      end

      %V1.CheckPermissionResponse{
        permissionship: :PERMISSIONSHIP_HAS_PERMISSION,
        checked_at: %V1.ZedToken{token: "rev"}
      }
    end

    def write_relationships(_request, _stream) do
      %{attempts: attempts} = :persistent_term.get(__MODULE__)
      :counters.add(attempts, index(:write), 1)
      raise GRPC.RPCError.exception(status: :unavailable, message: "transient, from the stand-in")
    end

    defp index(:check), do: 1
    defp index(:write), do: 2
  end

  defp stand_in_client(failures, status) do
    CountingService.configure(failures, status)
    port = StandIn.start!([CountingService])
    client = SpiceDB.new_plaintext!("127.0.0.1:#{port}", "some-token")
    on_exit(fn -> SpiceDB.close(client) end)
    client
  end

  defp rel, do: Relationship.from_tuple!("document:readme#viewer@user:alice")

  test "retries a read through UNAVAILABLE transparently" do
    client = stand_in_client(3, :unavailable)

    assert {:ok, result} = SpiceDB.check_permission(client, Consistency.full(), "view", rel())
    assert CheckResult.has_permission?(result)
    assert CountingService.attempts(:check) == 4
  end

  test "retries a read through ABORTED" do
    client = stand_in_client(2, :aborted)

    assert {:ok, _result} = SpiceDB.check_permission(client, Consistency.full(), "view", rel())
    assert CountingService.attempts(:check) == 3
  end

  test "gives up after 3 retries, 4 attempts in all" do
    client = stand_in_client(4, :unavailable)

    assert {:error, %SpiceDB.UnavailableError{code: 14}} =
             SpiceDB.check_permission(client, Consistency.full(), "view", rel())

    assert CountingService.attempts(:check) == 4
  end

  test "does not retry a mutation" do
    client = stand_in_client(0, :unavailable)
    txn = Transaction.touch(Transaction.new(), rel())

    assert {:error, %SpiceDB.UnavailableError{}} = SpiceDB.write_relationships(client, txn)
    assert CountingService.attempts(:write) == 1
  end

  test "does not retry RESOURCE_EXHAUSTED, even on a read" do
    client = stand_in_client(1, :resource_exhausted)

    assert {:error, %SpiceDB.ResourceExhaustedError{code: 8}} =
             SpiceDB.check_permission(client, Consistency.full(), "view", rel())

    assert CountingService.attempts(:check) == 1
  end
end
