"""Bounded single-RPC deletion and complete deletion with per-page guards."""

import pytest
from spicedb import Filter, Relationship, full
from spicedb.aio import SpiceDBClient
from spicedb.errors import FailedPreconditionError
from spicedb.types import Transaction

pytestmark = pytest.mark.integration


async def test_guarded_delete_succeeds(client: SpiceDBClient):
    txn = Transaction()
    txn.touch(Relationship.from_triple("document:guarded", "owner", "user:alice"))
    txn.touch(Relationship.from_triple("document:guarded", "viewer", "user:bob"))
    await client.write(txn)
    revision = await client.delete_all_relationships_non_atomic(
        Filter(resource_type="document", resource_id="guarded", relation="viewer"),
        must_match=[
            Filter(resource_type="document", resource_id="guarded", relation="owner")
        ],
    )
    assert str(revision.revision)
    remaining = [
        rel
        async for rel in client.read_relationships(
            Filter(resource_type="document", resource_id="guarded", relation="viewer"),
            consistency=full(),
        )
    ]
    assert remaining == []
    await client.delete_all_relationships_non_atomic(
        Filter(resource_type="document", resource_id="guarded")
    )


async def test_guarded_delete_rejected_when_precondition_fails(client: SpiceDBClient):
    txn = Transaction()
    txn.touch(Relationship.from_triple("document:unguarded", "viewer", "user:bob"))
    await client.write(txn)
    with pytest.raises(FailedPreconditionError):
        await client.delete_all_relationships_non_atomic(
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
            consistency=full(),
        )
    ]
    assert len(remaining) == 1
    await client.delete_all_relationships_non_atomic(
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
    assert str(revision.revision)
    remaining = [
        rel
        async for rel in client.read_relationships(
            Filter(resource_type="document", resource_id="limited", relation="viewer"),
            consistency=full(),
        )
    ]
    assert len(remaining) == 2
    await client.delete_all_relationships_non_atomic(
        Filter(resource_type="document", resource_id="limited")
    )


async def test_delete_all_with_small_pages(client: SpiceDBClient):
    txn = Transaction()
    for name in ("carol", "dave", "erin"):
        txn.touch(
            Relationship.from_triple("document:auto-paged", "viewer", f"user:{name}")
        )
    await client.write(txn)
    revision = await client.delete_all_relationships_non_atomic(
        Filter(resource_type="document", resource_id="auto-paged", relation="viewer"),
        page_size=1,
    )
    assert str(revision.revision)
    remaining = [
        rel
        async for rel in client.read_relationships(
            Filter(
                resource_type="document", resource_id="auto-paged", relation="viewer"
            ),
            consistency=full(),
        )
    ]
    assert remaining == []
