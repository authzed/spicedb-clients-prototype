"""Wire-level regression test for the authorization header.

Guards spec decision D8. Runs a real in-process gRPC server and counts the
`authorization` headers that actually arrive. Needs no SpiceDB instance.

Before D8 the secure path sent the header TWICE (interceptor + call
credentials). These tests fail loudly if that regresses.
"""

from __future__ import annotations

import contextlib
import pathlib
import subprocess
import tempfile
from concurrent import futures
from unittest import mock

import grpc
import pytest
from authzed.api.v1 import permission_service_pb2 as psp

from spicedb import Filter, Relationship
from spicedb.aio import SpiceDBClient
from spicedb.consistency import full

TOKEN = "test-token"


@pytest.fixture(scope="module")
def tls_pair() -> tuple[bytes, bytes]:
    """Self-signed cert/key for localhost."""
    with tempfile.TemporaryDirectory() as d:
        key = pathlib.Path(d) / "key.pem"
        crt = pathlib.Path(d) / "cert.pem"
        subprocess.run(
            [
                "openssl", "req", "-x509", "-newkey", "rsa:2048",
                "-keyout", str(key), "-out", str(crt), "-days", "1", "-nodes",
                "-subj", "/CN=localhost",
                "-addext", "subjectAltName=DNS:localhost",
            ],
            check=True,
            capture_output=True,
        )
        yield crt.read_bytes(), key.read_bytes()


class _Recorder:
    def __init__(self) -> None:
        self.headers: list[tuple[str, str]] = []

    def _record(self, context) -> None:
        self.headers.extend(
            (k, v) for k, v in context.invocation_metadata() if k == "authorization"
        )

    def check_bulk(self, request: bytes, context) -> bytes:
        self._record(context)
        return psp.CheckBulkPermissionsResponse().SerializeToString()

    def read_relationships(self, request: bytes, context):
        self._record(context)
        return iter(())  # empty page -> client stops after one request

    def import_bulk(self, request_iterator, context) -> bytes:
        """Stream-unary handler: drains the real request feed the sync/aio
        clients hand to grpc, mirroring what a real server does.
        """
        self._record(context)
        total = 0
        for raw in request_iterator:
            req = psp.ImportBulkRelationshipsRequest()
            req.ParseFromString(raw)
            total += len(req.relationships)
        return psp.ImportBulkRelationshipsResponse(num_loaded=total).SerializeToString()


def _serve(recorder: _Recorder, tls: tuple[bytes, bytes] | None):
    """Serve the real SpiceDB method paths so a real client can drive them.

    Identity serializers let handlers deal in raw bytes; the client still
    deserializes the returned bytes as the proper response proto.
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
                    "ReadRelationships": grpc.unary_stream_rpc_method_handler(
                        recorder.read_relationships, lambda b: b, lambda b: b
                    ),
                    "ImportBulkRelationships": grpc.stream_unary_rpc_method_handler(
                        recorder.import_bulk, lambda b: b, lambda b: b
                    ),
                },
            ),
        )
    )
    if tls is None:
        port = server.add_insecure_port("localhost:0")
    else:
        crt, key = tls
        port = server.add_secure_port(
            "localhost:0", grpc.ssl_server_credentials([(key, crt)])
        )
    server.start()
    return server, port


@contextlib.contextmanager
def _trusting(tls_pair):
    """Make the client's `grpc.ssl_channel_credentials()` trust the test CA.

    Patching here rather than adding a test-only seam to the client keeps the
    shipping code free of hooks that exist purely for tests.
    """
    real = grpc.ssl_channel_credentials
    with mock.patch.object(
        grpc,
        "ssl_channel_credentials",
        lambda *a, **kw: real(root_certificates=tls_pair[0]),
    ):
        yield


@pytest.mark.parametrize("secure", [False, True])
@pytest.mark.parametrize("streaming", [False, True])
async def test_aio_sends_exactly_one_authorization_header(
    secure: bool, streaming: bool, tls_pair
):
    recorder = _Recorder()
    server, port = _serve(recorder, tls_pair if secure else None)
    try:
        with _trusting(tls_pair) if secure else contextlib.nullcontext():
            client = SpiceDBClient(
                f"localhost:{port}", token=TOKEN, insecure=not secure
            )
            if streaming:
                async for _ in client.read_relationships(
                    Filter(resource_type="document"), full()
                ):
                    pass
            else:
                await client.check_permissions(full())
            await client.close()
    finally:
        server.stop(0)

    assert len(recorder.headers) == 1, (
        f"expected exactly 1 authorization header, got {len(recorder.headers)}: "
        f"{recorder.headers}"
    )
    assert recorder.headers[0][1] == f"Bearer {TOKEN}"


@pytest.mark.parametrize("secure", [False, True])
def test_sync_import_relationships_sends_exactly_one_authorization_header(
    secure: bool, tls_pair
):
    """Drives spicedb.sync's ImportBulkRelationships over a real grpc.Channel.

    `import_relationships` is the sync client's only stream-unary call --
    a fundamentally different grpc.Channel code path (request-iterator feed)
    than every other call, which is unary. This is the only test that
    actually runs that path against a real (in-process) gRPC server: it
    proves the `_requests.import_batches(...)` generator is drained by the
    real stream-unary send, that the resulting request reaches the server
    with the expected relationships (via `num_loaded`), and that the bearer
    auth metadata is attached exactly once on that path too.
    """
    from spicedb.sync import SpiceDBClient as SyncSpiceDBClient

    recorder = _Recorder()
    server, port = _serve(recorder, tls_pair if secure else None)
    try:
        with _trusting(tls_pair) if secure else contextlib.nullcontext():
            client = SyncSpiceDBClient(
                f"localhost:{port}", token=TOKEN, insecure=not secure
            )
            rels = [
                Relationship.from_triple("document:a", "viewer", "user:jimmy"),
                Relationship.from_triple("document:b", "viewer", "user:jimmy"),
            ]
            num_loaded = client.import_relationships(rels)
            client.close()
    finally:
        server.stop(0)

    assert num_loaded == 2, "the real server must see both relationships sent"
    assert len(recorder.headers) == 1, (
        f"expected exactly 1 authorization header, got {len(recorder.headers)}: "
        f"{recorder.headers}"
    )
    assert recorder.headers[0][1] == f"Bearer {TOKEN}"


@pytest.mark.parametrize("secure", [False, True])
@pytest.mark.parametrize("streaming", [False, True])
def test_sync_sends_exactly_one_authorization_header(
    secure: bool, streaming: bool, tls_pair
):
    from spicedb.sync import SpiceDBClient as SyncSpiceDBClient

    recorder = _Recorder()
    server, port = _serve(recorder, tls_pair if secure else None)
    try:
        with _trusting(tls_pair) if secure else contextlib.nullcontext():
            client = SyncSpiceDBClient(
                f"localhost:{port}", token=TOKEN, insecure=not secure
            )
            if streaming:
                for _ in client.read_relationships(
                    Filter(resource_type="document"), full()
                ):
                    pass
            else:
                client.check_permissions(full())
            client.close()
    finally:
        server.stop(0)

    assert len(recorder.headers) == 1, (
        f"expected exactly 1 authorization header, got {len(recorder.headers)}: "
        f"{recorder.headers}"
    )
    assert recorder.headers[0][1] == f"Bearer {TOKEN}"
