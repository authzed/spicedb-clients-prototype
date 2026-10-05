defmodule SpiceDB.Examples.DeleteRelationshipsTest do
  use SpiceDB.ExampleCase, async: false

  @viewers Filter.new("document")
           |> Filter.with_resource_id("firstdoc")
           |> Filter.with_relation("viewer")
  @owners Filter.new("document")
          |> Filter.with_resource_id("firstdoc")
          |> Filter.with_relation("owner")

  setup %{client: client} do
    txn =
      [
        "document:firstdoc#owner@user:alice",
        "document:firstdoc#viewer@user:bob",
        "document:firstdoc#viewer@user:carol"
      ]
      |> Enum.reduce(Transaction.new(), &Transaction.touch(&2, Relationship.from_tuple!(&1)))

    SpiceDB.write_relationships!(client, txn)
    :ok
  end

  defp count_at(client, consistency, filter) do
    {:ok, stream} = SpiceDB.read_relationships(client, consistency, filter)
    within(10_000, fn -> Enum.count(stream) end)
  end

  test "deletes only what the filter matches and returns the revision", %{client: client} do
    {:ok, revision} = SpiceDB.delete_relationships(client, @viewers)
    assert revision != ""

    assert count_at(client, Consistency.at_least(revision), @viewers) == 0
    assert count_at(client, Consistency.at_least(revision), @owners) == 1
  end

  test "deletes when every must_match precondition holds", %{client: client} do
    revision = SpiceDB.delete_relationships!(client, @viewers, must_match: [@owners])

    assert count_at(client, Consistency.at_least(revision), @viewers) == 0
    assert count_at(client, Consistency.at_least(revision), @owners) == 1
  end

  test "an unsatisfied must_match rejects the whole delete", %{client: client} do
    never =
      @viewers
      |> Filter.with_subject_type("user")
      |> Filter.with_subject_id("nonexistent-subject")

    assert {:error, %SpiceDB.FailedPreconditionError{}} =
             SpiceDB.delete_relationships(client, @owners, must_match: [never])

    assert count_at(client, Consistency.full(), @owners) == 1
  end

  test "a matching must_not_match rejects the delete, a non-matching one allows it", %{
    client: client
  } do
    bob = @viewers |> Filter.with_subject_type("user") |> Filter.with_subject_id("bob")
    mallory = @viewers |> Filter.with_subject_type("user") |> Filter.with_subject_id("mallory")

    assert {:error, %SpiceDB.FailedPreconditionError{}} =
             SpiceDB.delete_relationships(client, @owners, must_not_match: [bob])

    assert count_at(client, Consistency.full(), @owners) == 1

    revision = SpiceDB.delete_relationships!(client, @owners, must_not_match: [mallory])
    assert count_at(client, Consistency.at_least(revision), @owners) == 0
  end

  test "limit: pages the delete until nothing matches", %{client: client} do
    txn =
      ["document:firstdoc#owner@user:dave", "document:firstdoc#owner@user:erin"]
      |> Enum.reduce(Transaction.new(), &Transaction.touch(&2, Relationship.from_tuple!(&1)))

    SpiceDB.write_relationships!(client, txn)
    assert count_at(client, Consistency.full(), @owners) == 3

    revision = SpiceDB.delete_relationships!(client, @owners, limit: 1)

    assert count_at(client, Consistency.at_least(revision), @owners) == 0
    assert count_at(client, Consistency.at_least(revision), @viewers) == 2
  end
end
