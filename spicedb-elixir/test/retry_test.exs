defmodule SpiceDB.RetryTest do
  use ExUnit.Case, async: true

  alias Authzed.Api.V1
  alias SpiceDB.{Consistency, Relationship, Transaction}
  alias SpiceDB.Test.FakeTransport

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

  test "a read retries UNAVAILABLE and ABORTED, up to 3 retries" do
    for code <- [14, 10] do
      {handler, attempts} = failing(3, code, @schema)
      assert {:ok, {"s", "r"}} = SpiceDB.read_schema(FakeTransport.client(handler))
      assert attempts.() == 4
    end
  end

  test "a read gives up after 4 attempts" do
    {handler, attempts} = failing(10, 14, @schema)

    assert {:error, %SpiceDB.UnavailableError{}} =
             SpiceDB.read_schema(FakeTransport.client(handler))

    assert attempts.() == 4
  end

  test "RESOURCE_EXHAUSTED and other codes are never retried" do
    for code <- [8, 3, 4, 13] do
      {handler, attempts} = failing(10, code, @schema)
      assert {:error, _} = SpiceDB.read_schema(FakeTransport.client(handler))
      assert attempts.() == 1
    end
  end

  test "mutations are never retried" do
    rel = Relationship.from_triple("document", "d", "viewer", "user", "a")

    calls = [
      &SpiceDB.write_relationships(&1, Transaction.touch(Transaction.new(), rel)),
      &SpiceDB.write_schema(&1, "definition user {}"),
      &SpiceDB.delete_relationships(&1, SpiceDB.Filter.new("document")),
      &SpiceDB.import_relationships(&1, [rel])
    ]

    for call <- calls do
      {handler, attempts} = failing(10, 14, nil)
      assert {:error, %SpiceDB.UnavailableError{}} = call.(FakeTransport.client(handler))
      assert attempts.() == 1
    end
  end

  test "stream establishment is retried for reads, never for watch" do
    {handler, attempts} = failing(2, 14, nil)
    stream_handler = fn rpc, req -> with {:ok, _} <- handler.(rpc, req), do: {:stream, []} end
    client = FakeTransport.client(stream_handler)

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

    {:ok, watch} = SpiceDB.watch(FakeTransport.client(watch_handler), [])
    assert_raise SpiceDB.UnavailableError, fn -> Enum.to_list(watch) end
    assert :counters.get(counter, 1) == 1
  end

  test "backoff is full jitter bounded by base * 2^(attempt - 1)" do
    client = %SpiceDB.Client{transport: nil, conn: nil, retry_base_ms: 100}

    for attempt <- 1..3, _ <- 1..200 do
      delay = SpiceDB.Retry.backoff(client, attempt)
      assert delay >= 0 and delay <= 100 * Integer.pow(2, attempt - 1)
    end
  end

  test "unary calls default to a 30s per-attempt timeout, overridable per call and per client" do
    client = FakeTransport.client(fn _, _ -> {:ok, @schema} end)
    SpiceDB.read_schema(client)
    assert_received {:rpc, :ReadSchema, _, [timeout: 30_000]}
    SpiceDB.read_schema(client, timeout: 250)
    assert_received {:rpc, :ReadSchema, _, [timeout: 250]}

    client = FakeTransport.client(fn _, _ -> {:ok, @schema} end, default_timeout: 5)
    SpiceDB.read_schema(client)
    assert_received {:rpc, :ReadSchema, _, [timeout: 5]}
  end

  test "a transport failure that is not a status becomes UnavailableError" do
    client = FakeTransport.client(fn _, _ -> {:error, :closed} end)

    assert {:error, %SpiceDB.UnavailableError{message: "transport failure: :closed"}} =
             SpiceDB.read_schema(client)
  end
end
