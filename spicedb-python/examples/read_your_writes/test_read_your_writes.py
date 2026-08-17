"""Example: Read-your-writes via checked_at / looked_up_at.

Before this client exposed CheckPermissionResponse.checked_at and
LookupResourcesResponse/LookupSubjectsResponse.looked_up_at, a caller had no
public way to pin a later call to "at least as fresh as this earlier
check/lookup" -- the token existed on the wire but was dropped by every
mapper. This example proves those tokens are real, usable revisions: write a
relationship, take the revision the write returns, and use it (via
at_least()) to make a subsequent check/lookup observe that write.
"""

import pytest

from spicedb import Filter, Relationship
from spicedb.aio import SpiceDBClient
from spicedb.consistency import at_least
from spicedb.types import Transaction


pytestmark = pytest.mark.integration


async def test_check_observes_a_write_via_its_revision(client: SpiceDBClient):
    rel = Relationship.from_triple("document:rwdoc", "viewer", "user:alice")

    # Write a relationship and take the revision the write returns.
    txn = Transaction()
    txn.touch(rel)
    written_at = await client.write(txn)
    print(f"wrote at revision: {written_at}")
    assert written_at

    # A check pinned to at_least(written_at) MUST observe the write -- this
    # is the read-your-writes contract: threading a write's own revision
    # into a later call guarantees that call sees at least that write, with
    # no need to guess at a sleep/retry to avoid a race.
    result = await client.check_permission(at_least(written_at), rel)
    print(f"alice can view document:rwdoc (at_least written_at): {result.has_permission}")
    assert result.has_permission is True

    # checked_at is itself a revision token -- it can be threaded forward
    # the same way to make a still-later read observe this check (and
    # everything the check itself observed).
    assert result.checked_at

    # Clean up so later examples that write a narrower schema aren't blocked
    # by leftover relationships.
    await client.delete_relationships(
        Filter(resource_type="document", resource_id="rwdoc")
    )


async def test_lookup_resources_observes_a_write_via_its_revision(
    client: SpiceDBClient,
):
    rel = Relationship.from_triple("document:rwdoc2", "viewer", "user:alice")

    txn = Transaction()
    txn.touch(rel)
    written_at = await client.write(txn)
    assert written_at

    # lookup_resources() carries the same fix: each yielded LookupResource
    # has looked_up_at, the revision that result was computed at. Pinning
    # the lookup itself to at_least(written_at) makes it observe the write
    # above -- the same read-your-writes guarantee, for the lookup surface.
    found = [
        r
        async for r in client.lookup_resources(
            "document", "view", ("user:alice", ""), at_least(written_at)
        )
        if r.resource_id == "rwdoc2"
    ]
    assert found, "lookup_resources() did not observe the write via at_least(written_at)"
    assert found[0].looked_up_at

    # Clean up so later examples that write a narrower schema aren't blocked
    # by leftover relationships.
    await client.delete_relationships(
        Filter(resource_type="document", resource_id="rwdoc2")
    )
