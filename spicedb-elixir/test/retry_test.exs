defmodule SpiceDB.RetryTest do
  # grpc-elixir's Mint adapter is unreliable under many concurrent short-lived
  # connections; serialize this file rather than risk flaky failures.
  use ExUnit.Case, async: false

  alias Authzed.Api.V1
  alias SpiceDB.{Consistency, Relationship, Transaction}
  alias SpiceDB.Test.{Forwarder, StandIn}

  defp failing(times, code, success) do
    counter = :counters.new(1, [])

    handler = fn _rpc, _req ->
      :counters.add(counter, 1, 1)

      if :counters.get(counter, 1) <= times,
        do: {:error, %GRPC.RPCError{status: code, message: "fail"}},
        else: {:ok, success}
    end

    {handler, fn -> :counters.get(counter, 1) end}
  end

  @schema %V1.ReadSchemaResponse{schema_text: "s", read_at: %V1.ZedToken{token: "r"}}

  defp schema_client(handler) do
    Forwarder.client!(V1.SchemaService.Service, %{ReadSchema: :unary}, handler)
  end

  test "a read retries UNAVAILABLE and ABORTED, up to 3 retries" do
    for code <- [14, 10] do
      {handler, attempts} = failing(3, code, @schema)
      assert {:ok, {"s", "r"}} = SpiceDB.read_schema(schema_client(handler))
      assert attempts.() == 4
    end
  end

  test "a read gives up after 4 attempts" do
    {handler, attempts} = failing(10, 14, @schema)

    assert {:error, %SpiceDB.UnavailableError{}} =
             SpiceDB.read_schema(schema_client(handler))

    assert attempts.() == 4
  end

  test "RESOURCE_EXHAUSTED and other codes are never retried" do
    for code <- [8, 3, 4, 13] do
      {handler, attempts} = failing(10, code, @schema)
      assert {:error, _} = SpiceDB.read_schema(schema_client(handler))
      assert attempts.() == 1
    end
  end

  test "mutations are never retried" do
    rel = Relationship.from_triple("document", "d", "viewer", "user", "a")

    mutations = [
      {V1.PermissionsService.Service, :WriteRelationships, :unary,
       &SpiceDB.write_relationships(&1, Transaction.touch(Transaction.new(), rel))},
      {V1.SchemaService.Service, :WriteSchema, :unary,
       &SpiceDB.write_schema(&1, "definition user {}")},
      {V1.PermissionsService.Service, :DeleteRelationships, :unary,
       &SpiceDB.delete_relationships(&1, SpiceDB.Filter.new("document"))},
      {V1.PermissionsService.Service, :ImportBulkRelationships, :client_stream,
       &SpiceDB.import_relationships(&1, [rel])}
    ]

    for {service, rpc, kind, call} <- mutations do
      {handler, attempts} = failing(10, 14, nil)
      client = Forwarder.client!(service, %{rpc => kind}, handler)
      assert {:error, %SpiceDB.UnavailableError{}} = call.(client)
      assert attempts.() == 1
    end
  end

  test "stream establishment is retried for reads, never for watch" do
    {handler, attempts} = failing(2, 14, nil)
    stream_handler = fn rpc, req -> with {:ok, _} <- handler.(rpc, req), do: {:stream, []} end

    client =
      Forwarder.client!(
        V1.PermissionsService.Service,
        %{ReadRelationships: :server_stream},
        stream_handler
      )

    assert {:ok, stream} =
             SpiceDB.read_relationships(
               client,
               Consistency.full(),
               SpiceDB.Filter.new("document")
             )

    assert Enum.to_list(stream) == []
    assert attempts.() == 3

    counter = :counters.new(1, [])

    watch_handler = fn :Watch, _ ->
      :counters.add(counter, 1, 1)
      {:stream, [{:error, %GRPC.RPCError{status: 14, message: "down"}}]}
    end

    watch_client =
      Forwarder.client!(V1.WatchService.Service, %{Watch: :server_stream}, watch_handler)

    {:ok, watch} = SpiceDB.watch(watch_client, [])
    assert_raise SpiceDB.UnavailableError, fn -> Enum.to_list(watch) end
    assert :counters.get(counter, 1) == 1
  end

  test "backoff is full jitter bounded by base * 2^(attempt - 1)" do
    client = %SpiceDB.Client{conn: nil, retry_base_ms: 100}

    for attempt <- 1..3, _ <- 1..200 do
      delay = SpiceDB.Retry.backoff(client, attempt)
      assert delay >= 0 and delay <= 100 * Integer.pow(2, attempt - 1)
    end
  end

  test "unary calls default to a 30s per-attempt timeout, overridable per call and per client" do
    client = schema_client(fn _, _ -> {:ok, @schema} end)

    SpiceDB.read_schema(client)
    assert_received {:rpc, :ReadSchema, _, deadline_ms}
    assert deadline_ms > 25_000

    SpiceDB.read_schema(client, timeout: 250)
    assert_received {:rpc, :ReadSchema, _, deadline_ms}
    assert deadline_ms > 50 and deadline_ms <= 250

    per_client = %{client | default_timeout: 5_000}
    SpiceDB.read_schema(per_client)
    assert_received {:rpc, :ReadSchema, _, deadline_ms}
    assert deadline_ms > 4_000 and deadline_ms <= 5_000
  end

  test "a transport failure that is not a status becomes UnavailableError" do
    assert {:error, %SpiceDB.UnavailableError{}} =
             SpiceDB.new_plaintext("127.0.0.1:#{StandIn.free_port()}", "t")
  end
end
