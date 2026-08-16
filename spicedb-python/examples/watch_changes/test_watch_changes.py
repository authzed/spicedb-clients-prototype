"""Example: Watching for changes.

Demonstrates using the watch API to observe relationship changes.
"""

import asyncio

import pytest

from spicedb import Filter, Relationship, UpdateOperation
from spicedb.aio import SpiceDBClient
from spicedb.types import Transaction


pytestmark = pytest.mark.integration


async def test_watch_changes(client: SpiceDBClient):
    # Write an initial relationship to get a starting revision
    txn = Transaction()
    txn.touch(Relationship.from_triple("document:watched", "viewer", "user:alice"))
    revision = await client.write(txn)

    # Start watching from that revision
    received = []

    async def watch_task():
        async for updates, rev in client.watch(
            object_types=["document"],
            start_revision=revision,
        ):
            for update in updates:
                received.append(update)
            if received:
                return

    # Write another relationship to trigger a watch event
    txn2 = Transaction()
    txn2.touch(Relationship.from_triple("document:watched", "editor", "user:bob"))

    # Run watch and write concurrently
    watch = asyncio.create_task(watch_task())
    await asyncio.sleep(0.1)  # let watch start
    await client.write(txn2)

    try:
        await asyncio.wait_for(watch, timeout=5.0)
    except TimeoutError:
        pytest.skip("watch timed out — SpiceDB may not support watch")

    assert len(received) >= 1
    for update in received:
        # `update` is a native `spicedb.Update` — no proto types leak here.
        assert update.operation in (
            UpdateOperation.CREATE,
            UpdateOperation.TOUCH,
            UpdateOperation.DELETE,
        )
        assert isinstance(update.relationship, Relationship)
    print(
        f"received {len(received)} update(s): {[u.operation.value for u in received]}"
    )

    # Clean up so later examples that write a narrower schema aren't blocked
    # by leftover relationships.
    await client.delete_relationships(
        Filter(resource_type="document", resource_id="watched")
    )
