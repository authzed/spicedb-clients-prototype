defmodule SpiceDB.Examples.BulkOperationsTest do
  use SpiceDB.ExampleCase, async: false

  @users ["alice", "bob", "charlie"]

  setup %{client: client} do
    txn =
      Enum.reduce(
        @users,
        Transaction.new(),
        &Transaction.touch(
          &2,
          Relationship.from_triple("document", "report", "viewer", "user", &1)
        )
      )

    {:ok, revision: SpiceDB.write_relationships!(client, txn)}
  end

  defp checks(users),
    do: Enum.map(users, &Relationship.from_triple("document", "report", "viewer", "user", &1))

  defp export(client, consistency, opts) do
    {:ok, stream} = SpiceDB.export_relationships(client, consistency, opts)
    within(20_000, fn -> Enum.to_list(stream) end)
  end

  test "checks many relationships in input order", %{client: client, revision: revision} do
    results =
      SpiceDB.check_permissions!(
        client,
        Consistency.at_least(revision),
        "view",
        checks(["alice", "nobody", "charlie"])
      )

    assert Enum.map(results, & &1.permissionship) == [
             :has_permission,
             :no_permission,
             :has_permission
           ]
  end

  test "splits more than 1 000 checks into several requests and keeps the order", %{
    client: client,
    revision: revision
  } do
    users = for i <- 1..1_500, do: if(rem(i, 500) == 0, do: "alice", else: "nobody#{i}")

    results =
      SpiceDB.check_permissions!(client, Consistency.at_least(revision), "view", checks(users))

    assert length(results) == 1_500

    granted =
      for {r, i} <- Enum.with_index(results, 1), SpiceDB.CheckResult.has_permission?(r), do: i

    assert granted == [500, 1_000, 1_500]
  end

  test "check_all and check_any", %{client: client, revision: revision} do
    consistency = Consistency.at_least(revision)
    assert SpiceDB.check_all!(client, consistency, "view", checks(@users))
    refute SpiceDB.check_all!(client, consistency, "view", checks(["alice", "nobody"]))
    assert SpiceDB.check_any!(client, consistency, "view", checks(["nobody", "alice"]))
    refute SpiceDB.check_any!(client, consistency, "view", checks(["nobody", "noone"]))
  end

  test "imports a lazy stream in one call and refuses a duplicate import", %{client: client} do
    relationships =
      Stream.map(
        1..1_500,
        &Relationship.from_triple("document", "bulk-#{&1}", "viewer", "user", "alice")
      )

    assert {:ok, 1_500} = SpiceDB.import_relationships(client, relationships)

    assert length(
             export(client, Consistency.full(),
               filter: Filter.new("document") |> Filter.with_resource_id_prefix("bulk-")
             )
           ) ==
             1_500

    assert {:error, %SpiceDB.AlreadyExistsError{}} =
             SpiceDB.import_relationships(client, Stream.take(relationships, 1))
  end

  test "exports exactly what matches the filter", %{client: client, revision: revision} do
    SpiceDB.write_relationships!(
      client,
      Transaction.touch(
        Transaction.new(),
        Relationship.from_tuple!("document:report#owner@user:dana")
      )
    )

    viewers =
      export(client, Consistency.at_least(revision),
        filter: Filter.new("document") |> Filter.with_relation("viewer")
      )

    assert viewers |> Enum.map(& &1.subject_id) |> Enum.sort() == @users
    assert Enum.all?(viewers, &match?(%Relationship{resource_relation: "viewer"}, &1))
    assert length(export(client, Consistency.full(), [])) == 4
  end
end
