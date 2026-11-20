"""Example: Resource lookup.

Demonstrates finding resources a subject can access, including how to read
the ``permissionship`` of each result. A ``Permissionship.CONDITIONAL_PERMISSION``
result means the match depends on caveat context that wasn't supplied —
callers must not treat it as an unconditional grant.
"""

from spicedb import ObjectRef, SubjectRef
import pytest
from spicedb import Filter, LookupPermissionshipFilter, Permissionship, Relationship, full
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
    for resource_id in ("readme", "design", "secret"):
        await client.delete_all_relationships_non_atomic(
            Filter(resource_type="document", resource_id=resource_id)
        )


async def test_lookup_resources_response_batch_size(client: SpiceDBClient):
    """``response_batch_size`` asks the server to pack several results into
    each stream message instead of one per message, trading time-to-first-
    result for throughput. Whether the server honors it depends on its
    version, but every result still arrives exactly once either way, so
    callers can opt in without checking the server's capabilities first.
    """
    txn = Transaction()
    for resource_id in ("one", "two", "three", "four", "five"):
        txn.touch(Relationship.from_triple(f"document:{resource_id}", "viewer", "user:alice"))
    await client.write(txn)
    resource_ids = [
        resource.resource_id
        async for resource in client.lookup_resources(
            resource_type="document",
            permission="view",
            subject=SubjectRef(ObjectRef("user", "alice"), None),
            consistency=full(),
            response_batch_size=2,
        )
    ]
    assert set(resource_ids) == {"one", "two", "three", "four", "five"}
    await client.delete_all_relationships_non_atomic(Filter(resource_type="document"))
