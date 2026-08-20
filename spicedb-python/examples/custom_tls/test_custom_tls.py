"""Example: Connecting to a SpiceDB fronted by a private CA (and mutual TLS).

By default the client trusts whatever grpc's C-core trusts, which is a
`roots.pem` compiled into grpc -- NOT the host's certificate store. So a
SpiceDB terminated behind a corporate or cluster-internal CA is unreachable
until you hand the client that CA yourself:

    import pathlib
    from spicedb.aio import SpiceDBClient   # or spicedb.sync

    async with SpiceDBClient(
        "spicedb.internal:443",
        token=os.environ["SPICEDB_TOKEN"],
        ca_cert=pathlib.Path("/etc/ssl/certs/internal-ca.pem").read_bytes(),
    ) as client:
        ...

Add `client_cert=` and `client_key=` where the server requires mutual TLS --
both halves together; either alone is refused.

Unlike the other examples here, this one does not use the shared SpiceDB at
localhost:50051: a plaintext server has nothing to demonstrate about trust
material. It brings up its own TLS-terminated gRPC endpoint with a throwaway
CA instead, so what you see below is a real handshake against a certificate no
public root set would accept.
"""

import pathlib
import subprocess
import tempfile
from concurrent import futures

import grpc
import pytest
from authzed.api.v1 import permission_service_pb2 as psp

from spicedb import InvalidArgumentError, Relationship, full
from spicedb.aio import SpiceDBClient

TOKEN = "somerandomkeyhere"


def _openssl(*args: str) -> None:
    subprocess.run(["openssl", *args], check=True, capture_output=True)


@pytest.fixture(scope="module")
def pki():
    """A throwaway CA, a server certificate under it, and a client identity.

    Stands in for whatever internal PKI your deployment actually uses -- in
    production these are files on disk or keys in a mounted secret, and the
    only thing the client needs is their PEM bytes.
    """
    with tempfile.TemporaryDirectory() as tmp:
        d = pathlib.Path(tmp)
        _openssl("req", "-x509", "-newkey", "rsa:2048", "-nodes",
                 "-keyout", str(d / "ca.key"), "-out", str(d / "ca.crt"),
                 "-days", "1", "-subj", "/CN=example internal CA",
                 "-addext", "basicConstraints=critical,CA:TRUE")

        def leaf(name: str, ext: str) -> tuple[bytes, bytes]:
            (d / f"{name}.ext").write_text(
                f"basicConstraints=CA:FALSE\nkeyUsage=digitalSignature,keyEncipherment\n{ext}\n"
            )
            _openssl("req", "-newkey", "rsa:2048", "-nodes",
                     "-keyout", str(d / f"{name}.key"), "-out", str(d / f"{name}.csr"),
                     "-subj", f"/CN={name}")
            _openssl("x509", "-req", "-in", str(d / f"{name}.csr"),
                     "-CA", str(d / "ca.crt"), "-CAkey", str(d / "ca.key"),
                     "-CAcreateserial", "-out", str(d / f"{name}.crt"),
                     "-days", "1", "-extfile", str(d / f"{name}.ext"))
            return (d / f"{name}.crt").read_bytes(), (d / f"{name}.key").read_bytes()

        server_cert, server_key = leaf(
            "localhost",
            "subjectAltName=DNS:localhost,IP:127.0.0.1\nextendedKeyUsage=serverAuth",
        )
        client_cert, client_key = leaf("client", "extendedKeyUsage=clientAuth")
        yield {
            "ca": (d / "ca.crt").read_bytes(),
            "server_cert": server_cert,
            "server_key": server_key,
            "client_cert": client_cert,
            "client_key": client_key,
        }


def _serve_tls(pki, *, require_client_auth: bool):
    """A stand-in for a TLS-terminated SpiceDB, answering CheckBulkPermissions."""
    server = grpc.server(futures.ThreadPoolExecutor(max_workers=4))
    server.add_generic_rpc_handlers(
        (
            grpc.method_handlers_generic_handler(
                "authzed.api.v1.PermissionsService",
                {
                    "CheckBulkPermissions": grpc.unary_unary_rpc_method_handler(
                        lambda request, context: psp.CheckBulkPermissionsResponse(
                            pairs=[
                                psp.CheckBulkPermissionsPair(
                                    request=psp.CheckBulkPermissionsRequestItem(),
                                    item=psp.CheckBulkPermissionsResponseItem(
                                        permissionship=psp.CheckPermissionResponse.PERMISSIONSHIP_HAS_PERMISSION
                                    ),
                                )
                            ]
                        ).SerializeToString(),
                        lambda b: b,
                        lambda b: b,
                    ),
                },
            ),
        )
    )
    port = server.add_secure_port(
        "localhost:0",
        grpc.ssl_server_credentials(
            [(pki["server_key"], pki["server_cert"])],
            root_certificates=pki["ca"] if require_client_auth else None,
            require_client_auth=require_client_auth,
        ),
    )
    server.start()
    return server, port


async def test_connect_with_a_private_ca(pki):
    """Hand the client the CA that signed the server's certificate."""
    server, port = _serve_tls(pki, require_client_auth=False)
    try:
        async with SpiceDBClient(
            f"localhost:{port}",
            token=TOKEN,
            ca_cert=pki["ca"],
            default_timeout=10.0,
        ) as client:
            result = await client.check_permission(
                full(), Relationship.from_triple("document:readme", "view", "user:alice")
            )
            print(f"connected over TLS to a private CA; has_permission={result.has_permission}")
            assert result.has_permission is True
    finally:
        server.stop(0)


async def test_connect_with_mutual_tls(pki):
    """Add the client's own certificate where the server demands one."""
    server, port = _serve_tls(pki, require_client_auth=True)
    try:
        async with SpiceDBClient(
            f"localhost:{port}",
            token=TOKEN,
            ca_cert=pki["ca"],
            client_cert=pki["client_cert"],
            client_key=pki["client_key"],
            default_timeout=10.0,
        ) as client:
            result = await client.check_permission(
                full(), Relationship.from_triple("document:readme", "view", "user:alice")
            )
            print(f"connected over mutual TLS; has_permission={result.has_permission}")
            assert result.has_permission is True
    finally:
        server.stop(0)


def test_trust_material_never_makes_the_connection_plaintext():
    """`insecure=True` plus trust material is a contradiction, and is refused.

    A plaintext connection performs no handshake, so grpc would ignore the CA
    and send the bearer token in cleartext behind a call site that reads as
    though TLS were configured. See root DESIGN.md, "RULE: Credentials over
    insecure transport require an explicit opt-in".
    """
    with pytest.raises(InvalidArgumentError):
        SpiceDBClient(
            "localhost:50051", token=TOKEN, insecure=True, ca_cert=b"...."
        )
