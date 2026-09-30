defmodule SpiceDB.Examples.WriteRelationshipsTest do
  use SpiceDB.ExampleCase, async: false

  defp subjects_at(client, revision, filter) do
    {:ok, stream} = SpiceDB.read_relationships(client, Consistency.at_least(revision), filter)

    within(10_000, fn ->
      stream |> Enum.map(&"#{&1.resource_relation}@#{&1.subject_id}") |> Enum.sort()
    end)
  end

  test "touch writes relationships and returns the revision they are visible at", %{
    client: client
  } do
    txn =
      Transaction.new()
      |> Transaction.touch(Relationship.from_tuple!("document:firstdoc#viewer@user:alice"))
      |> Transaction.touch(Relationship.from_tuple!("document:firstdoc#editor@user:bob"))

    {:ok, revision} = SpiceDB.write_relationships(client, txn)
    assert is_binary(revision) and revision != ""

    assert subjects_at(
             client,
             revision,
             Filter.new("document") |> Filter.with_resource_id("firstdoc")
           ) ==
             ["editor@bob", "viewer@alice"]
  end

  test "touch is idempotent while create refuses an existing relationship", %{client: client} do
    rel = Relationship.from_tuple!("document:newdoc#owner@user:charlie")

    SpiceDB.write_relationships!(client, Transaction.create(Transaction.new(), rel))
    SpiceDB.write_relationships!(client, Transaction.touch(Transaction.new(), rel))

    assert {:error, %SpiceDB.AlreadyExistsError{}} =
             SpiceDB.write_relationships(client, Transaction.create(Transaction.new(), rel))
  end

  test "writes when a must_not_match precondition holds", %{client: client} do
    guard =
      Filter.new("document")
      |> Filter.with_resource_id("firstdoc")
      |> Filter.with_relation("owner")
      |> Filter.with_subject_type("user")
      |> Filter.with_subject_id("mallory")

    txn =
      Transaction.new()
      |> Transaction.touch(Relationship.from_tuple!("document:firstdoc#viewer@user:alice"))
      |> Transaction.must_not_match(guard)

    revision = SpiceDB.write_relationships!(client, txn)
    assert subjects_at(client, revision, Filter.new("document")) == ["viewer@alice"]
  end

  test "a failed precondition writes nothing and carries its reason and metadata", %{
    client: client
  } do
    unsatisfiable =
      Filter.new("document")
      |> Filter.with_resource_id("firstdoc")
      |> Filter.with_relation("owner")
      |> Filter.with_subject_type("user")
      |> Filter.with_subject_id("nobody")

    txn =
      Transaction.new()
      |> Transaction.touch(Relationship.from_tuple!("document:seconddoc#viewer@user:alice"))
      |> Transaction.must_match(unsatisfiable)

    assert {:error, %SpiceDB.FailedPreconditionError{} = error} =
             SpiceDB.write_relationships(client, txn)

    assert error.reason == "ERROR_REASON_WRITE_OR_DELETE_PRECONDITION_FAILURE"
    assert error.reason_domain == "authzed.com"
    assert error.reason_metadata["precondition_resource_id"] == "firstdoc"

    {:ok, stream} =
      SpiceDB.read_relationships(
        client,
        Consistency.full(),
        Filter.new("document") |> Filter.with_resource_id("seconddoc")
      )

    assert within(10_000, fn -> Enum.to_list(stream) end) == []
  end

  test "delete removes a relationship", %{client: client} do
    rel = Relationship.from_tuple!("document:firstdoc#viewer@user:alice")
    SpiceDB.write_relationships!(client, Transaction.touch(Transaction.new(), rel))

    revision = SpiceDB.write_relationships!(client, Transaction.delete(Transaction.new(), rel))

    assert subjects_at(client, revision, Filter.new("document")) == []
  end

  test "caveats and expirations round-trip through a read", %{client: client} do
    SpiceDB.write_schema!(client, """
    use expiration

    #{test_schema()}
    caveat active(now int, limit int) { now < limit }
    definition folder {
    \trelation viewer: user with active | user with expiration
    }
    """)

    expires = DateTime.add(DateTime.utc_now(), 3600, :second) |> DateTime.truncate(:second)

    caveated =
      Relationship.from_tuple!("folder:f#viewer@user:alice")
      |> Relationship.with_caveat("active", %{"limit" => 10})

    expiring =
      Relationship.from_tuple!("folder:f#viewer@user:bob")
      |> Relationship.with_expiration(expires)

    revision =
      SpiceDB.write_relationships!(
        client,
        Transaction.new() |> Transaction.touch(caveated) |> Transaction.touch(expiring)
      )

    {:ok, stream} =
      SpiceDB.read_relationships(client, Consistency.at_least(revision), Filter.new("folder"))

    by_subject = within(10_000, fn -> Map.new(stream, &{&1.subject_id, &1}) end)

    assert by_subject["alice"].caveat_name == "active"
    assert by_subject["alice"].caveat_context == %{"limit" => 10.0}
    assert DateTime.compare(by_subject["bob"].expiration, expires) == :eq
    assert by_subject["bob"].caveat_name in [nil, ""]
  end
end
