"""Regression tests for root DESIGN.md, "Accept the endpoint forms deployments
use, or reject them at construction".

Every rejected form must fail in the constructor, before any channel exists,
with an error naming the problem; every accepted URL must dial the host and
port it names.
"""

from __future__ import annotations

import os
import shutil
import tempfile
from concurrent import futures
from unittest import mock

import grpc
import pytest

from spicedb import PermissionCheck, Relationship, RemoteCredentials, TransportSecurity
from spicedb._endpoint import parse_endpoint
from spicedb.aio import SpiceDBClient as AsyncSpiceDBClient
from spicedb.consistency import full
from spicedb.errors import InvalidArgumentError, LocalValidationError
from spicedb.sync import SpiceDBClient as SyncSpiceDBClient
from tests.test_auth_headers import TOKEN, _handlers, _Recorder, _serve

PLAIN = TransportSecurity.PLAINTEXT
_REL = Relationship.from_triple("document:readme", "view", "user:alice")


@pytest.mark.parametrize(
    ("endpoint", "insecure", "target"),
    [
        ("127.0.0.1:50101", True, "127.0.0.1:50101"),
        ("spicedb.example.com:443", False, "spicedb.example.com:443"),
        ("spicedb.example.com", False, "spicedb.example.com:443"),
        ("[::1]:50051", True, "[::1]:50051"),
        ("::1", False, "[::1]:443"),
        ("[2001:db8::1]", False, "[2001:db8::1]:443"),
        ("unix:/var/run/spicedb.sock", True, "unix:/var/run/spicedb.sock"),
        ("dns:///localhost:50051", True, "dns:///localhost:50051"),
        ("DNS:///spicedb.example.com", False, "dns:///spicedb.example.com:443"),
        ("dns:///[::1]:50051", True, "dns:///[::1]:50051"),
        ("dns:///::1", False, "dns:///[::1]:443"),
        ("http://127.0.0.1:50101", True, "127.0.0.1:50101"),
        ("HTTP://localhost:50051/", True, "localhost:50051"),
        ("http://[::1]:50051", True, "[::1]:50051"),
        ("https://example.com", False, "example.com:443"),
        ("https://example.com/", False, "example.com:443"),
        ("https://example.com:8443", False, "example.com:8443"),
        ("https://[2001:db8::1]", False, "[2001:db8::1]:443"),
    ],
)
def test_accepted_forms_map_to_the_dialed_target(endpoint, insecure, target):
    assert parse_endpoint(endpoint, insecure=insecure) == target


@pytest.mark.parametrize(
    ("endpoint", "transport", "problem"),
    [
        ("http://localhost", PLAIN, "http:// needs an explicit port"),
        ("localhost", PLAIN, "a plaintext endpoint needs an explicit port, e.g. localhost:50051"),
        ("127.0.0.1", PLAIN, "e.g. 127.0.0.1:50051"),
        ("::1", PLAIN, "e.g. [::1]:50051"),
        ("[::1]", PLAIN, "e.g. [::1]:50051"),
        ("http://127.0.0.1:50101/x", PLAIN, "a path is not supported"),
        ("http://127.0.0.1:50101?a=b", PLAIN, "a query is not supported"),
        ("http://127.0.0.1:50101#f", PLAIN, "a fragment is not supported"),
        ("http://u@127.0.0.1:50101", PLAIN, "userinfo"),
        ("ftp://host:1", PLAIN, "unsupported scheme 'ftp'"),
        ("dns:spicedb:50051", PLAIN, "a dns target needs three slashes"),
        ("dns://8.8.8.8/spicedb:50051", PLAIN, "a DNS server authority"),
        ("dns:///localhost", PLAIN, "e.g. dns:///localhost:50051"),
        ("dns:///", PLAIN, "missing host"),
        ("dns:///localhost:50051/x", PLAIN, "a path is not supported"),
        ("dns:///u@localhost:50051", PLAIN, "userinfo"),
        ("dns:///localhost:abc", PLAIN, "port 'abc' is not a number"),
        ("grpc://localhost:50051", PLAIN, "unsupported scheme 'grpc'"),
        ("127.0.0.1:notaport", PLAIN, "port 'notaport' is not a number"),
        ("http://127.0.0.1:abc", PLAIN, "port 'abc' is not a number"),
        ("127.0.0.1:99999", PLAIN, "out of range"),
        ("127.0.0.1:", PLAIN, "empty port"),
        (":50051", PLAIN, "missing host"),
        ("http://", PLAIN, "missing host"),
        ("", PLAIN, "empty endpoint"),
        ("localhost :50051", PLAIN, "whitespace"),
        ("[::1", PLAIN, "unterminated"),
        ("http://127.0.0.1:50101", TransportSecurity.TLS, "http:// selects plaintext"),
        ("https://example.com", PLAIN, "https:// selects TLS"),
    ],
)
@pytest.mark.parametrize("client", [SyncSpiceDBClient, AsyncSpiceDBClient])
def test_rejected_forms_fail_at_construction(client, endpoint, transport, problem):
    with (
        mock.patch("grpc.insecure_channel") as sync_insecure,
        mock.patch("grpc.secure_channel") as sync_secure,
        mock.patch("grpc.aio.insecure_channel") as aio_insecure,
        mock.patch("grpc.aio.secure_channel") as aio_secure,
    ):
        with pytest.raises(InvalidArgumentError) as excinfo:
            client(endpoint, token=TOKEN, transport_security=transport)
    # Local validation, so ``except ValueError`` around construction catches
    # a bad endpoint the same way it catches every other bad argument.
    assert isinstance(excinfo.value, LocalValidationError)
    assert isinstance(excinfo.value, ValueError)
    assert excinfo.value.server_message is None
    message = str(excinfo.value)
    assert problem in message
    assert "expected host:port, http://host:port" in message
    assert "dns:///host:port or unix:/path/to/socket" in message
    assert "remote_credentials" not in message
    for channel in (sync_insecure, sync_secure, aio_insecure, aio_secure):
        channel.assert_not_called()


@pytest.mark.parametrize("client", [SyncSpiceDBClient, AsyncSpiceDBClient])
def test_plaintext_endpoint_without_port_is_caught_by_except_value_error(client):
    try:
        client("localhost", token=TOKEN, transport_security=PLAIN)
    except ValueError as error:
        assert isinstance(error, LocalValidationError)
        assert "needs an explicit port" in str(error)
    else:
        pytest.fail("a plaintext endpoint without a port was accepted")


@pytest.mark.parametrize("client", [SyncSpiceDBClient, AsyncSpiceDBClient])
def test_non_string_endpoint_is_local_validation(client):
    with pytest.raises(LocalValidationError, match="endpoint: expected str"):
        client(50051, token=TOKEN, transport_security=PLAIN)  # type: ignore[arg-type]


@pytest.mark.parametrize("client", [SyncSpiceDBClient, AsyncSpiceDBClient])
def test_malformed_remote_url_reports_the_problem_not_the_policy(client):
    """A malformed remote URL names its defect even with no opt-in, so the
    remote-plaintext message never describes a host that is not one."""
    with pytest.raises(InvalidArgumentError, match="a path is not supported"):
        client("http://example.com:50051/api", token=TOKEN, transport_security=PLAIN)


@pytest.mark.parametrize("client", [SyncSpiceDBClient, AsyncSpiceDBClient])
def test_remote_http_url_gets_the_policy_on_its_parsed_host(client):
    with pytest.raises(InvalidArgumentError, match="remote_credentials") as excinfo:
        client("http://example.com:50051", token=TOKEN, transport_security=PLAIN)
    assert "'example.com:50051'" in str(excinfo.value)
    client(
        "http://example.com:50051",
        token=TOKEN,
        transport_security=PLAIN,
        remote_credentials=RemoteCredentials.ALLOW,
    )


def test_tls_bare_host_without_port_dials_443():
    with mock.patch("grpc.secure_channel") as secure_channel:
        client = SyncSpiceDBClient("example.com", token=TOKEN)
        client.raw_grpc()
    assert secure_channel.call_args.args[0] == "example.com:443"


def test_https_url_without_port_dials_443():
    with mock.patch("grpc.secure_channel") as secure_channel:
        client = SyncSpiceDBClient("https://example.com", token=TOKEN)
        client.raw_grpc()
    assert secure_channel.call_args.args[0] == "example.com:443"


def test_sync_http_url_reaches_a_real_loopback_server():
    recorder = _Recorder()
    server, port = _serve(recorder, None)
    try:
        client = SyncSpiceDBClient(
            f"http://127.0.0.1:{port}", token=TOKEN, transport_security=PLAIN
        )
        client.check_bulk([PermissionCheck(_REL.resource, _REL.relation, _REL.subject)], consistency=full())
        client.close()
    finally:
        server.stop(0)
    assert recorder.headers[0][1] == f"Bearer {TOKEN}"


async def test_aio_http_url_reaches_a_real_loopback_server():
    recorder = _Recorder()
    server, port = _serve(recorder, None)
    try:
        client = AsyncSpiceDBClient(
            f"http://localhost:{port}/", token=TOKEN, transport_security=PLAIN
        )
        await client.check_bulk([PermissionCheck(_REL.resource, _REL.relation, _REL.subject)], consistency=full())
        await client.close()
    finally:
        server.stop(0)
    assert recorder.headers[0][1] == f"Bearer {TOKEN}"


def test_refused_connection_names_the_endpoint_and_cause():
    """grpc-python already reports the dialed address and the OS error for a
    refused connection; pin that so a transport upgrade cannot hide it."""
    from spicedb.errors import UnavailableError

    client = SyncSpiceDBClient(
        "http://127.0.0.1:1", token=TOKEN, transport_security=PLAIN, default_timeout=5.0
    )
    try:
        with pytest.raises(UnavailableError) as excinfo:
            client.read_schema()
    finally:
        client.close()
    message = str(excinfo.value)
    assert "127.0.0.1:1" in message
    assert "refused" in message.lower()


def test_remote_plaintext_refusal_names_every_option_needed():
    """A Compose service name needs both opt-ins; the refusal spells both
    exactly as the caller writes them."""
    with pytest.raises(InvalidArgumentError) as excinfo:
        SyncSpiceDBClient("spicedb:50051", token=TOKEN, transport_security=PLAIN)
    assert str(excinfo.value) == (
        "spicedb: refusing to send credentials over an insecure (plaintext) "
        "connection to non-loopback endpoint 'spicedb:50051': use TLS (the "
        "default), or, to send the bearer token in cleartext to this host (for "
        "example a Docker Compose service), pass both "
        "transport_security=TransportSecurity.PLAINTEXT and "
        "remote_credentials=RemoteCredentials.ALLOW"
    )
    SyncSpiceDBClient(
        "spicedb:50051",
        token=TOKEN,
        transport_security=PLAIN,
        remote_credentials=RemoteCredentials.ALLOW,
    ).close()


@pytest.mark.parametrize("client", [SyncSpiceDBClient, AsyncSpiceDBClient])
def test_remote_dns_target_gets_the_policy_on_its_host(client):
    """The loopback check runs on the host a dns:/// target resolves."""
    with pytest.raises(InvalidArgumentError, match="remote_credentials"):
        client("dns:///spicedb:50051", token=TOKEN, transport_security=PLAIN)
    client(
        "dns:///spicedb:50051",
        token=TOKEN,
        transport_security=PLAIN,
        remote_credentials=RemoteCredentials.ALLOW,
    )
    client("dns:///localhost:50051", token=TOKEN, transport_security=PLAIN)


def test_sync_dns_target_reaches_a_real_loopback_server():
    recorder = _Recorder()
    server, port = _serve(recorder, None)
    try:
        client = SyncSpiceDBClient(
            f"dns:///localhost:{port}", token=TOKEN, transport_security=PLAIN
        )
        client.check_bulk([PermissionCheck(_REL.resource, _REL.relation, _REL.subject)], consistency=full())
        client.close()
    finally:
        server.stop(0)
    assert recorder.headers[0][1] == f"Bearer {TOKEN}"


async def test_aio_dns_target_reaches_a_real_loopback_server():
    recorder = _Recorder()
    server, port = _serve(recorder, None)
    try:
        client = AsyncSpiceDBClient(
            f"dns:///localhost:{port}", token=TOKEN, transport_security=PLAIN
        )
        await client.check_bulk([PermissionCheck(_REL.resource, _REL.relation, _REL.subject)], consistency=full())
        await client.close()
    finally:
        server.stop(0)
    assert recorder.headers[0][1] == f"Bearer {TOKEN}"


def test_unix_socket_target_reaches_a_real_server():
    """A unix: target the constructor accepts dials the socket it names."""
    # Not pytest's tmp_path: a socket path must fit sun_path (104 bytes on
    # macOS), and tmp_path under $TMPDIR does not.
    directory = tempfile.mkdtemp(prefix="sdb", dir="/tmp")
    recorder = _Recorder()
    path = os.path.join(directory, "s.sock")
    server = grpc.server(futures.ThreadPoolExecutor(max_workers=2))
    server.add_generic_rpc_handlers(_handlers(recorder))
    server.add_insecure_port(f"unix:{path}")
    server.start()
    try:
        with SyncSpiceDBClient(f"unix:{path}", token=TOKEN, transport_security=PLAIN) as client:
            client.ready(timeout=5.0)
            client.check_bulk([PermissionCheck(_REL.resource, _REL.relation, _REL.subject)], consistency=full())
    finally:
        server.stop(0)
        shutil.rmtree(directory, ignore_errors=True)
    assert recorder.headers[0][1] == f"Bearer {TOKEN}"
