"""Example: Watching for changes.

Demonstrates using the watch API with a *bounded* consumer: subscribe from a
known revision, make a write that must produce a specific update, consume until
exactly that update has been observed, then close the stream explicitly.

Watch is an open-ended server stream -- it never completes on its own -- so an
example that just consumes it cannot end, and an example that consumes it and
skips on timeout cannot fail. Both assertions below are unconditional, and the
timeout is a failure rather than a `pytest.skip`.

On what these examples can and cannot prove about release: `aclose()` is the
caller-facing abandonment path, and this example requires it to finish promptly
and to leave the generator exhausted. Whether the *transport* then tells the
server to stop is only observable from a server that reports it, which is why
that half lives in `tests/test_stream_release.py` against a parked stub server.
See root DESIGN.md, "RULE: Abandoning a stream must release it".
"""
from spicedb import WatchContent

import asyncio
import pytest
from spicedb import Filter, Relationship, Update, UpdateOperation
from spicedb.aio import SpiceDBClient
from spicedb.types import Transaction

pytestmark = pytest.mark.integration
UPDATE_TIMEOUT = 30.0
RELEASE_TIMEOUT = 10.0


async def test_watch_changes(client: SpiceDBClient):
    txn = Transaction()
    txn.touch(Relationship.from_triple("document:watched", "viewer", "user:seed"))
    revision = (await client.write(txn)).revision
    txn2 = Transaction()
    txn2.touch(Relationship.from_triple("document:watched", "editor", "user:bob"))
    await client.write(txn2)
    events = client.watch(filters=[Filter("document")], start_revision=revision)
    received: list[Update] = []
    resume_token = None
    try:
        async with asyncio.timeout(UPDATE_TIMEOUT):
            async for event in events:
                received.extend(event.updates)
                if received:
                    resume_token = event.changes_through
                    break
    except TimeoutError:
        pytest.fail(
            f"no watch event arrived within {UPDATE_TIMEOUT}s for a relationship written after the subscription revision"
        )
    finally:
        await asyncio.wait_for(events.aclose(), timeout=RELEASE_TIMEOUT)
    with pytest.raises(StopAsyncIteration):
        await events.__anext__()
    assert resume_token
    assert len(received) == 1, (
        f"expected exactly the one update written after the subscription revision, got {[str(u.relationship) for u in received]}"
    )
    update = received[0]
    assert isinstance(update.relationship, Relationship)
    assert update.relationship.resource.object_type == "document"
    assert update.relationship.resource.object_id == "watched"
    assert update.relationship.relation == "editor"
    assert update.relationship.subject.object.object_type == "user"
    assert update.relationship.subject.object.object_id == "bob"
    assert update.operation in (UpdateOperation.CREATE, UpdateOperation.TOUCH)
    await client.delete_all_relationships_non_atomic(
        Filter(resource_type="document", resource_id="watched")
    )


async def test_watch_changes_with_checkpoints(client: SpiceDBClient):
    """`content=(WatchContent.RELATIONSHIPS, WatchContent.CHECKPOINTS)` asks the server for periodic checkpoint
    events in addition to relationship updates -- recommended behind a proxy
    that aborts idle connections, since a checkpoint keeps the stream alive
    even when nothing has changed. A checkpoint carries no updates, so a
    consumer must branch on `event.is_checkpoint` to tell "nothing changed,
    here is a fresh resume point" from "here are changes"."""
    seen_checkpoint = False
    seen_update = False
    events = client.watch(filters=[Filter("document")], content=(WatchContent.RELATIONSHIPS, WatchContent.CHECKPOINTS))

    async def watch_task():
        nonlocal seen_checkpoint, seen_update
        async for event in events:
            if event.is_checkpoint:
                assert event.updates == ()
                seen_checkpoint = True
            elif event.updates:
                seen_update = True
            if seen_checkpoint and seen_update:
                return

    watch = asyncio.create_task(watch_task())
    await asyncio.sleep(0.1)
    txn = Transaction()
    txn.touch(Relationship.from_triple("document:watched", "viewer", "user:carol"))
    await client.write(txn)
    try:
        await asyncio.wait_for(watch, timeout=UPDATE_TIMEOUT)
    except TimeoutError:
        watch.cancel()
        pytest.fail(
            f"did not observe both a checkpoint and an update within {UPDATE_TIMEOUT}s -- include_checkpoints did not reach the server, or updates are not being delivered"
        )
    finally:
        await asyncio.wait_for(events.aclose(), timeout=RELEASE_TIMEOUT)
    assert seen_checkpoint
    assert seen_update
    await client.delete_all_relationships_non_atomic(
        Filter(resource_type="document", resource_id="watched")
    )


async def test_cancelling_the_consumer_stops_it(client: SpiceDBClient):
    """A caller that walks away mid-stream must not be left waiting.

    The consumer here is parked on a quiet watch stream -- nothing is being
    written, so nothing will ever arrive -- and cancelling its task has to end
    it. A stream that ignored cancellation would leave this task running and
    fail on RELEASE_TIMEOUT.
    """
    events = client.watch(filters=[Filter("document")])
    started = asyncio.Event()

    async def consume():
        started.set()
        async for _event in events:
            pass

    consumer = asyncio.create_task(consume())
    await asyncio.wait_for(started.wait(), timeout=RELEASE_TIMEOUT)
    await asyncio.sleep(0.1)
    consumer.cancel()
    done, pending = await asyncio.wait({consumer}, timeout=RELEASE_TIMEOUT)
    assert not pending, (
        f"the watch consumer was still running {RELEASE_TIMEOUT}s after being cancelled: abandoning the stream did not stop it"
    )
    assert consumer.cancelled()
