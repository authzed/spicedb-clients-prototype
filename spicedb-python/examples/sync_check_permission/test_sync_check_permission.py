"""Example: Basic permission check from synchronous code.

Build the client once and reuse it -- there is no event loop to bind to, so a
module-level client is safe for the process lifetime. This is the pattern
most Django/Flask applications want: construct a `SpiceDBClient` once at
startup (e.g. in `settings.py` or an app factory) and hand every request
handler the same instance afterward. `spicedb.aio`'s client can't be built
that way -- its channel binds to whichever event loop is running the first
time it's used, so building it before a loop exists doesn't work, and
building one loop-bound client per request defeats connection reuse. Closing
that gap is exactly why `spicedb.sync` exists.

Demonstrates check_permission, check_permissions, check_any, and check_all,
all reusing the one client below.
"""

import pytest

from conftest import ENDPOINT, SCHEMA, TOKEN
from spicedb import Filter, Relationship, full
from spicedb.consistency import at_least
from spicedb.sync import SpiceDBClient
from spicedb.types import Transaction


pytestmark = pytest.mark.integration

# Built once, at import time -- not inside a fixture, not inside a `with`
# block scoped to a single test. A real app builds exactly one of these at
# startup and never rebuilds it; nothing below closes it early, to prove the
# point.
client = SpiceDBClient(ENDPOINT, token=TOKEN, insecure=True)


def _grant() -> Transaction:
    """Grant user:jimmy the viewer relation on document:readme."""
    txn = Transaction()
    txn.touch(Relationship.from_triple("document:readme", "viewer", "user:jimmy"))
    return txn


def test_check_permission_sync():
    # A real app would call write_schema once at startup too -- it's
    # colocated with the test body here only to keep the example
    # self-contained and independent of test ordering.
    client.write_schema(SCHEMA)
    revision = client.write(_grant())

    # Single permission check with full consistency, on the client built
    # once above -- this is the call every request handler in a real app
    # would make, all sharing the same client instance. check_permission()
    # returns a CheckResult, not a bare bool.
    rel = Relationship.from_triple("document:readme", "view", "user:jimmy")
    result = client.check_permission(full(), rel)
    print(f"user:jimmy can view document:readme: {result.has_permission}")
    assert result.has_permission is True

    other = Relationship.from_triple("document:readme", "view", "user:stranger")
    stranger_result = client.check_permission(full(), other)
    print(f"user:stranger can view document:readme: {stranger_result.has_permission}")
    assert stranger_result.has_permission is False

    # Check again pinned to the write's own revision -- still the same
    # client.
    result_at = client.check_permission(at_least(revision), rel)
    print(f"(at_least) user:jimmy can view document:readme: {result_at.has_permission}")
    assert result_at.has_permission is True

    # Bulk check: multiple permissions in one round trip.
    checks = [
        Relationship.from_triple("document:readme", "view", "user:jimmy"),
        Relationship.from_triple("document:readme", "edit", "user:jimmy"),
    ]
    results = client.check_permissions(full(), *checks)
    print(f"Bulk check results: {[r.has_permission for r in results]}")
    assert results[0].has_permission is True  # jimmy can view (he's a viewer)
    assert results[1].has_permission is False  # jimmy cannot edit

    # check_any / check_all round out the check surface -- same client,
    # again.
    has_any = client.check_any(
        full(),
        Relationship.from_triple("document:readme", "view", "user:jimmy"),
        Relationship.from_triple("document:readme", "edit", "user:jimmy"),
    )
    print(f"user:jimmy has any permission: {has_any}")
    assert has_any is True

    has_all = client.check_all(
        full(),
        Relationship.from_triple("document:readme", "view", "user:jimmy"),
        Relationship.from_triple("document:readme", "edit", "user:jimmy"),
    )
    print(f"user:jimmy has all permissions: {has_all}")
    assert has_all is False

    # Clean up so later examples that write a narrower schema aren't blocked
    # by leftover relationships.
    client.delete_relationships(
        Filter(resource_type="document", resource_id="readme")
    )
