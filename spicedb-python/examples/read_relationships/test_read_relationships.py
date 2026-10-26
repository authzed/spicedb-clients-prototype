"""Example: Reading relationships with async iterator.

Demonstrates read_relationships with a filter.
"""

import pytest
from spicedb import Filter, Relationship, full
from spicedb.aio import SpiceDBClient
from spicedb.types import Transaction

pytestmark = pytest.mark.integration


async def test_read_relationships(client: SpiceDBClient):
    txn = Transaction()
    txn.touch(Relationship.from_triple("document:firstdoc", "viewer", "user:alice"))
    txn.touch(Relationship.from_triple("document:firstdoc", "viewer", "user:bob"))
    txn.touch(Relationship.from_triple("document:firstdoc", "editor", "user:charlie"))
    await client.write(txn)
    filter = Filter(resource_type="document", resource_id="firstdoc", relation="viewer")
    found = []
    async for rel in client.read_relationships(filter, consistency=full()):
        print(
            f"found relationship: {rel.subject.object.object_type}:{rel.subject.object.object_id}"
        )
        found.append(rel)
    assert len(found) == 2
    subject_ids = {r.subject.object.object_id for r in found}
    assert subject_ids == {"alice", "bob"}
    await client.delete_all_relationships_non_atomic(
        Filter(resource_type="document", resource_id="firstdoc")
    )
