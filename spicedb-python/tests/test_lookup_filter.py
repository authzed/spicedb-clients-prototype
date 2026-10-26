"""Filtering counts grants, while raw responses drive pagination and cleanup."""
from unittest.mock import Mock

import pytest
from authzed.api.v1 import core_pb2 as core, permission_service_pb2 as psp
from spicedb import LookupPermissionshipFilter as Mode, InvalidArgumentError, ObjectRef, SubjectRef, full
from spicedb.sync import SpiceDBClient
from spicedb.aio import SpiceDBClient as AioClient


class Call:
    def __init__(self, responses):
        self.responses = responses
        self.cancelled = False

    def __iter__(self):
        return iter(self.responses)

    async def __aiter__(self):
        for response in self.responses:
            yield response

    def cancel(self):
        self.cancelled = True


def setup(client_type):
    client = client_type("localhost:50051", token="test")
    client._ensure_channel = Mock()
    calls, requests = [], []
    values = [2, 99, 1, 2, 1, 1]

    def lookup(request, **kwargs):
        start = int(request.optional_cursor.token or "0")
        requests.append((start, request.optional_limit))
        call = Call([
            psp.LookupResourcesResponse(resource_object_id=str(i), permissionship=value,
                after_result_cursor=core.Cursor(token=str(i + 1)))
            for i, value in enumerate(values[start:start + request.optional_limit], start)
        ])
        calls.append(call)
        return call

    client._permissions = Mock(LookupResources=lookup)
    return client, calls, requests


def stream(client, **kwargs):
    return client.lookup_resources(resource_type="document", permission="view",
        subject=SubjectRef(ObjectRef("user", "alice")), consistency=full(), **kwargs)


@pytest.mark.parametrize("client_type", [SpiceDBClient, AioClient])
@pytest.mark.parametrize("invalid", [True, "has_permission_only", 1, None])
def test_reject_invalid_filter_before_transport(client_type, invalid):
    client, calls, _ = setup(client_type)
    with pytest.raises(InvalidArgumentError):
        stream(client, permissionship_filter=invalid)
    assert not calls
    client._ensure_channel.assert_not_called()


@pytest.mark.parametrize("client_type", [SpiceDBClient, AioClient])
@pytest.mark.parametrize("filtered", [False, True])
async def test_filter_pagination_limit_and_cleanup(client_type, filtered):
    client, calls, requests = setup(client_type)
    kwargs = {"permissionship_filter": Mode.HAS_PERMISSION_ONLY} if filtered else {}
    results = stream(client, page_size=2, limit=2, **kwargs)
    if client_type is AioClient:
        async with results:
            ids = [r.resource_id async for r in results]
    else:
        with results:
            ids = [r.resource_id for r in results]
    assert ids == (["2", "4"] if filtered else ["0", "1"])
    assert requests == ([(0, 2), (2, 2), (4, 1)] if filtered else [(0, 2)])
    assert all(call.cancelled for call in calls)
