"""Bearer-token metadata, shared by both client flavors.

Both clients attach the token by passing this metadata on every RPC. Neither
uses a gRPC interceptor, and neither composes `access_token_call_credentials`.

Why (spec D8): the two libraries disagree about interceptors in ways that
silently change behavior. `grpc.aio` sorts a multi-ABC interceptor into ONE
call-type bucket via an elif chain, so it covers unary-unary only; sync's
`intercept_channel` wraps per interceptor and fires on every call type. Layering
call credentials on top of either duplicates the header. Passing metadata
per call is uniform, explicit, and immune to both quirks.
"""

from __future__ import annotations

import ipaddress


def bearer_metadata(token: str) -> list[tuple[str, str]]:
    """Build the gRPC metadata carrying the bearer token."""
    return [("authorization", f"Bearer {token}")]


def is_loopback_endpoint(endpoint: str) -> bool:
    """Report whether a gRPC target string names a loopback destination.

    True for the literal hostname "localhost", an IP in 127.0.0.0/8, the
    IPv6 loopback ::1, or a unix domain socket target (a "unix:" prefix). A
    unix socket never leaves the host's kernel, so it is loopback for this
    check even though it has no IP at all.

    This is the exemption in root DESIGN.md, "RULE: Credentials over
    insecure transport require an explicit opt-in": loopback is the reason
    insecure=True exists at all (local development, docker-compose, CI), so
    it must keep working with no extra ceremony. Anything else requires
    allow_insecure_remote_credentials=True -- see
    require_insecure_transport_allowed below.
    """
    if endpoint.startswith("unix:"):
        return True

    host = endpoint
    if endpoint.startswith("["):
        end = endpoint.find("]")
        if end != -1:
            host = endpoint[1:end]
    elif endpoint.count(":") > 1:
        # A bare IPv6 literal (e.g. "::1") -- no port is possible without
        # brackets, so the whole string is the host.
        host = endpoint
    elif ":" in endpoint:
        host = endpoint.rsplit(":", 1)[0]

    if host.lower() == "localhost":
        return True

    try:
        return ipaddress.ip_address(host).is_loopback
    except ValueError:
        return False


def require_insecure_transport_allowed(
    endpoint: str, *, insecure: bool, allow_insecure_remote_credentials: bool
) -> None:
    """Refuse an insecure connection to a non-loopback endpoint.

    See root DESIGN.md, "RULE: Credentials over insecure transport require
    an explicit opt-in". Call this before creating any channel or attaching
    any credential -- a rejected combination must never get far enough to
    put a bearer token on the wire.

    :raises spicedb.errors.InvalidArgumentError: if ``insecure`` is True,
        ``endpoint`` is not loopback, and ``allow_insecure_remote_credentials``
        is False.
    """
    if insecure and not allow_insecure_remote_credentials and not is_loopback_endpoint(endpoint):
        from spicedb.errors import InvalidArgumentError

        raise InvalidArgumentError(
            f"spicedb: refusing to send credentials over an insecure (plaintext) connection "
            f"to non-loopback endpoint {endpoint!r}: use TLS (insecure=False), or pass "
            f"allow_insecure_remote_credentials=True if you intend to send a bearer token in "
            f"cleartext to a remote host"
        )
