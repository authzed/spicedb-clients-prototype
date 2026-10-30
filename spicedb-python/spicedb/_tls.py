"""Caller-supplied TLS trust material, shared by both client flavors.

Both `spicedb.sync.SpiceDBClient` and `spicedb.aio.SpiceDBClient` accept
`ca_cert=`, `client_cert=` and `client_key=`; this module holds the single
implementation of what those mean, so the two flavors cannot drift.

Why this exists at all. Root DESIGN.md, "RULE: A system-TLS constructor must
reach a real server", requires the default secure path to delegate to the
ecosystem's default trust source -- for grpc-python that is
`grpc.ssl_channel_credentials()` with no arguments -- and explicitly names the
hazard that leaves visible: grpc's C-core compiles in its own `roots.pem`, so a
CA an operator installed in the host's trust store is NOT honoured by this
client's default. That rule permits delegating to the bundled set *because* a
caller can supply their own trust material instead. These parameters are what
makes that true. Without them, an operator terminating SpiceDB behind a private
or corporate CA had no supported way to connect at all.

The material is PEM bytes rather than a path so it can come from anywhere a
deployment already keeps it -- a mounted Kubernetes secret, a config store, an
environment variable -- with no assumption that it is on the local filesystem.
Reading a file is the caller's one-liner: `pathlib.Path("ca.pem").read_bytes()`.
"""

from __future__ import annotations

import base64
import binascii
import re
import ssl

import grpc


def channel_credentials(
    ca_cert: bytes | None,
    client_cert: bytes | None,
    client_key: bytes | None,
) -> grpc.ChannelCredentials:
    """Build the channel credentials for a secure connection.

    With all three arguments `None` this is exactly
    `grpc.ssl_channel_credentials()` -- the three keyword arguments below
    default to `None` in grpc itself -- so the default secure path still
    delegates to the ecosystem's default trust source, as root DESIGN.md,
    "RULE: A system-TLS constructor must reach a real server", clause 1
    requires. Supplying `ca_cert` REPLACES that default trust source for this
    client rather than adding to it; that is grpc's behavior, and it is the
    point: an operator pinning a private CA generally does not want the
    bundled public roots accepted as well.
    """
    return grpc.ssl_channel_credentials(
        root_certificates=ca_cert,
        private_key=client_key,
        certificate_chain=client_cert,
    )


def require_tls_material_usable(
    *,
    insecure: bool,
    ca_cert: bytes | None,
    client_cert: bytes | None,
    client_key: bytes | None,
) -> None:
    """Refuse a TLS configuration this client cannot honour.

    Call this before creating any channel or attaching any credential, in the
    constructor, so a rejected combination never opens a connection.

    Two refusals, both fail-closed:

    1. **TLS material with `transport_security=TransportSecurity.PLAINTEXT`.** A plaintext channel has no
       handshake to apply trust material to, so grpc would silently ignore all
       three arguments and send everything -- including the bearer token -- in
       cleartext, while the call site reads as though TLS were configured. That
       is precisely the failure root DESIGN.md, "RULE: Credentials over
       insecure transport require an explicit opt-in", exists to prevent, so
       supplying trust material must never be a second, quieter route to an
       insecure transport. Note the asymmetry this preserves: this raises
       instead of "helpfully" turning TLS on, because silently upgrading the
       transport would be just as much of a surprise in the other direction.
    2. **A client certificate without its key, or a key without its
       certificate.** Neither half is usable alone. grpc's C-core rejects the
       pair later, from a layer with no idea which argument the caller got
       wrong; failing here names it.

    :raises spicedb.errors.LocalValidationError: on either condition.
    """
    from spicedb.errors import LocalValidationError

    if insecure and (
        ca_cert is not None or client_cert is not None or client_key is not None
    ):
        supplied = ", ".join(
            name
            for name, value in (
                ("ca_cert", ca_cert),
                ("client_cert", client_cert),
                ("client_key", client_key),
            )
            if value is not None
        )
        raise LocalValidationError(
            f"spicedb: refusing to build a client with transport_security=TransportSecurity.PLAINTEXT and TLS material "
            f"({supplied}): a plaintext connection performs no TLS handshake, so the "
            f"material would be ignored and everything -- including the bearer token -- "
            f"would be sent in cleartext. Drop transport_security=TransportSecurity.PLAINTEXT to use TLS, or drop the TLS "
            f"material to connect in plaintext"
        )

    if (client_cert is None) != (client_key is None):
        missing = "client_key" if client_key is None else "client_cert"
        present = "client_cert" if client_key is None else "client_key"
        raise LocalValidationError(
            f"spicedb: {present} was supplied without {missing}: mutual TLS needs both "
            f"halves of the client identity, and neither is usable alone"
        )

    # Material that does not parse fails here, naming the input, rather than
    # as an unexplained transport failure on the first call. See root
    # DESIGN.md, "Reject unusable credentials and trust material at
    # construction".
    if ca_cert is not None:
        _require_certificates("ca_cert", ca_cert)
    if client_cert is not None:
        _require_certificates("client_cert", client_cert)
    if client_key is not None:
        _require_private_key("client_key", client_key)


_PEM_KEY = re.compile(
    rb"-----BEGIN ((?:RSA |EC |ENCRYPTED )?PRIVATE KEY)-----\s*(.+?)\s*-----END \1-----",
    re.DOTALL,
)


def _require_certificates(name: str, material: bytes) -> None:
    """Parse ``material`` as PEM X.509 certificates with the stdlib's OpenSSL."""
    from spicedb.errors import LocalValidationError

    try:
        text = bytes(material).decode("ascii")
        if "-----BEGIN CERTIFICATE-----" not in text:
            raise ValueError("no PEM CERTIFICATE block")
        ssl.SSLContext(ssl.PROTOCOL_TLS_CLIENT).load_verify_locations(cadata=text)
    except (TypeError, ValueError, ssl.SSLError) as e:
        raise LocalValidationError(
            f"spicedb: {name} is not a PEM-encoded X.509 certificate ({e}); pass the "
            f"certificate's PEM bytes, e.g. pathlib.Path('ca.pem').read_bytes()"
        ) from None


def _require_private_key(name: str, material: bytes) -> None:
    """Check ``material`` is a PEM private key whose body is base64 DER.

    The standard library cannot load a key from memory, so this checks the
    PEM structure and the DER envelope (an ASN.1 SEQUENCE); grpc then parses
    the key itself when the channel is built.
    """
    from spicedb.errors import LocalValidationError

    try:
        match = _PEM_KEY.search(bytes(material))
        if match is None:
            raise ValueError("no PEM PRIVATE KEY block")
        der = base64.b64decode(b"".join(match.group(2).split()), validate=True)
        if not der or der[0] != 0x30:
            raise ValueError("PEM body is not a DER-encoded key")
    except (TypeError, ValueError, binascii.Error) as e:
        raise LocalValidationError(
            f"spicedb: {name} is not a PEM-encoded private key ({e}); pass the key's "
            f"PEM bytes, e.g. pathlib.Path('client.key').read_bytes()"
        ) from None
