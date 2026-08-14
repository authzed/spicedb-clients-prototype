"""Example: Schema read/write/reflect/diff.

Demonstrates reading, writing, reflecting, and diffing schema.
"""

import pytest

from spicedb import SpiceDBClient, full


pytestmark = pytest.mark.integration


async def test_schema_management(client: SpiceDBClient):
    # Write a schema
    schema = """\
definition user {}

definition document {
    relation viewer: user
    relation editor: user
    permission view = viewer + editor
    permission edit = editor
}"""

    revision = await client.write_schema(schema)
    print(f"wrote schema at revision: {revision}")
    assert revision

    # Read the schema back
    read_schema = await client.read_schema()
    print(f"read schema:\n{read_schema}")
    assert "definition user" in read_schema
    assert "definition document" in read_schema


async def test_reflect_schema(client: SpiceDBClient):
    schema = """\
definition user {}

definition document {
    relation viewer: user
    relation editor: user
    permission view = viewer + editor
    permission edit = editor
}"""
    await client.write_schema(schema)

    # Reflect the schema: returns native ReflectSchemaResult, not proto.
    result = await client.reflect_schema(full())
    print(f"reflected {len(result.definitions)} definition(s)")

    definitions_by_name = {d.name: d for d in result.definitions}
    assert "document" in definitions_by_name
    doc = definitions_by_name["document"]
    relation_names = {r.name for r in doc.relations}
    permission_names = {p.name for p in doc.permissions}
    assert relation_names == {"viewer", "editor"}
    assert permission_names == {"view", "edit"}
    assert doc.relations[0].parent_definition_name == "document"
    assert doc.permissions[0].parent_definition_name == "document"


async def test_diff_schema(client: SpiceDBClient):
    schema = """\
definition user {}

definition document {
    relation viewer: user
    permission view = viewer
}"""
    await client.write_schema(schema)

    comparison_schema = """\
definition user {}

definition document {
    relation viewer: user
    relation editor: user
    permission view = viewer + editor
}"""

    # Diff against a comparison schema: returns a native list[SchemaDiff].
    diffs = await client.diff_schema(full(), comparison_schema)
    print(f"found {len(diffs)} diff(s)")

    kinds = {d.kind for d in diffs}
    assert "relation_added" in kinds
    added = next(d for d in diffs if d.kind == "relation_added")
    assert added.definition_name == "document"
    assert added.relation_name == "editor"
