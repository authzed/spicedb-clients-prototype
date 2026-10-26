"""Example: Reading relationships from synchronous code.

`read_relationships` returns a plain `Iterator[Relationship]` -- consume it
with a regular `for` loop, no `async for` required. Pagination across
multiple RPC pages happens transparently inside the iterator; the loop below
doesn't need to know or care how many pages the server used to deliver the
results.
"""
from spicedb import TransportSecurity

import pytest
from conftest import ENDPOINT, SCHEMA, TOKEN, clear_documents_sync
from spicedb import Filter, Relationship, full
from spicedb.sync import SpiceDBClient
from spicedb.types import Transaction

pytestmark = pytest.mark.integration


def test_read_relationships_sync():
    with SpiceDBClient(ENDPOINT, token=TOKEN, transport_security=TransportSecurity.PLAINTEXT) as client:
        clear_documents_sync(client)
        client.write_schema(SCHEMA)
        txn = Transaction()
        txn.touch(
            Relationship.from_triple("document:sync-firstdoc", "viewer", "user:alice")
        )
        txn.touch(
            Relationship.from_triple("document:sync-firstdoc", "viewer", "user:bob")
        )
        txn.touch(
            Relationship.from_triple("document:sync-firstdoc", "editor", "user:charlie")
        )
        client.write(txn)
        filter = Filter(
            resource_type="document", resource_id="sync-firstdoc", relation="viewer"
        )
        found = []
        for rel in client.read_relationships(filter, consistency=full()):
            print(
                f"found relationship: {rel.subject.object.object_type}:{rel.subject.object.object_id}"
            )
            found.append(rel)
        assert len(found) == 2
        subject_ids = {r.subject.object.object_id for r in found}
        assert subject_ids == {"alice", "bob"}
        client.delete_all_relationships_non_atomic(
            Filter(resource_type="document", resource_id="sync-firstdoc")
        )
