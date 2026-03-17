"""Example: Resource lookup.

Demonstrates finding resources a subject can access.
"""

import pytest

from spicedb import Relationship, SpiceDBClient, full
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
    async for resource_id in client.lookup_resources(
        "document", "view", ("user:alice", ""), full()
    ):
        print(f"alice can view document:{resource_id}")
        resource_ids.append(resource_id)

    assert "readme" in resource_ids
    assert "design" in resource_ids
