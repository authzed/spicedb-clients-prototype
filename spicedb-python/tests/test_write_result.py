"""Relationship writes expose immutable results with explicit revision access."""
from dataclasses import FrozenInstanceError
from unittest.mock import AsyncMock, Mock

import pytest
from authzed.api.v1 import core_pb2 as core, permission_service_pb2 as p
from spicedb import InternalError, InvalidArgumentError, Transaction, WriteResult, ZedToken, at_least
from spicedb.sync import SpiceDBClient
from spicedb.aio import SpiceDBClient as AioClient


@pytest.mark.parametrize("client_type", [SpiceDBClient, AioClient])
@pytest.mark.parametrize("token", [" opaque\x00☃ "])
async def test_write_result_preserves_revision_and_request(client_type, token):
    response = p.WriteRelationshipsResponse(written_at=core.ZedToken(token=token))
    client = client_type("localhost:50051", token="key")
    client._ensure_channel = Mock()
    client._permissions = Mock()
    client._rpc = AsyncMock(return_value=response) if client_type is AioClient else Mock(return_value=response)
    result = client.write(Transaction(), metadata={"request": "one"})
    if client_type is AioClient:
        result = await result
    assert type(result) is WriteResult
    assert type(result.revision) is ZedToken
    assert str(result.revision) == token
    assert client._rpc.call_args.args[1].optional_transaction_metadata["request"] == "one"
    if token:
        assert at_least(result.revision)._to_proto().at_least_as_fresh.token == token
    with pytest.raises(InvalidArgumentError):
        at_least(result)
    with pytest.raises(FrozenInstanceError):
        result.revision = ZedToken("changed")


@pytest.mark.parametrize("client_type", [SpiceDBClient, AioClient])
@pytest.mark.parametrize("token", [None, ""])
async def test_write_without_a_revision_is_internal_not_an_empty_token(client_type, token):
    """A response missing its revision is malformed: never an empty token."""
    response = p.WriteRelationshipsResponse()
    if token is not None:
        response.written_at.CopyFrom(core.ZedToken(token=token))
    client = client_type("localhost:50051", token="key")
    client._ensure_channel = Mock()
    client._permissions = Mock()
    client._rpc = AsyncMock(return_value=response) if client_type is AioClient else Mock(return_value=response)
    with pytest.raises(InternalError, match="written_at"):
        result = client.write(Transaction())
        if client_type is AioClient:
            await result


@pytest.mark.parametrize("invalid", ["raw", None, True])
def test_write_result_requires_native_revision(invalid):
    with pytest.raises(InvalidArgumentError):
        WriteResult(invalid)
