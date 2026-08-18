"""Regression tests for root DESIGN.md, "RULE: Credentials over insecure
transport require an explicit opt-in".

Reuses the real in-process gRPC server harness from test_auth_headers.py
(`_Recorder`/`_serve`) so the loopback and opt-in cases prove the token is
actually delivered over a real wire, not just that construction succeeded.
"""

from __future__ import annotations

from unittest import mock

import grpc
import pytest

from spicedb._auth import is_loopback_endpoint
from spicedb.aio import SpiceDBClient as AsyncSpiceDBClient
from spicedb.consistency import full
from spicedb.errors import InvalidArgumentError
from spicedb.sync import SpiceDBClient as SyncSpiceDBClient
from tests.test_auth_headers import TOKEN, _Recorder, _serve

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
]


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
        with pytest.raises(InvalidArgumentError, match="allow_insecure_remote_credentials"):
            SyncSpiceDBClient(endpoint, token=TOKEN, insecure=True)
    insecure_channel.assert_not_called()


@pytest.mark.parametrize("endpoint", AUTHORITY_SHIFTING_ENDPOINTS)
def test_aio_refuses_authority_shifting_endpoint_without_opt_in(endpoint: str):
    with mock.patch("grpc.aio.insecure_channel") as insecure_channel:
        with pytest.raises(InvalidArgumentError, match="allow_insecure_remote_credentials"):
            AsyncSpiceDBClient(endpoint, token=TOKEN, insecure=True)
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
        with pytest.raises(InvalidArgumentError, match="allow_insecure_remote_credentials"):
            SyncSpiceDBClient("evil.example.com:1234", token=TOKEN, insecure=True)
    insecure_channel.assert_not_called()


def test_aio_refuses_insecure_non_loopback_without_opt_in():
    with mock.patch("grpc.aio.insecure_channel") as insecure_channel:
        with pytest.raises(InvalidArgumentError, match="allow_insecure_remote_credentials"):
            AsyncSpiceDBClient("evil.example.com:1234", token=TOKEN, insecure=True)
    insecure_channel.assert_not_called()


def test_sync_loopback_allows_insecure_with_no_opt_in_and_sends_token():
    recorder = _Recorder()
    server, port = _serve(recorder, None)
    try:
        client = SyncSpiceDBClient(f"localhost:{port}", token=TOKEN, insecure=True)
        client.check_permissions(full())
        client.close()
    finally:
        server.stop(0)

    assert len(recorder.headers) == 1
    assert recorder.headers[0][1] == f"Bearer {TOKEN}"


async def test_aio_loopback_allows_insecure_with_no_opt_in_and_sends_token():
    recorder = _Recorder()
    server, port = _serve(recorder, None)
    try:
        client = AsyncSpiceDBClient(f"localhost:{port}", token=TOKEN, insecure=True)
        await client.check_permissions(full())
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
                insecure=True,
                allow_insecure_remote_credentials=True,
            )
            client.check_permissions(full())
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
                insecure=True,
                allow_insecure_remote_credentials=True,
            )
            await client.check_permissions(full())
            await client.close()
    finally:
        server.stop(0)

    assert len(recorder.headers) == 1
    assert recorder.headers[0][1] == f"Bearer {TOKEN}"
