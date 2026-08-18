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

    resume_token = None

    async def watch_task():
        nonlocal resume_token
        async for event in client.watch(
            object_types=["document"],
            start_revision=revision,
        ):
            for update in event.updates:
                received.append(update)
            if received:
                # `event.changes_through` is the resume point: keep it and
                # pass it as `start_revision` on a later `watch()` call to
                # pick back up after a dropped stream without reprocessing
                # everything since the original token or silently skipping
                # the gap by restarting from head.
                resume_token = event.changes_through
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
    assert resume_token
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


async def test_watch_changes_with_checkpoints(client: SpiceDBClient):
    """`include_checkpoints=True` asks the server for periodic checkpoint
    events in addition to relationship updates -- recommended behind a proxy
    that aborts idle connections, since a checkpoint keeps the stream alive
    even when nothing has changed. A checkpoint carries no updates, so a
    consumer must branch on `event.is_checkpoint` to tell "nothing changed,
    here is a fresh resume point" from "here are changes"."""
    seen_checkpoint = False
    seen_update = False

    async def watch_task():
        nonlocal seen_checkpoint, seen_update
        async for event in client.watch(
            object_types=["document"], include_checkpoints=True
        ):
            if event.is_checkpoint:
                assert event.updates == []
                seen_checkpoint = True
            elif event.updates:
                seen_update = True
            if seen_checkpoint and seen_update:
                return

    watch = asyncio.create_task(watch_task())
    await asyncio.sleep(0.1)  # let watch start

    txn = Transaction()
    txn.touch(Relationship.from_triple("document:watched", "viewer", "user:carol"))
    await client.write(txn)

    try:
        await asyncio.wait_for(watch, timeout=5.0)
    except TimeoutError:
        watch.cancel()
        pytest.skip(
            "did not observe both a checkpoint and an update in time — "
            "SpiceDB may not support checkpoints, or emits them too slowly "
            "for this example's timeout"
        )

    assert seen_checkpoint
    assert seen_update

    await client.delete_relationships(
        Filter(resource_type="document", resource_id="watched")
    )
