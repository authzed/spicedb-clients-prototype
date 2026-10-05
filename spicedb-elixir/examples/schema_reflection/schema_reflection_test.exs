defmodule SpiceDB.Examples.SchemaReflectionTest do
  use SpiceDB.ExampleCase, async: false

  test "reflects the current schema definitions", %{client: client} do
    {:ok, %SpiceDB.ReflectSchemaResult{} = result} =
      SpiceDB.reflect_schema(client, Consistency.full())

    assert result.revision != ""
    assert result.definitions |> Enum.map(& &1.name) |> Enum.sort() == ["document", "user"]

    document = Enum.find(result.definitions, &(&1.name == "document"))

    assert document.relations |> Enum.map(& &1.name) |> Enum.sort() == [
             "editor",
             "owner",
             "viewer"
           ]

    assert document.permissions |> Enum.map(& &1.name) |> Enum.sort() == [
             "delete",
             "edit",
             "view"
           ]

    assert Enum.all?(document.relations, &(&1.parent_definition_name == "document"))
  end

  test "reflects caveats with their parameters", %{client: client} do
    SpiceDB.write_schema!(client, """
    #{test_schema()}
    caveat active(now int) { now < 100 }
    """)

    %{caveats: [caveat]} = SpiceDB.reflect_schema!(client, Consistency.full())

    assert caveat.name == "active"
    assert caveat.expression =~ "now < 100"
    assert [%SpiceDB.SchemaCaveatParameter{name: "now", type: "int"}] = caveat.parameters
  end

  test "finds the permissions a relation contributes to", %{client: client} do
    {:ok, permissions} =
      SpiceDB.computable_permissions(client, Consistency.full(), "document", "viewer")

    assert Enum.map(permissions, &"#{&1.definition_name}##{&1.relation_name}") == [
             "document#view"
           ]

    assert [%SpiceDB.RelationReference{is_permission: true}] = permissions
  end

  test "finds the relations a permission depends on", %{client: client} do
    {:ok, relations} = SpiceDB.dependent_relations(client, Consistency.full(), "document", "view")

    assert relations |> Enum.map(&"#{&1.definition_name}##{&1.relation_name}") |> Enum.sort() ==
             ["document#editor", "document#owner", "document#viewer"]
  end

  test "diffs the current schema against a modified one", %{client: client} do
    new_schema = """
    definition user {}

    definition document {
    \trelation viewer: user
    \trelation editor: user
    \trelation owner: user
    \trelation admin: user
    \tpermission view = viewer + editor + owner + admin
    \tpermission edit = editor + owner + admin
    \tpermission delete = owner + admin
    \tpermission manage = admin
    }
    """

    {:ok, diffs} = SpiceDB.diff_schema(client, Consistency.full(), new_schema)

    tuples = Enum.map(diffs, &{&1.kind, &1.definition_name, &1.relation_name, &1.permission_name})

    assert {:relation_added, "document", "admin", nil} in tuples
    assert {:permission_added, "document", nil, "manage"} in tuples
    assert {:permission_expr_changed, "document", nil, "view"} in tuples
    refute Enum.any?(diffs, &(&1.kind == :unknown))
  end

  test "reports no diffs for an identical schema", %{client: client} do
    assert {:ok, []} = SpiceDB.diff_schema(client, Consistency.full(), test_schema())
  end
end
