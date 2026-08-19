"""Caller-supplied TLS trust material, proven against a real TLS handshake.

Root DESIGN.md, "RULE: A system-TLS constructor must reach a real server",
lets this client delegate to grpc's default trust source *because* a caller
can supply their own trust material when that default is not enough -- and it
names the hazard that makes the escape hatch necessary: grpc's C-core compiles
in its own `roots.pem`, so a CA an operator installed in the host's trust store
is not honoured here. Until `ca_cert=` existed, a SpiceDB behind a private or
corporate CA was simply unreachable, and that justification was not true.

Every test below stands up a real gRPC server over TLS with a certificate
signed by a throwaway CA generated in-process, and drives a real client
against it. The pairing is what makes each assertion mean something: the same
server, the same client code, differing only in whether the CA was supplied.
A test that only asserted the failure could not tell a rejected certificate
from an unreachable port, and a test that only asserted the success could not
tell a verified chain from a disabled one.
"""

from __future__ import annotations

import pathlib
import subprocess
import tempfile
from concurrent import futures

import grpc
import pytest
from authzed.api.v1 import permission_service_pb2 as psp

from spicedb.consistency import full
from spicedb.errors import InvalidArgumentError, SpiceDBError

TOKEN = "test-token"

# Short enough that the deliberately-failing handshakes below do not sit on
# the 30s production default, long enough that a loaded CI box still completes
# a real handshake. max_retries=0 everywhere for the same reason: a TLS
# failure surfaces as UNAVAILABLE, which is transient, so the default 3
# retries would multiply every negative case by four.
_TIMEOUT = 10.0


class Certs:
    """A throwaway CA plus the leaf certificates it signed, as PEM bytes."""

    def __init__(self, ca: bytes, server_cert: bytes, server_key: bytes,
                 client_cert: bytes, client_key: bytes) -> None:
        self.ca = ca
        self.server_cert = server_cert
        self.server_key = server_key
        self.client_cert = client_cert
        self.client_key = client_key


def _openssl(*args: str) -> None:
    subprocess.run(["openssl", *args], check=True, capture_output=True)


def _leaf(d: pathlib.Path, name: str, ext: str) -> tuple[bytes, bytes]:
    """Generate a key + CSR for `name` and sign it with the CA in `d`."""
    key = d / f"{name}.key"
    csr = d / f"{name}.csr"
    crt = d / f"{name}.crt"
    extfile = d / f"{name}.ext"
    extfile.write_text(
        "basicConstraints=CA:FALSE\n"
        "keyUsage=digitalSignature,keyEncipherment\n"
        f"{ext}\n"
    )
    _openssl("req", "-newkey", "rsa:2048", "-nodes",
             "-keyout", str(key), "-out", str(csr), "-subj", f"/CN={name}")
    _openssl("x509", "-req", "-in", str(csr),
             "-CA", str(d / "ca.crt"), "-CAkey", str(d / "ca.key"),
             "-CAcreateserial", "-out", str(crt), "-days", "1",
             "-extfile", str(extfile))
    return crt.read_bytes(), key.read_bytes()


@pytest.fixture(scope="module")
def certs() -> Certs:
    """A CA that exists only for this test module, and two leaves under it.

    Generated rather than committed: a checked-in certificate expires, and an
    expiry is a test that starts failing for a reason unrelated to the code it
    covers. `openssl` is required rather than optional -- a skip here would
    read as coverage while proving nothing.
    """
    with tempfile.TemporaryDirectory() as tmp:
        d = pathlib.Path(tmp)
        _openssl("req", "-x509", "-newkey", "rsa:2048", "-nodes",
                 "-keyout", str(d / "ca.key"), "-out", str(d / "ca.crt"),
                 "-days", "1", "-subj", "/CN=spicedb-python test CA",
                 "-addext", "basicConstraints=critical,CA:TRUE")
        # SAN, not CN: BoringSSL (which grpc's C-core embeds) ignores the
        # common name entirely, so a certificate without a matching SAN fails
        # verification even against its own CA.
        server_cert, server_key = _leaf(
            d, "localhost",
            "subjectAltName=DNS:localhost,IP:127.0.0.1\nextendedKeyUsage=serverAuth",
        )
        client_cert, client_key = _leaf(
            d, "client", "extendedKeyUsage=clientAuth",
        )
        yield Certs(
            ca=(d / "ca.crt").read_bytes(),
            server_cert=server_cert,
            server_key=server_key,
            client_cert=client_cert,
            client_key=client_key,
        )


def _check_bulk(request: bytes, context) -> bytes:
    return psp.CheckBulkPermissionsResponse().SerializeToString()


def _serve(certs: Certs, *, require_client_auth: bool = False):
    """A real gRPC server over TLS, presenting the CA-signed server leaf.

    Identity serializers let the handler deal in raw bytes; the client still
    deserializes what comes back as the proper response proto, so a completed
    call proves the whole path -- handshake, HTTP/2, gRPC framing -- and not
    just a socket that opened.
    """
    server = grpc.server(futures.ThreadPoolExecutor(max_workers=4))
    server.add_generic_rpc_handlers(
        (
            grpc.method_handlers_generic_handler(
                "authzed.api.v1.PermissionsService",
                {
                    "CheckBulkPermissions": grpc.unary_unary_rpc_method_handler(
                        _check_bulk, lambda b: b, lambda b: b
                    ),
                },
            ),
        )
    )
    port = server.add_secure_port(
        "localhost:0",
        grpc.ssl_server_credentials(
            [(certs.server_key, certs.server_cert)],
            root_certificates=certs.ca if require_client_auth else None,
            require_client_auth=require_client_auth,
        ),
    )
    server.start()
    return server, port


# ── The capability: a custom CA reaches a real server ────────────────


def test_sync_ca_cert_completes_a_handshake_the_default_roots_reject(certs):
    """The whole point, in one test.

    Same server, same client, same endpoint; the only difference is whether
    the CA that signed the server's certificate was supplied. With it, a real
    RPC completes. Without it, grpc's compiled-in roots reject the chain --
    which is exactly the situation an operator behind a private CA is in, and
    exactly why `ca_cert` had to exist.
    """
    from spicedb.sync import SpiceDBClient

    server, port = _serve(certs)
    try:
        with SpiceDBClient(
            f"localhost:{port}", token=TOKEN,
            ca_cert=certs.ca, default_timeout=_TIMEOUT, max_retries=0,
        ) as client:
            assert client.check_permissions(full()) == []

        with SpiceDBClient(
            f"localhost:{port}", token=TOKEN,
            default_timeout=_TIMEOUT, max_retries=0,
        ) as untrusting:
            with pytest.raises(SpiceDBError):
                untrusting.check_permissions(full())
    finally:
        server.stop(0)


async def test_aio_ca_cert_completes_a_handshake_the_default_roots_reject(certs):
    """The aio flavor of the test above.

    Not redundant: the two flavors build their channels through different
    grpc entry points (`grpc.aio.secure_channel` vs `grpc.secure_channel`),
    so threading the credentials correctly in one proves nothing about the
    other. `tests/test_parity.py` pins the two signatures; this pins that
    both actually connect.
    """
    from spicedb.aio import SpiceDBClient

    server, port = _serve(certs)
    try:
        async with SpiceDBClient(
            f"localhost:{port}", token=TOKEN,
            ca_cert=certs.ca, default_timeout=_TIMEOUT, max_retries=0,
        ) as client:
            assert await client.check_permissions(full()) == []

        async with SpiceDBClient(
            f"localhost:{port}", token=TOKEN,
            default_timeout=_TIMEOUT, max_retries=0,
        ) as untrusting:
            with pytest.raises(SpiceDBError):
                await untrusting.check_permissions(full())
    finally:
        server.stop(0)


# ── Mutual TLS ───────────────────────────────────────────────────────


def test_sync_client_cert_satisfies_a_server_requiring_mutual_tls(certs):
    """`client_cert`/`client_key` present the client's own identity.

    The server here refuses any connection that does not present a
    certificate under its CA, so the paired negative case cannot pass for an
    unrelated reason: it uses the identical `ca_cert`, and fails only for the
    missing client identity.
    """
    from spicedb.sync import SpiceDBClient

    server, port = _serve(certs, require_client_auth=True)
    try:
        with SpiceDBClient(
            f"localhost:{port}", token=TOKEN,
            ca_cert=certs.ca,
            client_cert=certs.client_cert,
            client_key=certs.client_key,
            default_timeout=_TIMEOUT, max_retries=0,
        ) as client:
            assert client.check_permissions(full()) == []

        with SpiceDBClient(
            f"localhost:{port}", token=TOKEN,
            ca_cert=certs.ca, default_timeout=_TIMEOUT, max_retries=0,
        ) as anonymous:
            with pytest.raises(SpiceDBError):
                anonymous.check_permissions(full())
    finally:
        server.stop(0)


async def test_aio_client_cert_satisfies_a_server_requiring_mutual_tls(certs):
    from spicedb.aio import SpiceDBClient

    server, port = _serve(certs, require_client_auth=True)
    try:
        async with SpiceDBClient(
            f"localhost:{port}", token=TOKEN,
            ca_cert=certs.ca,
            client_cert=certs.client_cert,
            client_key=certs.client_key,
            default_timeout=_TIMEOUT, max_retries=0,
        ) as client:
            assert await client.check_permissions(full()) == []

        async with SpiceDBClient(
            f"localhost:{port}", token=TOKEN,
            ca_cert=certs.ca, default_timeout=_TIMEOUT, max_retries=0,
        ) as anonymous:
            with pytest.raises(SpiceDBError):
                await anonymous.check_permissions(full())
    finally:
        server.stop(0)


# ── The capability must not become a hole in the credential guard ────

_FLAVORS = ["spicedb.sync", "spicedb.aio"]


def _client_class(module: str):
    import importlib

    return importlib.import_module(module).SpiceDBClient


@pytest.mark.parametrize("module", _FLAVORS)
@pytest.mark.parametrize(
    "material",
    [
        {"ca_cert": b"-----BEGIN CERTIFICATE-----"},
        {"client_cert": b"cert", "client_key": b"key"},
    ],
    ids=["ca_cert", "client_identity"],
)
def test_tls_material_is_refused_with_insecure_rather_than_ignored(module, material):
    """Trust material must never be a quieter route to a plaintext channel.

    grpc would silently drop all three arguments on an insecure channel, so a
    call site reading `insecure=True, ca_cert=...` would ship the bearer token
    in cleartext while looking like it had configured TLS. Refusing is the only
    honest answer -- and note it refuses rather than "helpfully" turning TLS
    on, since silently upgrading the transport is just as much of a surprise.
    Root DESIGN.md, "RULE: Credentials over insecure transport require an
    explicit opt-in".
    """
    with pytest.raises(InvalidArgumentError, match="insecure=True"):
        _client_class(module)(
            "localhost:50051", token=TOKEN, insecure=True, **material
        )


@pytest.mark.parametrize("module", _FLAVORS)
def test_tls_material_does_not_bypass_the_insecure_remote_host_guard(module):
    """Supplying a CA must not become a second constructor that skips the guard.

    The insecure/non-loopback refusal still applies with trust material in
    hand, and it applies FIRST -- its message, not the TLS one, is what a
    caller sees. Root DESIGN.md, "RULE: Credentials over insecure transport
    require an explicit opt-in".
    """
    with pytest.raises(InvalidArgumentError, match="allow_insecure_remote_credentials"):
        _client_class(module)(
            "evil.example.com:443", token=TOKEN, insecure=True, ca_cert=b"ca"
        )


@pytest.mark.parametrize("module", _FLAVORS)
@pytest.mark.parametrize(
    "material",
    [{"client_cert": b"cert"}, {"client_key": b"key"}],
    ids=["cert_without_key", "key_without_cert"],
)
def test_half_a_client_identity_is_refused(module, material):
    """Either half alone is unusable, and grpc's C-core would say so from a
    layer with no idea which argument the caller got wrong."""
    with pytest.raises(InvalidArgumentError, match="mutual TLS needs both"):
        _client_class(module)("localhost:50051", token=TOKEN, **material)


@pytest.mark.parametrize("module", _FLAVORS)
def test_tls_material_is_validated_before_any_channel_exists(module, monkeypatch):
    """The refusals above must land in the constructor, not on first use.

    A client that accepted the bad combination and only failed on the first
    RPC would already have built a channel -- and, on the insecure path, would
    already be a token leak waiting for its first call. Every channel
    constructor grpc offers is booby-trapped here, so "no channel was built"
    is proven rather than assumed.
    """
    def explode(*args, **kwargs):
        raise AssertionError("a channel must not be created for a refused config")

    for name in ("insecure_channel", "secure_channel"):
        monkeypatch.setattr(grpc, name, explode)
        monkeypatch.setattr(grpc.aio, name, explode)

    with pytest.raises(InvalidArgumentError):
        _client_class(module)(
            "localhost:50051", token=TOKEN, insecure=True, ca_cert=b"ca"
        )


def test_default_secure_path_still_delegates_to_grpc_default_roots():
    """No trust material means grpc's own default trust source, untouched.

    Root DESIGN.md, "RULE: A system-TLS constructor must reach a real server",
    clause 1 prohibits this library selecting its own root set. `ca_cert=None`
    must therefore produce byte-identical credentials to a bare
    `grpc.ssl_channel_credentials()` call -- which it does because all three
    of grpc's keyword arguments already default to None. Pinned here because
    a future edit reaching for "a sensible default bundle" is exactly the
    defect that clause exists to catch.
    """
    from spicedb import _tls

    calls = []
    real = grpc.ssl_channel_credentials

    def spy(**kwargs):
        calls.append(kwargs)
        return real(**kwargs)

    original = grpc.ssl_channel_credentials
    grpc.ssl_channel_credentials = spy
    try:
        _tls.channel_credentials(None, None, None)
    finally:
        grpc.ssl_channel_credentials = original

    assert calls == [
        {"root_certificates": None, "private_key": None, "certificate_chain": None}
    ]
