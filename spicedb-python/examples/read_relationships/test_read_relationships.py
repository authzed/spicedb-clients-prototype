"""Example: Reading relationships with async iterator.

Demonstrates read_relationships with a filter.
"""

import pytest

from spicedb import Filter, Relationship, full
from spicedb.aio import SpiceDBClient
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

    # The full set comes back -- alice and bob, but not charlie (an editor, not
    # a viewer). `>= 2` with two `in` checks passed on a filter that was
    # ignored entirely, since charlie would just be a third result; the sync
    # twin of this example got it right, and this one now matches it.
    assert len(found) == 2
    subject_ids = {r.subject_id for r in found}
    assert subject_ids == {"alice", "bob"}

    # Clean up so later examples that write a narrower schema aren't blocked
    # by leftover relationships.
    await client.delete_relationships(
        Filter(resource_type="document", resource_id="firstdoc")
    )
