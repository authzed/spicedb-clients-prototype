"""Example: Schema read/write.

Demonstrates reading and writing schema.
"""

import pytest

from spicedb import SpiceDBClient


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
