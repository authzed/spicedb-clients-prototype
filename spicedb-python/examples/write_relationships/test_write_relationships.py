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


async def test_write_prebuilt_updates_from_strings(client: SpiceDBClient):
    """Code that queues changes as data (a replayed watch, a work queue)
    writes prebuilt ``Update`` values; bad input is an ordinary ``ValueError``."""
    from spicedb import ObjectRef, PermissionCheck, SubjectRef, Update, UpdateOperation, full

    queued = [
        Update(UpdateOperation.TOUCH, Relationship.from_triple("document:queued", "viewer", "user:alice")),
        Update(UpdateOperation.TOUCH, Relationship.from_triple("document:queued", "owner", "user:bob")),
    ]
    result = await client.write(Transaction().extend(queued))
    check = PermissionCheck(
        resource=ObjectRef.parse("document:queued"),
        permission="view",
        subject=SubjectRef.parse("user:alice"),
    )
    assert (await client.check(check, consistency=full())).has_permission
    await client.write(Transaction().add(Update(UpdateOperation.DELETE, queued[0].relationship)))
    assert not (await client.check(check, consistency=full())).has_permission
    print(f"applied {len(queued)} queued updates at {result.revision}")

    # Local validation errors are ValueErrors (and InvalidArgumentErrors).
    with pytest.raises(ValueError):
        Relationship.from_triple("document", "viewer", "user:alice")
    with pytest.raises(ValueError):
        ObjectRef.parse("document:")
    await client.delete_all_relationships_non_atomic(
        Filter(resource_type="document", resource_id="queued")
    )
