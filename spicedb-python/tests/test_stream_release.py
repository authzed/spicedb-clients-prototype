"""Abandoning a stream must release it -- proved against a real server.

Root DESIGN.md, "RULE: Abandoning a stream must release it", clause 2: it is
not enough to expose a cancel; the transport underneath has to honor it. So
these tests do not assert that the consuming loop exited -- it always does,
leak or no leak. They run a real in-process gRPC server whose streaming
handlers send one message and then park until *their own* RPC context goes
inactive, and assert the server observed that. If the client never releases
the stream, the handler stays parked and the assertion times out.

Both surfaces are covered, because the release mechanism differs:

* ``spicedb.sync`` returns a plain generator. ``break`` drops the last
  reference, CPython closes the generator, ``GeneratorExit`` lands on the
  ``yield``, and the ``finally`` cancels the call -- deterministic.
* ``spicedb.aio`` returns an async generator, whose ``aclose()`` is a
  coroutine. ``contextlib.aclosing()`` is the explicit, deterministic way to
  close one (clause 1's "explicit, working way to stop consuming"); a bare
  ``break`` also gets there, via the event loop's async-generator finalizer.
  Both paths are tested.
"""

from __future__ import annotations

import asyncio
import contextlib
import threading
import time
from concurrent import futures

import grpc
import pytest
from authzed.api.v1 import core_pb2
from authzed.api.v1 import permission_service_pb2 as psp
from authzed.api.v1 import permission_service_pb2_grpc as psp_grpc
from authzed.api.v1 import watch_service_pb2 as wsp
from authzed.api.v1 import watch_service_pb2_grpc as wsp_grpc

from spicedb import Filter
from spicedb.aio import SpiceDBClient as AioClient
from spicedb.consistency import full
from spicedb.sync import SpiceDBClient as SyncClient

TOKEN = "test-token"

# How long the test waits for the server to see its stream end.
_RELEASE_TIMEOUT_SECONDS = 10.0


class _Parked:
    """Server-side record of whether a stream was ever released.

    ``terminated`` is driven by a gRPC termination callback registered
    *before* the first response is sent, not by polling from inside the
    handler. That ordering matters: when the client cancels quickly enough,
    gRPC never resumes the handler generator after its first ``yield``, so
    anything the handler would have recorded afterwards is never recorded at
    all. The callback fires either way.
    """

    def __init__(self) -> None:
        self.opened = threading.Event()
        self.terminated = threading.Event()
        self.shutdown = threading.Event()

    def on_open(self, context: grpc.ServicerContext) -> None:
        self.opened.set()
        if not context.add_callback(self.terminated.set):
            # Already over before we could register -- which is itself the
            # termination we are watching for.
            self.terminated.set()

    def park(self, context: grpc.ServicerContext) -> None:
        """Hold the stream open until the RPC ends or the fixture tears down.

        Never returning on its own is the whole point: a handler that ended
        the stream itself would let the test pass without the client having
        released anything. The ``shutdown`` escape exists only so a failing
        test does not leave a server thread parked forever.
        """
        while not self.shutdown.is_set() and context.is_active():
            time.sleep(0.005)


class _ParkingPermissions(psp_grpc.PermissionsServiceServicer):
    def __init__(self, parked: _Parked) -> None:
        self._parked = parked

    def ReadRelationships(self, request, context):
        self._parked.on_open(context)
        yield psp.ReadRelationshipsResponse(
            relationship=_relationship(),
            after_result_cursor=core_pb2.Cursor(token="cursor-1"),
        )
        self._parked.park(context)

    def LookupResources(self, request, context):
        self._parked.on_open(context)
        yield psp.LookupResourcesResponse(
            resource_object_id="doc1",
            permissionship=psp.LOOKUP_PERMISSIONSHIP_HAS_PERMISSION,
            after_result_cursor=core_pb2.Cursor(token="cursor-1"),
        )
        self._parked.park(context)

    def LookupSubjects(self, request, context):
        self._parked.on_open(context)
        yield psp.LookupSubjectsResponse(
            subject=psp.ResolvedSubject(
                subject_object_id="user1",
                permissionship=psp.LOOKUP_PERMISSIONSHIP_HAS_PERMISSION,
            )
        )
        self._parked.park(context)

    def ExportBulkRelationships(self, request, context):
        self._parked.on_open(context)
        yield psp.ExportBulkRelationshipsResponse(
            relationships=[_relationship()],
            after_result_cursor=core_pb2.Cursor(token="cursor-1"),
        )
        self._parked.park(context)


class _ParkingWatch(wsp_grpc.WatchServiceServicer):
    def __init__(self, parked: _Parked) -> None:
        self._parked = parked

    def Watch(self, request, context):
        self._parked.on_open(context)
        yield wsp.WatchResponse(changes_through=_zed_token("token-1"))
        self._parked.park(context)


def _relationship():
    from authzed.api.v1 import core_pb2

    return core_pb2.Relationship(
        resource=core_pb2.ObjectReference(object_type="document", object_id="doc1"),
        relation="viewer",
        subject=core_pb2.SubjectReference(
            object=core_pb2.ObjectReference(object_type="user", object_id="user1")
        ),
    )


def _zed_token(token: str):
    from authzed.api.v1 import core_pb2

    return core_pb2.ZedToken(token=token)


@pytest.fixture
def parking_server():
    """A real gRPC server whose streams never end on their own."""
    parked = _Parked()
    server = grpc.server(futures.ThreadPoolExecutor(max_workers=4))
    psp_grpc.add_PermissionsServiceServicer_to_server(_ParkingPermissions(parked), server)
    wsp_grpc.add_WatchServiceServicer_to_server(_ParkingWatch(parked), server)
    port = server.add_insecure_port("localhost:0")
    server.start()
    try:
        yield port, parked
    finally:
        parked.shutdown.set()
        server.stop(0)


def _assert_released(parked: _Parked) -> None:
    assert parked.opened.is_set(), "the server never saw the stream open"
    assert parked.terminated.wait(_RELEASE_TIMEOUT_SECONDS), (
        "the server never observed the stream ending: abandoning the iterator "
        "leaked the gRPC stream and the server-side dispatch"
    )


# ── Streams under test ──────────────────────────────────────────────
#
# Each entry is (name, take-one-then-stop callable). Keeping them in one
# table is what makes it obvious that no streaming call is exempt.

SYNC_STREAMS = {
    "read_relationships": lambda c: c.read_relationships(
        Filter(resource_type="document"), full()
    ),
    "lookup_resources": lambda c: c.lookup_resources(
        "document", "view", ("user:user1", ""), full()
    ),
    "lookup_subjects": lambda c: c.lookup_subjects(
        ("document", "doc1"), "view", "user", full()
    ),
    "export_relationships": lambda c: c.export_relationships(full()),
    "watch": lambda c: c.watch(),
}

AIO_STREAMS = dict(SYNC_STREAMS)


@pytest.mark.parametrize("stream_name", sorted(SYNC_STREAMS))
def test_sync_break_releases_the_stream(stream_name: str, parking_server) -> None:
    """A `break` out of a sync stream tells the server to stop."""
    port, parked = parking_server
    client = SyncClient(f"localhost:{port}", token=TOKEN, insecure=True)
    try:
        seen = 0
        for _ in SYNC_STREAMS[stream_name](client):
            seen += 1
            break
        assert seen == 1
        _assert_released(parked)
    finally:
        client.close()


@pytest.mark.parametrize("stream_name", sorted(AIO_STREAMS))
async def test_aio_aclose_releases_the_stream(stream_name: str, parking_server) -> None:
    """`aclosing()` -- the explicit close clause 1 requires -- releases it."""
    port, parked = parking_server
    client = AioClient(f"localhost:{port}", token=TOKEN, insecure=True)
    try:
        seen = 0
        async with contextlib.aclosing(AIO_STREAMS[stream_name](client)) as stream:
            async for _ in stream:
                seen += 1
                break
        assert seen == 1
        _assert_released(parked)
    finally:
        await client.close()


@pytest.mark.parametrize("stream_name", sorted(AIO_STREAMS))
async def test_aio_bare_break_releases_the_stream(
    stream_name: str, parking_server
) -> None:
    """A bare `break` gets there too, via async-generator finalization.

    This is the idiom callers actually write, so it has to work even though
    `aclosing()` is the deterministic form. Dropping the last reference makes
    the event loop schedule `aclose()`, which runs the `finally` that cancels
    the call -- but only because that `finally` exists.
    """
    port, parked = parking_server
    client = AioClient(f"localhost:{port}", token=TOKEN, insecure=True)
    try:
        seen = 0
        async for _ in AIO_STREAMS[stream_name](client):
            seen += 1
            break
        assert seen == 1

        # Give the loop a turn to run the finalizer it scheduled.
        for _ in range(10):
            await asyncio.sleep(0.01)
            if parked.terminated.is_set():
                break

        _assert_released(parked)
    finally:
        await client.close()
