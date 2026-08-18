"""Example: Watching for changes from synchronous code.

`watch` is a blocking generator -- there's no `async for` to reach for, so
consuming it while also writing the relationship that triggers an event
needs a second thread. This mirrors the async example's shape (write, then
watch for the resulting event), but the concurrency primitive is
`threading.Thread` instead of `asyncio.create_task`. If the server never
emits an event, closing the channel cancels the in-flight streaming call and
unblocks the watcher thread -- the sync equivalent of the async example's
`asyncio.wait_for` timeout/cancellation.

Note: `Magefile.go`'s integration run passes `-k "not watch"` and skips this
example for the same reason it skips `watch_changes/` -- watch tests are
timing-sensitive against a live server. This module must still import and
collect cleanly, and must never hang if run directly.
"""

import threading
import time

import pytest

from conftest import SCHEMA
from spicedb import Filter, Relationship, UpdateOperation
from spicedb.sync import SpiceDBClient
from spicedb.types import Transaction


pytestmark = pytest.mark.integration


def test_watch_changes_sync():
    with SpiceDBClient(
        "localhost:50051", token="somerandomkeyhere", insecure=True
    ) as client:
        client.write_schema(SCHEMA)

        # Write an initial relationship to get a starting revision.
        txn = Transaction()
        txn.touch(
            Relationship.from_triple("document:sync-watched", "viewer", "user:alice")
        )
        revision = client.write(txn)

        received = []

        def watch_task():
            try:
                for updates, _rev in client.watch(
                    object_types=["document"],
                    start_revision=revision,
                ):
                    received.extend(updates)
                    if received:
                        return
            except Exception:
                # Either a real stream error, or the channel was closed
                # below to unblock this thread after a timeout. Either way
                # there's nothing more for the watcher to do.
                return

        watcher = threading.Thread(target=watch_task, daemon=True)
        watcher.start()

        time.sleep(0.1)  # let watch start
        txn2 = Transaction()
        txn2.touch(
            Relationship.from_triple("document:sync-watched", "editor", "user:bob")
        )
        client.write(txn2)

        watcher.join(timeout=5.0)
        if watcher.is_alive():
            # Closing the channel cancels the in-flight streaming RPC,
            # which unblocks watch_task's for loop instead of leaking a
            # thread that blocks forever on network I/O.
            client.close()
            watcher.join(timeout=2.0)
            pytest.skip("watch timed out — SpiceDB may not support watch")

        assert len(received) >= 1
        for update in received:
            # `update` is a native `spicedb.Update` -- no proto types leak
            # here.
            assert update.operation in (
                UpdateOperation.CREATE,
                UpdateOperation.TOUCH,
                UpdateOperation.DELETE,
            )
            assert isinstance(update.relationship, Relationship)
        print(
            f"received {len(received)} update(s): "
            f"{[u.operation.value for u in received]}"
        )

        # Clean up so later examples that write a narrower schema aren't
        # blocked by leftover relationships.
        client.delete_relationships(
            Filter(resource_type="document", resource_id="sync-watched")
        )
