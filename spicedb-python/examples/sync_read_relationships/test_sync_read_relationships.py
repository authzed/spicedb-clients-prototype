"""Example: Reading relationships from synchronous code.

`read_relationships` returns a plain `Iterator[Relationship]` -- consume it
with a regular `for` loop, no `async for` required. Pagination across
multiple RPC pages happens transparently inside the iterator; the loop below
doesn't need to know or care how many pages the server used to deliver the
results.
"""

import pytest

from conftest import ENDPOINT, SCHEMA, TOKEN, clear_documents_sync
from spicedb import Filter, Relationship, full
from spicedb.sync import SpiceDBClient
from spicedb.types import Transaction


pytestmark = pytest.mark.integration


def test_read_relationships_sync():
    with SpiceDBClient(
        ENDPOINT, token=TOKEN, insecure=True
    ) as client:
        # Clear before writing the schema -- see conftest.clear_documents.
        clear_documents_sync(client)
        client.write_schema(SCHEMA)

        # Set up more than one relationship.
        txn = Transaction()
        txn.touch(
            Relationship.from_triple("document:sync-firstdoc", "viewer", "user:alice")
        )
        txn.touch(
            Relationship.from_triple("document:sync-firstdoc", "viewer", "user:bob")
        )
        txn.touch(
            Relationship.from_triple(
                "document:sync-firstdoc", "editor", "user:charlie"
            )
        )
        client.write(txn)

        # Read all viewers of document:sync-firstdoc with a plain for loop.
        filter = Filter(
            resource_type="document",
            resource_id="sync-firstdoc",
            relation="viewer",
        )

        found = []
        for rel in client.read_relationships(filter, full()):
            print(f"found relationship: {rel.subject_type}:{rel.subject_id}")
            found.append(rel)

        # The full set comes back -- alice and bob, but not charlie (an
        # editor, not a viewer).
        assert len(found) == 2
        subject_ids = {r.subject_id for r in found}
        assert subject_ids == {"alice", "bob"}

        # Clean up so later examples that write a narrower schema aren't
        # blocked by leftover relationships.
        client.delete_relationships(
            Filter(resource_type="document", resource_id="sync-firstdoc")
        )
