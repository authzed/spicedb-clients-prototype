import ipaddress
import re

import grpc
import grpc.aio

from authzed.api.v1 import experimental_service_pb2_grpc
from authzed.api.v1 import permission_service_pb2_grpc
from authzed.api.v1 import schema_service_pb2_grpc
from authzed.api.v1 import watch_service_pb2_grpc


# Characters that can move which part of a target string a URI parser treats
# as the authority: "@" (userinfo), "/" (path), "?" (query), "#" (fragment),
# and whitespace. See _is_loopback_endpoint below for why an endpoint holding
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


def _is_loopback_endpoint(endpoint: str) -> bool:
    """Report whether the connection this client would open for ``endpoint``
    terminates on a loopback destination.

    True for the literal hostname "localhost", an IP in 127.0.0.0/8, the
    IPv6 loopback ::1, or a unix domain socket target (a "unix:" prefix).
    See root DESIGN.md, "RULE: Credentials over insecure transport require
    an explicit opt-in" -- loopback is the exemption from the opt-in this
    rule otherwise requires.

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
    """
    # Checked first, and only on the raw string: a unix target is not a URI
    # authority at all (it carries a filesystem path, so it legitimately
    # contains the "/" the reserved-character check below refuses), and it
    # never leaves the host's kernel regardless of what the path says.
    if endpoint.startswith("unix:"):
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


class Client:
    """Wraps all generated gRPC service stubs for SpiceDB."""

    def __init__(
        self,
        endpoint: str,
        token: str,
        *,
        insecure: bool = False,
        allow_insecure_remote_credentials: bool = False,
    ):
        # See root DESIGN.md, "RULE: Credentials over insecure transport
        # require an explicit opt-in". This is the guard for
        # _BearerTokenInterceptor below -- the only reason it exists is that
        # channel credentials can't carry call credentials over a plaintext
        # channel, so nothing else here stops a bearer token from reaching
        # an arbitrary insecure host. Refuse before any channel or
        # interceptor is created, so a rejected combination can never put
        # the token on the wire.
        if insecure and not allow_insecure_remote_credentials and not _is_loopback_endpoint(endpoint):
            raise ValueError(
                f"spicedb: refusing to send credentials over an insecure (plaintext) connection "
                f"to non-loopback endpoint {endpoint!r}: use TLS (insecure=False), or pass "
                f"allow_insecure_remote_credentials=True if you intend to send a bearer token in "
                f"cleartext to a remote host"
            )

        if insecure:
            self._channel = grpc.aio.insecure_channel(
                endpoint,
                interceptors=[_BearerTokenInterceptor(token)],
            )
        else:
            call_creds = grpc.access_token_call_credentials(token)
            channel_creds = grpc.ssl_channel_credentials()
            composite_creds = grpc.composite_channel_credentials(
                channel_creds, call_creds
            )
            self._channel = grpc.aio.secure_channel(endpoint, composite_creds)

        self.permissions = permission_service_pb2_grpc.PermissionsServiceStub(
            self._channel
        )
        self.schema = schema_service_pb2_grpc.SchemaServiceStub(self._channel)
        self.watch = watch_service_pb2_grpc.WatchServiceStub(self._channel)
        self.experimental = experimental_service_pb2_grpc.ExperimentalServiceStub(
            self._channel
        )

    async def close(self):
        await self._channel.close()

    async def __aenter__(self):
        return self

    async def __aexit__(self, exc_type, exc_val, exc_tb):
        await self.close()
        return False


class _BearerTokenInterceptor(
    grpc.aio.UnaryUnaryClientInterceptor,
    grpc.aio.UnaryStreamClientInterceptor,
    grpc.aio.StreamUnaryClientInterceptor,
    grpc.aio.StreamStreamClientInterceptor,
):
    def __init__(self, token: str):
        self._metadata = (("authorization", f"Bearer {token}"),)

    def _add_metadata(self, client_call_details):
        metadata = list(client_call_details.metadata or [])
        metadata.extend(self._metadata)
        return grpc.aio.ClientCallDetails(
            client_call_details.method,
            client_call_details.timeout,
            metadata,
            client_call_details.credentials,
            client_call_details.wait_for_ready,
        )

    async def intercept_unary_unary(self, continuation, client_call_details, request):
        return await continuation(self._add_metadata(client_call_details), request)

    async def intercept_unary_stream(self, continuation, client_call_details, request):
        return await continuation(self._add_metadata(client_call_details), request)

    async def intercept_stream_unary(
        self, continuation, client_call_details, request_iterator
    ):
        return await continuation(
            self._add_metadata(client_call_details), request_iterator
        )

    async def intercept_stream_stream(
        self, continuation, client_call_details, request_iterator
    ):
        return await continuation(
            self._add_metadata(client_call_details), request_iterator
        )
