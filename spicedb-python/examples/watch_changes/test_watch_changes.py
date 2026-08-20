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

import asyncio

import pytest

from spicedb import Filter, Relationship, Update, UpdateOperation
from spicedb.aio import SpiceDBClient
from spicedb.types import Transaction


pytestmark = pytest.mark.integration


# Bounds the wait for the update this example wrote to come back out of the
# stream. Generous for a local SpiceDB; expiry is a failure, not a skip.
UPDATE_TIMEOUT = 30.0

# Bounds how long abandoning the stream may take.
RELEASE_TIMEOUT = 10.0


async def test_watch_changes(client: SpiceDBClient):
    # Write an initial relationship to get a starting revision. Watching from
    # it means the stream cannot replay what an earlier example left behind,
    # and cannot miss the write made below.
    txn = Transaction()
    txn.touch(Relationship.from_triple("document:watched", "viewer", "user:seed"))
    revision = await client.write(txn)

    # Write the relationship whose update this example is waiting for. It is
    # written *after* the subscription revision is fixed, so the stream is
    # guaranteed to carry it. (The `client` fixture clears `document` first, so
    # this is a real change: a TOUCH of an already-identical relationship is
    # not a change and produces no event.)
    txn2 = Transaction()
    txn2.touch(Relationship.from_triple("document:watched", "editor", "user:bob"))
    await client.write(txn2)

    events = client.watch(object_types=["document"], start_revision=revision)

    received: list[Update] = []
    resume_token = None
    try:
        async with asyncio.timeout(UPDATE_TIMEOUT):
            async for event in events:
                received.extend(event.updates)
                if received:
                    # `event.changes_through` is the resume point: keep it and
                    # pass it as `start_revision` on a later `watch()` call to
                    # pick back up after a dropped stream without reprocessing
                    # everything since the original token or silently skipping
                    # the gap by restarting from head.
                    resume_token = event.changes_through
                    break
    except TimeoutError:
        pytest.fail(
            f"no watch event arrived within {UPDATE_TIMEOUT}s for a relationship "
            "written after the subscription revision"
        )
    finally:
        # Abandon the stream explicitly. `aclose()` is the caller-facing
        # cancellation this client exposes for a server stream; the generator's
        # `finally` is what cancels the underlying gRPC call. Bounding it means
        # a close that never returns fails the example instead of hanging it.
        await asyncio.wait_for(events.aclose(), timeout=RELEASE_TIMEOUT)

    # Closing has to actually finish the generator, not merely return.
    with pytest.raises(StopAsyncIteration):
        await events.__anext__()

    assert resume_token
    assert len(received) == 1, (
        f"expected exactly the one update written after the subscription revision, "
        f"got {[str(u.relationship) for u in received]}"
    )

    # The update must be the one that was written, not merely "an update".
    update = received[0]
    assert isinstance(update.relationship, Relationship)
    assert update.relationship.resource_type == "document"
    assert update.relationship.resource_id == "watched"
    assert update.relationship.resource_relation == "editor"
    assert update.relationship.subject_type == "user"
    assert update.relationship.subject_id == "bob"
    # TOUCH is a write, so it can only be the mapping for an explicit
    # OPERATION_TOUCH -- never a default an unrecognized operation falls into.
    assert update.operation in (UpdateOperation.CREATE, UpdateOperation.TOUCH)

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

    events = client.watch(object_types=["document"], include_checkpoints=True)

    async def watch_task():
        nonlocal seen_checkpoint, seen_update
        async for event in events:
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
        await asyncio.wait_for(watch, timeout=UPDATE_TIMEOUT)
    except TimeoutError:
        watch.cancel()
        pytest.fail(
            f"did not observe both a checkpoint and an update within {UPDATE_TIMEOUT}s -- "
            "include_checkpoints did not reach the server, or updates are not "
            "being delivered"
        )
    finally:
        await asyncio.wait_for(events.aclose(), timeout=RELEASE_TIMEOUT)

    assert seen_checkpoint
    assert seen_update

    await client.delete_relationships(
        Filter(resource_type="document", resource_id="watched")
    )


async def test_cancelling_the_consumer_stops_it(client: SpiceDBClient):
    """A caller that walks away mid-stream must not be left waiting.

    The consumer here is parked on a quiet watch stream -- nothing is being
    written, so nothing will ever arrive -- and cancelling its task has to end
    it. A stream that ignored cancellation would leave this task running and
    fail on RELEASE_TIMEOUT.
    """
    events = client.watch(object_types=["document"])

    started = asyncio.Event()

    async def consume():
        started.set()
        async for _event in events:
            pass

    consumer = asyncio.create_task(consume())
    await asyncio.wait_for(started.wait(), timeout=RELEASE_TIMEOUT)
    # Give the stream a moment to actually open before abandoning it.
    await asyncio.sleep(0.1)

    consumer.cancel()
    done, pending = await asyncio.wait({consumer}, timeout=RELEASE_TIMEOUT)
    assert not pending, (
        f"the watch consumer was still running {RELEASE_TIMEOUT}s after being "
        "cancelled: abandoning the stream did not stop it"
    )
    assert consumer.cancelled()
