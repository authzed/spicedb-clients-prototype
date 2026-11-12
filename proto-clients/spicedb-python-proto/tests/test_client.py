import warnings

import pytest

from authzed.api.materialize.v0 import roaringlookupresources_pb2_grpc
from authzed.api.v1 import experimental_service_pb2_grpc
from authzed.api.v1 import permission_service_pb2_grpc
from authzed.api.v1 import schema_service_pb2_grpc
from authzed.api.v1 import watch_service_pb2_grpc

from client import Client, _DEPRECATED_EXPERIMENTAL_METHODS


def _assert_has_all_stubs(c):
    assert isinstance(
        c.permissions, permission_service_pb2_grpc.PermissionsServiceStub
    )
    assert isinstance(c.schema, schema_service_pb2_grpc.SchemaServiceStub)
    assert isinstance(c.watch, watch_service_pb2_grpc.WatchServiceStub)
    assert isinstance(
        c.experimental, experimental_service_pb2_grpc.ExperimentalServiceStub
    )
    assert isinstance(
        c.materialize,
        roaringlookupresources_pb2_grpc.RoaringLookupResourcesServiceStub,
    )


def test_constructor_creates_all_stubs():
    c = Client("localhost:50051", "test-token", insecure=True)
    _assert_has_all_stubs(c)


@pytest.mark.asyncio
async def test_context_manager():
    async with Client("localhost:50051", "test-token", insecure=True) as c:
        _assert_has_all_stubs(c)


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


def test_secure_constructor_accepts_tls_material():
    c = Client(
        "example.com:443",
        "test-token",
        ca_cert=b"ca",
        client_cert=b"cert",
        client_key=b"key",
    )
    _assert_has_all_stubs(c)


@pytest.mark.parametrize(
    "kwargs",
    [
        {"client_cert": b"cert"},
        {"client_key": b"key"},
    ],
)
def test_incomplete_client_cert_key_pair_rejected(kwargs):
    with pytest.raises(ValueError, match="mutual TLS needs both or neither"):
        Client("example.com:443", "test-token", **kwargs)


@pytest.mark.parametrize(
    "kwargs",
    [
        {"ca_cert": b"ca"},
        {"client_cert": b"cert", "client_key": b"key"},
    ],
)
def test_tls_material_with_insecure_rejected(kwargs):
    with pytest.raises(ValueError, match="insecure=True and TLS material"):
        Client("localhost:50051", "test-token", insecure=True, **kwargs)
