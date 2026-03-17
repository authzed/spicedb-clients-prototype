"""Example: Subject lookup.

Demonstrates finding subjects with access to a resource.
"""

import pytest

from spicedb import Relationship, SpiceDBClient, full
from spicedb.types import Transaction


pytestmark = pytest.mark.integration


async def test_lookup_subjects(client: SpiceDBClient):
    # Set up relationships
    txn = Transaction()
    txn.touch(Relationship.from_triple("document:firstdoc", "viewer", "user:alice"))
    txn.touch(Relationship.from_triple("document:firstdoc", "editor", "user:bob"))
    await client.write(txn)

    # Find all users who can view document:firstdoc
    subject_ids = []
    async for subject_id in client.lookup_subjects(
        ("document", "firstdoc"), "view", "user", full()
    ):
        print(f"user:{subject_id} can view document:firstdoc")
        subject_ids.append(subject_id)

    assert "alice" in subject_ids
    assert "bob" in subject_ids  # editors can also view
