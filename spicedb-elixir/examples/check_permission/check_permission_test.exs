defmodule SpiceDB.Examples.CheckPermissionTest do
  use SpiceDB.ExampleCase, async: false

  setup %{client: client} do
    txn =
      Transaction.new()
      |> Transaction.touch(Relationship.from_tuple!("document:firstdoc#viewer@user:alice"))

    {:ok, revision: SpiceDB.write_relationships!(client, txn)}
  end

  test "reports :has_permission when the permission is granted", %{client: client} do
    rel = Relationship.from_tuple!("document:firstdoc#viewer@user:alice")

    {:ok, %CheckResult{} = result} =
      SpiceDB.check_permission(client, Consistency.full(), "view", rel)

    assert CheckResult.has_permission?(result)
    assert result.permissionship == :has_permission
    assert result.checked_at != ""
  end

  test "reports :no_permission when it is not", %{client: client} do
    rel = Relationship.from_tuple!("document:firstdoc#viewer@user:alice")

    result = SpiceDB.check_permission!(client, Consistency.full(), "delete", rel)

    refute CheckResult.has_permission?(result)
    assert result.permissionship == :no_permission
  end

  test "reads at least as fresh as a write's revision", %{client: client, revision: revision} do
    rel = Relationship.from_tuple!("document:firstdoc#viewer@user:alice")

    assert SpiceDB.check_permission!(client, Consistency.at_least(revision), "view", rel)
           |> CheckResult.has_permission?()
  end

  test "reports :conditional_permission, not a grant, when caveat context is missing",
       %{client: client} do
    SpiceDB.write_schema!(client, """
    #{test_schema()}
    caveat active(now int) { now < 100 }
    definition doc {
    \trelation viewer: user with active
    \tpermission view = viewer
    }
    """)

    rel = Relationship.from_triple("doc", "conditionaldoc", "viewer", "user", "alice")

    SpiceDB.write_relationships!(
      client,
      Transaction.touch(Transaction.new(), Relationship.with_caveat(rel, "active"))
    )

    result = SpiceDB.check_permission!(client, Consistency.full(), "view", rel)
    refute CheckResult.has_permission?(result)
    assert result.permissionship == :conditional_permission
    assert result.missing_context == ["now"]

    assert SpiceDB.check_permission!(client, Consistency.full(), "view", rel,
             context: %{"now" => 5}
           )
           |> CheckResult.has_permission?()

    refute SpiceDB.check_permission!(client, Consistency.full(), "view", rel,
             context: %{"now" => 500}
           )
           |> CheckResult.has_permission?()
  end

  test "lets a relationship's own check context win over the call's per key", %{client: client} do
    SpiceDB.write_schema!(client, """
    #{test_schema()}
    caveat active(now int) { now < 100 }
    definition doc {
    \trelation viewer: user with active
    \tpermission view = viewer
    }
    """)

    rel = Relationship.from_triple("doc", "d", "viewer", "user", "alice")

    SpiceDB.write_relationships!(
      client,
      Transaction.touch(Transaction.new(), Relationship.with_caveat(rel, "active"))
    )

    item = Relationship.with_check_context(rel, %{"now" => 5})

    result =
      SpiceDB.check_permission!(client, Consistency.full(), "view", item,
        context: %{"now" => 500}
      )

    assert CheckResult.has_permission?(result)
  end

  test "checks many relationships in one call, in input order", %{client: client} do
    rels = [
      Relationship.from_tuple!("document:firstdoc#viewer@user:alice"),
      Relationship.from_tuple!("document:firstdoc#viewer@user:bob")
    ]

    {:ok, [alice, bob]} = SpiceDB.check_permissions(client, Consistency.full(), "view", rels)
    assert alice.permissionship == :has_permission
    assert bob.permissionship == :no_permission

    assert SpiceDB.check_any!(client, Consistency.full(), "view", rels)
    refute SpiceDB.check_all!(client, Consistency.full(), "view", rels)
    refute SpiceDB.check_all!(client, Consistency.full(), "view", [])
    refute SpiceDB.check_any!(client, Consistency.full(), "view", [])
  end
end
