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
