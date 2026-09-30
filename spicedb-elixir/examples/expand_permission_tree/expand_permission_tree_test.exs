defmodule SpiceDB.Examples.ExpandPermissionTreeTest do
  use SpiceDB.ExampleCase, async: false

  alias SpiceDB.{ExpandResult, IntermediateNode, LeafNode, PermissionTree}

  defp leaf_subject_ids(nil), do: []

  defp leaf_subject_ids(%PermissionTree{leaf: %LeafNode{subjects: subjects}}),
    do: Enum.map(subjects, & &1.subject_id)

  defp leaf_subject_ids(%PermissionTree{intermediate: %IntermediateNode{children: children}}),
    do: Enum.flat_map(children, &leaf_subject_ids/1)

  defp leaf_subject_ids(%PermissionTree{}), do: []

  setup %{client: client} do
    txn =
      [
        "document:firstdoc#viewer@user:alice",
        "document:firstdoc#editor@user:bob",
        "document:firstdoc#owner@user:carol"
      ]
      |> Enum.reduce(Transaction.new(), &Transaction.touch(&2, Relationship.from_tuple!(&1)))

    SpiceDB.write_relationships!(client, txn)
    :ok
  end

  test "expands a union into an intermediate node over the leaves", %{client: client} do
    {:ok, %ExpandResult{tree: tree, revision: revision}} =
      SpiceDB.expand_permission_tree(
        client,
        Consistency.full(),
        ObjectRef.new("document", "firstdoc"),
        "view"
      )

    assert revision != ""

    assert %PermissionTree{
             expanded_object: %ObjectRef{object_type: "document", object_id: "firstdoc"},
             expanded_relation: "view"
           } = tree

    assert %IntermediateNode{operation: :union} = tree.intermediate
    assert tree |> leaf_subject_ids() |> Enum.sort() == ["alice", "bob", "carol"]
  end

  test "a single-relation permission reaches only that relation's subjects", %{client: client} do
    result =
      SpiceDB.expand_permission_tree!(
        client,
        Consistency.full(),
        ObjectRef.new("document", "firstdoc"),
        "delete"
      )

    assert leaf_subject_ids(result.tree) == ["carol"]
  end

  test "an exclusion reports its operation", %{client: client} do
    SpiceDB.write_schema!(client, """
    #{test_schema()}
    definition gated {
      relation viewer: user
      relation banned: user
      permission view = viewer - banned
    }
    """)

    result =
      SpiceDB.expand_permission_tree!(
        client,
        Consistency.full(),
        ObjectRef.new("gated", "g"),
        "view"
      )

    assert %IntermediateNode{operation: :exclusion, children: [_, _]} = result.tree.intermediate
  end
end
