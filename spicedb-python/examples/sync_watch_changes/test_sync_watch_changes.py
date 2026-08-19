"""Example: Watching for changes from synchronous code.

`watch` is a blocking generator -- there's no `async for` to reach for -- so a
consumer that has to stay responsive while the stream is quiet needs a second
thread. This mirrors the async example's shape (subscribe from a known
revision, write the update it is waiting for, consume until exactly that update
arrives, then abandon the stream), but the concurrency primitive is
`threading.Thread` instead of `asyncio.create_task`.

Both assertions here are unconditional and the timeouts are failures, not
`pytest.skip`s: an example that skips itself when the server is slow reports
green while proving nothing.

On what this example can and cannot prove about release: `break` is the
caller-facing abandonment path for a sync generator, and closing the generator
runs the `finally` that cancels the underlying gRPC call. This example requires
the consumer to come back and the generator to be exhausted afterwards. Whether
the *transport* then tells the server to stop is only observable from a server
that reports it, which is why that half lives in `tests/test_stream_release.py`
against a parked stub server. See root DESIGN.md, "RULE: Abandoning a stream
must release it".
"""

import threading

import pytest

from conftest import ENDPOINT, SCHEMA, TOKEN, clear_documents_sync
from spicedb import Filter, Relationship, Update, UpdateOperation
from spicedb.sync import SpiceDBClient
from spicedb.types import Transaction


pytestmark = pytest.mark.integration


# Bounds the wait for the update this example wrote to come back out of the
# stream. Generous for a local SpiceDB; expiry is a failure, not a skip.
UPDATE_TIMEOUT = 30.0

# Bounds how long abandoning the stream may take.
RELEASE_TIMEOUT = 10.0


def test_watch_changes_sync():
    with SpiceDBClient(
        ENDPOINT, token=TOKEN, insecure=True
    ) as client:
        # Clear before writing the schema -- see conftest.clear_documents. It
        # also makes the write below a real change: a TOUCH of an
        # already-identical relationship is not a change, and SpiceDB emits no
        # watch event for it.
        clear_documents_sync(client)
        client.write_schema(SCHEMA)

        # Write an initial relationship to fix the revision to watch from.
        txn = Transaction()
        txn.touch(
            Relationship.from_triple("document:sync-watched", "viewer", "user:seed")
        )
        revision = client.write(txn)

        # Write the relationship whose update this example waits for. It lands
        # after the subscription revision, so the stream is guaranteed to carry
        # it and the consumer below cannot block forever on the happy path.
        txn2 = Transaction()
        txn2.touch(
            Relationship.from_triple("document:sync-watched", "editor", "user:bob")
        )
        client.write(txn2)

        events = client.watch(
            object_types=["document"],
            start_revision=revision,
        )

        received: list[Update] = []
        resume_token = None
        failure: list[BaseException] = []

        def watch_task():
            nonlocal resume_token
            try:
                for event in events:
                    received.extend(event.updates)
                    if received:
                        # `event.changes_through` is the resume point for a
                        # later `watch(start_revision=...)` call, so a
                        # consumer can pick back up after a dropped stream
                        # instead of reprocessing everything or silently
                        # skipping the gap by restarting from head.
                        resume_token = event.changes_through
                        # Abandon the stream. `break` closes the generator,
                        # whose `finally` cancels the underlying call.
                        break
                events.close()
            except BaseException as exc:  # noqa: BLE001 - reported below
                failure.append(exc)

        watcher = threading.Thread(target=watch_task, daemon=True)
        watcher.start()
        watcher.join(timeout=UPDATE_TIMEOUT)

        if watcher.is_alive():
            # Closing the channel cancels the in-flight streaming RPC, which
            # unblocks the watcher thread instead of leaking a thread parked
            # forever on network I/O. Then fail: a consumer that never saw an
            # update it was guaranteed to receive is a defect, not a reason to
            # skip.
            client.close()
            watcher.join(timeout=RELEASE_TIMEOUT)
            pytest.fail(
                f"no watch event arrived within {UPDATE_TIMEOUT}s for a "
                "relationship written after the subscription revision"
            )

        assert not failure, f"the watch consumer raised: {failure[0]!r}"

        # Abandoning has to actually finish the generator, not merely stop the
        # loop.
        with pytest.raises(StopIteration):
            next(events)

        assert resume_token
        assert len(received) == 1, (
            "expected exactly the one update written after the subscription "
            f"revision, got {[str(u.relationship) for u in received]}"
        )

        # The update must be the one that was written, not merely "an update".
        update = received[0]
        assert isinstance(update.relationship, Relationship)
        assert update.relationship.resource_type == "document"
        assert update.relationship.resource_id == "sync-watched"
        assert update.relationship.resource_relation == "editor"
        assert update.relationship.subject_type == "user"
        assert update.relationship.subject_id == "bob"
        # TOUCH is a write, so it can only be the mapping for an explicit
        # OPERATION_TOUCH -- never a default an unrecognized operation falls
        # into.
        assert update.operation in (UpdateOperation.CREATE, UpdateOperation.TOUCH)

        # Clean up so later examples that write a narrower schema aren't
        # blocked by leftover relationships.
        client.delete_relationships(
            Filter(resource_type="document", resource_id="sync-watched")
        )


def test_watch_changes_with_checkpoints_sync():
    """`include_checkpoints=True` asks the server for periodic checkpoint
    events in addition to relationship updates -- recommended behind a proxy
    that aborts idle connections, since a checkpoint keeps the stream alive
    even when nothing has changed. A checkpoint carries no updates, so a
    consumer must branch on `event.is_checkpoint` to tell "nothing changed,
    here is a fresh resume point" from "here are changes"."""
    with SpiceDBClient(
        ENDPOINT, token=TOKEN, insecure=True
    ) as client:
        # Clear before writing the schema -- see conftest.clear_documents.
        clear_documents_sync(client)
        client.write_schema(SCHEMA)

        seen_checkpoint = False
        seen_update = False
        failure: list[BaseException] = []

        events = client.watch(object_types=["document"], include_checkpoints=True)

        def watch_task():
            nonlocal seen_checkpoint, seen_update
            try:
                for event in events:
                    if event.is_checkpoint:
                        assert event.updates == []
                        seen_checkpoint = True
                    elif event.updates:
                        seen_update = True
                    if seen_checkpoint and seen_update:
                        break
                events.close()
            except BaseException as exc:  # noqa: BLE001 - reported below
                failure.append(exc)

        watcher = threading.Thread(target=watch_task, daemon=True)
        watcher.start()

        txn = Transaction()
        txn.touch(
            Relationship.from_triple("document:sync-watched", "viewer", "user:dave")
        )
        client.write(txn)

        watcher.join(timeout=UPDATE_TIMEOUT)
        if watcher.is_alive():
            client.close()
            watcher.join(timeout=RELEASE_TIMEOUT)
            pytest.fail(
                "did not observe both a checkpoint and an update within "
                f"{UPDATE_TIMEOUT}s -- include_checkpoints did not reach the "
                "server, or updates are not being delivered"
            )

        assert not failure, f"the watch consumer raised: {failure[0]!r}"
        assert seen_checkpoint
        assert seen_update

        client.delete_relationships(
            Filter(resource_type="document", resource_id="sync-watched")
        )
