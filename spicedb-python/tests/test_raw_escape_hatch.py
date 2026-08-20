"""The escape hatch reaches a real stub and makes a real call.

`raw_grpc()` exists so a gap in the idiomatic surface has a workaround short of
forking the client -- root DESIGN.md, "What NOT To Do", permits exactly this as
"clearly marked secondary API". A test that merely asserts the accessor exists,
or that the object it returns is a `grpc.Channel`, would prove none of that: the
question is whether a caller can build a generated stub on what comes back and
get an answer out of a real server, with this client's bearer token attached.

So these tests run a real in-process gRPC server (as tests/test_auth_headers.py
does), drive the hatch through the generated `PermissionsServiceStub`, and check
both the response and the `authorization` header the server actually received.
"""

from __future__ import annotations

import inspect
from concurrent import futures

import grpc
import pytest
from authzed.api.v1 import permission_service_pb2 as psp
from authzed.api.v1 import permission_service_pb2_grpc as psg

from spicedb.aio import SpiceDBClient as AsyncClient
from spicedb.raw import RawGrpc
from spicedb.sync import SpiceDBClient as SyncClient

TOKEN = "test-token"


class _Recorder:
    """Serves CheckBulkPermissions for real, recording what arrived."""

    def __init__(self) -> None:
        self.headers: list[str] = []

    def check_bulk(self, request: bytes, context) -> bytes:
        self.headers.extend(
            v for k, v in context.invocation_metadata() if k == "authorization"
        )
        req = psp.CheckBulkPermissionsRequest()
        req.ParseFromString(request)
        resp = psp.CheckBulkPermissionsResponse()
        for _ in req.items:
            pair = resp.pairs.add()
            pair.item.permissionship = (
                psp.CheckPermissionResponse.PERMISSIONSHIP_HAS_PERMISSION
            )
        return resp.SerializeToString()


def _serve(recorder: _Recorder):
    """Serve the real method path so a real generated stub can drive it.

    Identity serializers let the handler deal in raw bytes; the stub on the
    client side still (de)serializes the proper protos, so the round trip
    exercises the generated code end to end.
    """
    server = grpc.server(futures.ThreadPoolExecutor(max_workers=4))
    server.add_generic_rpc_handlers(
        (
            grpc.method_handlers_generic_handler(
                "authzed.api.v1.PermissionsService",
                {
                    "CheckBulkPermissions": grpc.unary_unary_rpc_method_handler(
                        recorder.check_bulk, lambda b: b, lambda b: b
                    ),
                },
            ),
        )
    )
    port = server.add_insecure_port("localhost:0")
    server.start()
    return server, port


def _request() -> psp.CheckBulkPermissionsRequest:
    req = psp.CheckBulkPermissionsRequest()
    req.consistency.fully_consistent = True
    item = req.items.add()
    item.resource.object_type = "document"
    item.resource.object_id = "doc1"
    item.permission = "view"
    item.subject.object.object_type = "user"
    item.subject.object.object_id = "alice"
    return req


def test_sync_raw_grpc_drives_a_real_stub_against_a_real_server():
    recorder = _Recorder()
    server, port = _serve(recorder)
    try:
        client = SyncClient(f"localhost:{port}", token=TOKEN, insecure=True)
        try:
            raw = client.raw_grpc()
            stub = psg.PermissionsServiceStub(raw.channel)
            resp = stub.CheckBulkPermissions(_request(), metadata=raw.metadata)
        finally:
            client.close()
    finally:
        server.stop(0)

    assert len(resp.pairs) == 1
    assert (
        resp.pairs[0].item.permissionship
        == psp.CheckPermissionResponse.PERMISSIONSHIP_HAS_PERMISSION
    )
    # The hatch's metadata is what authenticates a raw call: this client
    # attaches the token per call, not on the channel.
    assert recorder.headers == [f"Bearer {TOKEN}"]


async def test_aio_raw_grpc_drives_a_real_stub_against_a_real_server():
    recorder = _Recorder()
    server, port = _serve(recorder)
    try:
        client = AsyncClient(f"localhost:{port}", token=TOKEN, insecure=True)
        try:
            raw = await client.raw_grpc()
            stub = psg.PermissionsServiceStub(raw.channel)
            resp = await stub.CheckBulkPermissions(_request(), metadata=raw.metadata)
        finally:
            await client.close()
    finally:
        server.stop(0)

    assert len(resp.pairs) == 1
    assert (
        resp.pairs[0].item.permissionship
        == psp.CheckPermissionResponse.PERMISSIONSHIP_HAS_PERMISSION
    )
    assert recorder.headers == [f"Bearer {TOKEN}"]


def test_sync_raw_grpc_hands_back_the_channel_the_client_itself_uses():
    """Not a second connection built behind the caller's back."""
    client = SyncClient("localhost:50051", token=TOKEN, insecure=True)
    try:
        raw = client.raw_grpc()
        assert raw.channel is client._channel
        assert client.raw_grpc().channel is raw.channel
    finally:
        client.close()


async def test_aio_raw_grpc_hands_back_the_channel_the_client_itself_uses():
    client = AsyncClient("localhost:50051", token=TOKEN, insecure=True)
    try:
        raw = await client.raw_grpc()
        assert raw.channel is client._channel
        assert (await client.raw_grpc()).channel is raw.channel
    finally:
        await client.close()


def test_raw_grpc_metadata_cannot_corrupt_the_client_s_own():
    """An immutable copy, so a caller holding it cannot break later calls."""
    client = SyncClient("localhost:50051", token=TOKEN, insecure=True)
    try:
        raw = client.raw_grpc()
        assert raw.metadata == (("authorization", f"Bearer {TOKEN}"),)
        assert isinstance(raw.metadata, tuple)
        assert raw.metadata is not client._metadata
        with pytest.raises((AttributeError, TypeError)):
            raw.metadata.append(("x", "y"))  # type: ignore[attr-defined]
    finally:
        client.close()


@pytest.mark.parametrize("cls", [SyncClient, AsyncClient])
def test_raw_grpc_is_an_accessor_not_a_second_construction_path(cls):
    """The hatch must never grow into a way to build a connection.

    Root DESIGN.md, "RULE: Credentials over insecure transport require an
    explicit opt-in", is enforced in `__init__`, on the single path that
    creates a channel. Handing back an already-built channel cannot bypass
    that; accepting an endpoint, token, or transport setting here would --
    it would be a second construction path with no guard on it. Assert the
    shape that makes the bypass impossible: `raw_grpc` takes nothing but
    `self`, and `spicedb.raw` defines no constructor at all.
    """
    assert list(inspect.signature(cls.raw_grpc).parameters) == ["self"]

    import spicedb.raw as raw_module

    defined_here = sorted(
        n
        for n in dir(raw_module)
        if not n.startswith("_")
        and getattr(getattr(raw_module, n), "__module__", None) == "spicedb.raw"
    )
    assert defined_here == ["RawGrpc"]
    assert list(inspect.signature(RawGrpc).parameters) == ["channel", "metadata"]
