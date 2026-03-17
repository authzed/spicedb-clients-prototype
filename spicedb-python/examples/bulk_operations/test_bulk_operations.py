"""Example: Bulk checks and imports.

Demonstrates bulk permission checks and bulk relationship import/export.
"""

import pytest

from spicedb import Relationship, SpiceDBClient
from spicedb.consistency import at_least
from spicedb.types import Transaction


pytestmark = pytest.mark.integration


async def test_bulk_checks(client: SpiceDBClient):
    # Bulk write relationships
    users = ["alice", "bob", "charlie"]
    txn = Transaction()
    for user in users:
        txn.touch(
            Relationship.from_triple("document:report", "viewer", f"user:{user}")
        )
    revision = await client.write(txn)
    print(f"wrote {len(users)} relationships at revision: {revision}")

    # Bulk check permissions
    checks = [
        Relationship.from_triple("document:report", "view", f"user:{user}")
        for user in users
    ]
    results = await client.check_permissions(at_least(revision), *checks)

    for user, result in zip(users, results):
        print(f"user:{user} can view document:report: {result}")
        assert result is True

    # check_all — verify all users have permission
    all_allowed = await client.check_all(at_least(revision), *checks)
    print(f"all users can view: {all_allowed}")
    assert all_allowed is True

    # check_any — verify at least one user has permission
    any_allowed = await client.check_any(at_least(revision), *checks)
    print(f"any user can view: {any_allowed}")
    assert any_allowed is True
