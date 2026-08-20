"""Example: Writing relationships with transaction builder.

Demonstrates create, touch, delete, and preconditions.
"""

import pytest

from spicedb import FailedPreconditionError, Filter, Relationship
from spicedb.aio import SpiceDBClient
from spicedb.types import Transaction


pytestmark = pytest.mark.integration


async def test_write_relationships(client: SpiceDBClient):
    # Touch relationships (create or update)
    txn = Transaction()
    txn.touch(Relationship.from_triple("document:firstdoc", "viewer", "user:alice"))
    txn.touch(Relationship.from_triple("document:firstdoc", "editor", "user:bob"))

    # Add a precondition: mallory must NOT be an owner
    txn.must_not_match(
        Filter(
            resource_type="document",
            resource_id="firstdoc",
            relation="editor",
            subject_type="user",
            subject_id="mallory",
        )
    )

    revision = await client.write(txn)
    print(f"wrote relationships at revision: {revision}")
    assert revision  # non-empty revision string

    # A failed precondition arrives with SpiceDB's structured explanation
    # attached, not just a message: the typed error carries the
    # authzed.api.v1.ErrorReason name and the metadata naming which
    # precondition did not hold, so recovery can be written against data rather
    # than against a parsed string.
    doomed = Transaction()
    doomed.touch(Relationship.from_triple("document:seconddoc", "viewer", "user:alice"))
    doomed.must_match(
        Filter(
            resource_type="document",
            resource_id="firstdoc",
            relation="owner",
            subject_type="user",
            subject_id="nobody",
        )
    )

    with pytest.raises(FailedPreconditionError) as excinfo:
        await client.write(doomed)

    err = excinfo.value
    print(f"precondition failed: reason={err.reason!r} metadata={err.reason_metadata}")
    assert err.reason == "ERROR_REASON_WRITE_OR_DELETE_PRECONDITION_FAILURE"
    assert err.reason_domain == "authzed.com"
    assert err.reason_metadata

    # Clean up so later examples that write a narrower schema aren't blocked
    # by leftover relationships.
    await client.delete_relationships(
        Filter(resource_type="document", resource_id="firstdoc")
    )
