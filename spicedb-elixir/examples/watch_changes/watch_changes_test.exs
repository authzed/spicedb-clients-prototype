defmodule SpiceDB.Examples.WatchChangesTest do
  use SpiceDB.ExampleCase, async: false

  @watch_timeout 30_000

  setup %{client: client} do
    first =
      Transaction.touch(
        Transaction.new(),
        Relationship.from_tuple!("document:firstdoc#viewer@user:alice")
      )

    {:ok, revision: SpiceDB.write_relationships!(client, first)}
  end

  test "receives the relationship update it wrote, and stops at it",
       %{client: client, revision: revision} do
    txn =
      Transaction.touch(
        Transaction.new(),
        Relationship.from_tuple!("document:seconddoc#editor@user:bob")
      )

    SpiceDB.write_relationships!(client, txn)

    {:ok, events} = SpiceDB.watch(client, ["document"], start_revision: revision)

    event =
      within(@watch_timeout, fn ->
        Enum.find(events, fn %SpiceDB.WatchEvent{updates: updates} -> updates != [] end)
      end)

    assert event.changes_through != ""
    assert [%SpiceDB.Update{operation: operation, relationship: rel}] = event.updates
    assert operation in [:create, :touch]
    assert to_string(rel) == "document:seconddoc#editor@user:bob"
  end

  test "resumes from an event's changes_through without replaying it",
       %{client: client, revision: revision} do
    for tuple <- ["document:a#viewer@user:x", "document:b#viewer@user:y"] do
      SpiceDB.write_relationships!(
        client,
        Transaction.touch(Transaction.new(), Relationship.from_tuple!(tuple))
      )
    end

    {:ok, events} = SpiceDB.watch(client, ["document"], start_revision: revision)
    first = within(@watch_timeout, fn -> Enum.find(events, &(&1.updates != [])) end)
    assert [%{relationship: %{resource_id: "a"}}] = first.updates

    {:ok, resumed} = SpiceDB.watch(client, ["document"], start_revision: first.changes_through)
    second = within(@watch_timeout, fn -> Enum.find(resumed, &(&1.updates != [])) end)
    assert [%{relationship: %{resource_id: "b"}}] = second.updates
  end

  test "receives a checkpoint event distinguishable from an update event",
       %{client: client, revision: revision} do
    txn =
      Transaction.touch(
        Transaction.new(),
        Relationship.from_tuple!("document:thirddoc#viewer@user:carol")
      )

    SpiceDB.write_relationships!(client, txn)

    {:ok, events} =
      SpiceDB.watch(client, ["document"], start_revision: revision, include_checkpoints: true)

    seen =
      within(@watch_timeout, fn ->
        Enum.reduce_while(events, [], fn event, seen ->
          seen = [event | seen]

          if Enum.any?(seen, & &1.is_checkpoint) and Enum.any?(seen, &(&1.updates != [])),
            do: {:halt, seen},
            else: {:cont, seen}
        end)
      end)

    checkpoints = Enum.filter(seen, & &1.is_checkpoint)
    assert checkpoints != []
    assert Enum.all?(checkpoints, &(&1.updates == []))
    assert Enum.any?(seen, &(&1.updates != [] and not &1.is_checkpoint))
  end

  test "surfaces a bad start revision as a typed error on first enumeration", %{client: client} do
    assert {:ok, events} = SpiceDB.watch(client, ["document"], start_revision: "not-a-token")

    raised =
      assert_raise SpiceDB.InvalidArgumentError, fn ->
        within(@watch_timeout, fn -> Enum.take(events, 1) end)
      end

    assert raised.code == 3
    assert raised.message =~ "start revision"
  end
end
