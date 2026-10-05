defmodule SpiceDB.Examples.ReadRelationshipsTest do
  use SpiceDB.ExampleCase, async: false

  defp read(client, filter, consistency \\ Consistency.full()) do
    {:ok, stream} = SpiceDB.read_relationships(client, consistency, filter)
    within(15_000, fn -> Enum.to_list(stream) end)
  end

  defp write_viewers(client, doc, ids) do
    ids
    |> Enum.reduce(
      Transaction.new(),
      &Transaction.touch(&2, Relationship.from_triple("document", doc, "viewer", "user", &1))
    )
    |> then(&SpiceDB.write_relationships!(client, &1))
  end

  test "reads the relationships matching a filter", %{client: client} do
    write_viewers(client, "firstdoc", ["alice", "bob"])

    SpiceDB.write_relationships!(
      client,
      Transaction.touch(
        Transaction.new(),
        Relationship.from_tuple!("document:firstdoc#editor@user:carol")
      )
    )

    rels =
      read(
        client,
        Filter.new("document")
        |> Filter.with_resource_id("firstdoc")
        |> Filter.with_relation("viewer")
      )

    assert Enum.all?(
             rels,
             &match?(%Relationship{resource_type: "document", resource_id: "firstdoc"}, &1)
           )

    assert rels |> Enum.map(& &1.subject_id) |> Enum.sort() == ["alice", "bob"]
  end

  test "an unmatched filter yields an empty stream", %{client: client} do
    write_viewers(client, "firstdoc", ["alice"])
    assert read(client, Filter.new("document") |> Filter.with_resource_id("nonexistent")) == []
  end

  test "filters by subject type and id", %{client: client} do
    write_viewers(client, "firstdoc", ["alice", "bob"])

    filter =
      Filter.new("document")
      |> Filter.with_resource_id("firstdoc")
      |> Filter.with_subject_type("user")
      |> Filter.with_subject_id("alice")

    assert [%Relationship{subject_id: "alice"}] = read(client, filter)
  end

  test "a subject constraint without a subject type is rejected before any request", %{
    client: client
  } do
    filter = Filter.new("document") |> Filter.with_subject_id("alice")

    assert {:error, %SpiceDB.InvalidArgumentError{message: message}} =
             SpiceDB.read_relationships(client, Consistency.full(), filter)

    assert message =~ "with_subject_type"
  end

  test "pages past the 512-per-request limit transparently", %{client: client} do
    ids = for i <- 1..600, do: "user#{i}"
    revision = write_viewers(client, "bigdoc", ids)

    rels =
      read(
        client,
        Filter.new("document") |> Filter.with_resource_id("bigdoc"),
        Consistency.at_least(revision)
      )

    assert length(rels) == 600
    assert rels |> Enum.map(& &1.subject_id) |> Enum.sort() == Enum.sort(ids)
  end

  test "halting early returns only what was taken, and re-enumerating re-reads", %{client: client} do
    revision = write_viewers(client, "bigdoc", for(i <- 1..600, do: "user#{i}"))

    {:ok, stream} =
      SpiceDB.read_relationships(client, Consistency.at_least(revision), Filter.new("document"))

    assert within(10_000, fn -> Enum.take(stream, 3) end) |> length() == 3
    assert within(15_000, fn -> Enum.count(stream) end) == 600
  end
end
