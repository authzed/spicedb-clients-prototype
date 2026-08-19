"""Shared fixtures for example integration tests."""

import os

import pytest

from spicedb.aio import SpiceDBClient


# Endpoint and token come from the environment so the examples run against
# whichever SpiceDB the caller started; the defaults match
# docker-compose.test.yml. `mage integrationTest` exports both.
ENDPOINT = os.environ.get("SPICEDB_ENDPOINT") or "localhost:50051"
TOKEN = os.environ.get("SPICEDB_TOKEN") or "somerandomkeyhere"


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
        ENDPOINT, token=TOKEN, insecure=True
    ) as c:
        await c.write_schema(SCHEMA)
        yield c
