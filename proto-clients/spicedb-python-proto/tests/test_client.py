import warnings

import pytest

from authzed.api.v1 import experimental_service_pb2_grpc
from authzed.api.v1 import permission_service_pb2_grpc
from authzed.api.v1 import schema_service_pb2_grpc
from authzed.api.v1 import watch_service_pb2_grpc

from client import Client, _DEPRECATED_EXPERIMENTAL_METHODS


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


@pytest.mark.parametrize("method_name", sorted(_DEPRECATED_EXPERIMENTAL_METHODS))
def test_deprecated_experimental_method_warns(method_name):
    c = Client("localhost:50051", "test-token", insecure=True)
    method = getattr(c.experimental, method_name)
    with pytest.warns(DeprecationWarning, match=method_name):
        try:
            method(object())
        except Exception:
            # The warning fires before the call reaches the (unconnected)
            # channel; any resulting RPC/serialization error is irrelevant.
            pass


def test_non_deprecated_experimental_method_does_not_warn():
    c = Client("localhost:50051", "test-token", insecure=True)
    with warnings.catch_warnings():
        warnings.simplefilter("error", DeprecationWarning)
        try:
            c.experimental.ExperimentalCountRelationships(object())
        except DeprecationWarning:
            pytest.fail("non-deprecated method raised a DeprecationWarning")
        except Exception:
            pass
