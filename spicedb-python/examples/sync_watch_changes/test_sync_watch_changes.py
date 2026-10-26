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
from spicedb import TransportSecurity, WatchContent

import threading
import pytest
from conftest import ENDPOINT, SCHEMA, TOKEN, clear_documents_sync
from spicedb import Filter, Relationship, Update, UpdateOperation
from spicedb.sync import SpiceDBClient
from spicedb.types import Transaction

pytestmark = pytest.mark.integration
UPDATE_TIMEOUT = 30.0
RELEASE_TIMEOUT = 10.0


def test_watch_changes_sync():
    with SpiceDBClient(ENDPOINT, token=TOKEN, transport_security=TransportSecurity.PLAINTEXT) as client:
        clear_documents_sync(client)
        client.write_schema(SCHEMA)
        txn = Transaction()
        txn.touch(
            Relationship.from_triple("document:sync-watched", "viewer", "user:seed")
        )
        revision = client.write(txn).revision
        txn2 = Transaction()
        txn2.touch(
            Relationship.from_triple("document:sync-watched", "editor", "user:bob")
        )
        client.write(txn2)
        events = client.watch(filters=[Filter("document")], start_revision=revision)
        received: list[Update] = []
        resume_token = None
        failure: list[BaseException] = []

        def watch_task():
            nonlocal resume_token
            try:
                for event in events:
                    received.extend(event.updates)
                    if received:
                        resume_token = event.changes_through
                        break
                events.close()
            except BaseException as exc:
                failure.append(exc)

        watcher = threading.Thread(target=watch_task, daemon=True)
        watcher.start()
        watcher.join(timeout=UPDATE_TIMEOUT)
        if watcher.is_alive():
            client.close()
            watcher.join(timeout=RELEASE_TIMEOUT)
            pytest.fail(
                f"no watch event arrived within {UPDATE_TIMEOUT}s for a relationship written after the subscription revision"
            )
        assert not failure, f"the watch consumer raised: {failure[0]!r}"
        with pytest.raises(StopIteration):
            next(events)
        assert resume_token
        assert len(received) == 1, (
            f"expected exactly the one update written after the subscription revision, got {[str(u.relationship) for u in received]}"
        )
        update = received[0]
        assert isinstance(update.relationship, Relationship)
        assert update.relationship.resource.object_type == "document"
        assert update.relationship.resource.object_id == "sync-watched"
        assert update.relationship.relation == "editor"
        assert update.relationship.subject.object.object_type == "user"
        assert update.relationship.subject.object.object_id == "bob"
        assert update.operation in (UpdateOperation.CREATE, UpdateOperation.TOUCH)
        client.delete_all_relationships_non_atomic(
            Filter(resource_type="document", resource_id="sync-watched")
        )


def test_watch_changes_with_checkpoints_sync():
    """`content=(WatchContent.RELATIONSHIPS, WatchContent.CHECKPOINTS)` asks the server for periodic checkpoint
    events in addition to relationship updates -- recommended behind a proxy
    that aborts idle connections, since a checkpoint keeps the stream alive
    even when nothing has changed. A checkpoint carries no updates, so a
    consumer must branch on `event.is_checkpoint` to tell "nothing changed,
    here is a fresh resume point" from "here are changes"."""
    with SpiceDBClient(ENDPOINT, token=TOKEN, transport_security=TransportSecurity.PLAINTEXT) as client:
        clear_documents_sync(client)
        client.write_schema(SCHEMA)
        seen_checkpoint = False
        seen_update = False
        failure: list[BaseException] = []
        events = client.watch(filters=[Filter("document")], content=(WatchContent.RELATIONSHIPS, WatchContent.CHECKPOINTS))

        def watch_task():
            nonlocal seen_checkpoint, seen_update
            try:
                for event in events:
                    if event.is_checkpoint:
                        assert event.updates == ()
                        seen_checkpoint = True
                    elif event.updates:
                        seen_update = True
                    if seen_checkpoint and seen_update:
                        break
                events.close()
            except BaseException as exc:
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
                f"did not observe both a checkpoint and an update within {UPDATE_TIMEOUT}s -- include_checkpoints did not reach the server, or updates are not being delivered"
            )
        assert not failure, f"the watch consumer raised: {failure[0]!r}"
        assert seen_checkpoint
        assert seen_update
        client.delete_all_relationships_non_atomic(
            Filter(resource_type="document", resource_id="sync-watched")
        )
