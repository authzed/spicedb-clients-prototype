"""Shared fixtures for example integration tests."""

import pytest

from spicedb import SpiceDBClient


SCHEMA = """\
definition user {}

definition document {
    relation viewer: user
    relation editor: user
    relation owner: user
    permission view = viewer + editor + owner
    permission edit = editor + owner
    permission delete = owner
}
"""


@pytest.fixture
async def client():
    """Create a client connected to a local SpiceDB and write the test schema."""
    async with SpiceDBClient(
        "localhost:50051", token="somerandomkeyhere", insecure=True
    ) as c:
        await c.write_schema(SCHEMA)
        yield c
