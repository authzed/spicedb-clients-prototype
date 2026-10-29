"""Per-page default deadlines. Root DESIGN.md, "Treat a deadline as a budget".

Finite paged reads apply the client default deadline to each page's wait on
the server, so a server that stalls fails the read. The deadline never
counts the caller's processing between items: a slow consumer of a healthy
server always finishes.

The slow-consumer tests must defeat transport buffering, or they would pass
against an implementation that counts the caller's time: with small messages
the server finishes sending, the client buffers it all, and the deadline is
never reached. So every message is padded with 64 KiB of unknown-field bytes
and the client's channel has BDP probing off, which keeps HTTP/2 flow control
at its 64 KiB initial window: the server can only send the next message as
the caller pulls the previous one.
"""

from __future__ import annotations

import asyncio
import threading
import time
from concurrent import futures

import grpc
import grpc.aio
import pytest
from authzed.api.materialize.v0 import watchpermissionsets_pb2 as m
from authzed.api.v1 import core_pb2 as core
from authzed.api.v1 import permission_service_pb2 as p

from spicedb import Filter, ObjectRef, SubjectRef, TransportSecurity
from spicedb.aio import SpiceDBClient as AioClient
from spicedb.consistency import full
from spicedb.errors import DeadlineExceededError
from spicedb.sync import SpiceDBClient as SyncClient

ITEMS = 5
DEFAULT = 0.2  # the per-page default deadline under test
CONSUMER_SLEEP = 0.1  # per item, so the caller's total (0.5s) dwarfs DEFAULT
STALL = 3.0  # the stalling server's pause; far beyond DEFAULT
_PAD_FIELD = 9999
_PAD = b"x" * (64 * 1024)
_NO_BDP = [("grpc.http2.bdp_probe", 0)]


def _varint(value: int) -> bytes:
    out = bytearray()
    while True:
        byte = value & 0x7F
        value >>= 7
        if value:
            out.append(byte | 0x80)
        else:
            out.append(byte)
            return bytes(out)


def _padded(message) -> bytes:
    """``message`` plus a 64 KiB unknown field, which parsers keep and ignore."""
    return message.SerializeToString() + _varint(_PAD_FIELD << 3 | 2) + _varint(len(_PAD)) + _PAD


def _token(value: str = "t"):
    return core.ZedToken(token=value)


def _rel(i: int):
    return core.Relationship(
        resource=core.ObjectReference(object_type="document", object_id=f"doc{i}"),
        relation="viewer",
        subject=core.SubjectReference(object=core.ObjectReference(object_type="user", object_id="alice")),
    )


def _first_page(request: bytes, request_type) -> bool:
    parsed = request_type()
    parsed.ParseFromString(request)
    return not parsed.HasField("optional_cursor")


class _Server:
    """Answers each paged read with ITEMS padded messages on its first page
    and an empty second page. ``stall_on_page`` pauses before that page."""

    def __init__(self, stall_on_page: int | None = None) -> None:
        self.stall_on_page = stall_on_page
        self.pages = 0
        self.lock = threading.Lock()

    def _page(self) -> int:
        with self.lock:
            self.pages += 1
            return self.pages

    def _maybe_stall(self, context) -> None:
        if self.stall_on_page is not None and self._page() == self.stall_on_page:
            deadline = time.monotonic() + STALL
            while context.is_active() and time.monotonic() < deadline:
                time.sleep(0.01)

    def read_relationships(self, request, context):
        self._maybe_stall(context)
        if not _first_page(request, p.ReadRelationshipsRequest):
            return
        for i in range(ITEMS):
            yield _padded(p.ReadRelationshipsResponse(
                read_at=_token(), relationship=_rel(i), after_result_cursor=core.Cursor(token=f"c{i}"),
            ))

    def lookup_resources(self, request, context):
        self._maybe_stall(context)
        parsed = p.LookupResourcesRequest()
        parsed.ParseFromString(request)
        # The stall test pages through page_size=1 pages; the others get ITEMS.
        count = 1 if self.stall_on_page is not None else (ITEMS if not parsed.HasField("optional_cursor") else 0)
        for i in range(count):
            yield _padded(p.LookupResourcesResponse(
                looked_up_at=_token(), resource_object_id=f"doc{self.pages}-{i}",
                permissionship=p.LOOKUP_PERMISSIONSHIP_HAS_PERMISSION,
                after_result_cursor=core.Cursor(token=f"c{self.pages}-{i}"),
            ))

    def lookup_subjects(self, request, context):
        self._maybe_stall(context)
        if not _first_page(request, p.LookupSubjectsRequest):
            return
        for i in range(ITEMS):
            yield _padded(p.LookupSubjectsResponse(
                looked_up_at=_token(),
                subject=p.ResolvedSubject(
                    subject_object_id=f"user{i}", permissionship=p.LOOKUP_PERMISSIONSHIP_HAS_PERMISSION
                ),
                after_result_cursor=core.Cursor(token=f"c{i}"),
            ))

    def export(self, request, context):
        self._maybe_stall(context)
        if not _first_page(request, p.ExportBulkRelationshipsRequest):
            return
        for i in range(ITEMS):
            yield _padded(p.ExportBulkRelationshipsResponse(
                relationships=[_rel(i)], after_result_cursor=core.Cursor(token=f"c{i}"),
            ))

    def lookup_permission_sets(self, request, context):
        self._maybe_stall(context)
        for i in range(ITEMS):
            yield _padded(m.LookupPermissionSetsResponse(
                change=m.PermissionSetChange(
                    at_revision=_token(),
                    operation=m.PermissionSetChange.SET_OPERATION_ADDED,
                    parent_set=m.SetReference(object_type="group", object_id="eng", permission_or_relation="member"),
                    child_member=m.MemberReference(object_type="user", object_id=f"user{i}"),
                ),
                cursor=m.Cursor(limit=ITEMS, token=_token(), cursor=f"c{i}"),
            ))


def _serve(handlers: _Server) -> tuple[grpc.Server, int]:
    def stream(fn):
        return grpc.unary_stream_rpc_method_handler(fn, lambda b: b, lambda b: b)

    server = grpc.server(futures.ThreadPoolExecutor(max_workers=8), options=_NO_BDP)
    server.add_generic_rpc_handlers((
        grpc.method_handlers_generic_handler("authzed.api.v1.PermissionsService", {
            "ReadRelationships": stream(handlers.read_relationships),
            "LookupResources": stream(handlers.lookup_resources),
            "LookupSubjects": stream(handlers.lookup_subjects),
            "ExportBulkRelationships": stream(handlers.export),
        }),
        grpc.method_handlers_generic_handler("authzed.api.materialize.v0.WatchPermissionSetsService", {
            "LookupPermissionSets": stream(handlers.lookup_permission_sets),
        }),
    ))
    port = server.add_insecure_port("127.0.0.1:0")
    server.start()
    return server, port


_ALICE = SubjectRef(ObjectRef("user", "alice"))

READS = {
    "read_relationships": lambda c: c.read_relationships(Filter(resource_type="document"), consistency=full(), page_size=ITEMS),
    "lookup_resources": lambda c: c.lookup_resources(
        resource_type="document", permission="view", subject=_ALICE, consistency=full(), page_size=ITEMS
    ),
    "lookup_subjects_paged": lambda c: c.lookup_subjects(
        resource=ObjectRef("document", "doc1"), permission="view", subject_type="user",
        consistency=full(), page_size=ITEMS,
    ),
    "lookup_subjects_unpaged": lambda c: c.lookup_subjects(
        resource=ObjectRef("document", "doc1"), permission="view", subject_type="user", consistency=full()
    ),
    "export_relationships": lambda c: c.export_relationships(consistency=full(), page_size=ITEMS),
    "lookup_permission_sets": lambda c: c.lookup_permission_sets(limit=ITEMS),
}


@pytest.fixture
def healthy():
    server, port = _serve(_Server())
    yield port
    server.stop(0)


@pytest.mark.parametrize("read", sorted(READS))
def test_sync_slow_consumer_finishes_a_healthy_read(healthy, read):
    channel = grpc.insecure_channel(f"127.0.0.1:{healthy}", options=_NO_BDP)
    client = SyncClient.from_channel(channel, default_timeout=DEFAULT)
    seen = 0
    try:
        with READS[read](client) as stream:
            for _ in stream:
                time.sleep(CONSUMER_SLEEP)
                seen += 1
    finally:
        channel.close()
    assert seen == ITEMS


@pytest.mark.parametrize("read", sorted(READS))
async def test_aio_slow_consumer_finishes_a_healthy_read(healthy, read):
    channel = grpc.aio.insecure_channel(f"127.0.0.1:{healthy}", options=_NO_BDP)
    client = AioClient.from_channel(channel, default_timeout=DEFAULT)
    seen = 0
    try:
        async with READS[read](client) as stream:
            async for _ in stream:
                await asyncio.sleep(CONSUMER_SLEEP)
                seen += 1
    finally:
        await channel.close()
    assert seen == ITEMS


def _stalling_lookup(client):
    return client.lookup_resources(
        resource_type="document", permission="view", subject=_ALICE, consistency=full(), page_size=1
    )


def test_sync_lookup_stalling_on_page_three_fails_after_the_per_page_default():
    """No caller option: only the client default bounds each page."""
    server, port = _serve(_Server(stall_on_page=3))
    client = SyncClient(f"127.0.0.1:{port}", token="t", transport_security=TransportSecurity.PLAINTEXT,
                        default_timeout=DEFAULT)
    got = []
    started = time.monotonic()
    try:
        with pytest.raises(DeadlineExceededError, match="did not send a page"):
            for item in _stalling_lookup(client):
                got.append(item.resource_id)
    finally:
        client.close()
        server.stop(0)
    assert got == ["doc1-0", "doc2-0"]
    assert time.monotonic() - started < STALL


async def test_aio_lookup_stalling_on_page_three_fails_after_the_per_page_default():
    server, port = _serve(_Server(stall_on_page=3))
    client = AioClient(f"127.0.0.1:{port}", token="t", transport_security=TransportSecurity.PLAINTEXT,
                       default_timeout=DEFAULT)
    got = []
    started = time.monotonic()
    try:
        with pytest.raises(DeadlineExceededError, match="did not send a page"):
            async for item in _stalling_lookup(client):
                got.append(item.resource_id)
    finally:
        await client.close()
        server.stop(0)
    assert got == ["doc1-0", "doc2-0"]
    assert time.monotonic() - started < STALL


def test_sync_caller_total_deadline_wins_when_earlier():
    server, port = _serve(_Server(stall_on_page=1))
    client = SyncClient(f"127.0.0.1:{port}", token="t", transport_security=TransportSecurity.PLAINTEXT,
                        default_timeout=30.0)
    started = time.monotonic()
    try:
        with pytest.raises(DeadlineExceededError):
            list(client.lookup_resources(
                resource_type="document", permission="view", subject=_ALICE, consistency=full(), timeout=0.2
            ))
    finally:
        client.close()
        server.stop(0)
    assert time.monotonic() - started < STALL
