defmodule SpiceDB.Examples.CallDeadlinesTest do
  use SpiceDB.ExampleCase, async: false

  @wedged_timeout 2_000
  @watchdog 17_000

  defp rel, do: Relationship.from_tuple!("document:readme#viewer@user:alice")

  test "accepts default_timeout: on the real construction path" do
    client = SpiceDB.new_plaintext!(endpoint(), token(), default_timeout: 5_000)
    on_exit(fn -> SpiceDB.close(client) end)

    SpiceDB.write_relationships!(client, Transaction.touch(Transaction.new(), rel()))

    assert SpiceDB.check_permission!(client, Consistency.full(), "view", rel())
           |> CheckResult.has_permission?()
  end

  test "lets a per-call timeout: override the client default", %{client: client} do
    SpiceDB.write_relationships!(client, Transaction.touch(Transaction.new(), rel()),
      timeout: 5_000
    )

    assert SpiceDB.check_permission!(client, Consistency.full(), "view", rel(), timeout: 5_000)
           |> CheckResult.has_permission?()
  end

  test "does not bound bulk import by the unary default", %{client: client} do
    rels =
      for i <- 1..50,
          do: Relationship.from_triple("document", "bulk", "viewer", "user", "user#{i}")

    assert SpiceDB.import_relationships!(client, rels) == 50

    more =
      for i <- 1..50,
          do: Relationship.from_triple("document", "bulk2", "viewer", "user", "user#{i}")

    assert SpiceDB.import_relationships!(client, more, timeout: 30_000) == 50
  end

  defp wedged_endpoint do
    {:ok, listener} = :gen_tcp.listen(0, ip: {127, 0, 0, 1}, backlog: 16)
    {:ok, port} = :inet.port(listener)
    on_exit(fn -> :gen_tcp.close(listener) end)
    "127.0.0.1:#{port}"
  end

  defp assert_deadline_fires(fun) do
    {elapsed_us, result} = :timer.tc(fn -> within(@watchdog, fun) end)
    assert {:error, %SpiceDB.DeadlineExceededError{}} = result
    elapsed = div(elapsed_us, 1_000)
    assert elapsed >= @wedged_timeout - 100
    assert elapsed < @wedged_timeout * 3
  end

  @tag :no_spicedb
  test "expires default_timeout: against a server that never answers" do
    {:ok, wedged} =
      SpiceDB.new_plaintext(wedged_endpoint(), token(), default_timeout: @wedged_timeout)

    on_exit(fn -> SpiceDB.close(wedged) end)

    assert_deadline_fires(fn ->
      SpiceDB.check_permission(wedged, Consistency.full(), "view", rel())
    end)
  end

  @tag :no_spicedb
  test "expires a per-call timeout: against a server that never answers" do
    {:ok, wedged} = SpiceDB.new_plaintext(wedged_endpoint(), token())
    on_exit(fn -> SpiceDB.close(wedged) end)

    assert_deadline_fires(fn ->
      SpiceDB.check_permission(wedged, Consistency.full(), "view", rel(),
        timeout: @wedged_timeout
      )
    end)
  end
end
