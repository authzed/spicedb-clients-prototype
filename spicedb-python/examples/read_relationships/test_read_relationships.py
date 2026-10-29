"""Example: Reading relationships with async iterator.

Demonstrates read_relationships with a filter.
"""

import pytest
from spicedb import Filter, Paging, Relationship, full
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
    async for item in client.read_relationships(filter, consistency=full()):
        # Each item carries its after-result cursor and read-at revision.
        rel = item.relationship
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


async def test_single_request_and_empty_writes(client: SpiceDBClient):
    """``Paging.SINGLE_REQUEST`` sends one request, as the raw API does, and an
    empty write (preconditions only) is sent to SpiceDB, which checks the
    preconditions and returns the current revision."""
    txn = Transaction()
    for user in ("alice", "bob", "carol"):
        txn.touch(Relationship.from_triple("document:paged", "viewer", f"user:{user}"))
    await client.write(txn)
    doc = Filter(resource_type="document", resource_id="paged")

    # page_size is ignored: the one request is unbounded, so all three arrive.
    items = [
        item
        async for item in client.read_relationships(
            doc, consistency=full(), page_size=1, paging=Paging.SINGLE_REQUEST
        )
    ]
    assert len(items) == 3 and all(item.after_result_cursor for item in items)

    revision = (await client.write(Transaction().must_match(doc))).revision
    assert str(revision)
    await client.delete_all_relationships_non_atomic(doc)
