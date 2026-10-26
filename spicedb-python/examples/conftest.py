"""Shared fixtures for example integration tests."""
from spicedb import TransportSecurity

import os
import pytest
from spicedb import Filter
from spicedb.aio import SpiceDBClient
from spicedb.errors import FailedPreconditionError

ENDPOINT = os.environ.get("SPICEDB_ENDPOINT") or "localhost:50051"
TOKEN = os.environ.get("SPICEDB_TOKEN") or "somerandomkeyhere"
SCHEMA = "definition user {}\n\ndefinition document {\n    relation viewer: user\n    relation editor: user\n    relation owner: user\n    permission view = viewer + editor + owner\n    permission edit = editor + owner\n    permission delete = owner\n}\n"


def clear_documents_sync(c) -> None:
    """Synchronous twin of :func:`clear_documents`, for the ``sync_*`` examples."""
    try:
        c.delete_all_relationships_non_atomic(Filter(resource_type="document"))
    except FailedPreconditionError:
        pass


async def clear_documents(c) -> None:
    """Delete every ``document`` relationship, tolerating a schema without one.

    Every example runs against the same SpiceDB and writes a whole schema, and
    SpiceDB refuses a ``WriteSchema`` that drops a relation while a
    relationship still exists under it. So what a *previous* example left
    behind is the next one's problem, and it has to be cleared **before** the
    schema write -- a cleanup at teardown does nothing when the test that
    should have run it failed first. On a fresh server there is no ``document``
    definition yet, which SpiceDB reports as FAILED_PRECONDITION
    (``ERROR_REASON_UNKNOWN_DEFINITION``). That one error is tolerated and no
    other: an unreachable server or a bad token must still fail the example.
    """
    try:
        await c.delete_all_relationships_non_atomic(Filter(resource_type="document"))
    except FailedPreconditionError:
        pass


@pytest.fixture
async def client():
    """Create a client connected to a local SpiceDB and write the test schema."""
    async with SpiceDBClient(ENDPOINT, token=TOKEN, transport_security=TransportSecurity.PLAINTEXT) as c:
        await clear_documents(c)
        await c.write_schema(SCHEMA)
        yield c
