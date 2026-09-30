defmodule SpiceDB.Examples.LookupResourcesTest do
  use SpiceDB.ExampleCase, async: false

  alias SpiceDB.LookupResource

  @alice SubjectRef.new("user", "alice")

  defp lookup(client, permission, subject, opts \\ [], consistency \\ Consistency.full()) do
    {:ok, stream} =
      SpiceDB.lookup_resources(client, consistency, "document", permission, subject, opts)

    within(15_000, fn -> Enum.to_list(stream) end)
  end

  defp touch_all(client, tuples) do
    txn =
      Enum.reduce(tuples, Transaction.new(), &Transaction.touch(&2, Relationship.from_tuple!(&1)))

    SpiceDB.write_relationships!(client, txn)
  end

  test "finds every resource the subject can reach through any relation", %{client: client} do
    touch_all(client, [
      "document:firstdoc#viewer@user:alice",
      "document:seconddoc#editor@user:alice",
      "document:third#viewer@user:bob"
    ])

    results = lookup(client, "view", @alice)

    assert Enum.all?(results, &match?(%LookupResource{permissionship: :has_permission}, &1))

    assert results |> Enum.map(& &1.resource_id) |> Enum.uniq() |> Enum.sort() == [
             "firstdoc",
             "seconddoc"
           ]

    assert Enum.all?(results, &(&1.looked_up_at != ""))
  end

  test "returns only resources where the requested permission holds", %{client: client} do
    touch_all(client, [
      "document:firstdoc#viewer@user:alice",
      "document:seconddoc#owner@user:alice"
    ])

    assert client |> lookup("delete", @alice) |> Enum.map(& &1.resource_id) |> Enum.uniq() == [
             "seconddoc"
           ]
  end

  test "returns nothing for a subject with no access", %{client: client} do
    touch_all(client, ["document:firstdoc#viewer@user:alice"])
    assert lookup(client, "view", SubjectRef.new("user", "nobody")) == []
  end

  test "pages past 512 results", %{client: client} do
    revision = touch_all(client, for(i <- 1..600, do: "document:doc#{i}#viewer@user:alice"))

    ids =
      client
      |> lookup("view", @alice, [], Consistency.at_least(revision))
      |> Enum.map(& &1.resource_id)
      |> Enum.uniq()

    assert length(ids) == 600
  end

  test "a caveated grant is conditional until its context is supplied", %{client: client} do
    SpiceDB.write_schema!(client, """
    #{test_schema()}
    caveat has_valid_ip(ip_address string) {
      ip_address == "127.0.0.1"
    }

    definition caveated_doc {
      relation viewer: user with has_valid_ip
      permission view = viewer
    }
    """)

    rel = Relationship.from_triple("caveated_doc", "caveated", "viewer", "user", "alice")

    SpiceDB.write_relationships!(
      client,
      Transaction.touch(Transaction.new(), Relationship.with_caveat(rel, "has_valid_ip"))
    )

    lookup = fn opts ->
      {:ok, stream} =
        SpiceDB.lookup_resources(client, Consistency.full(), "caveated_doc", "view", @alice, opts)

      within(10_000, fn -> Enum.to_list(stream) end)
    end

    assert [
             %LookupResource{resource_id: "caveated", permissionship: :conditional_permission} =
               result
           ] = lookup.([])

    assert "ip_address" in result.partial_caveat.missing_required_context

    assert [%LookupResource{permissionship: :has_permission, partial_caveat: nil}] =
             lookup.(context: %{"ip_address" => "127.0.0.1"})

    assert lookup.(context: %{"ip_address" => "10.0.0.1"}) == []
  end
end
