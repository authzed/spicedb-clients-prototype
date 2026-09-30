defmodule SpiceDB.Examples.LookupSubjectsTest do
  use SpiceDB.ExampleCase, async: false

  alias SpiceDB.{LookupSubject, ResolvedSubject}

  defp lookup(client, resource, permission, opts \\ []) do
    {:ok, stream} =
      SpiceDB.lookup_subjects(client, Consistency.full(), resource, permission, "user", opts)

    within(10_000, fn -> Enum.to_list(stream) end)
  end

  defp ids(results),
    do: results |> Enum.map(& &1.subject.subject_id) |> Enum.uniq() |> Enum.sort()

  defp touch_all(client, tuples) do
    txn =
      Enum.reduce(tuples, Transaction.new(), &Transaction.touch(&2, Relationship.from_tuple!(&1)))

    SpiceDB.write_relationships!(client, txn)
  end

  test "finds every subject with the permission", %{client: client} do
    touch_all(client, [
      "document:firstdoc#viewer@user:alice",
      "document:firstdoc#editor@user:bob",
      "document:other#viewer@user:carol"
    ])

    results = lookup(client, ObjectRef.new("document", "firstdoc"), "view")

    assert ids(results) == ["alice", "bob"]

    assert Enum.all?(
             results,
             &match?(
               %LookupSubject{subject: %ResolvedSubject{permissionship: :has_permission}},
               &1
             )
           )

    assert Enum.all?(results, &(&1.looked_up_at != ""))
  end

  test "returns only subjects holding the specific permission", %{client: client} do
    touch_all(client, ["document:firstdoc#viewer@user:alice", "document:firstdoc#owner@user:bob"])
    assert client |> lookup(ObjectRef.new("document", "firstdoc"), "delete") |> ids() == ["bob"]
  end

  test "returns nothing for a resource no one can reach", %{client: client} do
    assert lookup(client, ObjectRef.new("document", "nonexistent"), "view") == []
  end

  test "a wildcard grant reports the subjects carved back out of it", %{client: client} do
    SpiceDB.write_schema!(client, """
    #{test_schema()}
    definition public_doc {
      relation viewer: user | user:*
      relation banned: user
      permission view = viewer - banned
    }
    """)

    touch_all(client, ["public_doc:p#viewer@user:*", "public_doc:p#banned@user:eve"])

    results = lookup(client, ObjectRef.new("public_doc", "p"), "view")

    assert %LookupSubject{excluded_subjects: excluded} =
             Enum.find(results, &(&1.subject.subject_id == "*"))

    assert [%ResolvedSubject{subject_id: "eve"}] = excluded
  end

  test "subject_relation: finds subject sets rather than direct subjects", %{client: client} do
    SpiceDB.write_schema!(client, """
    #{test_schema()}
    definition team {
      relation member: user
    }

    definition team_doc {
      relation viewer: user | team#member
      permission view = viewer
    }
    """)

    touch_all(client, [
      "team_doc:t#viewer@team:eng#member",
      "team_doc:t#viewer@user:alice",
      "team:eng#member@user:bob"
    ])

    {:ok, stream} =
      SpiceDB.lookup_subjects(
        client,
        Consistency.full(),
        ObjectRef.new("team_doc", "t"),
        "view",
        "team",
        subject_relation: "member"
      )

    assert within(10_000, fn -> stream |> Enum.map(& &1.subject.subject_id) |> Enum.uniq() end) ==
             ["eng"]

    assert client |> lookup(ObjectRef.new("team_doc", "t"), "view") |> ids() == ["alice", "bob"]
  end
end
