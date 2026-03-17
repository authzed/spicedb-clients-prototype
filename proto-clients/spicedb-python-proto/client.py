import grpc
import grpc.aio

from authzed.api.v1 import experimental_service_pb2_grpc
from authzed.api.v1 import permission_service_pb2_grpc
from authzed.api.v1 import schema_service_pb2_grpc
from authzed.api.v1 import watch_service_pb2_grpc


class Client:
    """Wraps all generated gRPC service stubs for SpiceDB."""

    def __init__(self, endpoint: str, token: str, *, insecure: bool = False):
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
