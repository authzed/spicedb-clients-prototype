"""Example: Basic permission check.

Demonstrates check_permission, check_permissions, check_any, and check_all.
"""

import pytest

from spicedb import Relationship, SpiceDBClient, full
from spicedb.consistency import at_least
from spicedb.types import Transaction


pytestmark = pytest.mark.integration


async def test_check_permission(client: SpiceDBClient):
    # Write a relationship: alice is a viewer of document:readme
    txn = Transaction()
    txn.touch(Relationship.from_triple("document:readme", "viewer", "user:alice"))
    txn.touch(Relationship.from_triple("document:readme", "editor", "user:bob"))
    revision = await client.write(txn)

    # Single permission check with full consistency
    rel = Relationship.from_triple("document:readme", "view", "user:alice")
    allowed = await client.check_permission(full(), rel)
    print(f"user:alice can view document:readme: {allowed}")
    assert allowed is True

    # Check with at_least consistency
    allowed_at = await client.check_permission(at_least(revision), rel)
    print(f"(at_least) user:alice can view document:readme: {allowed_at}")
    assert allowed_at is True

    # Bulk check: multiple permissions at once
    checks = [
        Relationship.from_triple("document:readme", "view", "user:alice"),
        Relationship.from_triple("document:readme", "edit", "user:alice"),
        Relationship.from_triple("document:readme", "view", "user:bob"),
    ]
    results = await client.check_permissions(full(), *checks)
    print(f"Bulk check results: {results}")
    assert results[0] is True   # alice can view (she's a viewer)
    assert results[1] is False  # alice cannot edit
    assert results[2] is True   # bob can view (he's an editor, editor implies view)

    # check_any: does alice have any of view or edit?
    has_any = await client.check_any(
        full(),
        Relationship.from_triple("document:readme", "view", "user:alice"),
        Relationship.from_triple("document:readme", "edit", "user:alice"),
    )
    print(f"user:alice has any permission: {has_any}")
    assert has_any is True

    # check_all: does bob have both view and edit?
    has_all = await client.check_all(
        full(),
        Relationship.from_triple("document:readme", "view", "user:bob"),
        Relationship.from_triple("document:readme", "edit", "user:bob"),
    )
    print(f"user:bob has all permissions: {has_all}")
    assert has_all is True
