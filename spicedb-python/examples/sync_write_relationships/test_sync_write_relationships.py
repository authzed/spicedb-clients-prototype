"""Example: Writing relationships from synchronous code with the transaction
builder.

Demonstrates create, touch, delete, and preconditions, all built with
`Transaction` and executed with a single blocking `write` call -- no
`await` anywhere in this file. `write` returns the revision string directly.
"""

import pytest

from conftest import ENDPOINT, SCHEMA, TOKEN, clear_documents_sync
from spicedb import Filter, Relationship, full
from spicedb.sync import SpiceDBClient
from spicedb.types import Transaction


pytestmark = pytest.mark.integration


def test_write_relationships_sync():
    with SpiceDBClient(
        ENDPOINT, token=TOKEN, insecure=True
    ) as client:
        # Clear before writing the schema -- see conftest.clear_documents.
        clear_documents_sync(client)
        client.write_schema(SCHEMA)

        # `create` fails if the relationship already exists; safe here since
        # this is a fresh resource. `touch` creates or updates.
        txn = Transaction()
        txn.create(
            Relationship.from_triple("document:sync-report", "owner", "user:priya")
        )
        txn.touch(
            Relationship.from_triple("document:sync-report", "editor", "user:bob")
        )

        # Add a precondition: mallory must NOT be an editor.
        txn.must_not_match(
            Filter(
                resource_type="document",
                resource_id="sync-report",
                relation="editor",
                subject_type="user",
                subject_id="mallory",
            )
        )

        revision = client.write(txn)
        print(f"wrote relationships at revision: {revision}")
        assert isinstance(revision, str)
        assert revision  # non-empty revision string

        # `delete` removes a specific relationship, same builder as above.
        delete_txn = Transaction()
        delete_txn.delete(
            Relationship.from_triple("document:sync-report", "editor", "user:bob")
        )
        revision = client.write(delete_txn)
        assert revision

        remaining = list(
            client.read_relationships(
                Filter(
                    resource_type="document",
                    resource_id="sync-report",
                    relation="editor",
                ),
                full(),
            )
        )
        assert remaining == []

        # Clean up so later examples that write a narrower schema aren't
        # blocked by leftover relationships.
        client.delete_relationships(
            Filter(resource_type="document", resource_id="sync-report")
        )
