"""Startup readiness check: root DESIGN.md, "Accept the endpoint forms
deployments use, or reject them at construction" (lazy clients offer one).

Both clients connect lazily, so ``ready()`` is how a wrong or unreachable
endpoint fails at startup instead of on the first real call.
"""

from __future__ import annotations

import socket
import time

import grpc
import pytest

from spicedb import TransportSecurity
from spicedb.aio import SpiceDBClient as AsyncSpiceDBClient
from spicedb.errors import DeadlineExceededError, LocalValidationError, UnavailableError
from spicedb.sync import SpiceDBClient as SyncSpiceDBClient
from spicedb.testing import AsyncInMemoryClient, InMemoryClient
from tests.test_auth_headers import TOKEN, _Recorder, _serve

PLAIN = TransportSecurity.PLAINTEXT


@pytest.fixture
def server_port():
    server, port = _serve(_Recorder(), None)
    yield port
    server.stop(0)


@pytest.fixture
def stalled_port():
    """A port that accepts TCP but never speaks HTTP/2, so the channel stays
    CONNECTING for as long as the test waits."""
    listener = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    listener.bind(("127.0.0.1", 0))
    listener.listen(16)
    yield listener.getsockname()[1]
    listener.close()


def test_sync_connect_returns_once_ready(server_port):
    with SyncSpiceDBClient(f"127.0.0.1:{server_port}", token=TOKEN, transport_security=PLAIN) as client:
        client.ready(timeout=5.0)


async def test_aio_connect_returns_once_ready(server_port):
    async with AsyncSpiceDBClient(f"127.0.0.1:{server_port}", token=TOKEN, transport_security=PLAIN) as client:
        await client.ready(timeout=5.0)


def test_sync_connect_over_dns_target(server_port):
    with SyncSpiceDBClient(f"dns:///localhost:{server_port}", token=TOKEN, transport_security=PLAIN) as client:
        client.ready(timeout=5.0)


def test_sync_connect_refused_names_endpoint_and_cause():
    client = SyncSpiceDBClient("127.0.0.1:1", token=TOKEN, transport_security=PLAIN)
    try:
        with pytest.raises(UnavailableError) as excinfo:
            client.ready(timeout=1.0)
    finally:
        client.close()
    message = str(excinfo.value)
    assert "'127.0.0.1:1' not ready within 1s" in message
    assert "refused" in message.lower()
    assert excinfo.value.code() is grpc.StatusCode.UNAVAILABLE


async def test_aio_connect_refused_names_endpoint_and_cause():
    client = AsyncSpiceDBClient("127.0.0.1:1", token=TOKEN, transport_security=PLAIN)
    try:
        with pytest.raises(UnavailableError) as excinfo:
            await client.ready(timeout=1.0)
    finally:
        await client.close()
    message = str(excinfo.value)
    assert "'127.0.0.1:1' not ready within 1s" in message
    assert "refused" in message.lower()


def test_sync_connect_unresolvable_name_reports_dns():
    client = SyncSpiceDBClient("dns:///spicedb.invalid:50051", token=TOKEN)
    try:
        with pytest.raises(UnavailableError, match="(?i)dns"):
            client.ready(timeout=2.0)
    finally:
        client.close()


def test_sync_connect_still_connecting_is_deadline_exceeded(stalled_port):
    client = SyncSpiceDBClient(f"127.0.0.1:{stalled_port}", token=TOKEN, transport_security=PLAIN)
    started = time.monotonic()
    try:
        with pytest.raises(DeadlineExceededError, match="still connecting"):
            client.ready(timeout=0.5)
    finally:
        client.close()
    assert time.monotonic() - started < 3.0


async def test_aio_connect_still_connecting_is_deadline_exceeded(stalled_port):
    client = AsyncSpiceDBClient(f"127.0.0.1:{stalled_port}", token=TOKEN, transport_security=PLAIN)
    try:
        with pytest.raises(DeadlineExceededError, match="still connecting"):
            await client.ready(timeout=0.5)
    finally:
        await client.close()


def test_connect_defaults_to_the_client_deadline():
    client = SyncSpiceDBClient(
        "127.0.0.1:1", token=TOKEN, transport_security=PLAIN, default_timeout=0.5
    )
    try:
        with pytest.raises(UnavailableError, match="within 0.5s"):
            client.ready()
    finally:
        client.close()


def test_sync_connect_over_a_supplied_channel(server_port):
    channel = grpc.insecure_channel(f"127.0.0.1:{server_port}")
    try:
        SyncSpiceDBClient.from_channel(channel).ready(timeout=5.0)
    finally:
        channel.close()


def test_sync_connect_rejects_an_invalid_timeout_before_dialing():
    client = SyncSpiceDBClient("127.0.0.1:1", token=TOKEN, transport_security=PLAIN)
    with pytest.raises(LocalValidationError, match="timeout"):
        client.ready(timeout=-1)
    assert client._channel is None


async def test_aio_connect_rejects_an_invalid_timeout_before_dialing():
    client = AsyncSpiceDBClient("127.0.0.1:1", token=TOKEN, transport_security=PLAIN)
    with pytest.raises(LocalValidationError, match="timeout"):
        await client.ready(timeout=-1)
    assert client._channel is None


def test_double_connect_is_a_noop():
    double = InMemoryClient()
    double.ready()
    double.ready(timeout=1.0)
    assert double.calls() == []
    with pytest.raises(LocalValidationError):
        double.ready(timeout=0)


async def test_async_double_connect_is_a_noop():
    double = AsyncInMemoryClient()
    await double.ready(timeout=1.0)
    with pytest.raises(LocalValidationError):
        await double.ready(timeout=float("nan"))
