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
import re

# Characters that can move which part of a target string a URI parser treats
# as the authority: "@" (userinfo), "/" (path), "?" (query), "#" (fragment),
# and whitespace. See is_loopback_endpoint below for why an endpoint holding
# any of them is refused outright rather than parsed.
_AUTHORITY_SHIFTING = re.compile(r"[@/?#]|\s")


def _is_ascii_port(port: str) -> bool:
    """Report whether ``port`` is a run of ASCII digits, as C-core requires.

    Not ``str.isdigit()``: that is true for non-ASCII digits and other numeric
    characters -- ``"٤٤٣"`` (Arabic-Indic), ``"４４３"`` (fullwidth) and ``"²"``
    all pass it, and none of them is a port C-core would parse. This predicate
    is the whole basis for splitting ``host:port`` at all, and a fallback whose
    justification is "mirror C-core exactly" cannot be looser than C-core.
    """
    return port.isascii() and port.isdigit()


def bearer_metadata(token: str) -> list[tuple[str, str]]:
    """Build the gRPC metadata carrying the bearer token."""
    return [("authorization", f"Bearer {token}")]


def is_loopback_endpoint(endpoint: str) -> bool:
    """Report whether the connection this client would open for ``endpoint``
    terminates on a loopback destination.

    True for the literal hostname "localhost", an IP in 127.0.0.0/8, the
    IPv6 loopback ::1, or a unix domain socket target (a "unix:" prefix). A
    unix socket never leaves the host's kernel, so it is loopback for this
    check even though it has no IP at all.

    That wording is deliberate. This does not answer "does this string look
    like it names a loopback host"; it answers "will the transport dial
    loopback". Those are the same question only if this function and the
    transport agree on where the host ends and the rest of the target
    begins, and a hand-rolled split can always diverge from the transport's
    own parse. The equivalent guard in this repo's C#, Rust, TypeScript and
    Java clients diverged exactly that way: given ``"127.0.0.1:443@evil.com"``
    a last-colon split yields host "127.0.0.1" and reports loopback, while
    their transports parsed the same string as a URI, read "127.0.0.1:443"
    as *userinfo*, and connected to evil.com -- shipping the bearer token
    there in cleartext.

    **grpc-python cannot reach its transport's parse.** The target is handed
    to grpc's C-core, which parses it in C++ (``grpc_core::URI::Parse`` plus
    ``SplitHostPort``) and exposes no Python-callable equivalent -- unlike
    Go, C#, Rust, TypeScript and Java, where this guard now derives its host
    from the very parser the transport dials with. So this function does the
    next best thing, in two parts:

    1. Refuse outright any endpoint containing a character that could move
       the authority under URI parsing -- ``@``, ``/``, ``?``, ``#``, or
       whitespace. A legitimate SpiceDB target contains none of those, and
       failing closed on a weird endpoint is the correct trade for a
       credential leak. This is what actually closes the class here.
    2. Split what remains the way C-core's ``SplitHostPort`` does -- a
       bracketed host must be followed by end-of-string or ``":"`` + a
       numeric port, a string with two or more colons is a bare IPv6
       literal, and only a single-colon ``host:port`` with a numeric port is
       split. Requiring the port to be numeric is what C-core does and is
       not decoration: dropping exactly that check from the C# guard is what
       opened the bypass above.

    (For the record, grpc-python was *not* exploitable by
    ``"127.0.0.1:443@evil.com"``: C-core resolves it to ``ipv4:127.0.0.1:443``
    and never contacts evil.com. The point of the above is to stop depending
    on that.)

    This is the exemption in root DESIGN.md, "RULE: Credentials over
    insecure transport require an explicit opt-in": loopback is the reason
    insecure=True exists at all (local development, docker-compose, CI), so
    it must keep working with no extra ceremony. Anything else requires
    allow_insecure_remote_credentials=True -- see
    require_insecure_transport_allowed below.
    """
    # Checked first, and only on the raw string: a unix target is not a URI
    # authority at all (it carries a filesystem path, so it legitimately
    # contains the "/" the reserved-character check below refuses), and it
    # never leaves the host's kernel regardless of what the path says.
    # Case-insensitive because a URI scheme is: C-core normalizes "UNIX:" and
    # dials the socket just the same, so a case-sensitive check here would
    # refuse a target the transport happily treats as local.
    if endpoint[:5].lower() == "unix:":
        return True

    if _AUTHORITY_SHIFTING.search(endpoint):
        return False

    host = endpoint
    if endpoint.startswith("["):
        end = endpoint.find("]")
        if end == -1:
            return False
        rest = endpoint[end + 1 :]
        # C-core accepts "[host]" or "[host]:<digits>" and rejects anything
        # else; "[::1]:443@evil.com" is NOT a bracketed loopback host.
        if rest and not (rest.startswith(":") and _is_ascii_port(rest[1:])):
            return False
        host = endpoint[1:end]
    elif endpoint.count(":") > 1:
        # A bare IPv6 literal (e.g. "::1") -- no port is possible without
        # brackets, so the whole string is the host.
        host = endpoint
    elif ":" in endpoint:
        candidate, _, port = endpoint.rpartition(":")
        # Only a numeric port makes this a host:port; otherwise the colon is
        # part of something C-core would not split here either.
        if _is_ascii_port(port):
            host = candidate

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
