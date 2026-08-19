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
    # A URI scheme is case-insensitive and C-core normalizes it, so an
    # upper-cased unix target reaches the same UDS connect and must be
    # recognized here too.
    "UNIX:/var/run/spicedb.sock", "Unix:///var/run/spicedb.sock",
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

# Authority-shifting targets: endpoints whose URI authority is not what a
# naive host:port split reads out of them. This exact set defeated the
# equivalent guard in this repo's C#, Rust, TypeScript and Java clients --
# a last-colon (or first-"]") split reads a loopback host out of them while
# those transports parsed the same string as a URI, took "127.0.0.1:443" for
# userinfo, and connected to evil.com, shipping the bearer token there in
# cleartext. grpc-python was not exploitable by them (C-core resolves
# "127.0.0.1:443@evil.com" to ipv4:127.0.0.1:443 and never contacts
# evil.com), but the guard must fail closed on a target it cannot vouch for,
# and this fixture is what would catch a future edit that loosened the split
# here the way the C# one was loosened.
AUTHORITY_SHIFTING_ENDPOINTS = [
    "127.0.0.1:443@evil.com",
    "[::1]:443@evil.com",
    "[::1]:0@127.0.0.1:19999",
    "[localhost]:1@127.0.0.1:19999",
    "localhost@evil.com",
    "localhost/../evil.com",
    "localhost#@evil.com",
    "localhost?@evil.com",
    "localhost.",
    "localhost :50051",
    "127.0.0.1 :50051",
    # The port validation whose removal from the C# guard opened the bypass.
    "127.0.0.1:notaport",
    # Non-ASCII "digits". str.isdigit() is true for all of these, so a port
    # predicate built on it would split here and hand back the loopback host --
    # while C-core, which this fallback exists to mirror, parses none of them
    # as a port. Ruby's /\A\d+\z/ was already correct; Python's was not.
    "127.0.0.1:\u0664\u0664\u0663",
    "127.0.0.1:\uff14\uff14\uff13",
    "127.0.0.1:\u00b2",
]


@pytest.mark.parametrize("endpoint", LOOPBACK_ENDPOINTS)
def test_is_loopback_endpoint_true(endpoint: str):
    assert _is_loopback_endpoint(endpoint) is True


@pytest.mark.parametrize("endpoint", NON_LOOPBACK_ENDPOINTS)
def test_is_loopback_endpoint_false(endpoint: str):
    assert _is_loopback_endpoint(endpoint) is False


@pytest.mark.parametrize("endpoint", AUTHORITY_SHIFTING_ENDPOINTS)
def test_is_loopback_endpoint_false_for_authority_shifting(endpoint: str):
    assert _is_loopback_endpoint(endpoint) is False


@pytest.mark.parametrize("endpoint", AUTHORITY_SHIFTING_ENDPOINTS)
def test_refuses_authority_shifting_endpoint_without_opt_in(endpoint: str):
    """The regression test for the loopback-guard bypass, asserting
    non-transmission rather than "an exception was raised": patching
    grpc.aio.insecure_channel means the channel the token would ride on is
    the mock, and assert_not_called proves it was never even constructed.
    An implementation that opened the channel and only then raised would
    still satisfy pytest.raises but would fail this.
    """
    with mock.patch("grpc.aio.insecure_channel") as insecure_channel:
        with pytest.raises(ValueError, match="allow_insecure_remote_credentials"):
            Client(endpoint, TOKEN, insecure=True)
    insecure_channel.assert_not_called()


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


# F4: an authority-bearing unix target is guard-loopback (the prefix check is
# deliberately case-insensitive and does not inspect the authority), so what
# actually stops it is the transport. C-core refuses to build a usable channel
# for ANY casing -- "authority-based URIs not supported by the unix scheme" ->
# "the target uri is not valid" -- so nothing is dialed and no token moves.
#
# This pins that. It is NOT a red-first regression test: it passes before and
# after, because it asserts existing transport behaviour rather than a change.
# Its job is to fail loudly if C-core ever starts accepting authority-bearing
# unix targets, at which point the guard's permissiveness would become a real
# leak instead of a harmless over-permission.
@pytest.mark.parametrize(
    "endpoint", ["unix://evil.com/tmp/x.sock", "UNIX://evil.com/tmp/x.sock"]
)
def test_authority_bearing_unix_target_is_refused_by_the_transport(endpoint: str):
    # Precondition: the guard alone does not stop these.
    assert _is_loopback_endpoint(endpoint) is True

    channel = grpc.insecure_channel(endpoint)
    try:
        with pytest.raises(grpc.RpcError) as excinfo:
            channel.unary_unary("/demo.Svc/Method")(b"", timeout=5.0)
        assert "target uri is not valid" in excinfo.value.details()
    finally:
        channel.close()
