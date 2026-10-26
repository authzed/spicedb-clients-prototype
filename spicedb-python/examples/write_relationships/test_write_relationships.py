"""Example: Writing relationships with transaction builder.

Demonstrates create, touch, delete, and preconditions.
"""

from spicedb import SubjectFilter
import pytest
from spicedb import FailedPreconditionError, Filter, Relationship
from spicedb.aio import SpiceDBClient
from spicedb.types import Transaction

pytestmark = pytest.mark.integration


async def test_write_relationships(client: SpiceDBClient):
    txn = Transaction()
    txn.touch(Relationship.from_triple("document:firstdoc", "viewer", "user:alice"))
    txn.touch(Relationship.from_triple("document:firstdoc", "editor", "user:bob"))
    txn.must_not_match(
        Filter(
            resource_type="document",
            resource_id="firstdoc",
            relation="editor",
            subject=SubjectFilter("user", subject_id="mallory"),
        )
    )
    result = await client.write(txn)
    print(f"wrote relationships at revision: {result.revision}")
    assert str(result.revision)
    doomed = Transaction()
    doomed.touch(Relationship.from_triple("document:seconddoc", "viewer", "user:alice"))
    doomed.must_match(
        Filter(
            resource_type="document",
            resource_id="firstdoc",
            relation="owner",
            subject=SubjectFilter("user", subject_id="nobody"),
        )
    )
    with pytest.raises(FailedPreconditionError) as excinfo:
        await client.write(doomed)
    err = excinfo.value
    print(f"precondition failed: reason={err.reason!r} metadata={err.reason_metadata}")
    assert err.reason == "ERROR_REASON_WRITE_OR_DELETE_PRECONDITION_FAILURE"
    assert err.reason_domain == "authzed.com"
    assert err.reason_metadata
    await client.delete_all_relationships_non_atomic(
        Filter(resource_type="document", resource_id="firstdoc")
    )
