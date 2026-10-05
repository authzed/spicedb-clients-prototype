defmodule SpiceDB.Examples.RelationshipCountersTest do
  use SpiceDB.ExampleCase, async: false

  @settle_timeout_ms 30_000
  @poll_interval_ms 100

  setup %{client: client} do
    txn =
      Enum.reduce(
        [
          "document:firstdoc#viewer@user:alice",
          "document:seconddoc#viewer@user:bob",
          "document:firstdoc#editor@user:carol"
        ],
        Transaction.new(),
        &Transaction.touch(&2, Relationship.from_tuple!(&1))
      )

    SpiceDB.write_relationships!(client, txn)
    name = "document_viewers_#{System.unique_integer([:positive])}"

    :ok =
      SpiceDB.experimental_register_relationship_counter!(
        client,
        name,
        Filter.new("document") |> Filter.with_relation("viewer")
      )

    {:ok, name: name}
  end

  test "registers, reads, and unregisters a relationship counter", %{client: client, name: name} do
    result = settled_count(client, name)

    assert %SpiceDB.CountResult{still_calculating: false, relationship_count: 2} = result
    assert result.revision != ""

    assert :ok = SpiceDB.experimental_unregister_relationship_counter(client, name)

    assert {:error,
            %SpiceDB.FailedPreconditionError{reason: "ERROR_REASON_COUNTER_NOT_REGISTERED"}} =
             SpiceDB.experimental_count_relationships(client, name)
  end

  test "refuses to register the same counter name twice", %{client: client, name: name} do
    assert {:error, %SpiceDB.FailedPreconditionError{}} =
             SpiceDB.experimental_register_relationship_counter(
               client,
               name,
               Filter.new("document")
             )

    SpiceDB.experimental_unregister_relationship_counter!(client, name)
  end

  defp settled_count(client, name) do
    deadline = System.monotonic_time(:millisecond) + @settle_timeout_ms
    poll(client, name, deadline)
  end

  defp poll(client, name, deadline) do
    case SpiceDB.experimental_count_relationships!(client, name) do
      %SpiceDB.CountResult{still_calculating: true} ->
        if System.monotonic_time(:millisecond) > deadline,
          do: flunk("counter #{name} never settled within #{@settle_timeout_ms}ms")

        Process.sleep(@poll_interval_ms)
        poll(client, name, deadline)

      result ->
        result
    end
  end
end
