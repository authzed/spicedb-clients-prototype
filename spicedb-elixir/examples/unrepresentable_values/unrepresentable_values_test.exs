defmodule SpiceDB.Examples.UnrepresentableValuesTest do
  use SpiceDB.ExampleCase, async: false

  alias SpiceDB.Examples.StandIn

  defmodule FuturePermissions do
    use GRPC.Server, service: Authzed.Api.V1.PermissionsService.Service

    alias Authzed.Api.V1

    def check_permission(_request, _stream),
      do: %V1.CheckPermissionResponse{permissionship: 4242}

    def check_bulk_permissions(request, _stream) do
      %V1.CheckBulkPermissionsResponse{
        pairs:
          Enum.map(request.items, fn item ->
            %V1.CheckBulkPermissionsPair{
              request: item,
              response: {:item, %V1.CheckBulkPermissionsResponseItem{permissionship: 4242}}
            }
          end)
      }
    end
  end

  @caveated_schema """
  caveat only_on_tuesday(day string) { day == "tuesday" }
  definition docs {
  \trelation viewer: user with only_on_tuesday
  \tpermission view = viewer
  }
  """

  setup tags do
    if client = tags[:client],
      do: SpiceDB.write_schema!(client, test_schema() <> @caveated_schema)

    :ok
  end

  test "refuses unconvertible caveat context, naming the key, and writes nothing",
       %{client: client} do
    rel =
      Relationship.from_triple("docs", "readme", "viewer", "user", "alice")
      |> Relationship.with_caveat("only_on_tuesday", %{"day" => "tuesday", "impostor" => self()})

    assert {:error, %SpiceDB.InvalidArgumentError{message: message}} =
             SpiceDB.write_relationships(client, Transaction.touch(Transaction.new(), rel))

    assert message =~ ~s("impostor")

    {:ok, stream} = SpiceDB.read_relationships(client, Consistency.full(), Filter.new("docs"))
    assert within(10_000, fn -> Enum.to_list(stream) end) == []
  end

  test "names the full path of a nested unconvertible value", %{client: client} do
    rel = Relationship.from_triple("docs", "readme", "viewer", "user", "alice")

    for {context, path} <- [
          {%{"outer" => %{"inner" => [1, {:tuple}]}}, "outer.inner[1]"},
          {%{"bytes" => <<0xFF, 0xFE>>}, "bytes"},
          {%{"ref" => make_ref()}, "ref"}
        ] do
      assert {:error, %SpiceDB.InvalidArgumentError{message: message}} =
               SpiceDB.check_permission(client, Consistency.full(), "view", rel, context: context)

      assert message =~ ~s("#{path}"), "expected #{inspect(path)} in #{inspect(message)}"
    end
  end

  test "refuses a subject filter the wire cannot express, rather than widening it",
       %{client: client} do
    assert {:error, %SpiceDB.InvalidArgumentError{message: message}} =
             SpiceDB.delete_relationships(
               client,
               Filter.new("document") |> Filter.with_subject_id("alice")
             )

    assert message =~ "subject_type"

    assert {:ok, _revision} =
             SpiceDB.delete_relationships(
               client,
               Filter.new("document")
               |> Filter.with_subject_type("user")
               |> Filter.with_subject_id("alice")
             )
  end

  @tag :no_spicedb
  test "neither raises nor grants on a permissionship it has never seen" do
    port = StandIn.start!([FuturePermissions])
    client = SpiceDB.new_plaintext!("127.0.0.1:#{port}", "some-token")
    rel = Relationship.from_triple("document", "readme", "view", "user", "alice")

    result = SpiceDB.check_permission!(client, Consistency.full(), "view", rel)
    assert result.permissionship == :unspecified

    refute CheckResult.has_permission?(result),
           "SECURITY: an unknown permissionship was treated as a grant"

    assert [%CheckResult{permissionship: :unspecified}] =
             SpiceDB.check_permissions!(client, Consistency.full(), "view", [rel])

    refute SpiceDB.check_all!(client, Consistency.full(), "view", [rel])
    refute SpiceDB.check_any!(client, Consistency.full(), "view", [rel])
    SpiceDB.close(client)
  end
end
