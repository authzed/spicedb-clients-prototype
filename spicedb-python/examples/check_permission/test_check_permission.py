"""Example: Basic permission check.

Demonstrates check_permission, check_permissions, check_any, and check_all.
See examples/caveated_check/ for the CONDITIONAL_PERMISSION case (a caveated
relationship checked without its context) and examples/read_your_writes/ for
using CheckResult.checked_at to make a later read observe this check.
"""

import pytest

from spicedb import Filter, Relationship, full
from spicedb.aio import SpiceDBClient
from spicedb.consistency import at_least
from spicedb.types import Transaction


pytestmark = pytest.mark.integration


async def test_check_permission(client: SpiceDBClient):
    # Write a relationship: alice is a viewer of document:readme
    txn = Transaction()
    txn.touch(Relationship.from_triple("document:readme", "viewer", "user:alice"))
    txn.touch(Relationship.from_triple("document:readme", "editor", "user:bob"))
    revision = await client.write(txn)

    # Single permission check with full consistency. check_permission()
    # returns a CheckResult, not a bare bool -- .has_permission is true ONLY
    # for a full HAS_PERMISSION grant.
    rel = Relationship.from_triple("document:readme", "view", "user:alice")
    result = await client.check_permission(full(), rel)
    print(f"user:alice can view document:readme: {result.has_permission}")
    assert result.has_permission is True

    # checked_at is the revision this check was evaluated at -- thread it
    # into at_least() to make a later read observe this check
    # (read-your-writes for checks). See examples/read_your_writes/ for the
    # end-to-end demonstration.
    assert result.checked_at

    # Check with at_least consistency
    result_at = await client.check_permission(at_least(revision), rel)
    print(f"(at_least) user:alice can view document:readme: {result_at.has_permission}")
    assert result_at.has_permission is True

    # Bulk check: multiple permissions at once
    checks = [
        Relationship.from_triple("document:readme", "view", "user:alice"),
        Relationship.from_triple("document:readme", "edit", "user:alice"),
        Relationship.from_triple("document:readme", "view", "user:bob"),
    ]
    results = await client.check_permissions(full(), *checks)
    print(f"Bulk check results: {[r.has_permission for r in results]}")
    assert results[0].has_permission is True  # alice can view (she's a viewer)
    assert results[1].has_permission is False  # alice cannot edit
    assert results[2].has_permission is True  # bob can view (editor implies view)

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

    # Clean up so later examples that write a narrower schema aren't blocked
    # by leftover relationships.
    await client.delete_relationships(
        Filter(resource_type="document", resource_id="readme")
    )
