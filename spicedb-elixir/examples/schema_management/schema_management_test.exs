defmodule SpiceDB.Examples.SchemaManagementTest do
  use SpiceDB.ExampleCase, async: false

  @updated_schema """
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

  test "writes a schema and returns a revision", %{client: client} do
    {:ok, revision} = SpiceDB.write_schema(client, test_schema())

    assert is_binary(revision)
    assert revision != ""
  end

  test "reads back a previously written schema", %{client: client} do
    SpiceDB.write_schema!(client, test_schema())

    {:ok, {schema_text, revision}} = SpiceDB.read_schema(client)

    assert revision != ""
    assert schema_text =~ "definition user"
    assert schema_text =~ "definition document"
    assert schema_text =~ "permission view"
    refute schema_text =~ "relation admin"
  end

  test "overwrites the schema with a new version", %{client: client} do
    revision = SpiceDB.write_schema!(client, @updated_schema)
    assert revision != ""

    {schema_text, _revision} = SpiceDB.read_schema!(client)
    assert schema_text =~ "relation admin"
    assert schema_text =~ "permission manage"
  end

  test "rejects an unparseable schema with a typed error and keeps the old one", %{client: client} do
    assert {:error, %SpiceDB.InvalidArgumentError{reason: "ERROR_REASON_SCHEMA_PARSE_ERROR"}} =
             SpiceDB.write_schema(client, "definition user {")

    {schema_text, _revision} = SpiceDB.read_schema!(client)
    assert schema_text =~ "definition document"
  end
end
