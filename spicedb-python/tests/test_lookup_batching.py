"""lookup_resources asks for batched responses and still yields one resource at a time."""
from unittest.mock import Mock

import grpc
import pytest
from authzed.api.v1 import core_pb2 as core, permission_service_pb2 as psp
from spicedb import (
    InternalError,
    LocalValidationError,
    LookupBatching,
    LookupPermissionshipFilter as Mode,
    ObjectRef,
    Paging,
    Permissionship,
    Relationship,
    SubjectRef,
    ZedToken,
    _requests,
    full,
)
from spicedb.aio import SpiceDBClient as AioClient
from spicedb.sync import SpiceDBClient
from spicedb.testing import AsyncInMemoryClient, InMemoryClient, cursor_after

HAS = psp.LOOKUP_PERMISSIONSHIP_HAS_PERMISSION
CONDITIONAL = psp.LOOKUP_PERMISSIONSHIP_CONDITIONAL_PERMISSION
BOTH = pytest.mark.parametrize("client_type", [SpiceDBClient, AioClient])


class Call:
    def __init__(self, responses):
        self.responses = responses

    def __iter__(self):
        return iter(self.responses)

    async def __aiter__(self):
        for response in self.responses:
            yield response

    def cancel(self):
        pass


def batch(start, values, after=None):
    """A batched response holding results ``start``.. with the given permissionships."""
    return psp.LookupResourcesResponse(
        looked_up_at=core.ZedToken(token="t"),
        batched=psp.LookupResourcesBatch(
            results=[
                psp.LookupResourcesResult(resource_object_id=str(i), permissionship=value)
                for i, value in enumerate(values, start)
            ],
            after_cursor=core.Cursor(token=after if after is not None else str(start + len(values))),
        ),
    )


def serve(client_type, values, batching=True):
    """A client whose server holds ``values`` and batches as SpiceDB does:
    a batch every ``optional_response_batch_size`` results and the final
    partial one at the end of each request (or never, when not ``batching``)."""
    client = client_type("localhost:50051", token="test")
    client._ensure_channel = Mock()
    requests = []

    def lookup(request, **kwargs):
        requests.append(request)
        start = int(request.optional_cursor.token or "0")
        end = start + request.optional_limit if request.optional_limit else len(values)
        page = list(enumerate(values[start:end], start))
        size = request.optional_response_batch_size if batching else 0
        if not size:
            return Call([
                psp.LookupResourcesResponse(
                    looked_up_at=core.ZedToken(token="t"), resource_object_id=str(i), permissionship=value,
                    after_result_cursor=core.Cursor(token=str(i + 1)),
                )
                for i, value in page
            ])
        return Call([
            batch(page[offset][0], [value for _, value in page[offset:offset + size]])
            for offset in range(0, len(page), size)
        ])

    client._permissions = Mock(LookupResources=lookup)
    return client, requests


def stream(client, **kwargs):
    return client.lookup_resources(
        resource_type="document", permission="view", subject=SubjectRef(ObjectRef("user", "alice")),
        consistency=full(), **kwargs,
    )


async def collect(results):
    if hasattr(results, "__aiter__"):
        return [item async for item in results]
    return list(results)


def sent(requests):
    """(cursor, limit, batch size) of every request, in order."""
    return [
        (r.optional_cursor.token, r.optional_limit, r.optional_response_batch_size) for r in requests
    ]


@BOTH
@pytest.mark.parametrize("kwargs, want", [
    ({}, 1000),
    ({"page_size": 50}, 50),
    ({"page_size": 5000}, 1000),
    ({"page_size": 50, "response_batch_size": 7}, 7),
    ({"response_batch_size": 5000}, 5000),  # sent as given; the server rejects, the client never clamps
    ({"batching": LookupBatching.DISABLED}, 0),
    ({"batching": LookupBatching.DISABLED, "response_batch_size": 7}, 0),
    ({"paging": Paging.SINGLE_REQUEST}, 1000),
    ({"paging": Paging.SINGLE_REQUEST, "limit": 30}, 30),
    ({"paging": Paging.SINGLE_REQUEST, "limit": 3000}, 1000),
])
async def test_request_batch_size(client_type, kwargs, want):
    client, requests = serve(client_type, [HAS])
    await collect(stream(client, **kwargs))
    assert requests[0].optional_response_batch_size == want


@BOTH
@pytest.mark.parametrize("kwargs", [
    {"response_batch_size": 0},
    {"response_batch_size": -1},
    {"response_batch_size": True},
    {"response_batch_size": 1.5},
    {"batching": False},
    {"batching": "disabled"},
    {"batching": None},
])
def test_invalid_batching_options_fail_before_transport(client_type, kwargs):
    client, requests = serve(client_type, [HAS])
    with pytest.raises(LocalValidationError):
        stream(client, **kwargs)
    assert not requests
    client._ensure_channel.assert_not_called()


@BOTH
async def test_batches_expand_in_order_and_only_batch_ends_carry_cursors(client_type):
    client, requests = serve(client_type, [HAS, CONDITIONAL, HAS, HAS, CONDITIONAL])
    items = await collect(stream(client, response_batch_size=2))
    assert [(i.resource_id, i.permissionship, i.after_result_cursor) for i in items] == [
        ("0", Permissionship.HAS_PERMISSION, None),
        ("1", Permissionship.CONDITIONAL_PERMISSION, "2"),
        ("2", Permissionship.HAS_PERMISSION, None),
        ("3", Permissionship.HAS_PERMISSION, "4"),
        ("4", Permissionship.CONDITIONAL_PERMISSION, "5"),
    ]
    assert {i.looked_up_at for i in items} == {ZedToken("t")}
    assert sent(requests) == [("", 1000, 2)]


@BOTH
async def test_batch_result_fields(client_type):
    client, _ = serve(client_type, [])
    response = batch(0, [])
    response.batched.results.add(resource_object_id="a", permissionship=CONDITIONAL).partial_caveat_info.missing_required_context.append("ip")
    response.batched.results.add(resource_object_id="b", permissionship=99)
    client._permissions = Mock(LookupResources=lambda request, **kwargs: Call([response]))
    first, second = await collect(stream(client))
    assert first.partial_caveat is not None
    assert list(first.partial_caveat.missing_required_context) == ["ip"]
    # A permissionship newer than this client is not malformed and never a grant.
    assert second.permissionship is Permissionship.UNSPECIFIED and second.partial_caveat is None


@BOTH
async def test_paging_counts_results_and_resumes_from_the_batch_cursor(client_type):
    client, requests = serve(client_type, [HAS] * 7)
    items = await collect(stream(client, page_size=3, response_batch_size=2))
    assert [i.resource_id for i in items] == [str(i) for i in range(7)]
    # Three results in two messages fill a page; the short third page ends the read.
    assert sent(requests) == [("", 3, 2), ("3", 3, 2), ("6", 3, 2)]
    assert [i.after_result_cursor for i in items] == [None, "2", "3", None, "5", "6", "7"]


@BOTH
async def test_full_last_page_is_followed_by_an_empty_one(client_type):
    client, requests = serve(client_type, [HAS] * 4)
    items = await collect(stream(client, page_size=2))
    assert len(items) == 4
    assert sent(requests) == [("", 2, 2), ("2", 2, 2), ("4", 2, 2)]


@BOTH
async def test_empty_batch_yields_nothing(client_type):
    client, _ = serve(client_type, [])
    responses = [batch(0, [], after="0"), batch(0, [HAS])]
    client._permissions = Mock(LookupResources=lambda request, **kwargs: Call(responses))
    items = await collect(stream(client))
    assert [(i.resource_id, i.after_result_cursor) for i in items] == [("0", "1")]


@BOTH
async def test_singular_responses_to_a_batched_request_keep_per_item_cursors(client_type):
    client, requests = serve(client_type, [HAS] * 5, batching=False)
    items = await collect(stream(client, page_size=2))
    assert [(i.resource_id, i.after_result_cursor) for i in items] == [(str(i), str(i + 1)) for i in range(5)]
    assert sent(requests) == [("", 2, 2), ("2", 2, 2), ("4", 2, 2)]


@BOTH
async def test_disabled_batching_requests_and_reads_singular_responses(client_type):
    client, requests = serve(client_type, [HAS] * 3)
    items = await collect(stream(client, batching=LookupBatching.DISABLED))
    assert [i.after_result_cursor for i in items] == ["1", "2", "3"]
    assert sent(requests) == [("", 1000, 0)]


@BOTH
async def test_filter_moves_the_cursor_to_the_last_kept_item(client_type):
    values = [HAS, HAS, CONDITIONAL, CONDITIONAL, CONDITIONAL, CONDITIONAL, HAS, CONDITIONAL]
    client, requests = serve(client_type, values)
    items = await collect(stream(client, page_size=4, response_batch_size=3, permissionship_filter=Mode.HAS_PERMISSION_ONLY))
    # Batches: [0 1 2] [3] | [4 5 6] [7]. The first batch's trailing result is
    # dropped, so its cursor moves to item 1; the wholly dropped batches put
    # their cursor on nothing, and paging still advances past them.
    assert [(i.resource_id, i.after_result_cursor) for i in items] == [("0", None), ("1", "3"), ("6", "7")]
    assert sent(requests) == [("", 4, 3), ("4", 4, 3), ("8", 4, 3)]


@BOTH
@pytest.mark.parametrize("kwargs, want_requests", [
    # remaining < batch size: the last page asks for exactly the remainder.
    ({"page_size": 4, "limit": 6}, [("", 4, 4), ("4", 4, 2)]),
    # remaining is a multiple of the batch size: unchanged.
    ({"page_size": 4, "response_batch_size": 2, "limit": 6}, [("", 4, 2), ("4", 4, 2)]),
    # neither: batching is off for that page.
    ({"page_size": 4, "response_batch_size": 2, "limit": 7}, [("", 4, 2), ("4", 4, 0)]),
    ({"page_size": 4, "limit": 3}, [("", 4, 3)]),
])
async def test_limited_call_ends_on_a_cursor(client_type, kwargs, want_requests):
    client, requests = serve(client_type, [HAS] * 20)
    items = await collect(stream(client, **kwargs))
    assert len(items) == kwargs["limit"]
    assert items[-1].after_result_cursor == str(kwargs["limit"])
    assert sent(requests) == want_requests
    rest = await collect(stream(client, cursor=items[-1].after_result_cursor, page_size=kwargs["page_size"]))
    assert [i.resource_id for i in rest] == [str(i) for i in range(kwargs["limit"], 20)]


@BOTH
async def test_limit_with_the_unconditional_only_filter_disables_batching(client_type):
    client, requests = serve(client_type, [HAS, CONDITIONAL, HAS, HAS, HAS])
    items = await collect(stream(client, page_size=2, limit=2, permissionship_filter=Mode.HAS_PERMISSION_ONLY))
    assert [(i.resource_id, i.after_result_cursor) for i in items] == [("0", "1"), ("2", "3")]
    assert sent(requests) == [("", 2, 0), ("2", 2, 0)]


@BOTH
async def test_single_request_limit_ends_on_a_cursor(client_type):
    client, requests = serve(client_type, [HAS] * 9)
    items = await collect(stream(client, paging=Paging.SINGLE_REQUEST, limit=5, response_batch_size=2))
    assert [i.after_result_cursor for i in items] == [None, "2", None, "4", "5"]
    assert sent(requests) == [("", 5, 2)]


@pytest.mark.parametrize("batch_size, page_size, limit, yielded, single, only, want", [
    (0, 100, 50, 0, False, False, 0),      # disabled stays disabled
    (100, 100, None, 0, False, False, 100),  # no limit
    (100, 100, 250, 0, False, False, 100),   # a full page remains
    (100, 100, 250, 200, False, False, 50),  # remaining < batch size
    (25, 100, 250, 200, False, False, 25),   # remaining a multiple of the batch size
    (30, 100, 250, 200, False, False, 0),    # neither
    (100, 100, 250, 0, False, True, 0),      # limit with the unconditional-only filter
    (100, 100, None, 0, False, True, 100),   # the filter alone keeps batching
    (30, 100, 50, 0, True, False, 30),       # single request: the server ends the last batch at the limit
    (30, 100, 50, 0, True, True, 30),
])
def test_page_batch_size(batch_size, page_size, limit, yielded, single, only, want):
    assert _requests.lookup_page_batch_size(batch_size, page_size, limit, yielded, single, only) == want


@BOTH
@pytest.mark.parametrize("result", [
    psp.LookupResourcesResult(permissionship=HAS),
    psp.LookupResourcesResult(resource_object_id="a"),
])
async def test_malformed_batch_result_is_internal(client_type, result):
    client, _ = serve(client_type, [])
    response = batch(0, [HAS])
    response.batched.results.append(result)
    client._permissions = Mock(LookupResources=lambda request, **kwargs: Call([response]))
    with pytest.raises(InternalError) as raised:
        await collect(stream(client))
    assert raised.value.code() is grpc.StatusCode.INTERNAL
    assert "malformed response" in str(raised.value)


@BOTH
async def test_batched_response_without_looked_up_at_is_internal(client_type):
    client, _ = serve(client_type, [])
    response = batch(0, [HAS])
    response.ClearField("looked_up_at")
    client._permissions = Mock(LookupResources=lambda request, **kwargs: Call([response]))
    with pytest.raises(InternalError):
        await collect(stream(client))


# --- the in-memory double ---------------------------------------------------


def _double(double_type, n=7, conditional=()):
    return double_type([
        Relationship.from_tuple(f"document:d{i}#view@user:alice" + ("[c]" if i in conditional else ""))
        for i in range(n)
    ])


DOUBLES = pytest.mark.parametrize("double_type", [InMemoryClient, AsyncInMemoryClient])


@DOUBLES
async def test_double_puts_cursors_on_batch_ends_only(double_type):
    client = _double(double_type)
    items = await collect(stream(client, page_size=5, response_batch_size=2))
    # Pages of five results, each cut into batches of two.
    assert [(i.resource_id, i.after_result_cursor) for i in items] == [
        ("d0", None), ("d1", "2"), ("d2", None), ("d3", "4"), ("d4", "5"), ("d5", None), ("d6", "7"),
    ]
    assert items[3].after_result_cursor == cursor_after(4)


@DOUBLES
async def test_double_resumes_from_a_batch_end_cursor(double_type):
    client = _double(double_type)
    items = await collect(stream(client, response_batch_size=3))
    assert [i.after_result_cursor for i in items] == [None, None, "3", None, None, "6", "7"]
    rest = await collect(stream(client, response_batch_size=3, cursor=items[2].after_result_cursor))
    assert [i.resource_id for i in rest] == ["d3", "d4", "d5", "d6"]


@DOUBLES
async def test_double_disabled_batching_gives_per_item_cursors(double_type):
    client = _double(double_type, 3)
    items = await collect(stream(client, batching=LookupBatching.DISABLED))
    assert [i.after_result_cursor for i in items] == ["1", "2", "3"]


@DOUBLES
async def test_double_limit_ends_on_a_cursor(double_type):
    client = _double(double_type)
    items = await collect(stream(client, limit=3))
    assert [i.after_result_cursor for i in items] == [None, None, "3"]
    # A remainder the batch size does not divide turns batching off for that page.
    items = await collect(stream(client, page_size=4, response_batch_size=2, limit=7))
    assert [i.after_result_cursor for i in items] == [None, "2", None, "4", "5", "6", "7"]
    items = await collect(stream(client, limit=2, permissionship_filter=Mode.HAS_PERMISSION_ONLY))
    assert [i.after_result_cursor for i in items] == ["1", "2"]


@DOUBLES
async def test_double_filter_moves_the_cursor_to_the_last_kept_item(double_type):
    client = _double(double_type, 6, conditional={2, 3, 4, 5})
    items = await collect(stream(client, response_batch_size=3, permissionship_filter=Mode.HAS_PERMISSION_ONLY))
    assert [(i.resource_id, i.after_result_cursor) for i in items] == [("d0", None), ("d1", "3")]


@DOUBLES
async def test_double_single_request_limit_is_the_request_limit(double_type):
    client = _double(double_type, 6, conditional={1, 2})
    only = Mode.HAS_PERMISSION_ONLY
    # One request ends after `limit` granted results; the filter applies to
    # those, so fewer than `limit` items come back, as from a real request.
    items = await collect(stream(client, paging=Paging.SINGLE_REQUEST, limit=3, permissionship_filter=only))
    assert [(i.resource_id, i.after_result_cursor) for i in items] == [("d0", "3")]
    items = await collect(stream(
        client, paging=Paging.SINGLE_REQUEST, limit=3, permissionship_filter=only, batching=LookupBatching.DISABLED,
    ))
    assert [(i.resource_id, i.after_result_cursor) for i in items] == [("d0", "1")]
    items = await collect(stream(client, paging=Paging.SINGLE_REQUEST, limit=3))
    assert [(i.resource_id, i.after_result_cursor) for i in items] == [("d0", None), ("d1", None), ("d2", "3")]
    # Automatic paging keeps counting yielded items.
    items = await collect(stream(client, limit=3, permissionship_filter=only))
    assert [i.resource_id for i in items] == ["d0", "d3", "d4"]


@DOUBLES
def test_double_rejects_invalid_batching_options(double_type):
    client = _double(double_type)
    with pytest.raises(LocalValidationError):
        stream(client, response_batch_size=0)
    with pytest.raises(LocalValidationError):
        stream(client, batching=False)
