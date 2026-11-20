"""Example: Resource lookup.

Demonstrates finding resources a subject can access, including how to read
the ``permissionship`` of each result. A ``Permissionship.CONDITIONAL_PERMISSION``
result means the match depends on caveat context that wasn't supplied —
callers must not treat it as an unconditional grant.
"""

from spicedb import ObjectRef, SubjectRef
import pytest
from spicedb import Filter, LookupBatching, LookupPermissionshipFilter, Permissionship, Relationship, full
from spicedb.aio import SpiceDBClient
from spicedb.types import Transaction

pytestmark = pytest.mark.integration


async def test_lookup_resources(client: SpiceDBClient):
    txn = Transaction()
    txn.touch(Relationship.from_triple("document:readme", "viewer", "user:alice"))
    txn.touch(Relationship.from_triple("document:design", "viewer", "user:alice"))
    txn.touch(Relationship.from_triple("document:secret", "viewer", "user:bob"))
    await client.write(txn)
    resource_ids = []
    async for resource in client.lookup_resources(
        resource_type="document",
        permission="view",
        subject=SubjectRef(ObjectRef("user", "alice"), None),
        consistency=full(),
        permissionship_filter=LookupPermissionshipFilter.HAS_PERMISSION_ONLY,
    ):
        print(
            f"alice can view document:{resource.resource_id} (permissionship={resource.permissionship})"
        )
        assert resource.permissionship == Permissionship.HAS_PERMISSION, (
            f"unexpected permissionship for document:{resource.resource_id}: {resource.permissionship} (missing context: {resource.partial_caveat})"
        )
        resource_ids.append(resource.resource_id)
    assert "readme" in resource_ids
    assert "design" in resource_ids

    # Responses may arrive batched: only the last item of a batch carries a
    # cursor, so a checkpoint is the latest non-None cursor seen.
    alice = SubjectRef(ObjectRef("user", "alice"), None)
    checkpoint = None
    seen = []
    async for resource in client.lookup_resources(
        resource_type="document", permission="view", subject=alice, consistency=full(),
    ):
        seen.append(resource.resource_id)
        checkpoint = resource.after_result_cursor or checkpoint
    assert {"readme", "design"} <= set(seen)
    assert checkpoint, "the last item of a lookup carries a cursor"

    # One resource per response message puts a cursor on every item.
    async for resource in client.lookup_resources(
        resource_type="document", permission="view", subject=alice, consistency=full(),
        batching=LookupBatching.DISABLED,
    ):
        assert resource.after_result_cursor
    for resource_id in ("readme", "design", "secret"):
        await client.delete_all_relationships_non_atomic(
            Filter(resource_type="document", resource_id=resource_id)
        )
