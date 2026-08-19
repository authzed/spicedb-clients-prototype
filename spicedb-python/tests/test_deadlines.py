"""Call-deadline enforcement. Root DESIGN.md, "RULE: A unary call must have a deadline".

Runs a real in-process gRPC server whose handlers deliberately stall, so these
tests exercise the real `timeout=` plumbing through `grpc`/`grpc.aio` end to
end -- a mocked stub can't prove a deadline is actually enforced, since
`grpc`'s deadline machinery lives below the mock.

Every blocking handler self-bounds its stall to `_STALL_SECONDS` (2s) rather
than blocking forever: long enough to dwarf the tiny per-test deadlines below
(proving enforcement, not luck), short enough that a leaked server-side
handler thread (Python's `grpc` server does not forcibly kill a handler on
client-side deadline expiry -- it only marks the RPC context inactive) can't
wedge interpreter shutdown. Each call is additionally wrapped in
`_run_with_watchdog`, which fails the test (instead of hanging it, and CI
along with it) if a regression reintroduces an unbounded call.
"""

from __future__ import annotations

import asyncio
import threading
import time
from collections.abc import Callable
from concurrent import futures
from typing import Any, TypeVar

import grpc
import grpc.aio
import pytest
from authzed.api.v1 import permission_service_pb2 as psp

from spicedb import Filter, Relationship
from spicedb.aio import SpiceDBClient as AioSpiceDBClient
from spicedb.consistency import full
from spicedb.errors import DeadlineExceededError
from spicedb.sync import SpiceDBClient as SyncSpiceDBClient

# One relationship, because `check_permissions` with zero relationships now
# sends no request at all -- and a call that never reaches the wire can
# never demonstrate a deadline.
_ONE_REL = Relationship.from_triple("document:readme", "view", "user:alice")

TOKEN = "test-token"

# How long a "never responds" handler stalls before finally answering. Every
# per-test deadline below is far smaller than this, so a passing test proves
# the client's timeout fired -- it did not just get lucky with scheduling.
_STALL_SECONDS = 2.0

# Wall-clock budget for a single watchdog-wrapped call. Generous relative to
# the deadlines under test (all <= 0.3s) so it never flakes, but far below
# `_STALL_SECONDS` plus this module's own regression-safety margin -- if the
# fix regresses, the call still returns (the fake server does eventually
# answer), so THIS bound exists only to keep a truly-hung client (e.g. a
# retry loop bug) from wedging the suite instead of failing loudly.
_WATCHDOG_SECONDS = 10.0

T = TypeVar("T")


def _run_with_watchdog(fn: Callable[[], T], timeout: float = _WATCHDOG_SECONDS) -> T:
    """Run `fn()` in a background thread; fail the test if it outlives `timeout`.

    Without this, a regression that drops the `timeout=` kwarg entirely could
    hang this test -- and the CI job running it -- instead of failing.
    """
    box: dict[str, Any] = {}

    def _target() -> None:
        try:
            box["value"] = fn()
        except BaseException as e:  # noqa: BLE001 - re-raised on the test thread below
            box["error"] = e

    t = threading.Thread(target=_target, daemon=True)
    t.start()
    t.join(timeout)
    if t.is_alive():
        pytest.fail(
            f"call did not return within {timeout}s -- deadline enforcement regressed"
        )
    if "error" in box:
        raise box["error"]
    return box["value"]  # type: ignore[no-any-return]


class _StallingHandlers:
    """gRPC service handlers used to prove deadline enforcement.

    `check_bulk` and `write` back the unary tests (never respond inside the
    test's deadline). `read_relationships` backs the streaming
    non-inheritance test: it stalls past the client's tiny unary default
    before finally yielding, proving the stream was never bound by it.
    `import_bulk_relationships` backs the bulk-import tests: it drains the
    client-streaming request before stalling, proving `import_relationships`
    is neither bound by the unary default nor unresponsive to an explicit
    per-call timeout.
    """

    def check_bulk(self, request: bytes, context: grpc.ServicerContext) -> bytes:
        time.sleep(_STALL_SECONDS)
        return psp.CheckBulkPermissionsResponse().SerializeToString()

    def write(self, request: bytes, context: grpc.ServicerContext) -> bytes:
        time.sleep(_STALL_SECONDS)
        return psp.WriteRelationshipsResponse().SerializeToString()

    def read_relationships(self, request: bytes, context: grpc.ServicerContext):
        time.sleep(_STALL_SECONDS)
        yield psp.ReadRelationshipsResponse(
            relationship=Relationship.from_triple(
                "document:a", "viewer", "user:jimmy"
            )._to_proto()
        ).SerializeToString()

    def import_bulk_relationships(
        self, request_iterator: Any, context: grpc.ServicerContext
    ) -> bytes:
        num_loaded = 0
        for raw in request_iterator:
            req = psp.ImportBulkRelationshipsRequest()
            req.ParseFromString(raw)
            num_loaded += len(req.relationships)
        time.sleep(_STALL_SECONDS)
        return psp.ImportBulkRelationshipsResponse(
            num_loaded=num_loaded
        ).SerializeToString()


def _serve(handlers: _StallingHandlers) -> tuple[grpc.Server, int]:
    server = grpc.server(futures.ThreadPoolExecutor(max_workers=4))
    server.add_generic_rpc_handlers(
        (
            grpc.method_handlers_generic_handler(
                "authzed.api.v1.PermissionsService",
                {
                    "CheckBulkPermissions": grpc.unary_unary_rpc_method_handler(
                        handlers.check_bulk, lambda b: b, lambda b: b
                    ),
                    "WriteRelationships": grpc.unary_unary_rpc_method_handler(
                        handlers.write, lambda b: b, lambda b: b
                    ),
                    "ReadRelationships": grpc.unary_stream_rpc_method_handler(
                        handlers.read_relationships, lambda b: b, lambda b: b
                    ),
                    "ImportBulkRelationships": grpc.stream_unary_rpc_method_handler(
                        handlers.import_bulk_relationships, lambda b: b, lambda b: b
                    ),
                },
            ),
        )
    )
    port = server.add_insecure_port("localhost:0")
    server.start()
    return server, port


# ── sync ────────────────────────────────────────────────────────────────


def test_sync_unary_call_times_out_with_deadline_exceeded_error():
    """A unary call against a stub that never responds fails with
    DeadlineExceededError, not a hang -- the whole point of the client
    default."""
    server, port = _serve(_StallingHandlers())
    try:
        client = SyncSpiceDBClient(
            f"localhost:{port}", token=TOKEN, insecure=True, default_timeout=0.2
        )
        start = time.monotonic()
        with pytest.raises(DeadlineExceededError):
            _run_with_watchdog(lambda: client.check_permissions(full(), _ONE_REL))
        elapsed = time.monotonic() - start
        client.close()
    finally:
        server.stop(0)
    assert elapsed < _STALL_SECONDS, (
        "the call must fail at the ~0.2s client default, not wait out the "
        f"server's {_STALL_SECONDS}s stall (elapsed={elapsed:.2f}s)"
    )


def test_sync_per_call_timeout_overrides_client_default():
    """A per-call `timeout=` overrides a much larger client default."""
    server, port = _serve(_StallingHandlers())
    try:
        # Client default is larger than the server's stall -- if the
        # per-call override did not take effect, this call would succeed
        # (or at least not fail quickly) instead of timing out fast.
        client = SyncSpiceDBClient(
            f"localhost:{port}",
            token=TOKEN,
            insecure=True,
            default_timeout=_STALL_SECONDS * 10,
        )
        start = time.monotonic()
        with pytest.raises(DeadlineExceededError):
            _run_with_watchdog(
                lambda: client.check_permissions(full(), _ONE_REL, timeout=0.2)
            )
        elapsed = time.monotonic() - start
        client.close()
    finally:
        server.stop(0)
    assert elapsed < _STALL_SECONDS, (
        "the per-call timeout=0.2 must override the large client default "
        f"(elapsed={elapsed:.2f}s)"
    )


def test_sync_streaming_call_does_not_inherit_unary_default():
    """A streaming call must not be cut off by the (tiny) unary default."""
    server, port = _serve(_StallingHandlers())
    try:
        # default_timeout is far smaller than the server's stall. If
        # read_relationships() inherited it, this would raise
        # DeadlineExceededError instead of yielding the item.
        client = SyncSpiceDBClient(
            f"localhost:{port}", token=TOKEN, insecure=True, default_timeout=0.1
        )
        start = time.monotonic()
        got = _run_with_watchdog(
            lambda: list(
                client.read_relationships(Filter(resource_type="document"), full())
            )
        )
        elapsed = time.monotonic() - start
        client.close()
    finally:
        server.stop(0)
    assert [r.resource_id for r in got] == ["a"]
    assert elapsed >= _STALL_SECONDS, (
        "the stream must outlive the tiny unary default -- it should have "
        f"waited out the server's {_STALL_SECONDS}s stall (elapsed={elapsed:.2f}s)"
    )


def test_sync_import_relationships_does_not_inherit_unary_default():
    """import_relationships (ImportBulkRelationships) is client-streaming: its
    duration scales with the size of the caller's dataset, not with server
    latency, so it must NOT be bound by default_timeout. default_timeout
    here is far smaller than the server's stall -- if import_relationships
    inherited it, this call would raise DeadlineExceededError instead of
    completing."""
    server, port = _serve(_StallingHandlers())
    try:
        client = SyncSpiceDBClient(
            f"localhost:{port}", token=TOKEN, insecure=True, default_timeout=0.1
        )
        rel = Relationship.from_triple("document:bulk", "viewer", "user:alice")
        start = time.monotonic()
        num_loaded = _run_with_watchdog(lambda: client.import_relationships([rel]))
        elapsed = time.monotonic() - start
        client.close()
    finally:
        server.stop(0)
    assert num_loaded == 1
    assert elapsed >= _STALL_SECONDS, (
        "import_relationships must outlive the tiny unary default -- it "
        f"should have waited out the server's {_STALL_SECONDS}s stall "
        f"(elapsed={elapsed:.2f}s)"
    )


def test_sync_import_relationships_with_explicit_timeout_still_fires():
    """The exclusion above is from the *default*, not from the ability to
    bound the call at all -- an explicit timeout= must still fire against a
    stalling server."""
    server, port = _serve(_StallingHandlers())
    try:
        client = SyncSpiceDBClient(f"localhost:{port}", token=TOKEN, insecure=True)
        rel = Relationship.from_triple("document:bulk", "viewer", "user:alice")
        start = time.monotonic()
        with pytest.raises(DeadlineExceededError):
            _run_with_watchdog(
                lambda: client.import_relationships([rel], timeout=0.2)
            )
        elapsed = time.monotonic() - start
        client.close()
    finally:
        server.stop(0)
    assert elapsed < _STALL_SECONDS, (
        "an explicit timeout=0.2 on import_relationships must still fire "
        f"(elapsed={elapsed:.2f}s)"
    )


# ── aio ─────────────────────────────────────────────────────────────────


async def test_aio_unary_call_times_out_with_deadline_exceeded_error():
    server, port = _serve(_StallingHandlers())
    try:
        client = AioSpiceDBClient(
            f"localhost:{port}", token=TOKEN, insecure=True, default_timeout=0.2
        )
        start = time.monotonic()
        with pytest.raises(DeadlineExceededError):
            await asyncio.wait_for(
                client.check_permissions(full(), _ONE_REL), timeout=_WATCHDOG_SECONDS
            )
        elapsed = time.monotonic() - start
        await client.close()
    finally:
        server.stop(0)
    assert elapsed < _STALL_SECONDS


async def test_aio_per_call_timeout_overrides_client_default():
    server, port = _serve(_StallingHandlers())
    try:
        client = AioSpiceDBClient(
            f"localhost:{port}",
            token=TOKEN,
            insecure=True,
            default_timeout=_STALL_SECONDS * 10,
        )
        start = time.monotonic()
        with pytest.raises(DeadlineExceededError):
            await asyncio.wait_for(
                client.check_permissions(full(), _ONE_REL, timeout=0.2),
                timeout=_WATCHDOG_SECONDS,
            )
        elapsed = time.monotonic() - start
        await client.close()
    finally:
        server.stop(0)
    assert elapsed < _STALL_SECONDS


async def test_aio_streaming_call_does_not_inherit_unary_default():
    server, port = _serve(_StallingHandlers())
    try:
        client = AioSpiceDBClient(
            f"localhost:{port}", token=TOKEN, insecure=True, default_timeout=0.1
        )

        async def _collect() -> list[Relationship]:
            return [
                r
                async for r in client.read_relationships(
                    Filter(resource_type="document"), full()
                )
            ]

        start = time.monotonic()
        got = await asyncio.wait_for(_collect(), timeout=_WATCHDOG_SECONDS)
        elapsed = time.monotonic() - start
        await client.close()
    finally:
        server.stop(0)
    assert [r.resource_id for r in got] == ["a"]
    assert elapsed >= _STALL_SECONDS


async def test_aio_import_relationships_does_not_inherit_unary_default():
    """aio counterpart of test_sync_import_relationships_does_not_inherit_unary_default
    -- sync and aio are separate implementations (tests/test_parity.py
    enforces lockstep signatures but not behavior), so this is exercised
    independently rather than assumed from the sync result."""
    server, port = _serve(_StallingHandlers())
    try:
        client = AioSpiceDBClient(
            f"localhost:{port}", token=TOKEN, insecure=True, default_timeout=0.1
        )
        rel = Relationship.from_triple("document:bulk", "viewer", "user:alice")
        start = time.monotonic()
        num_loaded = await asyncio.wait_for(
            client.import_relationships([rel]), timeout=_WATCHDOG_SECONDS
        )
        elapsed = time.monotonic() - start
        await client.close()
    finally:
        server.stop(0)
    assert num_loaded == 1
    assert elapsed >= _STALL_SECONDS, (
        "import_relationships must outlive the tiny unary default -- it "
        f"should have waited out the server's {_STALL_SECONDS}s stall "
        f"(elapsed={elapsed:.2f}s)"
    )


async def test_aio_import_relationships_with_explicit_timeout_still_fires():
    server, port = _serve(_StallingHandlers())
    try:
        client = AioSpiceDBClient(f"localhost:{port}", token=TOKEN, insecure=True)
        rel = Relationship.from_triple("document:bulk", "viewer", "user:alice")
        start = time.monotonic()
        with pytest.raises(DeadlineExceededError):
            await asyncio.wait_for(
                client.import_relationships([rel], timeout=0.2),
                timeout=_WATCHDOG_SECONDS,
            )
        elapsed = time.monotonic() - start
        await client.close()
    finally:
        server.stop(0)
    assert elapsed < _STALL_SECONDS, (
        "an explicit timeout=0.2 on import_relationships must still fire "
        f"(elapsed={elapsed:.2f}s)"
    )


# ── default value / resolution (no server needed) ─────────────────────────


def test_default_timeout_is_thirty_seconds():
    """Documents the chosen default -- see spicedb/sync/client.py's
    `_DEFAULT_TIMEOUT_SECONDS` docstring for why 30s, mirroring
    authzed-node's `DEFAULT_DEADLINE_MS`."""
    client = SyncSpiceDBClient("localhost:50051", token=TOKEN, insecure=True)
    assert client._default_timeout == 30.0


def test_effective_timeout_falls_back_to_client_default():
    client = SyncSpiceDBClient(
        "localhost:50051", token=TOKEN, insecure=True, default_timeout=7.5
    )
    assert client._effective_timeout(None) == 7.5
    assert client._effective_timeout(1.5) == 1.5
