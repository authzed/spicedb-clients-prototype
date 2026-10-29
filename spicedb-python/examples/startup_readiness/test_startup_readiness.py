"""Fail at startup, not on the first request: ``connect()`` and target forms.

Both clients connect lazily. Call ``connect(timeout=...)`` once at startup
(for example in a lifespan hook) so a wrong or unreachable endpoint stops the
process with a clear error instead of failing the first user request.

The endpoint may be ``host:port``, an ``http://``/``https://`` URL, grpc's
``dns:///host:port`` (the DNS resolver, for client-side load balancing over
every address the name resolves to) or a ``unix:`` socket.
"""

import os

import pytest
from spicedb import TransportSecurity
from spicedb.aio import SpiceDBClient as AsyncSpiceDBClient
from spicedb.errors import UnavailableError
from spicedb.sync import SpiceDBClient
from conftest import ENDPOINT, SCHEMA, TOKEN, clear_documents, clear_documents_sync

PLAIN = TransportSecurity.PLAINTEXT

pytestmark = pytest.mark.integration


def test_sync_connect_at_startup():
    with SpiceDBClient(ENDPOINT, token=TOKEN, transport_security=PLAIN) as client:
        client.connect(timeout=10.0)
        clear_documents_sync(client)
        client.write_schema(SCHEMA)
        assert "definition document" in client.read_schema().schema


async def test_aio_connect_at_startup():
    async with AsyncSpiceDBClient(ENDPOINT, token=TOKEN, transport_security=PLAIN) as client:
        await client.connect(timeout=10.0)
        await clear_documents(client)
        await client.write_schema(SCHEMA)
        assert "definition document" in (await client.read_schema()).schema


def test_unreachable_endpoint_fails_at_startup():
    with SpiceDBClient("localhost:1", token=TOKEN, transport_security=PLAIN) as client:
        with pytest.raises(UnavailableError) as excinfo:
            client.connect(timeout=1.0)
    # The error names the endpoint and the transport's own cause.
    assert "'localhost:1' not ready" in str(excinfo.value)
    assert "refused" in str(excinfo.value).lower()


def test_sync_dns_target():
    with SpiceDBClient(f"dns:///{ENDPOINT}", token=TOKEN, transport_security=PLAIN) as client:
        client.connect(timeout=10.0)
        clear_documents_sync(client)
        client.write_schema(SCHEMA)
        assert "definition document" in client.read_schema().schema


async def test_aio_dns_target():
    async with AsyncSpiceDBClient(f"dns:///{ENDPOINT}", token=TOKEN, transport_security=PLAIN) as client:
        await client.connect(timeout=10.0)
        await clear_documents(client)
        await client.write_schema(SCHEMA)
        assert "definition document" in (await client.read_schema()).schema


# A unix socket needs a SpiceDB on this host, serving with
# `--grpc-network unix --grpc-addr <path>`; the containerized CI SpiceDB
# cannot share one. Set SPICEDB_UNIX_SOCKET to that path to run these.
UNIX_SOCKET = os.environ.get("SPICEDB_UNIX_SOCKET")
needs_socket = pytest.mark.skipif(not UNIX_SOCKET, reason="SPICEDB_UNIX_SOCKET not set")


@needs_socket
def test_sync_unix_socket_target():
    with SpiceDBClient(f"unix:{UNIX_SOCKET}", token=TOKEN, transport_security=PLAIN) as client:
        client.connect(timeout=10.0)
        clear_documents_sync(client)
        client.write_schema(SCHEMA)
        assert "definition document" in client.read_schema().schema


@needs_socket
async def test_aio_unix_socket_target():
    async with AsyncSpiceDBClient(f"unix:{UNIX_SOCKET}", token=TOKEN, transport_security=PLAIN) as client:
        await client.connect(timeout=10.0)
        await clear_documents(client)
        await client.write_schema(SCHEMA)
        assert "definition document" in (await client.read_schema()).schema
