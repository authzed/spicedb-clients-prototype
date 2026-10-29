"""Acceptance tests for the explicitly breaking native API reset."""
from authzed.api.v1 import core_pb2 as _core_tok
from spicedb import TransportSecurity
from spicedb import InternalError, ZedToken, Tracing
import grpc

import pytest
from unittest.mock import AsyncMock
from spicedb import Permissionship
import inspect
from unittest.mock import Mock
from authzed.api.v1 import permission_service_pb2 as p, core_pb2 as c, debug_pb2 as d
from spicedb.types import (
    ObjectRef,
    SubjectRef,
    PermissionCheck,
    Caveat,
    Relationship,
    Filter,
    SubjectFilter,
    SubjectRelationFilter,
)
from spicedb import full
from spicedb.sync import SpiceDBClient
from spicedb.aio import SpiceDBClient as AsyncClient

CHECK = PermissionCheck(
    ObjectRef("document", "one"), "view", SubjectRef(ObjectRef("user", "alice"))
)


def test_native_check_and_tracing():
    client = SpiceDBClient("localhost:1", token="x", transport_security=TransportSecurity.PLAINTEXT)
    client._ensure_channel = Mock()
    response = p.CheckPermissionResponse(
        checked_at=c.ZedToken(token="revision"),
        permissionship=2,
        debug_trace=d.DebugInformation(
            schema_used="schema",
            check=d.CheckDebugTrace(
                resource=c.ObjectReference(object_type="document", object_id="one"),
                permission="view",
                result=2,
                was_cached_result=False,
            ),
        ),
    )
    client._permissions = Mock(CheckPermission=Mock(return_value=response))
    result = client.check(CHECK, consistency=full(), tracing=Tracing.ENABLED)
    request = client._permissions.CheckPermission.call_args.args[0]
    assert request.with_tracing
    assert result.has_permission and result.checked_at == ZedToken("revision")
    assert result.debug_trace.schema_used == "schema"
    assert result.debug_trace.check.was_cached_result is False
    assert result.debug_trace.check.sub_problems is None
    assert not hasattr(client, "check_permission")


def test_context_snapshot_and_exact_filter():
    ctx = {"nested": {"values": [None, True, 3]}}
    check = PermissionCheck(CHECK.resource, "view", CHECK.subject, ctx)
    caveat = Caveat("test", ctx)
    ctx["nested"]["values"].append("changed")
    assert check.context["nested"]["values"] == (None, True, 3)
    rel = Relationship(CHECK.resource, "viewer", CHECK.subject, caveat)
    filt = rel.to_filter()._to_proto()
    assert filt.optional_subject_filter.HasField("optional_relation")
    assert filt.optional_subject_filter.optional_relation.relation == ""
    assert (
        not Filter("document", subject=SubjectFilter("user"))
        ._to_proto()
        .optional_subject_filter.HasField("optional_relation")
    )
    assert (
        Filter(
            "document",
            subject=SubjectFilter(
                "group", relation=SubjectRelationFilter.named("member")
            ),
        )
        ._to_proto()
        .optional_subject_filter.optional_relation.relation
        == "member"
    )


def test_stream_is_context_managed():
    client = SpiceDBClient("localhost:1", token="x", transport_security=TransportSecurity.PLAINTEXT)
    stream = client.read_relationships(Filter("document"), consistency=full())
    with stream:
        pass
    assert list(stream) == []


def test_async_stream_methods_are_immediate():
    for name in (
        "read_relationships",
        "lookup_resources",
        "lookup_subjects",
        "watch",
        "export_relationships",
    ):
        assert not inspect.iscoroutinefunction(getattr(AsyncClient, name))


def test_recursive_trace_native_values_and_absence():
    from datetime import timedelta
    from google.protobuf import duration_pb2, timestamp_pb2, struct_pb2
    from spicedb._mapping import check_result, check_results
    from spicedb import CaveatResult, PermissionType

    context = struct_pb2.Struct()
    context.update({"nested": {"array": [None, True, 2]}})
    expiration = timestamp_pb2.Timestamp(seconds=42)
    child = d.CheckDebugTrace(
        permission="child", result=99, permission_type=99, was_cached_result=False
    )
    trace = d.CheckDebugTrace(
        resource=c.ObjectReference(object_type="document", object_id="one"),
        permission="view",
        permission_type=2,
        subject=CHECK.subject._to_proto(),
        result=2,
        caveat_evaluation_info=d.CaveatEvalInfo(
            expression="test",
            result=4,
            context=context,
            partial_caveat_info=c.PartialCaveatInfo(
                missing_required_context=["region"]
            ),
            caveat_name="test",
        ),
        duration=duration_pb2.Duration(seconds=1, nanos=2000),
        sub_problems=d.CheckDebugTrace.SubProblems(traces=[child]),
        optional_expires_at=expiration,
        trace_operation_id="operation",
        source="server",
    )
    response = p.CheckPermissionResponse(checked_at=_core_tok.ZedToken(token="t"),
        permissionship=2,
        debug_trace=d.DebugInformation(schema_used="schema", check=trace),
    )
    result = check_result(response)
    native = result.debug_trace.check
    assert native.permission_type is PermissionType.PERMISSION
    assert native.duration == timedelta(seconds=1, microseconds=2)
    assert native.expiration.timestamp() == 42
    assert native.was_cached_result is None
    assert native.trace_operation_id == "operation" and native.source == "server"
    assert native.caveat_evaluation.result is CaveatResult.MISSING_SOME_CONTEXT
    assert native.caveat_evaluation.context["nested"]["array"] == (None, True, 2)
    assert native.caveat_evaluation.missing_context == ("region",)
    assert native.sub_problems[0].result is Permissionship.UNSPECIFIED
    assert native.sub_problems[0].permission_type is PermissionType.UNSPECIFIED
    assert native.sub_problems[0].was_cached_result is False
    assert check_result(p.CheckPermissionResponse(checked_at=_core_tok.ZedToken(token="t"))).debug_trace is None
    bulk = p.CheckBulkPermissionsResponse(
        checked_at=c.ZedToken(token="chunk"),
        pairs=[
            p.CheckBulkPermissionsPair(
                item=p.CheckBulkPermissionsResponseItem(
                    permissionship=2, debug_trace=response.debug_trace
                )
            ),
            p.CheckBulkPermissionsPair(
                item=p.CheckBulkPermissionsResponseItem(permissionship=1)
            ),
        ],
    )
    from spicedb._requests import check_bulk_request

    expected = check_bulk_request(full(), [CHECK, CHECK]).items
    for pair, item in zip(bulk.pairs, expected):
        pair.request.CopyFrom(item)
    results = check_results(bulk, expected)
    assert results[0].debug_trace == result.debug_trace
    assert results[1].debug_trace is None


def test_bulk_tracing_chunks_and_revisions():
    client = SpiceDBClient("localhost:1", token="x", transport_security=TransportSecurity.PLAINTEXT)
    client._ensure_channel = Mock()

    def response(request, **kwargs):
        token = "a" if len(request.items) == 1000 else "b"
        return p.CheckBulkPermissionsResponse(
            checked_at=c.ZedToken(token=token),
            pairs=[
                p.CheckBulkPermissionsPair(
                    item=p.CheckBulkPermissionsResponseItem(permissionship=2),
                    request=item,
                )
                for item in request.items
            ],
        )

    client._permissions = Mock(CheckBulkPermissions=Mock(side_effect=response))
    results = client.check_bulk([CHECK] * 1001, consistency=full(), tracing=Tracing.ENABLED)
    assert len(results) == 1001
    assert results[0].checked_at == ZedToken("a") and results[-1].checked_at == ZedToken("b")
    assert all(
        (
            call.args[0].with_tracing
            for call in client._permissions.CheckBulkPermissions.call_args_list
        )
    )


def test_one_budget_across_retries_and_chunks(monkeypatch):
    from types import SimpleNamespace
    from spicedb.errors import DeadlineExceededError
    import grpc

    now = [0.0]
    monkeypatch.setattr(
        "spicedb._budget.time", SimpleNamespace(monotonic=lambda: now[0])
    )
    monkeypatch.setattr(
        "spicedb.sync.client.time.sleep",
        lambda delay: now.__setitem__(0, now[0] + delay),
    )

    class Retry(grpc.RpcError):
        def code(self):
            return grpc.StatusCode.UNAVAILABLE

        def details(self):
            return "retry"

    client = SpiceDBClient("localhost:1", token="x", transport_security=TransportSecurity.PLAINTEXT)
    client._ensure_channel = Mock()
    client._backoff_seconds = lambda *_: 0.1
    timeouts = []

    def attempt(request, **kwargs):
        timeouts.append(kwargs["timeout"])
        now[0] += 0.2
        if len(timeouts) == 1:
            raise Retry()
        return p.CheckPermissionResponse(checked_at=_core_tok.ZedToken(token="t"), permissionship=2)

    client._permissions = Mock(CheckPermission=Mock(side_effect=attempt))
    client.check(CHECK, consistency=full(), timeout=0.6)
    assert timeouts == pytest.approx([0.6, 0.3])
    now[0] = 0

    def bulk(request, **kwargs):
        now[0] += 0.6
        return p.CheckBulkPermissionsResponse(checked_at=_core_tok.ZedToken(token="t"),
            pairs=[
                p.CheckBulkPermissionsPair(
                    item=p.CheckBulkPermissionsResponseItem(permissionship=2),
                    request=item,
                )
                for item in request.items
            ]
        )

    client._permissions.CheckBulkPermissions = Mock(side_effect=bulk)
    with pytest.raises(DeadlineExceededError):
        client.check_bulk([CHECK] * 1001, consistency=full(), timeout=0.5)
    assert client._permissions.CheckBulkPermissions.call_count == 1


def test_delete_all_progress_and_budget(monkeypatch):
    from types import SimpleNamespace
    from spicedb import DeleteProgress, DeadlineExceededError

    now = [0.0]
    monkeypatch.setattr(
        "spicedb._budget.time", SimpleNamespace(monotonic=lambda: now[0])
    )
    client = SpiceDBClient("localhost:1", token="x", transport_security=TransportSecurity.PLAINTEXT)
    client._ensure_channel = Mock()

    def response(request, **kwargs):
        now[0] += 0.6
        return p.DeleteRelationshipsResponse(
            deleted_at=c.ZedToken(token="acknowledged"),
            relationships_deleted_count=3,
            deletion_progress=2,
            after_result_cursor=c.Cursor(token="cursor"),
        )

    client._permissions = Mock(DeleteRelationships=Mock(side_effect=response))
    with pytest.raises(DeadlineExceededError) as caught:
        client.delete_all_relationships_non_atomic(Filter("document"), page_size=3, timeout=0.5)
    assert caught.value.delete_progress.deleted_count == 3
    assert caught.value.delete_progress.revision == ZedToken("acknowledged")
    assert caught.value.delete_progress.cursor == "cursor"
    assert caught.value.delete_progress.progress is DeleteProgress.PARTIAL
    assert client._permissions.DeleteRelationships.call_count == 1


async def test_native_cancellation_retains_delete_progress():
    import asyncio

    client = AsyncClient("localhost:1", token="x", transport_security=TransportSecurity.PLAINTEXT)
    client._ensure_channel = Mock()
    client._permissions = Mock(
        DeleteRelationships=AsyncMock(
            side_effect=[
                p.DeleteRelationshipsResponse(
                    deleted_at=c.ZedToken(token="acknowledged"),
                    relationships_deleted_count=2,
                    deletion_progress=2,
                ),
                asyncio.CancelledError(),
            ]
        )
    )
    with pytest.raises(asyncio.CancelledError) as caught:
        await client.delete_all_relationships_non_atomic(Filter("document"), page_size=2)
    assert caught.value.delete_progress.deleted_count == 2
    assert caught.value.delete_progress.revision == ZedToken("acknowledged")


async def test_native_cancellation_interrupts_backoff():
    import asyncio
    import grpc
    from types import SimpleNamespace

    class Retry(grpc.RpcError):
        def code(self):
            return grpc.StatusCode.UNAVAILABLE

        def details(self):
            return "retry"

    client = AsyncClient("localhost:1", token="x", transport_security=TransportSecurity.PLAINTEXT)
    client._ensure_channel = Mock()
    client._backoff_seconds = lambda *_: 10
    rpc = AsyncMock(side_effect=Retry())
    client._permissions = SimpleNamespace(CheckPermission=rpc)
    pending = asyncio.create_task(client.check(CHECK, consistency=full()))
    await asyncio.sleep(0.01)
    pending.cancel()
    with pytest.raises(asyncio.CancelledError):
        await pending
    assert rpc.call_count == 1


@pytest.mark.parametrize("value", [float("nan"), float("inf"), object(), {1: "value"}])
def test_invalid_nested_json_paths(value):
    from spicedb import InvalidArgumentError

    with pytest.raises(InvalidArgumentError, match="check.context.nested\\[0\\]"):
        PermissionCheck(CHECK.resource, "view", CHECK.subject, {"nested": [value]})


def test_cyclic_json_is_typed_and_shared_values_are_valid():
    from spicedb import InvalidArgumentError

    data = {}
    data["cycle"] = data
    with pytest.raises(InvalidArgumentError, match="check.context.cycle"):
        PermissionCheck(CHECK.resource, "view", CHECK.subject, data)
    shared = [None, True, {"value": [2]}]
    check = PermissionCheck(
        CHECK.resource, "view", CHECK.subject, {"a": shared, "b": shared}
    )
    shared[2]["value"].append(3)
    assert check.context["a"][2]["value"] == (2,)
    assert check.context["b"][2]["value"] == (2,)


@pytest.fixture
def import_server():
    import grpc
    from concurrent.futures import ThreadPoolExecutor
    from authzed.api.v1 import permission_service_pb2_grpc as stubs

    class Importer(stubs.PermissionsServiceServicer):
        def ImportBulkRelationships(self, requests, context):
            count = 0
            for request in requests:
                count += len(request.relationships)
            return p.ImportBulkRelationshipsResponse(num_loaded=count)

    executor = ThreadPoolExecutor(max_workers=2)
    server = grpc.server(executor)
    stubs.add_PermissionsServiceServicer_to_server(Importer(), server)
    port = server.add_insecure_port("127.0.0.1:0")
    server.start()
    yield f"127.0.0.1:{port}"
    server.stop(0).wait()
    executor.shutdown(wait=True)


def test_sync_import_preserves_producer_error(import_server):

    class ProducerFailure(Exception):
        pass

    failure = ProducerFailure("producer failed")

    def source():
        for _ in range(1000):
            yield Relationship(CHECK.resource, "viewer", CHECK.subject)
        raise failure

    with SpiceDBClient(import_server, token="x", transport_security=TransportSecurity.PLAINTEXT) as client:
        with pytest.raises(ProducerFailure) as caught:
            client.import_relationships(source())
    assert caught.value is failure


async def test_async_import_preserves_producer_error(import_server):

    class ProducerFailure(Exception):
        pass

    failure = ProducerFailure("producer failed")

    async def source():
        for _ in range(1000):
            yield Relationship(CHECK.resource, "viewer", CHECK.subject)
        raise failure

    async with AsyncClient(import_server, token="x", transport_security=TransportSecurity.PLAINTEXT) as client:
        with pytest.raises(ProducerFailure) as caught:
            await client.import_relationships(source())
    assert caught.value is failure


@pytest.mark.parametrize("asynchronous", [False, True])
async def test_experimental_rpc_coverage_and_exact_counter_filter(asynchronous):
    import inspect
    from types import SimpleNamespace
    from authzed.api.v1 import experimental_service_pb2 as e
    from authzed.api.materialize.v0 import roaringlookupresources_pb2 as roaring

    factory = AsyncMock if asynchronous else Mock
    client = (AsyncClient if asynchronous else SpiceDBClient)(
        "localhost:1", token="secret", transport_security=TransportSecurity.PLAINTEXT
    )
    client._ensure_channel = Mock()
    register = factory(return_value=e.ExperimentalRegisterRelationshipCounterResponse())
    count = factory(
        return_value=e.ExperimentalCountRelationshipsResponse(
            read_counter_value=e.ReadCounterValue(
                relationship_count=17, read_at=c.ZedToken(token="count-revision")
            )
        )
    )
    unregister = factory(
        return_value=e.ExperimentalUnregisterRelationshipCounterResponse()
    )
    materialize = factory(
        return_value=roaring.ExperimentalRoaringLookupResourcesResponse(
            bitmap=b"\x00\xff",
            cardinality=5,
            at_revision=c.ZedToken(token="bitmap-revision"),
        )
    )
    client._experimental = SimpleNamespace(
        ExperimentalRegisterRelationshipCounter=register,
        ExperimentalCountRelationships=count,
        ExperimentalUnregisterRelationshipCounter=unregister,
    )
    client._materialize = SimpleNamespace(
        ExperimentalRoaringLookupResources=materialize
    )

    async def result(value):
        return await value if inspect.isawaitable(value) else value

    exact = Relationship(CHECK.resource, "viewer", CHECK.subject).to_filter()
    await result(client.experimental_register_relationship_counter("counter", exact))
    assert register.call_args.args[
        0
    ].relationship_filter.optional_subject_filter.HasField("optional_relation")
    counter = await result(client.experimental_count_relationships("counter"))
    assert counter.count == 17 and counter.revision == ZedToken("count-revision")
    await result(client.experimental_unregister_relationship_counter("counter"))
    bitmap = await result(
        client.experimental_roaring_lookup_resources(
            resource_type="document",
            permission="view",
            subject=CHECK.subject,
            consistency=full(),
        )
    )
    assert (
        bitmap.bitmap == b"\x00\xff"
        and bitmap.cardinality == 5
        and (bitmap.revision == ZedToken("bitmap-revision"))
    )
    for rpc in (register, count, unregister, materialize):
        assert tuple(rpc.call_args.kwargs["metadata"]) == (
            ("authorization", "Bearer secret"),
        )
        assert 0 < rpc.call_args.kwargs["timeout"] <= 30


def test_materialize_surface_is_complete():
    for cls in (SpiceDBClient, AsyncClient):
        for name in (
            "experimental_count_relationships_by_filter",
            "watch_permission_sets",
            "lookup_permission_sets",
            "download_permission_sets",
        ):
            assert hasattr(cls, name)


@pytest.mark.parametrize("asynchronous", [False, True])
async def test_materialize_native_requests_results_and_lifecycle(asynchronous):
    import inspect
    from datetime import datetime, timezone
    from types import SimpleNamespace
    from google.protobuf.timestamp_pb2 import Timestamp
    from authzed.api.materialize.v0 import (
        relationships_pb2 as mr,
        watchpermissionsets_pb2 as sets,
        watchpermissions_pb2 as watched,
    )
    from spicedb import (
        PermissionSetCursor,
        PermissionSetOperation,
        PermissionSetMember,
        PermissionSetRef,
        PermissionSetCheckpoint,
        PermissionSetsLookupRequired,
        PermissionSetSchemaChange,
        UnknownPermissionSetEvent,
    )

    client = (AsyncClient if asynchronous else SpiceDBClient)(
        "localhost:1", token="secret", transport_security=TransportSecurity.PLAINTEXT
    )
    client._ensure_channel = Mock()
    factory = AsyncMock if asynchronous else Mock
    count = factory(
        return_value=mr.ExperimentalCountRelationshipsByFilterResponse(
            relationship_count=9, read_at=c.ZedToken(token="count")
        )
    )
    download = factory(
        return_value=sets.DownloadPermissionSetsResponse(
            files=[sets.File(name="data.avro", url="https://example.test/data")],
            timestamp=Timestamp(seconds=42),
            at_revision=c.ZedToken(token="files"),
        )
    )
    change = sets.PermissionSetChange(
        at_revision=c.ZedToken(token="revision"),
        operation=1,
        parent_set=sets.SetReference(
            object_type="document", object_id="one", permission_or_relation="view"
        ),
        child_member=sets.MemberReference(object_type="user", object_id="alice"),
    )
    cursor = PermissionSetCursor(2, ZedToken("cursor-revision"), 7, True, "key", "opaque")
    lookup_response = sets.LookupPermissionSetsResponse(
        change=change, cursor=cursor._to_proto()
    )
    events = [
        sets.WatchPermissionSetsResponse(change=change),
        sets.WatchPermissionSetsResponse(
            completed_revision=c.ZedToken(token="complete")
        ),
        sets.WatchPermissionSetsResponse(
            lookup_permission_sets_required=sets.LookupPermissionSetsRequired(
                required_lookup_at=c.ZedToken(token="lookup")
            )
        ),
        sets.WatchPermissionSetsResponse(
            breaking_schema_change=sets.BreakingSchemaChange(
                change_at=c.ZedToken(token="schema"),
                affected_permissions=[
                    watched.WatchedPermission(
                        resource_type="document",
                        permission="view",
                        subject_type="group",
                        optional_subject_relation="member",
                    )
                ],
            )
        ),
        sets.WatchPermissionSetsResponse(),
    ]

    class Call:
        def __init__(self, responses):
            self.responses = responses
            self.cancelled = False

        def __iter__(self):
            return iter(self.responses)

        def __aiter__(self):

            async def items():
                for item in self.responses:
                    yield item

            return items()

        def cancel(self):
            self.cancelled = True

    lookup_call = Call([lookup_response] * 3)
    watch_call = Call(events)
    lookup = Mock(return_value=lookup_call)
    watch = Mock(return_value=watch_call)
    client._materialize_relationships = SimpleNamespace(
        ExperimentalCountRelationshipsByFilter=count
    )
    client._permission_sets = SimpleNamespace(
        DownloadPermissionSets=download,
        LookupPermissionSets=lookup,
        WatchPermissionSets=watch,
    )

    async def result(value):
        return await value if inspect.isawaitable(value) else value

    counted = await result(
        client.experimental_count_relationships_by_filter(
            Relationship(CHECK.resource, "viewer", CHECK.subject).to_filter()
        )
    )
    assert counted.count == 9 and counted.revision == ZedToken("count")
    assert count.call_args.args[0].relationship_filter.optional_subject_filter.HasField(
        "optional_relation"
    )
    files = await result(client.download_permission_sets(at_revision=ZedToken("requested")))
    assert files.revision == ZedToken("files") and files.timestamp == datetime.fromtimestamp(
        42, timezone.utc
    )
    assert (
        files.files[0].name == "data.avro"
        and files.files[0].url == "https://example.test/data"
    )
    assert download.call_args.args[0].optional_at_revision.token == "requested"
    stream = client.lookup_permission_sets(
        limit=2, at_revision=ZedToken("requested"), cursor=cursor, timeout=5
    )
    if asynchronous:
        async with stream:
            looked_up = [item async for item in stream]
    else:
        with stream:
            looked_up = list(stream)
    assert len(looked_up) == 2 and lookup_call.cancelled
    assert looked_up[0].cursor == cursor
    assert looked_up[0].change.operation is PermissionSetOperation.ADDED
    assert isinstance(looked_up[0].change.parent, PermissionSetRef)
    assert isinstance(looked_up[0].change.child, PermissionSetMember)
    request = lookup.call_args.args[0]
    assert request.limit == 2 and request.optional_at_revision.token == "requested"
    assert request.optional_starting_after_cursor == cursor._to_proto()
    stream = client.watch_permission_sets(start_revision=ZedToken("after"), timeout=5)
    if asynchronous:
        async with stream:
            received = [item async for item in stream]
    else:
        with stream:
            received = list(stream)
    assert watch.call_args.args[0].optional_starting_after.token == "after"
    assert watch_call.cancelled and len(received) == 5
    assert (
        isinstance(received[1], PermissionSetCheckpoint)
        and received[1].revision == ZedToken("complete")
    )
    assert (
        isinstance(received[2], PermissionSetsLookupRequired)
        and received[2].revision == ZedToken("lookup")
    )
    assert (
        isinstance(received[3], PermissionSetSchemaChange)
        and received[3].affected_permissions[0].subject_relation == "member"
    )
    assert isinstance(received[4], UnknownPermissionSetEvent)
    for rpc in (count, download, lookup, watch):
        assert tuple(rpc.call_args.kwargs["metadata"]) == (
            ("authorization", "Bearer secret"),
        )


@pytest.mark.parametrize("asynchronous", [False, True])
async def test_lookup_subjects_omits_unsupported_concrete_limit_by_default(
    asynchronous,
):
    from types import SimpleNamespace

    client = (AsyncClient if asynchronous else SpiceDBClient)(
        "localhost:1", token="x", transport_security=TransportSecurity.PLAINTEXT
    )
    client._ensure_channel = Mock()

    class Call:
        def __iter__(self):
            for name in ("alice", "bob", "carol"):
                yield p.LookupSubjectsResponse(looked_up_at=_core_tok.ZedToken(token="t"),
                    subject=p.ResolvedSubject(subject_object_id=name, permissionship=1)
                )

        def __aiter__(self):

            async def items():
                for item in self:
                    yield item

            return items()

        def cancel(self):
            pass

    rpc = Mock(return_value=Call())
    client._permissions = SimpleNamespace(LookupSubjects=rpc)
    stream = client.lookup_subjects(
        resource=CHECK.resource,
        permission="view",
        subject_type="user",
        consistency=full(),
        limit=2,
    )
    if asynchronous:
        async with stream:
            results = [item async for item in stream]
    else:
        with stream:
            results = list(stream)
    assert len(results) == 2
    assert rpc.call_args.args[0].optional_concrete_limit == 0
    assert rpc.call_count == 1


async def test_async_import_deadline_cancels_blocked_producer(import_server):
    import asyncio
    from spicedb import DeadlineExceededError

    closed = asyncio.Event()

    async def source():
        try:
            await asyncio.Event().wait()
            yield Relationship(CHECK.resource, "viewer", CHECK.subject)
        finally:
            closed.set()

    async with AsyncClient(import_server, token="x", transport_security=TransportSecurity.PLAINTEXT) as client:
        with pytest.raises(DeadlineExceededError):
            await asyncio.wait_for(
                client.import_relationships(source(), timeout=0.03), 0.5
            )
    assert closed.is_set()


@pytest.mark.parametrize("asynchronous", [False, True])
@pytest.mark.parametrize(
    "corruption", ["missing", "reorder", "duplicate", "context", "context_presence"]
)
async def test_bulk_rejects_unmatched_echoed_requests(asynchronous, corruption):
    import inspect
    from types import SimpleNamespace
    from spicedb import SpiceDBError

    client = (AsyncClient if asynchronous else SpiceDBClient)(
        "localhost:1", token="x", transport_security=TransportSecurity.PLAINTEXT
    )
    client._ensure_channel = Mock()
    checks = [
        CHECK,
        PermissionCheck(
            ObjectRef("document", "two"),
            "view",
            CHECK.subject,
            {"nested": [None, True]},
        ),
    ]

    def respond(request, **kwargs):
        response = p.CheckBulkPermissionsResponse(checked_at=_core_tok.ZedToken(token="t"),
            pairs=[
                p.CheckBulkPermissionsPair(
                    request=item,
                    item=p.CheckBulkPermissionsResponseItem(permissionship=2),
                )
                for item in request.items
            ]
        )
        if corruption == "missing":
            response.pairs[1].ClearField("request")
        elif corruption == "reorder":
            response.pairs[0].request.CopyFrom(request.items[1])
            response.pairs[1].request.CopyFrom(request.items[0])
        elif corruption == "duplicate":
            response.pairs[1].request.CopyFrom(request.items[0])
        elif corruption == "context":
            response.pairs[1].request.context.update({"nested": [None, False]})
        elif corruption == "context_presence":
            response.pairs[0].request.context.SetInParent()
        return response

    client._permissions = SimpleNamespace(
        CheckBulkPermissions=(AsyncMock if asynchronous else Mock)(side_effect=respond)
    )
    with pytest.raises(SpiceDBError, match="request") as caught:
        value = client.check_bulk(checks, consistency=full())
        if inspect.isawaitable(value):
            await value
    assert caught.value.input_index == (
        0 if corruption in ("reorder", "context_presence") else 1
    )


def test_bulk_matched_error_and_bad_echo_keep_absolute_index():
    from google.rpc import status_pb2
    from spicedb import InvalidArgumentError, SpiceDBError
    from spicedb._requests import check_bulk_request
    from spicedb._mapping import check_results

    expected = check_bulk_request(
        full(),
        [CHECK, PermissionCheck(ObjectRef("document", "two"), "view", CHECK.subject)],
    ).items
    response = p.CheckBulkPermissionsResponse(checked_at=_core_tok.ZedToken(token="t"),
        pairs=[
            p.CheckBulkPermissionsPair(
                request=expected[0],
                item=p.CheckBulkPermissionsResponseItem(permissionship=2),
            ),
            p.CheckBulkPermissionsPair(
                request=expected[1], error=status_pb2.Status(code=3, message="bad item")
            ),
        ]
    )
    with pytest.raises(InvalidArgumentError, match="bad item") as caught:
        check_results(response, expected, offset=1000)
    assert caught.value.input_index == 1001
    response.pairs[1].request.CopyFrom(expected[0])
    with pytest.raises(SpiceDBError, match="check item 1001.*request") as caught:
        check_results(response, expected, offset=1000)
    assert type(caught.value) is InternalError
    assert caught.value.code() is grpc.StatusCode.INTERNAL
    assert caught.value.input_index == 1001


async def test_async_import_remote_rejection_cancels_blocked_producer():
    import asyncio
    import grpc
    from authzed.api.v1 import permission_service_pb2_grpc as stubs
    from spicedb import PermissionDeniedError

    started = asyncio.Event()
    closed = asyncio.Event()

    class RejectingImporter(stubs.PermissionsServiceServicer):
        async def ImportBulkRelationships(self, requests, context):
            await anext(requests)
            await started.wait()
            await context.abort(grpc.StatusCode.PERMISSION_DENIED, "upload rejected")

    server = grpc.aio.server()
    stubs.add_PermissionsServiceServicer_to_server(RejectingImporter(), server)
    port = server.add_insecure_port("127.0.0.1:0")
    await server.start()

    async def source():
        try:
            for _ in range(1000):
                yield Relationship(CHECK.resource, "viewer", CHECK.subject)
            started.set()
            await asyncio.Event().wait()
        finally:
            closed.set()

    try:
        async with AsyncClient(f"127.0.0.1:{port}", token="x", transport_security=TransportSecurity.PLAINTEXT) as client:
            with pytest.raises(
                PermissionDeniedError, match="upload rejected"
            ) as caught:
                await asyncio.wait_for(client.import_relationships(source()), timeout=2)
            assert isinstance(caught.value.__cause__, grpc.aio.AioRpcError)
            assert caught.value.__cause__.code() is grpc.StatusCode.PERMISSION_DENIED
            assert closed.is_set()
    finally:
        await server.stop(None)


@pytest.mark.parametrize("client_type", [SpiceDBClient, AsyncClient])
def test_delete_all_requires_explicit_non_atomic_name(client_type):
    assert callable(client_type.delete_all_relationships_non_atomic)
    assert not hasattr(client_type, "delete_all_relationships")
