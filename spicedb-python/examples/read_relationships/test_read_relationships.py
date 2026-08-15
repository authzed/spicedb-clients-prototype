"""Example: Reading relationships with async iterator.

Demonstrates read_relationships with a filter.
"""

import pytest

from spicedb import Filter, Relationship, SpiceDBClient, full
from spicedb.types import Transaction


pytestmark = pytest.mark.integration


async def test_read_relationships(client: SpiceDBClient):
    # Set up some relationships
    txn = Transaction()
    txn.touch(Relationship.from_triple("document:firstdoc", "viewer", "user:alice"))
    txn.touch(Relationship.from_triple("document:firstdoc", "viewer", "user:bob"))
    txn.touch(Relationship.from_triple("document:firstdoc", "editor", "user:charlie"))
    await client.write(txn)

    # Read all viewers of document:firstdoc
    filter = Filter(
        resource_type="document",
        resource_id="firstdoc",
        relation="viewer",
    )

    found = []
    async for rel in client.read_relationships(filter, full()):
        print(f"found relationship: {rel.subject_type}:{rel.subject_id}")
        found.append(rel)

    assert len(found) >= 2
    subject_ids = {r.subject_id for r in found}
    assert "alice" in subject_ids
    assert "bob" in subject_ids

    # Clean up so later examples that write a narrower schema aren't blocked
    # by leftover relationships.
    await client.delete_relationships(
        Filter(resource_type="document", resource_id="firstdoc")
    )
