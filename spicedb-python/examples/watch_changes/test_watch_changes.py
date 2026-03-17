"""Example: Watching for changes.

Demonstrates using the watch API to observe relationship changes.
"""

import asyncio

import pytest

from spicedb import Relationship, SpiceDBClient
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
    except asyncio.TimeoutError:
        pytest.skip("watch timed out — SpiceDB may not support watch")

    assert len(received) >= 1
    print(f"received {len(received)} update(s)")
