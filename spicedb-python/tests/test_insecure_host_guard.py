"""Regression tests for root DESIGN.md, "RULE: Credentials over insecure
transport require an explicit opt-in".

Reuses the real in-process gRPC server harness from test_auth_headers.py
(`_Recorder`/`_serve`) so the loopback and opt-in cases prove the token is
actually delivered over a real wire, not just that construction succeeded.
"""

from __future__ import annotations
from spicedb import TransportSecurity, RemoteCredentials
from spicedb import PermissionCheck
from unittest import mock
import grpc
import pytest
from spicedb import Relationship
from spicedb._auth import is_loopback_endpoint
from spicedb.aio import SpiceDBClient as AsyncSpiceDBClient
from spicedb.consistency import full
from spicedb.errors import InvalidArgumentError
from spicedb.sync import SpiceDBClient as SyncSpiceDBClient
from tests.test_auth_headers import TOKEN, _Recorder, _serve

_ONE_REL = Relationship.from_triple("document:readme", "view", "user:alice")
LOOPBACK_ENDPOINTS = [
    "localhost:50051",
    "LOCALHOST:50051",
    "localhost",
    "127.0.0.1:50051",
    "127.0.0.1",
    "127.55.66.77:50051",
    "[::1]:50051",
    "::1",
    "unix:/var/run/spicedb.sock",
    "unix:///var/run/spicedb.sock",
    "UNIX:/var/run/spicedb.sock",
    "Unix:///var/run/spicedb.sock",
    "dns:///localhost:50051",
    "DNS:///127.0.0.1:50051",
    "dns:///[::1]:50051",
]
NON_LOOPBACK_ENDPOINTS = [
    "example.com:443",
    "staging.internal:443",
    "10.0.0.5:50051",
    "8.8.8.8:443",
    "0.0.0.0:50051",
    "localhost.evil.com:443",
    "127.0.0.1.evil.com:443",
    "evil-localhost:443",
    "dns:///example.com:443",
    "dns:///10.0.0.5:50051",
]
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
    "127.0.0.1:notaport",
    "127.0.0.1:٤٤٣",
    "127.0.0.1:４４３",
    "127.0.0.1:²",
    "dns:///127.0.0.1:443@evil.com",
    "dns:////localhost:50051",
    "dns://evil.com/localhost:50051",
]


# Every authority-shifting endpoint is malformed (including "localhost.",
# a plaintext endpoint without a port), so it fails as invalid before the
# remote-plaintext policy runs; the lookalike hosts below are well-formed and
# get that policy's message instead.
@pytest.mark.parametrize("endpoint", ["localhost.:50051", "127.0.0.1.evil.com:443"])
def test_well_formed_lookalike_host_gets_the_remote_policy(endpoint: str):
    with mock.patch("grpc.insecure_channel") as insecure_channel:
        with pytest.raises(InvalidArgumentError, match="remote_credentials"):
            SyncSpiceDBClient(endpoint, token=TOKEN, transport_security=TransportSecurity.PLAINTEXT)
    insecure_channel.assert_not_called()


@pytest.mark.parametrize("endpoint", LOOPBACK_ENDPOINTS)
def test_is_loopback_endpoint_true(endpoint: str):
    assert is_loopback_endpoint(endpoint) is True


@pytest.mark.parametrize("endpoint", NON_LOOPBACK_ENDPOINTS)
def test_is_loopback_endpoint_false(endpoint: str):
    assert is_loopback_endpoint(endpoint) is False


@pytest.mark.parametrize("endpoint", AUTHORITY_SHIFTING_ENDPOINTS)
def test_is_loopback_endpoint_false_for_authority_shifting(endpoint: str):
    assert is_loopback_endpoint(endpoint) is False


@pytest.mark.parametrize("endpoint", AUTHORITY_SHIFTING_ENDPOINTS)
def test_sync_refuses_authority_shifting_endpoint_without_opt_in(endpoint: str):
    """The regression test for the loopback-guard bypass, asserting
    non-transmission rather than "an exception was raised": patching
    grpc.insecure_channel means the channel the token would ride on is the
    mock, and assert_not_called proves it was never even constructed. An
    implementation that opened the channel and only then raised would still
    satisfy pytest.raises but would fail this.
    """
    with mock.patch("grpc.insecure_channel") as insecure_channel:
        with pytest.raises(
            InvalidArgumentError, match="invalid endpoint"
        ):
            SyncSpiceDBClient(endpoint, token=TOKEN, transport_security=TransportSecurity.PLAINTEXT)
    insecure_channel.assert_not_called()


@pytest.mark.parametrize("endpoint", AUTHORITY_SHIFTING_ENDPOINTS)
def test_aio_refuses_authority_shifting_endpoint_without_opt_in(endpoint: str):
    with mock.patch("grpc.aio.insecure_channel") as insecure_channel:
        with pytest.raises(
            InvalidArgumentError, match="invalid endpoint"
        ):
            AsyncSpiceDBClient(endpoint, token=TOKEN, transport_security=TransportSecurity.PLAINTEXT)
    insecure_channel.assert_not_called()


def test_sync_refuses_insecure_non_loopback_without_opt_in():
    """The regression test: a rejected combination must never reach
    grpc.insecure_channel at all -- proving the token never reaches
    anything capable of putting it on the wire, not merely that an
    exception was raised. An implementation that opened the channel and
    only then raised would still fail a bare pytest.raises check but would
    fail this assert_not_called().
    """
    with mock.patch("grpc.insecure_channel") as insecure_channel:
        with pytest.raises(
            InvalidArgumentError, match="remote_credentials"
        ):
            SyncSpiceDBClient("evil.example.com:1234", token=TOKEN, transport_security=TransportSecurity.PLAINTEXT)
    insecure_channel.assert_not_called()


def test_aio_refuses_insecure_non_loopback_without_opt_in():
    with mock.patch("grpc.aio.insecure_channel") as insecure_channel:
        with pytest.raises(
            InvalidArgumentError, match="remote_credentials"
        ):
            AsyncSpiceDBClient("evil.example.com:1234", token=TOKEN, transport_security=TransportSecurity.PLAINTEXT)
    insecure_channel.assert_not_called()


def test_sync_loopback_allows_insecure_with_no_opt_in_and_sends_token():
    recorder = _Recorder()
    server, port = _serve(recorder, None)
    try:
        client = SyncSpiceDBClient(f"localhost:{port}", token=TOKEN, transport_security=TransportSecurity.PLAINTEXT)
        client.check_bulk([_check(_ONE_REL)], consistency=full())
        client.close()
    finally:
        server.stop(0)
    assert len(recorder.headers) == 1
    assert recorder.headers[0][1] == f"Bearer {TOKEN}"


async def test_aio_loopback_allows_insecure_with_no_opt_in_and_sends_token():
    recorder = _Recorder()
    server, port = _serve(recorder, None)
    try:
        client = AsyncSpiceDBClient(f"localhost:{port}", token=TOKEN, transport_security=TransportSecurity.PLAINTEXT)
        await client.check_bulk([_check(_ONE_REL)], consistency=full())
        await client.close()
    finally:
        server.stop(0)
    assert len(recorder.headers) == 1
    assert recorder.headers[0][1] == f"Bearer {TOKEN}"


def test_sync_allow_insecure_remote_credentials_sends_token_to_non_loopback():
    """Redirects a non-loopback-looking endpoint to the real local server via
    grpc.insecure_channel -- the same patching technique test_auth_headers.py
    already uses for grpc.ssl_channel_credentials (`_trusting`) -- proving
    the opt-in actually attaches and sends the token once construction is
    permitted for a non-loopback endpoint.
    """
    recorder = _Recorder()
    server, port = _serve(recorder, None)
    real_insecure_channel = grpc.insecure_channel
    try:
        with mock.patch(
            "grpc.insecure_channel",
            side_effect=lambda target, *a, **kw: real_insecure_channel(
                f"localhost:{port}", *a, **kw
            ),
        ):
            client = SyncSpiceDBClient(
                "evil.example.com:1234",
                token=TOKEN,
                transport_security=TransportSecurity.PLAINTEXT,
                remote_credentials=RemoteCredentials.ALLOW,
            )
            client.check_bulk([_check(_ONE_REL)], consistency=full())
            client.close()
    finally:
        server.stop(0)
    assert len(recorder.headers) == 1
    assert recorder.headers[0][1] == f"Bearer {TOKEN}"


async def test_aio_allow_insecure_remote_credentials_sends_token_to_non_loopback():
    recorder = _Recorder()
    server, port = _serve(recorder, None)
    real_insecure_channel = grpc.aio.insecure_channel
    try:
        with mock.patch(
            "grpc.aio.insecure_channel",
            side_effect=lambda target, *a, **kw: real_insecure_channel(
                f"localhost:{port}", *a, **kw
            ),
        ):
            client = AsyncSpiceDBClient(
                "evil.example.com:1234",
                token=TOKEN,
                transport_security=TransportSecurity.PLAINTEXT,
                remote_credentials=RemoteCredentials.ALLOW,
            )
            await client.check_bulk([_check(_ONE_REL)], consistency=full())
            await client.close()
    finally:
        server.stop(0)
    assert len(recorder.headers) == 1
    assert recorder.headers[0][1] == f"Bearer {TOKEN}"


@pytest.mark.parametrize(
    "endpoint", ["unix://evil.com/tmp/x.sock", "UNIX://evil.com/tmp/x.sock"]
)
def test_authority_bearing_unix_target_is_refused_by_the_transport(endpoint: str):
    assert is_loopback_endpoint(endpoint) is True
    channel = grpc.insecure_channel(endpoint)
    try:
        with pytest.raises(grpc.RpcError) as excinfo:
            channel.unary_unary("/demo.Svc/Method")(b"", timeout=5.0)
        assert "target uri is not valid" in excinfo.value.details()
    finally:
        channel.close()


def _check(value):
    if isinstance(value, PermissionCheck):
        return value
    return PermissionCheck(value.resource, value.relation, value.subject)
