from unittest import mock

import pytest

from authzed.api.v1 import experimental_service_pb2_grpc
from authzed.api.v1 import permission_service_pb2_grpc
from authzed.api.v1 import schema_service_pb2_grpc
from authzed.api.v1 import watch_service_pb2_grpc

from client import Client, _EXPERIMENTAL_DEPRECATED_METHODS


def test_constructor_creates_all_stubs():
    c = Client("localhost:50051", "test-token", insecure=True)
    assert isinstance(
        c.permissions, permission_service_pb2_grpc.PermissionsServiceStub
    )
    assert isinstance(c.schema, schema_service_pb2_grpc.SchemaServiceStub)
    assert isinstance(c.watch, watch_service_pb2_grpc.WatchServiceStub)
    assert isinstance(
        c.experimental, experimental_service_pb2_grpc.ExperimentalServiceStub
    )


@pytest.mark.parametrize(
    "name,replacement", list(_EXPERIMENTAL_DEPRECATED_METHODS.items())
)
def test_deprecated_experimental_methods_warn(name: str, replacement: str):
    c = Client("localhost:50051", "test-token", insecure=True)
    method = getattr(c.experimental, name)
    with mock.patch.object(method, "_wrapped") as wrapped:
        with pytest.warns(DeprecationWarning, match=replacement):
            method(mock.sentinel.request)
    wrapped.assert_called_once_with(mock.sentinel.request)


@pytest.mark.asyncio
async def test_context_manager():
    async with Client("localhost:50051", "test-token", insecure=True) as c:
        assert isinstance(
            c.permissions, permission_service_pb2_grpc.PermissionsServiceStub
        )
        assert isinstance(c.schema, schema_service_pb2_grpc.SchemaServiceStub)
        assert isinstance(c.watch, watch_service_pb2_grpc.WatchServiceStub)
        assert isinstance(
            c.experimental, experimental_service_pb2_grpc.ExperimentalServiceStub
        )
