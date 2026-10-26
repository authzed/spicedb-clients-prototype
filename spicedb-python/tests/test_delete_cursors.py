"""Non-atomic deletion forwards each page cursor and retains acknowledged progress."""
from unittest.mock import AsyncMock, Mock

import pytest
from authzed.api.v1 import core_pb2 as core, permission_service_pb2 as p
from spicedb import Filter, ZedToken, UnavailableError
from spicedb.sync import SpiceDBClient
from spicedb.aio import SpiceDBClient as AioClient


@pytest.mark.parametrize("client_type", [SpiceDBClient, AioClient])
@pytest.mark.parametrize("middle_cursor", ["second", "", None])
@pytest.mark.parametrize("failure", [False, True])
async def test_non_atomic_delete_cursor_chain(client_type, middle_cursor, failure):
    client = client_type("localhost:50051", token="key")
    client._ensure_channel = Mock()
    responses = []
    for index, cursor in enumerate(["first", middle_cursor, None]):
        response = p.DeleteRelationshipsResponse(
            deleted_at=core.ZedToken(token=str(index)), relationships_deleted_count=2,
            deletion_progress=1 if index == 2 else 2)
        if cursor is not None:
            response.after_result_cursor.CopyFrom(core.Cursor(token=cursor))
        responses.append(response)
    error = UnavailableError("later batch failed")
    if failure:
        responses[-1] = error
    rpc = AsyncMock(side_effect=responses) if client_type is AioClient else Mock(side_effect=responses)
    client._permissions = Mock(DeleteRelationships=rpc)
    result = None
    try:
        result = client.delete_all_relationships_non_atomic(Filter("document"), page_size=2)
        if client_type is AioClient:
            result = await result
    except UnavailableError as caught:
        assert failure
        assert caught is error
        result = caught.delete_progress
    assert result is not None
    assert result.deleted_count == (4 if failure else 6)
    assert result.revision == ZedToken("1" if failure else "2")
    assert result.cursor == (middle_cursor if failure else None)
    requests = [call.args[0] for call in rpc.call_args_list]
    assert len(requests) == 3
    assert not requests[0].HasField("optional_cursor")
    assert requests[1].optional_cursor.token == "first"
    if middle_cursor:
        assert requests[2].optional_cursor.token == middle_cursor
    else:
        assert not requests[2].HasField("optional_cursor")
