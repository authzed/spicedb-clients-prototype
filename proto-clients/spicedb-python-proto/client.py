import ipaddress

import grpc
import grpc.aio

from authzed.api.v1 import experimental_service_pb2_grpc
from authzed.api.v1 import permission_service_pb2_grpc
from authzed.api.v1 import schema_service_pb2_grpc
from authzed.api.v1 import watch_service_pb2_grpc


def _is_loopback_endpoint(endpoint: str) -> bool:
    """Report whether a gRPC target string names a loopback destination.

    True for the literal hostname "localhost", an IP in 127.0.0.0/8, the
    IPv6 loopback ::1, or a unix domain socket target (a "unix:" prefix).
    See root DESIGN.md, "RULE: Credentials over insecure transport require
    an explicit opt-in" -- loopback is the exemption from the opt-in this
    rule otherwise requires.
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
