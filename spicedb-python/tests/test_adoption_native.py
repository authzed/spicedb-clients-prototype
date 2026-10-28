"""Tooling-grade native behaviors, without a server.

Each test fails if the behavior it names is removed. Transport-level behavior
(import abort status, response metadata, caller-owned channels) runs against
a real in-process gRPC server; paging and retry run against recording stubs.
"""

from __future__ import annotations

import asyncio
import json
from concurrent import futures
from datetime import datetime, timezone
from unittest.mock import AsyncMock, Mock

import grpc
import grpc.aio
import pytest
from authzed.api.v1 import core_pb2, permission_service_pb2 as psp, schema_service_pb2 as ssp
from google.rpc import error_details_pb2, status_pb2

from spicedb import (
    AlreadyExistsError,
    BulkItemErrors,
    CallOptions,
    Caveat,
    DefaultDeadline,
    DeletePartial,
    Paging,
    DeadlineExceededError,
    Filter,
    FailedPreconditionError,
    InvalidArgumentError,
    NotFoundError,
    ObjectRef,
    PermissionCheck,
    Permissionship,
    Precondition,
    PreconditionOperation,
    Relationship,
    ResponseMetadata,
    Retries,
    RetryPolicy,
    SpiceDBError,
    StreamRetries,
    SubjectFilter,
    SubjectRelationFilter,
    SubjectRef,
    Transaction,
    TransportSecurity,
    Update,
    UpdateOperation,
    ZedToken,
    code_of,
    full,
)
from spicedb.aio import SpiceDBClient as AioClient
from spicedb.errors import to_spicedb_error
from spicedb.sync import SpiceDBClient as SyncClient


def _sync(**kwargs) -> SyncClient:
    client = SyncClient("localhost:1", token="t", transport_security=TransportSecurity.PLAINTEXT, **kwargs)
    client._ensure_channel()
    return client


def _rpc_error(code=grpc.StatusCode.UNAVAILABLE, details="boom"):
    return grpc.aio.AioRpcError(code, grpc.aio.Metadata(), grpc.aio.Metadata(), details=details)


# --- errors -----------------------------------------------------


@pytest.mark.parametrize(
    "error_type, code",
    [
        (NotFoundError, grpc.StatusCode.NOT_FOUND),
        (AlreadyExistsError, grpc.StatusCode.ALREADY_EXISTS),
        (InvalidArgumentError, grpc.StatusCode.INVALID_ARGUMENT),
        (FailedPreconditionError, grpc.StatusCode.FAILED_PRECONDITION),
        (DeadlineExceededError, grpc.StatusCode.DEADLINE_EXCEEDED),
    ],
)
def test_native_errors_report_their_grpc_code(error_type, code):
    error = error_type("local")
    assert isinstance(error, grpc.RpcError)
    assert error.code() is code
    assert error.details() == "local"
    assert code_of(error) is code
    assert error.server_message is None


@pytest.mark.parametrize("code", list(grpc.StatusCode)[1:])
def test_from_code_matches_server_errors_for_every_code(code):
    built = SpiceDBError.from_code(code, "msg")
    from_server = to_spicedb_error(_rpc_error(code, "msg"))
    assert type(built) is type(from_server)
    assert built.code() is code is from_server.code()
    assert built.server_message == "msg" == from_server.server_message


def test_server_message_is_verbatim_and_unmapped_codes_are_preserved():
    error = to_spicedb_error(_rpc_error(grpc.StatusCode.INTERNAL, "  exact server text "))
    assert type(error) is SpiceDBError
    assert error.code() is grpc.StatusCode.INTERNAL
    assert error.server_message == "  exact server text "


def test_code_of_classifies_raw_and_local_errors():
    assert code_of(_rpc_error(grpc.StatusCode.ABORTED)) is grpc.StatusCode.ABORTED
    assert code_of(asyncio.CancelledError()) is grpc.StatusCode.CANCELLED
    assert code_of(TimeoutError()) is grpc.StatusCode.DEADLINE_EXCEEDED
    assert code_of(ValueError()) is grpc.StatusCode.UNKNOWN
    assert code_of(None) is grpc.StatusCode.OK


def test_caller_built_error_carries_rich_status_trailer():
    error = SpiceDBError.from_code(
        grpc.StatusCode.FAILED_PRECONDITION, "nope", reason="R", reason_domain="authzed.com"
    )
    (key, value), = error.trailing_metadata()
    status = status_pb2.Status.FromString(value)
    info = error_details_pb2.ErrorInfo()
    status.details[0].Unpack(info)
    assert key == "grpc-status-details-bin"
    assert (status.code, status.message, info.reason) == (grpc.StatusCode.FAILED_PRECONDITION.value[0], "nope", "R")


def test_except_grpc_rpc_error_catches_native_errors():
    client = _sync()
    client._permissions.WriteRelationships = Mock(side_effect=_rpc_error(grpc.StatusCode.NOT_FOUND))
    with pytest.raises(grpc.RpcError) as caught:
        client.write(Transaction().touch(Relationship.from_tuple("d:1#r@u:1")))
    assert caught.value.code() is grpc.StatusCode.NOT_FOUND


def test_local_validation_and_deadline_report_codes():
    client = _sync(default_timeout=0.01)
    with pytest.raises(SpiceDBError) as caught:
        client.check_bulk([], consistency=full(), tracing="yes")
    assert code_of(caught.value) is grpc.StatusCode.INVALID_ARGUMENT

    def slow(*_a, **_k):
        raise _rpc_error(grpc.StatusCode.UNAVAILABLE)

    client._schema.ReadSchema = Mock(side_effect=slow)
    client._backoff_seconds = lambda *_: 1.0
    with pytest.raises(SpiceDBError) as caught:
        client.read_schema()
    assert code_of(caught.value) is grpc.StatusCode.DEADLINE_EXCEEDED


# --- filter matching ----------------------------------------------------

_REL = Relationship.from_tuple("document:doc-1#viewer@group:eng#member")
_DIRECT = Relationship.from_tuple("document:doc-1#viewer@user:alice")


@pytest.mark.parametrize(
    "filter, rel, expected",
    [
        (Filter("document"), _REL, True),
        (Filter("folder"), _REL, False),
        (Filter("document", resource_id="doc-1"), _REL, True),
        (Filter("document", resource_id="doc-2"), _REL, False),
        (Filter("document", resource_id_prefix="doc-"), _REL, True),
        (Filter("document", resource_id_prefix="x"), _REL, False),
        (Filter("document", relation="viewer"), _REL, True),
        (Filter("document", relation="editor"), _REL, False),
        (Filter("document", subject=SubjectFilter("group")), _REL, True),
        (Filter("document", subject=SubjectFilter("user")), _REL, False),
        (Filter("document", subject=SubjectFilter("group", subject_id="eng")), _REL, True),
        (Filter("document", subject=SubjectFilter("group", subject_id="ops")), _REL, False),
        (Filter("document", subject=SubjectFilter("group", relation=SubjectRelationFilter.any())), _REL, True),
        (Filter("document", subject=SubjectFilter("group", relation=SubjectRelationFilter.direct())), _REL, False),
        (Filter("document", subject=SubjectFilter("user", relation=SubjectRelationFilter.direct())), _DIRECT, True),
        (Filter("document", subject=SubjectFilter("group", relation=SubjectRelationFilter.named("member"))), _REL, True),
        (Filter("document", subject=SubjectFilter("group", relation=SubjectRelationFilter.named("admin"))), _REL, False),
    ],
)
def test_filter_matches_uses_server_semantics(filter, rel, expected):
    assert filter.matches(rel) is expected


# --- observable values --------------------------------------------------


def test_transaction_length_strings_and_json():
    txn = Transaction()
    assert len(txn) == 0 and not txn
    txn.must_match(Filter("document", resource_id="a"))
    txn.touch(Relationship.from_tuple("document:a#viewer@user:b"))
    txn.delete(Relationship.from_tuple("document:a#editor@team:x#member"))
    assert len(txn) == 2
    assert str(txn) == (
        "[MUST_MATCH(document:a#*@*), TOUCH(document:a#viewer@user:b), "
        "DELETE(document:a#editor@team:x#member)]"
    )
    assert json.loads(json.dumps(txn.to_dict())) == {
        "preconditions": [{"operation": "MUST_MATCH", "filter": "document:a#*@*"}],
        "updates": [
            {"operation": "TOUCH", "relationship": {"resource": "document:a", "relation": "viewer", "subject": "user:b"}},
            {"operation": "DELETE", "relationship": {"resource": "document:a", "relation": "editor", "subject": "team:x#member"}},
        ],
    }
    assert "updates=" in repr(txn) and txn.updates[0].operation is UpdateOperation.TOUCH


def test_value_strings_and_dicts_are_never_empty():
    rel = Relationship(
        ObjectRef("document", "a"),
        "viewer",
        SubjectRef(ObjectRef("user", "b")),
        Caveat("ip", {"cidr": "10.0.0.0/8", "a": [1, None]}),
        datetime(2030, 1, 2, 3, 4, 5, tzinfo=timezone.utc),
    )
    assert str(ObjectRef("document", "a")) == "document:a"
    assert str(SubjectRef(ObjectRef("team", "x"), "member")) == "team:x#member"
    assert json.dumps(rel.to_dict()) == json.dumps({
        "resource": "document:a",
        "relation": "viewer",
        "subject": "user:b",
        "caveat_name": "ip",
        "caveat_context": {"cidr": "10.0.0.0/8", "a": [1, None]},
        "expiration": "2030-01-02T03:04:05Z",
    })
    f = Filter("document", resource_id_prefix="doc", subject=SubjectFilter("user", relation=SubjectRelationFilter.direct()))
    assert str(f) == "document:doc%*#*@user:*#..."
    assert f.to_dict() == {"resource_type": "document", "resource_id_prefix": "doc", "subject_type": "user", "subject_relation": "..."}
    assert str(Update(UpdateOperation.CREATE, rel)).startswith("CREATE(document:a#viewer@user:b[ip:")
    p = Precondition(PreconditionOperation.MUST_NOT_MATCH, Filter("document"))
    assert str(p) == "MUST_NOT_MATCH(document:*#*@*)"
    assert p.to_dict() == {"operation": "MUST_NOT_MATCH", "filter": "document:*#*@*"}


def test_short_relationship_accessors():
    assert (_REL.resource_type, _REL.resource_id, _REL.subject_type, _REL.subject_id, _REL.subject_relation) == (
        "document", "doc-1", "group", "eng", "member",
    )
    assert _DIRECT.subject_relation is None


# --- tuple syntax and copy helpers ---------------------------------------

# The same fixtures spicedb-gen/tuplecompat checks against SpiceDB's parser.
TUPLES = [
    "document:readme#viewer@user:alice",
    "document:readme#viewer@team:eng#member",
    "document:readme#viewer@user:*",
    "document:readme#viewer@user:alice[only_name]",
    'document:readme#viewer@user:alice[ip_range:{"cidr":"10.0.0.0/8","list":["a","]"],"n":1.5,"nested":{"b":true}}]',
    "document:readme#viewer@user:alice[expiration:2030-01-02T03:04:05Z]",
    "document:readme#viewer@user:alice[expiration:2030-01-02T03:04:05.123456Z]",
    'document:readme#viewer@team:eng#member[ip_range:{"cidr":"x"}][expiration:2030-01-02T03:04:05Z]',
]


@pytest.mark.parametrize("text", TUPLES)
def test_tuple_round_trips(text):
    rel = Relationship.from_tuple(text)
    assert str(rel) == text
    assert Relationship.from_tuple(str(rel)) == rel


def test_tuple_parsing_details():
    rel = Relationship.from_tuple('document:a#viewer@user:b#...[c:{"k":"]"}][expiration:2030-01-02T05:04:05+02:00]')
    assert rel.subject.relation is None
    assert rel.caveat == Caveat("c", {"k": "]"})
    assert rel.expiration == datetime(2030, 1, 2, 3, 4, 5, tzinfo=timezone.utc)
    nanos = Relationship.from_tuple("document:a#viewer@user:b[expiration:2030-01-02T03:04:05.123456789Z]")
    assert nanos.expiration.microsecond == 123456


@pytest.mark.parametrize(
    "bad",
    [
        "document:a#viewer",
        "document:a@user:b",
        "document#viewer@user:b",
        "document:a#viewer@user:b#",
        "document:a#viewer@user:b[",
        "document:a#viewer@user:b[c:{]",
        "document:a#viewer@user:b[c:[1]]",
        "document:a#viewer@user:b[expiration:2030-01-02]",
        "document:a#viewer@user:b[expiration:2030-01-02T03:04:05]",
        "document:a#viewer@user:b[expiration:2030-01-02T03:04:05Z][c]",
    ],
)
def test_invalid_tuples_are_rejected(bad):
    with pytest.raises(InvalidArgumentError):
        Relationship.from_tuple(bad)


def test_copy_helpers_keep_caveat_and_expiration():
    rel = Relationship.from_tuple("document:a#viewer@user:b[c][expiration:2030-01-02T03:04:05Z]")
    moved = rel.with_resource(ObjectRef("folder", "f")).with_relation("owner").with_subject(SubjectRef(ObjectRef("team", "t"), "m"))
    assert str(moved) == "folder:f#owner@team:t#m[c][expiration:2030-01-02T03:04:05Z]"
    assert str(rel) == "document:a#viewer@user:b[c][expiration:2030-01-02T03:04:05Z]"


# --- Paging (bug fix) and cursors --------------------------------------------------


class _Call:
    def __init__(self, responses):
        self.responses = responses

    def __iter__(self):
        return iter(self.responses)

    async def __aiter__(self):
        for r in self.responses:
            yield r

    def cancel(self):
        return True


def _paged_reads(total):
    requests = []

    def read(request, **kwargs):
        requests.append(request)
        start = int(request.optional_cursor.token or "0")
        return _Call([
            psp.ReadRelationshipsResponse(
                read_at=core_pb2.ZedToken(token=f"rev{start}"),
                relationship=Relationship.from_tuple(f"document:{i}#viewer@user:u")._to_proto(),
                after_result_cursor=core_pb2.Cursor(token=str(i + 1)),
            )
            for i in range(start, min(start + request.optional_limit, total))
        ])

    return read, requests


@pytest.mark.parametrize("client_type", [SyncClient, AioClient])
async def test_every_page_repeats_consistency_and_page_size(client_type):
    client = client_type("localhost:1", token="t", transport_security=TransportSecurity.PLAINTEXT)
    client._ensure_channel = Mock()
    read, requests = _paged_reads(5)
    client._permissions = Mock(ReadRelationships=read)
    stream = client.read_relationships(Filter("document"), consistency=full(), page_size=2, cursor="1")
    items = [x async for x in stream] if client_type is AioClient else list(stream)
    assert [x.relationship.resource_id for x in items] == ["1", "2", "3", "4"]
    assert [x.after_result_cursor for x in items] == ["2", "3", "4", "5"]
    assert items[0].read_at == ZedToken("rev1")
    assert [(r.optional_limit, r.optional_cursor.token) for r in requests] == [(2, "1"), (2, "3"), (2, "5")]
    assert all(r.consistency == full()._to_proto() for r in requests)


@pytest.mark.parametrize("client_type", [SyncClient, AioClient])
async def test_lookup_limit_never_shrinks_the_page(client_type):
    client = client_type("localhost:1", token="t", transport_security=TransportSecurity.PLAINTEXT)
    client._ensure_channel = Mock()
    requests = []

    def lookup(request, **kwargs):
        requests.append((request.optional_limit, request.optional_cursor.token))
        start = int(request.optional_cursor.token or "0")
        return _Call([
            psp.LookupResourcesResponse(
                resource_object_id=str(i),
                permissionship=psp.LOOKUP_PERMISSIONSHIP_HAS_PERMISSION,
                after_result_cursor=core_pb2.Cursor(token=str(i + 1)),
            )
            for i in range(start, start + request.optional_limit)
        ])

    client._permissions = Mock(LookupResources=lookup)
    stream = client.lookup_resources(
        resource_type="document", permission="view", subject=SubjectRef(ObjectRef("user", "u")),
        consistency=full(), page_size=2, limit=3, cursor="10",
    )
    items = [x async for x in stream] if client_type is AioClient else list(stream)
    assert [x.resource_id for x in items] == ["10", "11", "12"]
    assert items[-1].after_result_cursor == "13"
    assert requests == [(2, "10"), (2, "12")]


def test_export_items_share_their_response_cursor():
    client = _sync()
    client._permissions.ExportBulkRelationships = Mock(return_value=_Call([
        psp.ExportBulkRelationshipsResponse(
            after_result_cursor=core_pb2.Cursor(token="c1"),
            relationships=[_REL._to_proto(), _DIRECT._to_proto()],
        )
    ]))
    items = list(client.export_relationships(consistency=full(), page_size=5, cursor="c0"))
    assert [(x.relationship, x.after_result_cursor) for x in items] == [(_REL, "c1"), (_DIRECT, "c1")]
    assert client._permissions.ExportBulkRelationships.call_args.args[0].optional_cursor.token == "c0"


# --- retry policy and default deadline -------------------------------


def test_retry_policy_codes_callback_and_mutations():
    seen = []
    client = _sync(retry_policy=RetryPolicy(
        max_retries=2,
        retryable_codes=frozenset({grpc.StatusCode.RESOURCE_EXHAUSTED}),
        initial_backoff=0,
        on_retry=seen.append,
    ))
    client._schema.ReadSchema = Mock(side_effect=[
        _rpc_error(grpc.StatusCode.RESOURCE_EXHAUSTED),
        _rpc_error(grpc.StatusCode.RESOURCE_EXHAUSTED),
        ssp.ReadSchemaResponse(schema_text="s"),
    ])
    assert client.read_schema().schema == "s"
    assert [(a.attempt, a.error.code()) for a in seen] == [(1, grpc.StatusCode.RESOURCE_EXHAUSTED)] * 1 + [(2, grpc.StatusCode.RESOURCE_EXHAUSTED)]
    client._schema.ReadSchema = Mock(side_effect=_rpc_error(grpc.StatusCode.UNAVAILABLE))
    with pytest.raises(SpiceDBError):
        client.read_schema()
    assert client._schema.ReadSchema.call_count == 1  # UNAVAILABLE not in this policy
    client._schema.WriteSchema = Mock(side_effect=_rpc_error(grpc.StatusCode.RESOURCE_EXHAUSTED))
    with pytest.raises(SpiceDBError):
        client.write_schema("s")
    assert client._schema.WriteSchema.call_count == 1  # mutations never replay


def test_retries_disabled_and_stream_retries_disabled():
    client = _sync(retry_policy=Retries.DISABLED)
    client._schema.ReadSchema = Mock(side_effect=_rpc_error())
    with pytest.raises(SpiceDBError):
        client.read_schema()
    assert client._schema.ReadSchema.call_count == 1

    client = _sync(stream_retries=StreamRetries.DISABLED, retry_policy=RetryPolicy(initial_backoff=0))
    client._permissions.ReadRelationships = Mock(side_effect=_rpc_error())
    with pytest.raises(SpiceDBError):
        list(client.read_relationships(Filter("document"), consistency=full()))
    assert client._permissions.ReadRelationships.call_count == 1
    client._schema.ReadSchema = Mock(side_effect=[_rpc_error(), ssp.ReadSchemaResponse()])
    client.read_schema()  # unary retry unaffected
    assert client._schema.ReadSchema.call_count == 2


_RETRY_ONCE = CallOptions(retry_policy=RetryPolicy(max_retries=1, initial_backoff=0))


def _touch() -> Transaction:
    return Transaction().touch(Relationship.from_tuple("document:a#viewer@user:alice"))


@pytest.mark.parametrize("client_type", [SyncClient, AioClient])
async def test_per_call_retry_policy(client_type):
    async def run(value):
        return await value if client_type is AioClient else value

    def stub(*responses):
        return (AsyncMock if client_type is AioClient else Mock)(side_effect=list(responses))

    client = client_type("localhost:1", token="t", transport_security=TransportSecurity.PLAINTEXT)
    client._ensure_channel = Mock()
    written = psp.WriteRelationshipsResponse(written_at=core_pb2.ZedToken(token="w"))

    # Mutations are never retried by default.
    client._permissions = Mock(WriteRelationships=stub(_rpc_error(), written))
    with pytest.raises(SpiceDBError):
        await run(client.write(_touch()))
    assert client._permissions.WriteRelationships.call_count == 1

    # A touch-only write without preconditions may opt in for one call.
    client._permissions = Mock(WriteRelationships=stub(_rpc_error(), written))
    assert (await run(client.write(_touch(), call_options=_RETRY_ONCE))).revision == ZedToken("w")
    assert client._permissions.WriteRelationships.call_count == 2

    # Non-idempotent mutations refuse the opt-in before any request.
    client._permissions = Mock(WriteRelationships=stub(written))
    created = Transaction().create(Relationship.from_tuple("document:a#viewer@user:alice"))
    guarded = _touch().must_match(Filter("document"))
    for txn in (created, guarded):
        with pytest.raises(InvalidArgumentError):
            await run(client.write(txn, call_options=_RETRY_ONCE))
    for refused in (
        lambda: client.delete_relationships(Filter("document"), limit=1, call_options=_RETRY_ONCE),
        lambda: client.delete_all_relationships_non_atomic(Filter("document"), call_options=_RETRY_ONCE),
        lambda: client.import_relationships([], call_options=_RETRY_ONCE),
        lambda: client.experimental_register_relationship_counter("c", Filter("document"), call_options=_RETRY_ONCE),
        lambda: client.experimental_unregister_relationship_counter("c", call_options=_RETRY_ONCE),
    ):
        with pytest.raises(InvalidArgumentError):
            await run(refused())
    assert client._permissions.WriteRelationships.call_count == 0
    # Opting out (no retries) is always allowed.
    client._permissions = Mock(DeleteRelationships=stub(psp.DeleteRelationshipsResponse(deletion_progress=1)))
    await run(client.delete_relationships(
        Filter("document"), limit=1, call_options=CallOptions(retry_policy=Retries.DISABLED)))

    # Schema writes are idempotent and may opt in.
    client._schema = Mock(WriteSchema=stub(_rpc_error(), ssp.WriteSchemaResponse()))
    await run(client.write_schema("definition user {}", call_options=_RETRY_ONCE))
    assert client._schema.WriteSchema.call_count == 2

    # A read can be exempted from the client's retries for one call.
    client._schema = Mock(ReadSchema=stub(_rpc_error(), ssp.ReadSchemaResponse()))
    with pytest.raises(SpiceDBError):
        await run(client.read_schema(call_options=CallOptions(retry_policy=Retries.DISABLED)))
    assert client._schema.ReadSchema.call_count == 1


@pytest.mark.parametrize("client_type", [SyncClient, AioClient])
async def test_per_call_retry_policy_governs_stream_establishment(client_type):
    async def drain(stream):
        return [x async for x in stream] if client_type is AioClient else list(stream)

    client = client_type("localhost:1", token="t", transport_security=TransportSecurity.PLAINTEXT,
                         retry_policy=RetryPolicy(initial_backoff=0))
    client._ensure_channel = Mock()
    read, _ = _paged_reads(1)
    calls = []

    def flaky(request, **kwargs):
        calls.append(request)
        if len(calls) == 1:
            raise _rpc_error()
        return read(request, **kwargs)

    # The client retries stream establishment; one call is exempted.
    client._permissions = Mock(ReadRelationships=flaky)
    with pytest.raises(SpiceDBError):
        await drain(client.read_relationships(
            Filter("document"), consistency=full(), call_options=CallOptions(retry_policy=Retries.DISABLED)))
    assert len(calls) == 1
    calls.clear()
    assert len(await drain(client.read_relationships(Filter("document"), consistency=full()))) == 1
    assert len(calls) == 2

    # Watch never retries by default; one call may opt in before its first event.
    watches = []

    def watch(request, **kwargs):
        watches.append(request)
        if len(watches) == 1:
            raise _rpc_error()
        return _Call([])

    client._watch = Mock(Watch=watch)
    with pytest.raises(SpiceDBError):
        await drain(client.watch())
    watches.clear()
    await drain(client.watch(call_options=CallOptions(retry_policy=RetryPolicy(initial_backoff=0))))
    assert len(watches) == 2


@pytest.mark.parametrize("client_type", [SyncClient, AioClient])
async def test_empty_write_is_sent_to_the_server(client_type):
    client = client_type("localhost:1", token="t", transport_security=TransportSecurity.PLAINTEXT)
    client._ensure_channel = Mock()
    response = psp.WriteRelationshipsResponse(written_at=core_pb2.ZedToken(token="w"))
    rpc = (AsyncMock if client_type is AioClient else Mock)(return_value=response)
    client._permissions = Mock(WriteRelationships=rpc)
    result = client.write(Transaction().must_match(Filter("document")))
    result = await result if client_type is AioClient else result
    assert result.revision == ZedToken("w")
    sent = rpc.call_args.args[0]
    assert len(sent.updates) == 0 and len(sent.optional_preconditions) == 1


@pytest.mark.parametrize("client_type", [SyncClient, AioClient])
async def test_single_request_paging(client_type):
    async def drain(stream):
        return [x async for x in stream] if client_type is AioClient else list(stream)

    client = client_type("localhost:1", token="t", transport_security=TransportSecurity.PLAINTEXT)
    client._ensure_channel = Mock()
    reads = []

    def read(request, **kwargs):
        reads.append(request)
        return _Call([
            psp.ReadRelationshipsResponse(
                relationship=Relationship.from_tuple(f"document:{i}#viewer@user:u")._to_proto(),
                after_result_cursor=core_pb2.Cursor(token=str(i + 1)),
            )
            for i in range(2)
        ])

    client._permissions = Mock(ReadRelationships=read)
    items = await drain(client.read_relationships(
        Filter("document"), consistency=full(), page_size=1, paging=Paging.SINGLE_REQUEST))
    assert [x.after_result_cursor for x in items] == ["1", "2"]
    assert len(reads) == 1, "exactly one request, though a full page would ask for more"
    assert reads[0].optional_limit == 0, "no limit means unbounded"

    lookups = []

    def lookup(request, **kwargs):
        lookups.append(request)
        return _Call([
            psp.LookupResourcesResponse(
                resource_object_id=str(i), permissionship=psp.LOOKUP_PERMISSIONSHIP_HAS_PERMISSION,
                after_result_cursor=core_pb2.Cursor(token=f"c{i}"),
            )
            for i in range(3)
        ])

    client._permissions = Mock(LookupResources=lookup)
    common = dict(resource_type="document", permission="view", subject=SubjectRef(ObjectRef("user", "u")),
                  consistency=full(), page_size=1, paging=Paging.SINGLE_REQUEST)
    items = await drain(client.lookup_resources(**common))
    assert [x.after_result_cursor for x in items] == ["c0", "c1", "c2"]
    assert len(lookups) == 1 and lookups[0].optional_limit == 0
    lookups.clear()
    await drain(client.lookup_resources(limit=7, **common))
    assert len(lookups) == 1 and lookups[0].optional_limit == 7
    with pytest.raises(InvalidArgumentError):
        client.lookup_resources(**{**common, "paging": "single"})


def test_wrapped_item_and_progress_errors_report_the_server_message_once():
    """Bulk item and delete-progress failures are not wrappers in Python: the
    item error is the server's own error (with ``input_index``) and progress is
    an attribute on the original error, so the status message appears once."""
    client = _sync()
    status = status_pb2.Status(code=grpc.StatusCode.FAILED_PRECONDITION.value[0], message="no such permission")
    check = PermissionCheck(ObjectRef("document", "a"), "view", SubjectRef(ObjectRef("user", "u")))
    request = psp.CheckBulkPermissionsRequestItem(
        resource=check.resource._to_proto(), permission="view", subject=check.subject._to_proto())
    client._permissions.CheckBulkPermissions = Mock(return_value=psp.CheckBulkPermissionsResponse(
        pairs=[psp.CheckBulkPermissionsPair(request=request, error=status)]))
    with pytest.raises(FailedPreconditionError) as caught:
        client.check_bulk([check], consistency=full())
    assert caught.value.details() == str(caught.value) == "no such permission"
    assert caught.value.input_index == 0

    client._permissions.DeleteRelationships = Mock(side_effect=[
        psp.DeleteRelationshipsResponse(relationships_deleted_count=1, deletion_progress=2,
                                        after_result_cursor=core_pb2.Cursor(token="c")),
        _rpc_error(grpc.StatusCode.UNAVAILABLE, "gone"),
    ])
    with pytest.raises(SpiceDBError) as caught:
        client.delete_all_relationships_non_atomic(Filter("document"), call_options=CallOptions(retry_policy=Retries.DISABLED))
    assert caught.value.details() == "gone"
    assert caught.value.delete_progress.deleted_count == 1


@pytest.mark.parametrize("bad", [None, 0, -1, float("inf"), "30"])
def test_default_timeout_has_no_magic_disable_value(bad):
    with pytest.raises(InvalidArgumentError):
        SyncClient("localhost:1", token="t", transport_security=TransportSecurity.PLAINTEXT, default_timeout=bad)


def test_default_deadline_disabled_sends_no_deadline():
    client = _sync(default_timeout=DefaultDeadline.DISABLED)
    client._schema.ReadSchema = Mock(return_value=ssp.ReadSchemaResponse())
    client.read_schema()
    assert client._schema.ReadSchema.call_args.kwargs["timeout"] is None
    client.read_schema(timeout=5)
    assert 0 < client._schema.ReadSchema.call_args.kwargs["timeout"] <= 5


# --- per-item bulk errors -------------------------------------------------


def test_bulk_item_errors_returned_never_grant():
    client = _sync()
    checks = [
        PermissionCheck(ObjectRef("document", str(i)), "view", SubjectRef(ObjectRef("user", "u")))
        for i in range(2)
    ]
    request = psp.CheckBulkPermissionsRequest(items=[
        psp.CheckBulkPermissionsRequestItem(resource=c.resource._to_proto(), permission="view", subject=c.subject._to_proto())
        for c in checks
    ])
    response = psp.CheckBulkPermissionsResponse(pairs=[
        psp.CheckBulkPermissionsPair(request=request.items[0], item=psp.CheckBulkPermissionsResponseItem(
            permissionship=psp.CheckPermissionResponse.PERMISSIONSHIP_HAS_PERMISSION)),
        psp.CheckBulkPermissionsPair(request=request.items[1], error=status_pb2.Status(code=5, message="missing")),
    ])
    client._permissions.CheckBulkPermissions = Mock(return_value=response)
    with pytest.raises(NotFoundError):
        client.check_bulk(checks, consistency=full())
    ok, failed = client.check_bulk(checks, consistency=full(), item_errors=BulkItemErrors.RETURN)
    assert ok.has_permission and ok.error is None
    assert isinstance(failed.error, NotFoundError) and failed.error.input_index == 1
    assert not failed and failed.permissionship is Permissionship.UNSPECIFIED


# --- deletes ----------------------------------------------------------------


def test_delete_partial_modes():
    client = _sync()
    client._permissions.DeleteRelationships = Mock(return_value=psp.DeleteRelationshipsResponse(
        deletion_progress=psp.DeleteRelationshipsResponse.DELETION_PROGRESS_COMPLETE))
    with pytest.raises(InvalidArgumentError):
        client.delete_relationships(Filter("document"))
    client.delete_relationships(Filter("document"), partial=DeletePartial.DISALLOWED)
    request = client._permissions.DeleteRelationships.call_args.args[0]
    assert (request.optional_limit, request.optional_allow_partial_deletions) == (0, False)
    client.delete_relationships(Filter("document"), limit=7, partial=DeletePartial.DISALLOWED)
    request = client._permissions.DeleteRelationships.call_args.args[0]
    assert (request.optional_limit, request.optional_allow_partial_deletions) == (7, False)
    client.delete_relationships(Filter("document"), limit=7)
    assert client._permissions.DeleteRelationships.call_args.args[0].optional_allow_partial_deletions


async def test_delete_all_reports_progress_per_batch():
    responses = [
        psp.DeleteRelationshipsResponse(deleted_at=core_pb2.ZedToken(token="r1"), relationships_deleted_count=2,
            deletion_progress=psp.DeleteRelationshipsResponse.DELETION_PROGRESS_PARTIAL,
            after_result_cursor=core_pb2.Cursor(token="c1")),
        psp.DeleteRelationshipsResponse(deleted_at=core_pb2.ZedToken(token="r2"), relationships_deleted_count=1,
            deletion_progress=psp.DeleteRelationshipsResponse.DELETION_PROGRESS_COMPLETE),
    ]
    sync = _sync()
    sync._permissions.DeleteRelationships = Mock(side_effect=list(responses))
    seen = []
    sync.delete_all_relationships_non_atomic(Filter("document"), page_size=2, on_progress=seen.append)
    assert [(p.deleted_count, str(p.revision), p.cursor) for p in seen] == [(2, "r1", "c1"), (3, "r2", None)]

    aio = AioClient("localhost:1", token="t", transport_security=TransportSecurity.PLAINTEXT)
    aio._ensure_channel()
    aio._permissions.DeleteRelationships = AsyncMock(side_effect=list(responses))
    async_seen = []

    async def record(progress):
        async_seen.append(progress.deleted_count)

    await aio.delete_all_relationships_non_atomic(Filter("document"), page_size=2, on_progress=record)
    assert async_seen == [2, 3]
    await aio.close()


# --- import batch size and call options --------------------------------


def test_import_batch_size_and_call_options_are_forwarded():
    client = _sync()
    sent = []

    def upload(requests, **kwargs):
        sent.extend(len(r.relationships) for r in requests)
        upload.kwargs = kwargs
        return psp.ImportBulkRelationshipsResponse(num_loaded=sum(sent))

    client._permissions.ImportBulkRelationships = upload
    rels = [Relationship.from_tuple(f"document:{i}#viewer@user:u") for i in range(5)]
    options = CallOptions(metadata=(("x-test", "1"),), wait_for_ready=True, compression=grpc.Compression.Gzip)
    assert client.import_relationships(rels, batch_size=2, call_options=options) == 5
    assert sent == [2, 2, 1]
    assert ("x-test", "1") in upload.kwargs["metadata"] and ("authorization", "Bearer t") in upload.kwargs["metadata"]
    assert upload.kwargs["wait_for_ready"] is True and upload.kwargs["compression"] is grpc.Compression.Gzip


def test_call_options_cannot_replace_the_client_token():
    client = _sync()
    with pytest.raises(InvalidArgumentError):
        client.read_schema(call_options=CallOptions(metadata=(("authorization", "Bearer x"),)))


# --- against a real in-process server ---------------------------------


class _Server:
    """Serves ReadSchema (with headers/trailers) and an import that aborts."""

    def __init__(self) -> None:
        self.imported_messages = 0
        self.authorization: list[str] = []

    def read_schema(self, request, context):
        self.authorization.extend(v for k, v in context.invocation_metadata() if k == "authorization")
        context.send_initial_metadata((("x-server-version", "v1.99.0"),))
        context.set_trailing_metadata((("x-trailer", "t"),))
        return ssp.ReadSchemaResponse(schema_text="definition user {}")

    def import_bulk(self, request_iterator, context):
        for _ in request_iterator:
            self.imported_messages += 1
            context.abort(grpc.StatusCode.ALREADY_EXISTS, "could not CREATE relationship, as it already existed")
        return psp.ImportBulkRelationshipsResponse()


@pytest.fixture
def server():
    state = _Server()
    grpc_server = grpc.server(futures.ThreadPoolExecutor(max_workers=4))
    grpc_server.add_generic_rpc_handlers((
        grpc.method_handlers_generic_handler("authzed.api.v1.SchemaService", {
            "ReadSchema": grpc.unary_unary_rpc_method_handler(
                state.read_schema, ssp.ReadSchemaRequest.FromString, ssp.ReadSchemaResponse.SerializeToString),
        }),
        grpc.method_handlers_generic_handler("authzed.api.v1.PermissionsService", {
            "ImportBulkRelationships": grpc.stream_unary_rpc_method_handler(
                state.import_bulk, psp.ImportBulkRelationshipsRequest.FromString,
                psp.ImportBulkRelationshipsResponse.SerializeToString),
        }),
    ))
    state.port = grpc_server.add_insecure_port("localhost:0")
    grpc_server.start()
    yield state
    grpc_server.stop(0)


def _many(n=5000):
    for i in range(n):
        yield Relationship.from_tuple(f"document:{i}#viewer@user:u")


def test_sync_import_reports_server_abort_status(server):
    with SyncClient(f"localhost:{server.port}", token="t", transport_security=TransportSecurity.PLAINTEXT) as client:
        with pytest.raises(AlreadyExistsError) as caught:
            client.import_relationships(_many(), batch_size=1)
    assert caught.value.code() is grpc.StatusCode.ALREADY_EXISTS
    assert "already existed" in caught.value.server_message


async def test_aio_import_reports_server_abort_status(server):
    async with AioClient(f"localhost:{server.port}", token="t", transport_security=TransportSecurity.PLAINTEXT) as client:
        with pytest.raises(AlreadyExistsError):
            await client.import_relationships(_many(), batch_size=1)


def test_sync_from_channel_keeps_options_and_ownership(server):
    channel = grpc.insecure_channel(f"localhost:{server.port}")
    try:
        client = SyncClient.from_channel(channel, retry_policy=Retries.DISABLED)
        captured = ResponseMetadata()
        assert client.read_schema(call_options=CallOptions(
            metadata=(("authorization", "Bearer from-caller"),), response_metadata=captured,
        )).schema == "definition user {}"
        assert ("x-server-version", "v1.99.0") in captured.headers
        assert ("x-trailer", "t") in captured.trailers
        assert server.authorization == ["Bearer from-caller"]
        client.close()
        # The caller's channel is still open after close().
        grpc.channel_ready_future(channel).result(timeout=5)
        assert SyncClient.from_channel(channel).read_schema().schema
    finally:
        channel.close()


async def test_aio_from_channel_keeps_options_and_ownership(server):
    channel = grpc.aio.insecure_channel(f"localhost:{server.port}")
    try:
        client = AioClient.from_channel(channel)
        captured = ResponseMetadata()
        await client.read_schema(call_options=CallOptions(response_metadata=captured))
        assert ("x-server-version", "v1.99.0") in captured.headers
        assert ("x-trailer", "t") in captured.trailers
        await client.close()
        assert (await AioClient.from_channel(channel).read_schema()).schema
        assert server.authorization == []
    finally:
        await channel.close()


def test_from_channel_rejects_the_wrong_channel_flavor():
    with pytest.raises(InvalidArgumentError):
        SyncClient.from_channel(grpc.aio.insecure_channel("localhost:1"))
    with pytest.raises(InvalidArgumentError):
        AioClient.from_channel(grpc.insecure_channel("localhost:1"))


def test_unary_response_metadata_captured_on_failure():
    client = _sync()
    error = grpc.aio.AioRpcError(
        grpc.StatusCode.NOT_FOUND, grpc.aio.Metadata(("h", "1")), grpc.aio.Metadata(("t", "2")), details="x"
    )
    def read_schema(*_args, **_kwargs):  # a plain callable: no with_call
        raise error

    client._schema.ReadSchema = read_schema
    captured = ResponseMetadata()
    with pytest.raises(NotFoundError):
        client.read_schema(call_options=CallOptions(response_metadata=captured))
    assert captured.headers == (("h", "1"),) and captured.trailers == (("t", "2"),)
