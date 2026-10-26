"""Example: Writing relationships from synchronous code with the transaction
builder.

Demonstrates create, touch, delete, and preconditions, all built with
`Transaction` and executed with a single blocking `write` call -- no
`await` anywhere in this file. `write` returns the ZedToken directly.
"""
from spicedb import ZedToken
from spicedb import TransportSecurity

from spicedb import SubjectFilter
import pytest
from conftest import ENDPOINT, SCHEMA, TOKEN, clear_documents_sync
from spicedb import Filter, Relationship, full
from spicedb.sync import SpiceDBClient
from spicedb.types import Transaction

pytestmark = pytest.mark.integration


def test_write_relationships_sync():
    with SpiceDBClient(ENDPOINT, token=TOKEN, transport_security=TransportSecurity.PLAINTEXT) as client:
        clear_documents_sync(client)
        client.write_schema(SCHEMA)
        txn = Transaction()
        txn.create(
            Relationship.from_triple("document:sync-report", "owner", "user:priya")
        )
        txn.touch(
            Relationship.from_triple("document:sync-report", "editor", "user:bob")
        )
        txn.must_not_match(
            Filter(
                resource_type="document",
                resource_id="sync-report",
                relation="editor",
                subject=SubjectFilter("user", subject_id="mallory"),
            )
        )
        revision = client.write(txn).revision
        print(f"wrote relationships at revision: {revision}")
        assert isinstance(revision, ZedToken)
        assert str(revision)
        delete_txn = Transaction()
        delete_txn.delete(
            Relationship.from_triple("document:sync-report", "editor", "user:bob")
        )
        revision = client.write(delete_txn).revision
        assert str(revision)
        remaining = list(
            client.read_relationships(
                Filter(
                    resource_type="document",
                    resource_id="sync-report",
                    relation="editor",
                ),
                consistency=full(),
            )
        )
        assert remaining == []
        client.delete_all_relationships_non_atomic(
            Filter(resource_type="document", resource_id="sync-report")
        )
