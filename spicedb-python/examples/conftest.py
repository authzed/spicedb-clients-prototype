"""Shared fixtures for example integration tests."""

import os

import pytest

from spicedb import Filter
from spicedb.aio import SpiceDBClient
from spicedb.errors import SpiceDBError


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


def clear_documents_sync(c) -> None:
    """Synchronous twin of :func:`clear_documents`, for the ``sync_*`` examples."""
    try:
        c.delete_relationships(Filter(resource_type="document"))
    except SpiceDBError:
        pass


async def clear_documents(c) -> None:
    """Delete every ``document`` relationship, tolerating a schema without one.

    Every example runs against the same SpiceDB and writes a whole schema, and
    SpiceDB refuses a ``WriteSchema`` that drops a relation while a
    relationship still exists under it. So what a *previous* example left
    behind is the next one's problem, and it has to be cleared **before** the
    schema write -- a cleanup at teardown does nothing when the test that
    should have run it failed first. On a fresh server there is no ``document``
    definition yet, which is not a failure.
    """
    try:
        await c.delete_relationships(Filter(resource_type="document"))
    except SpiceDBError:
        pass


@pytest.fixture
async def client():
    """Create a client connected to a local SpiceDB and write the test schema."""
    async with SpiceDBClient(
        ENDPOINT, token=TOKEN, insecure=True
    ) as c:
        await clear_documents(c)
        await c.write_schema(SCHEMA)
        yield c
