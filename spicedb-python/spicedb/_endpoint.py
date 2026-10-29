"""Endpoint parsing, shared by both client flavors.

See root DESIGN.md, "Accept the endpoint forms deployments use, or reject them
at construction". A constructor accepts:

- ``host:port`` (or ``[ipv6]:port``); a TLS endpoint without a port (a bare
  host or IPv6 literal) uses 443, while a plaintext one must name its port;
- ``http://host:port`` -- plaintext; the port is required, so no client
  silently dials port 80;
- ``https://host[:port]`` -- TLS; the port defaults to 443;
- ``dns:///host:port`` -- grpc's DNS resolver, for client-side load
  balancing across every address the name resolves to; the port follows the
  same rule as ``host:port`` (TLS defaults to 443, plaintext must name it).
  An explicit DNS-server authority (``dns://8.8.8.8/host:port``) and the
  slashless ``dns:host:port`` are rejected;
- a gRPC ``unix:`` socket target, handed to the transport unchanged.

Anything else fails here, before a channel exists, with a
``LocalValidationError`` (an ``InvalidArgumentError`` and a ``ValueError``)
naming the actual problem and the accepted forms.
grpc's C-core would otherwise accept almost any string and fail every call
with a DNS or "target uri is not valid" error.

The URL scheme selects the transport and must agree with
``transport_security``. Malformed forms are rejected before the
remote-plaintext policy runs, so that policy's message only ever describes a
well-formed remote host.
"""

from __future__ import annotations

import ipaddress
import re

ENDPOINT_FORMS = (
    "expected host:port, http://host:port (plaintext), https://host[:port] (TLS), "
    "dns:///host:port or unix:/path/to/socket"
)

_SCHEME = re.compile(r"^([A-Za-z][A-Za-z0-9+.-]*)://(.*)$", re.DOTALL)
# A bare "scheme:" prefix on a target that is not an IPv6 literal, for
# example grpc's "dns:host:port". Only consulted for strings with two or more
# colons, where host:port cannot apply.
_BARE_SCHEME = re.compile(r"^([A-Za-z][A-Za-z0-9+.-]*):")


def _invalid(endpoint: object, reason: str) -> Exception:
    from spicedb.errors import LocalValidationError

    return LocalValidationError(
        f"spicedb: invalid endpoint {endpoint!r}: {reason}; {ENDPOINT_FORMS}"
    )


def _check_port(endpoint: str, port: str) -> None:
    if not port:
        raise _invalid(endpoint, "empty port")
    # Not str.isdigit(): that accepts non-ASCII digits C-core cannot parse.
    if not (port.isascii() and port.isdigit()):
        raise _invalid(endpoint, f"port {port!r} is not a number")
    if not 0 < int(port) <= 65535:
        raise _invalid(endpoint, f"port {port} is out of range (1-65535)")


def _split_authority(endpoint: str, authority: str) -> tuple[str, str | None]:
    """Split ``authority`` into host and port (``None`` when absent),
    rejecting anything that is not a plain host or host:port."""
    if any(c.isspace() for c in authority):
        raise _invalid(endpoint, "contains whitespace")
    if "@" in authority:
        raise _invalid(endpoint, "userinfo (user@) is not supported")
    for char, what in (("/", "a path"), ("?", "a query"), ("#", "a fragment")):
        if char in authority:
            raise _invalid(endpoint, f"{what} is not supported")
    if authority.startswith("["):
        end = authority.find("]")
        if end == -1:
            raise _invalid(endpoint, "unterminated [ in IPv6 host")
        host, rest = authority[1:end], authority[end + 1 :]
        try:
            ipaddress.IPv6Address(host)
        except ValueError:
            raise _invalid(endpoint, f"{host!r} is not an IPv6 address") from None
        if not rest:
            return authority[: end + 1], None
        if not rest.startswith(":"):
            raise _invalid(endpoint, f"unexpected {rest!r} after IPv6 host")
        _check_port(endpoint, rest[1:])
        return authority[: end + 1], rest[1:]
    if authority.count(":") > 1:
        try:
            ipaddress.IPv6Address(authority)
        except ValueError:
            scheme = _BARE_SCHEME.match(authority)
            if scheme:
                raise _invalid(
                    endpoint, f"unsupported scheme {scheme.group(1)!r}"
                ) from None
            raise _invalid(
                endpoint, "too many colons (bracket an IPv6 host: [::1]:50051)"
            ) from None
        return f"[{authority}]", None
    host, sep, port = authority.partition(":")
    if not host:
        raise _invalid(endpoint, "missing host")
    if sep:
        _check_port(endpoint, port)
        return host, port
    return host, None


def _dns_target(endpoint: str, insecure: bool) -> str:
    """Validate a ``dns:///host[:port]`` target and return it with a port.

    The host and port are checked exactly as a bare ``host:port`` is, so the
    remote-plaintext policy sees the same host the resolver will look up.
    """
    if endpoint[4:7] != "///":
        if endpoint[4:6] == "//":
            reason = "a DNS server authority (dns://server/...) is not supported"
        else:
            reason = "a dns target needs three slashes (dns:///host:port)"
        raise _invalid(endpoint, reason)
    rest = endpoint[7:]
    if not rest:
        raise _invalid(endpoint, "missing host")
    host, port = _split_authority(endpoint, rest)
    if port is None:
        if insecure:
            raise _invalid(
                endpoint,
                "a plaintext endpoint needs an explicit port, e.g. "
                f"dns:///{host}:50051",
            )
        port = "443"
    return f"dns:///{host}:{port}"


def parse_endpoint(endpoint: str, *, insecure: bool) -> str:
    """Validate ``endpoint`` and return the gRPC target to dial.

    :raises spicedb.errors.LocalValidationError: for a malformed endpoint, an
        unsupported scheme, an ``http://`` URL without a port, or a scheme
        that disagrees with ``insecure``.
    """
    if not isinstance(endpoint, str):
        from spicedb.errors import LocalValidationError

        raise LocalValidationError("endpoint: expected str")
    if not endpoint:
        raise _invalid(endpoint, "empty endpoint")
    # A unix-socket target is a gRPC target URI carrying a filesystem path,
    # not a host; the transport parses it (and refuses an authority-bearing
    # one), and it never leaves the host.
    if endpoint[:5].lower() == "unix:":
        return endpoint
    if endpoint[:4].lower() == "dns:":
        return _dns_target(endpoint, insecure)

    url = _SCHEME.match(endpoint)
    if url is None:
        host, port = _split_authority(endpoint, endpoint)
        if port is not None:
            return endpoint
        if insecure:
            raise _invalid(
                endpoint,
                "a plaintext endpoint needs an explicit port, e.g. "
                f"{host}:50051",
            )
        return f"{host}:443"

    scheme, rest = url.group(1).lower(), url.group(2)
    if scheme not in ("http", "https"):
        raise _invalid(endpoint, f"unsupported scheme {url.group(1)!r}")
    # Deployment URLs often carry one trailing slash; accept exactly that.
    if rest.endswith("/"):
        rest = rest[:-1]
    if not rest:
        raise _invalid(endpoint, "missing host")
    host, port = _split_authority(endpoint, rest)
    if scheme == "http":
        if not insecure:
            raise _invalid(
                endpoint,
                "http:// selects plaintext but transport_security is TLS "
                "(use https://, or pass "
                "transport_security=TransportSecurity.PLAINTEXT)",
            )
        if port is None:
            raise _invalid(endpoint, "http:// needs an explicit port")
    else:
        if insecure:
            raise _invalid(
                endpoint,
                "https:// selects TLS but transport_security is PLAINTEXT "
                "(use http://host:port, or drop "
                "transport_security=TransportSecurity.PLAINTEXT)",
            )
        if port is None:
            port = "443"
    return f"{host}:{port}"
