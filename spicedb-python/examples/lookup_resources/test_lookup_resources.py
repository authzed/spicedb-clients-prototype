"""Example: Resource lookup.

Demonstrates finding resources a subject can access, including how to read
the ``permissionship`` of each result. A ``Permissionship.CONDITIONAL_PERMISSION``
result means the match depends on caveat context that wasn't supplied —
callers must not treat it as an unconditional grant.
"""

import pytest

from spicedb import Permissionship, Relationship, SpiceDBClient, full
from spicedb.types import Transaction


pytestmark = pytest.mark.integration


async def test_lookup_resources(client: SpiceDBClient):
    # Set up relationships
    txn = Transaction()
    txn.touch(Relationship.from_triple("document:readme", "viewer", "user:alice"))
    txn.touch(Relationship.from_triple("document:design", "viewer", "user:alice"))
    txn.touch(Relationship.from_triple("document:secret", "viewer", "user:bob"))
    await client.write(txn)

    # Find all documents alice can view
    resource_ids = []
    async for resource in client.lookup_resources(
        "document", "view", ("user:alice", ""), full()
    ):
        print(
            f"alice can view document:{resource.resource_id} "
            f"(permissionship={resource.permissionship})"
        )
        # A conditional result means caveat context is missing;
        # resource.partial_caveat lists which context. Never treat a
        # conditional result as a full grant.
        assert resource.permissionship == Permissionship.HAS_PERMISSION, (
            f"unexpected permissionship for document:{resource.resource_id}: "
            f"{resource.permissionship} (missing context: {resource.partial_caveat})"
        )
        resource_ids.append(resource.resource_id)

    assert "readme" in resource_ids
    assert "design" in resource_ids
