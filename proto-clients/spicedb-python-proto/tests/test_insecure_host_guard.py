"""Regression tests for root DESIGN.md, "RULE: Credentials over insecure
transport require an explicit opt-in".
"""

from unittest import mock

import grpc
import grpc.aio
import pytest
from authzed.api.v1 import permission_service_pb2 as psp

from client import Client, _is_loopback_endpoint

TOKEN = "test-token"

LOOPBACK_ENDPOINTS = [
    "localhost:50051", "LOCALHOST:50051", "localhost",
    "127.0.0.1:50051", "127.0.0.1", "127.55.66.77:50051",
    "[::1]:50051", "::1",
    "unix:/var/run/spicedb.sock", "unix:///var/run/spicedb.sock",
]

NON_LOOPBACK_ENDPOINTS = [
    "example.com:443", "staging.internal:443",
    "10.0.0.5:50051", "8.8.8.8:443", "0.0.0.0:50051",
    # Typosquats/lookalikes: a future refactor toward substring matching on
    # "localhost"/"127.0.0.1" would wrongly treat these as loopback and
    # reopen a credential leak. Must stay non-loopback under exact-match
    # host comparison.
    "localhost.evil.com:443", "127.0.0.1.evil.com:443", "evil-localhost:443",
]


@pytest.mark.parametrize("endpoint", LOOPBACK_ENDPOINTS)
def test_is_loopback_endpoint_true(endpoint: str):
    assert _is_loopback_endpoint(endpoint) is True


@pytest.mark.parametrize("endpoint", NON_LOOPBACK_ENDPOINTS)
def test_is_loopback_endpoint_false(endpoint: str):
    assert _is_loopback_endpoint(endpoint) is False


def test_refuses_insecure_non_loopback_without_opt_in():
    """The regression test: a rejected combination must never reach
    grpc.aio.insecure_channel at all -- proving the token never reaches
    anything capable of putting it on the wire, not merely that an
    exception was raised.
    """
    with mock.patch("grpc.aio.insecure_channel") as insecure_channel:
        with pytest.raises(ValueError, match="allow_insecure_remote_credentials"):
            Client("evil.example.com:1234", TOKEN, insecure=True)
    insecure_channel.assert_not_called()


async def _serve():
    """Starts a real in-process aio gRPC server that records the
    "authorization" metadata it observes on CheckBulkPermissions, using
    identity byte serializers -- the real Client still parses genuine
    CheckBulkPermissionsResponse bytes back out, so this proves the token
    was carried over an actual send, not merely constructed.
    """
    recorded: list[str | None] = []

    async def handler(request: bytes, context: grpc.aio.ServicerContext) -> bytes:
        md = dict(context.invocation_metadata())
        recorded.append(md.get("authorization"))
        return psp.CheckBulkPermissionsResponse().SerializeToString()

    server = grpc.aio.server()
    server.add_generic_rpc_handlers(
        (
            grpc.method_handlers_generic_handler(
                "authzed.api.v1.PermissionsService",
                {
                    "CheckBulkPermissions": grpc.unary_unary_rpc_method_handler(
                        handler, lambda b: b, lambda b: b
                    ),
                },
            ),
        )
    )
    port = server.add_insecure_port("localhost:0")
    await server.start()
    return server, port, recorded


async def test_loopback_allows_insecure_with_no_opt_in_and_sends_token():
    server, port, recorded = await _serve()
    try:
        client = Client(f"localhost:{port}", TOKEN, insecure=True)
        await client.permissions.CheckBulkPermissions(
            psp.CheckBulkPermissionsRequest()
        )
        await client.close()
    finally:
        await server.stop(None)

    assert recorded == [f"Bearer {TOKEN}"]


async def test_allow_insecure_remote_credentials_sends_token_to_non_loopback():
    """Redirects a non-loopback-looking endpoint to the real local server via
    grpc.aio.insecure_channel, proving the opt-in actually attaches and
    sends the token once construction is permitted for a non-loopback
    endpoint.
    """
    server, port, recorded = await _serve()
    real_insecure_channel = grpc.aio.insecure_channel
    try:
        with mock.patch(
            "grpc.aio.insecure_channel",
            side_effect=lambda target, *a, **kw: real_insecure_channel(
                f"localhost:{port}", *a, **kw
            ),
        ):
            client = Client(
                "evil.example.com:1234",
                TOKEN,
                insecure=True,
                allow_insecure_remote_credentials=True,
            )
            await client.permissions.CheckBulkPermissions(
                psp.CheckBulkPermissionsRequest()
            )
            await client.close()
    finally:
        await server.stop(None)

    assert recorded == [f"Bearer {TOKEN}"]
