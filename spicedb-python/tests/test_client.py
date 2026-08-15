"""Unit tests for SpiceDBClient construction — no SpiceDB instance needed."""

import inspect
from unittest.mock import AsyncMock

import pytest
from authzed.api.v1 import permission_service_pb2
from google.rpc import status_pb2

from spicedb import Relationship, SpiceDBClient, full
from spicedb.errors import InvalidArgumentError, SpiceDBError


def test_constructor_insecure():
    c = SpiceDBClient("localhost:50051", token="testtoken", insecure=True)
    assert c._channel is not None


async def test_context_manager():
    async with SpiceDBClient(
        "localhost:50051", token="testtoken", insecure=True
    ) as c:
        assert c._channel is not None


class TestSchemaReflectionSignatures:
    """`reflect_schema`/`diff_schema` must return native types, not proto
    (NOT-1: no proto in the public API). `client.py` uses
    `from __future__ import annotations`, so annotations are unevaluated
    strings — check them directly rather than importing proto types here.
    """

    def test_reflect_schema_returns_native_result(self):
        sig = inspect.signature(SpiceDBClient.reflect_schema)
        assert sig.return_annotation == "ReflectSchemaResult"

    def test_diff_schema_returns_native_list(self):
        sig = inspect.signature(SpiceDBClient.diff_schema)
        assert sig.return_annotation == "list[SchemaDiff]"


class TestBulkCheckPerItemErrorFidelity:
    """check_permissions must surface the real per-item google.rpc.Status
    from a CheckBulkPermissions pair as a typed SpiceDBError, not fabricate
    a generic INTERNAL error (CI-2)."""

    async def test_per_item_invalid_argument_surfaces_as_typed_error(self):
        client = SpiceDBClient("localhost:50051", token="testtoken", insecure=True)
        pair = permission_service_pb2.CheckBulkPermissionsPair(
            error=status_pb2.Status(code=3, message="bad item"),  # INVALID_ARGUMENT
        )
        response = permission_service_pb2.CheckBulkPermissionsResponse(pairs=[pair])
        client._permissions.CheckBulkPermissions = AsyncMock(return_value=response)

        rel = Relationship(
            resource_type="document",
            resource_id="1",
            resource_relation="view",
            subject_type="user",
            subject_id="alice",
        )

        with pytest.raises(InvalidArgumentError) as exc_info:
            await client.check_permissions(full(), rel)

        # Must not be downgraded to a generic base SpiceDBError (e.g. the
        # old fabricated INTERNAL path).
        assert type(exc_info.value) is not SpiceDBError
        assert str(exc_info.value) == "bad item"
