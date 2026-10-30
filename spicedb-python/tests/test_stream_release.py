"""Retained streams release real server calls on context exit and explicit close.

The server callback proves transport release, including pending next/anext.
A bare break requires a context manager while the stream remains retained.
"""

from __future__ import annotations
from authzed.api.v1 import core_pb2 as _core_tok
from spicedb import TransportSecurity
from spicedb import ObjectRef, SubjectRef
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
from authzed.api.materialize.v0 import (
    watchpermissionsets_pb2 as materialize,
    watchpermissionsets_pb2_grpc as materialize_grpc,
)
from spicedb import Filter
from spicedb.aio import SpiceDBClient as AioClient
from spicedb.consistency import full
from spicedb.sync import SpiceDBClient as SyncClient


class _ParkingPermissionSets(materialize_grpc.WatchPermissionSetsServiceServicer):
    def __init__(self, parked):
        self._parked = parked

    def WatchPermissionSets(self, request, context):
        self._parked.on_open(context)
        yield materialize.WatchPermissionSetsResponse(
            completed_revision=core_pb2.ZedToken(token="materialize")
        )
        self._parked.park(context)

    def LookupPermissionSets(self, request, context):
        self._parked.on_open(context)
        yield materialize.LookupPermissionSetsResponse(
            change=materialize.PermissionSetChange(
                at_revision=core_pb2.ZedToken(token="materialize"),
                operation=materialize.PermissionSetChange.SET_OPERATION_ADDED,
                parent_set=materialize.SetReference(
                    object_type="group", object_id="eng", permission_or_relation="member"
                ),
                child_member=materialize.MemberReference(object_type="user", object_id="alice"),
            ),
            cursor=materialize.Cursor(
                limit=request.limit,
                token=core_pb2.ZedToken(token="materialize"),
                cursor="next",
            )
        )
        self._parked.park(context)


TOKEN = "test-token"
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
        yield psp.ReadRelationshipsResponse(read_at=_core_tok.ZedToken(token="t"),
            relationship=_relationship(),
            after_result_cursor=core_pb2.Cursor(token="cursor-1"),
        )
        self._parked.park(context)

    def LookupResources(self, request, context):
        self._parked.on_open(context)
        yield psp.LookupResourcesResponse(looked_up_at=_core_tok.ZedToken(token="t"),
            resource_object_id="doc1",
            permissionship=psp.LOOKUP_PERMISSIONSHIP_HAS_PERMISSION,
            after_result_cursor=core_pb2.Cursor(token="cursor-1"),
        )
        self._parked.park(context)

    def LookupSubjects(self, request, context):
        self._parked.on_open(context)
        yield psp.LookupSubjectsResponse(looked_up_at=_core_tok.ZedToken(token="t"),
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
    psp_grpc.add_PermissionsServiceServicer_to_server(
        _ParkingPermissions(parked), server
    )
    wsp_grpc.add_WatchServiceServicer_to_server(_ParkingWatch(parked), server)
    materialize_grpc.add_WatchPermissionSetsServiceServicer_to_server(
        _ParkingPermissionSets(parked), server
    )
    port = server.add_insecure_port("localhost:0")
    server.start()
    try:
        yield (port, parked)
    finally:
        parked.shutdown.set()
        server.stop(0)


def _assert_released(parked: _Parked) -> None:
    assert parked.opened.is_set(), "the server never saw the stream open"
    assert parked.terminated.wait(_RELEASE_TIMEOUT_SECONDS), (
        "the server never observed the stream ending: abandoning the iterator leaked the gRPC stream and the server-side dispatch"
    )


SYNC_STREAMS = {
    "read_relationships": lambda c: c.read_relationships(
        Filter(resource_type="document"), consistency=full()
    ),
    "lookup_resources": lambda c: c.lookup_resources(
        resource_type="document",
        permission="view",
        subject=_subject("user:user1", ""),
        consistency=full(),
    ),
    "lookup_subjects": lambda c: c.lookup_subjects(
        resource=ObjectRef("document", "doc1"),
        permission="view",
        subject_type="user",
        consistency=full(),
    ),
    "export_relationships": lambda c: c.export_relationships(consistency=full()),
    "watch": lambda c: c.watch(),
    "watch_permission_sets": lambda c: c.watch_permission_sets(),
    "lookup_permission_sets": lambda c: c.lookup_permission_sets(),
}
AIO_STREAMS = dict(SYNC_STREAMS)


@pytest.mark.parametrize("stream_name", sorted(SYNC_STREAMS))
def test_sync_retained_context_releases_the_stream(
    stream_name: str, parking_server
) -> None:
    """A `break` out of a sync stream tells the server to stop."""
    port, parked = parking_server
    client = SyncClient(f"localhost:{port}", token=TOKEN, transport_security=TransportSecurity.PLAINTEXT)
    try:
        seen = 0
        with SYNC_STREAMS[stream_name](client) as stream:
            for _ in stream:
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
    client = AioClient(f"localhost:{port}", token=TOKEN, transport_security=TransportSecurity.PLAINTEXT)
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
async def test_aio_retained_context_releases_the_stream(
    stream_name: str, parking_server
) -> None:
    """A bare `break` gets there too, via async-generator finalization.

    This is the idiom callers actually write, so it has to work even though
    `aclosing()` is the deterministic form. Dropping the last reference makes
    the event loop schedule `aclose()`, which runs the `finally` that cancels
    the call -- but only because that `finally` exists.
    """
    port, parked = parking_server
    client = AioClient(f"localhost:{port}", token=TOKEN, transport_security=TransportSecurity.PLAINTEXT)
    try:
        seen = 0
        async with AIO_STREAMS[stream_name](client) as stream:
            async for _ in stream:
                seen += 1
                break
        assert seen == 1
        for _ in range(10):
            await asyncio.sleep(0.01)
            if parked.terminated.is_set():
                break
        _assert_released(parked)
    finally:
        await client.close()


def _subject(value, relation=None):
    typ, identifier = value.split(":", 1)
    return SubjectRef(ObjectRef(typ, identifier), relation or None)


def test_sync_close_cancels_a_pending_next(parking_server):
    port, parked = parking_server
    client = SyncClient(f"localhost:{port}", token=TOKEN, transport_security=TransportSecurity.PLAINTEXT)
    stream = client.watch()
    next(stream)
    observed = []

    def consume():
        try:
            next(stream)
        except BaseException as error:
            observed.append(error)

    worker = threading.Thread(target=consume)
    worker.start()
    time.sleep(0.01)
    stream.close()
    worker.join(2)
    try:
        assert not worker.is_alive()
        assert observed
        _assert_released(parked)
        with pytest.raises(StopIteration):
            next(stream)
    finally:
        client.close()


async def test_aio_aclose_cancels_a_pending_anext(parking_server):
    port, parked = parking_server
    async with AioClient(f"localhost:{port}", token=TOKEN, transport_security=TransportSecurity.PLAINTEXT) as client:
        stream = client.watch()
        await anext(stream)
        pending = asyncio.create_task(anext(stream))
        await asyncio.sleep(0.01)
        await stream.aclose()
        assert pending.done()
        _assert_released(parked)
        with pytest.raises(StopAsyncIteration):
            await anext(stream)


async def test_aio_task_cancellation_releases_pending_call(parking_server):
    port, parked = parking_server
    async with AioClient(f"localhost:{port}", token=TOKEN, transport_security=TransportSecurity.PLAINTEXT) as client:
        stream = client.watch()
        await anext(stream)
        pending = asyncio.create_task(anext(stream))
        await asyncio.sleep(0.01)
        pending.cancel()
        with pytest.raises(asyncio.CancelledError):
            await pending
        _assert_released(parked)
