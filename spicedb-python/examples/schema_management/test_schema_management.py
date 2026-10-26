"""Example: Schema read/write/reflect/diff/introspect.

Demonstrates reading, writing, reflecting, diffing, and introspecting
(computable_permissions/dependent_relations) schema.
"""

import pytest
from spicedb import full
from spicedb.aio import SpiceDBClient

pytestmark = pytest.mark.integration


async def test_schema_management(client: SpiceDBClient):
    schema = "definition user {}\n\ndefinition document {\n    relation viewer: user\n    relation editor: user\n    permission view = viewer + editor\n    permission edit = editor\n}"
    revision = await client.write_schema(schema)
    print(f"wrote schema at revision: {revision}")
    assert str(revision)
    read_schema = await client.read_schema()
    print(f"read schema:\n{read_schema}")
    assert "definition user" in read_schema.schema
    assert "definition document" in read_schema.schema


async def test_reflect_schema(client: SpiceDBClient):
    schema = "definition user {}\n\ndefinition document {\n    relation viewer: user\n    relation editor: user\n    permission view = viewer + editor\n    permission edit = editor\n}"
    await client.write_schema(schema)
    result = await client.reflect_schema(consistency=full())
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
    schema = "definition user {}\n\ndefinition document {\n    relation viewer: user\n    permission view = viewer\n}"
    await client.write_schema(schema)
    comparison_schema = "definition user {}\n\ndefinition document {\n    relation viewer: user\n    relation editor: user\n    permission view = viewer + editor\n}"
    diffs = await client.diff_schema(comparison_schema, consistency=full())
    print(f"found {len(diffs.diffs)} diff(s)")
    kinds = {d.kind for d in diffs.diffs}
    assert "relation_added" in kinds
    added = next((d for d in diffs.diffs if d.kind == "relation_added"))
    assert added.definition_name == "document"
    assert added.relation_name == "editor"


async def test_computable_permissions(client: SpiceDBClient):
    schema = "definition user {}\n\ndefinition document {\n    relation viewer: user\n    relation editor: user\n    permission view = viewer + editor\n    permission edit = editor\n}"
    await client.write_schema(schema)
    permissions = await client.computable_permissions(
        consistency=full(), definition_name="document", relation_name="editor"
    )
    print(f"permissions computable from document#editor: {permissions}")
    permission_names = {p.relation_name for p in permissions.relations}
    assert permission_names == {"view", "edit"}
    for p in permissions.relations:
        assert p.definition_name == "document"
        assert p.is_permission is True


async def test_dependent_relations(client: SpiceDBClient):
    schema = "definition user {}\n\ndefinition document {\n    relation viewer: user\n    relation editor: user\n    permission view = viewer + editor\n    permission edit = editor\n}"
    await client.write_schema(schema)
    relations = await client.dependent_relations(
        consistency=full(), definition_name="document", permission_name="view"
    )
    print(f"relations that document#view depends on: {relations}")
    relation_names = {r.relation_name for r in relations.relations}
    assert relation_names == {"viewer", "editor"}
    for r in relations.relations:
        assert r.definition_name == "document"
        assert r.is_permission is False
