"""Example: Writing relationships with transaction builder.

Demonstrates create, touch, delete, and preconditions.
"""

import pytest

from spicedb import Filter, Relationship, SpiceDBClient, full
from spicedb.types import Transaction


pytestmark = pytest.mark.integration


async def test_write_relationships(client: SpiceDBClient):
    # Touch relationships (create or update)
    txn = Transaction()
    txn.touch(Relationship.from_triple("document:firstdoc", "viewer", "user:alice"))
    txn.touch(Relationship.from_triple("document:firstdoc", "editor", "user:bob"))

    # Add a precondition: mallory must NOT be an owner
    txn.must_not_match(
        Filter(
            resource_type="document",
            resource_id="firstdoc",
            relation="editor",
            subject_type="user",
            subject_id="mallory",
        )
    )

    revision = await client.write(txn)
    print(f"wrote relationships at revision: {revision}")
    assert revision  # non-empty revision string
