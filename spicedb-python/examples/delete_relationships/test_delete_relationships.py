"""Example: Deleting relationships with optional preconditions and limit.

Demonstrates delete_relationships with must_match/must_not_match
preconditions (guarded deletes) and a limit override.
"""

import pytest

from spicedb import Filter, Relationship, full
from spicedb.aio import SpiceDBClient
from spicedb.errors import FailedPreconditionError
from spicedb.types import Transaction


pytestmark = pytest.mark.integration


async def test_guarded_delete_succeeds(client: SpiceDBClient):
    # A document with an owner and a viewer.
    txn = Transaction()
    txn.touch(Relationship.from_triple("document:guarded", "owner", "user:alice"))
    txn.touch(Relationship.from_triple("document:guarded", "viewer", "user:bob"))
    await client.write(txn)

    # Delete the viewer relationship, but only if the owner relationship
    # still exists (a MUST_MATCH precondition).
    revision = await client.delete_relationships(
        Filter(resource_type="document", resource_id="guarded", relation="viewer"),
        must_match=[
            Filter(resource_type="document", resource_id="guarded", relation="owner")
        ],
    )
    assert revision

    remaining = [
        rel
        async for rel in client.read_relationships(
            Filter(resource_type="document", resource_id="guarded", relation="viewer"),
            full(),
        )
    ]
    assert remaining == []

    # Clean up the remaining owner relationship so later tests that write a
    # narrower schema (e.g. one without an `owner` relation) aren't blocked
    # by leftover relationships.
    await client.delete_relationships(
        Filter(resource_type="document", resource_id="guarded")
    )


async def test_guarded_delete_rejected_when_precondition_fails(client: SpiceDBClient):
    txn = Transaction()
    txn.touch(Relationship.from_triple("document:unguarded", "viewer", "user:bob"))
    await client.write(txn)

    # The MUST_MATCH filter references an owner relationship that does not
    # exist, so the server rejects the whole call — nothing is deleted.
    with pytest.raises(FailedPreconditionError):
        await client.delete_relationships(
            Filter(
                resource_type="document", resource_id="unguarded", relation="viewer"
            ),
            must_match=[
                Filter(
                    resource_type="document", resource_id="unguarded", relation="owner"
                )
            ],
        )

    remaining = [
        rel
        async for rel in client.read_relationships(
            Filter(
                resource_type="document", resource_id="unguarded", relation="viewer"
            ),
            full(),
        )
    ]
    assert len(remaining) == 1

    # Clean up so later tests that write a narrower schema aren't blocked by
    # leftover relationships.
    await client.delete_relationships(
        Filter(resource_type="document", resource_id="unguarded")
    )


async def test_delete_with_limit(client: SpiceDBClient):
    txn = Transaction()
    for name in ("carol", "dave", "erin"):
        txn.touch(
            Relationship.from_triple("document:limited", "viewer", f"user:{name}")
        )
    await client.write(txn)

    revision = await client.delete_relationships(
        Filter(resource_type="document", resource_id="limited", relation="viewer"),
        limit=1,
    )
    assert revision

    remaining = [
        rel
        async for rel in client.read_relationships(
            Filter(resource_type="document", resource_id="limited", relation="viewer"),
            full(),
        )
    ]
    # Only one of the three relationships was deleted (limit=1); this client
    # does not yet auto-page deletes across multiple RPCs.
    assert len(remaining) == 2

    # Clean up the rest so later tests that write a narrower schema aren't
    # blocked by leftover relationships.
    await client.delete_relationships(
        Filter(resource_type="document", resource_id="limited")
    )
