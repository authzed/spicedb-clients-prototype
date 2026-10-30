"""Filtering counts grants, while raw responses drive pagination and cleanup."""
from authzed.api.v1 import core_pb2 as _core_tok
from unittest.mock import Mock

import grpc
import grpc.aio
import pytest
from authzed.api.v1 import core_pb2 as core, permission_service_pb2 as psp
from spicedb import LookupPermissionshipFilter as Mode, InvalidArgumentError, ObjectRef, RetryPolicy, SubjectRef, full
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
            psp.LookupResourcesResponse(looked_up_at=_core_tok.ZedToken(token="t"), resource_object_id=str(i), permissionship=value,
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
    assert requests == ([(0, 2), (2, 2), (4, 2)] if filtered else [(0, 2)])
    assert all(call.cancelled for call in calls)


class FailingCall(Call):
    """Yields its responses, then fails with a retryable status mid-page."""

    def __init__(self, responses, error):
        super().__init__(responses)
        self.error = error

    def __iter__(self):
        yield from self.responses
        raise self.error

    async def __aiter__(self):
        for response in self.responses:
            yield response
        raise self.error


@pytest.mark.parametrize("client_type", [SpiceDBClient, AioClient])
async def test_retried_page_does_not_double_count_skipped_items(client_type):
    # The first attempt reads one filtered-out item and then fails. The retry
    # re-reads the whole (short) page; counting the failed attempt's item too
    # would make the page look full and send a needless follow-up request.
    client = client_type("localhost:50051", token="test", retry_policy=RetryPolicy(initial_backoff=0))
    client._ensure_channel = Mock()
    requests = []
    values = [2, 2]  # conditional only: every item is filtered out

    def responses(start, size):
        return [
            psp.LookupResourcesResponse(looked_up_at=_core_tok.ZedToken(token="t"), resource_object_id=str(i), permissionship=value,
                after_result_cursor=core.Cursor(token=str(i + 1)))
            for i, value in enumerate(values[start:start + size], start)
        ]

    def lookup(request, **kwargs):
        start = int(request.optional_cursor.token or "0")
        requests.append((start, request.optional_limit))
        page = responses(start, request.optional_limit)
        if len(requests) == 1:
            error = grpc.aio.AioRpcError(grpc.StatusCode.UNAVAILABLE, grpc.aio.Metadata(), grpc.aio.Metadata())
            return FailingCall(page[:1], error)
        return Call(page)

    client._permissions = Mock(LookupResources=lookup)
    results = stream(client, page_size=3, permissionship_filter=Mode.HAS_PERMISSION_ONLY)
    if client_type is AioClient:
        ids = [r.resource_id async for r in results]
    else:
        ids = [r.resource_id for r in results]
    assert ids == []
    assert requests == [(0, 3), (0, 3)]
