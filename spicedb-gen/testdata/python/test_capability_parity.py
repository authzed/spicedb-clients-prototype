"""Generated clients expose native capabilities without weakening schema helpers."""
import inspect
import pytest
from spicedb.sync import SpiceDBClient
from spicedb.aio import SpiceDBClient as AioClient
from testdata.sync import TypedClient
from testdata.aio import TypedClient as AioTypedClient

from unittest.mock import Mock, AsyncMock
from authzed.api.v1 import core_pb2 as core, permission_service_pb2 as p, watch_service_pb2 as w
from spicedb import (
    Filter, SubjectFilter, SubjectRelationFilter, DeleteProgress, DeleteResult,
    ZedToken, UnavailableError, Transaction, WriteResult, PermissionCheck, ObjectRef,
    SubjectRef, Tracing, WatchContent, full, LookupDebug, Wildcards,
    LookupPermissionshipFilter, PermissionSetCursor, CountResult, BulkItemErrors, DeletePartial,
    InvalidArgumentError,
)
from testdata.permissions import Document, DOCUMENT_VIEW, User, TypedTransaction


@pytest.mark.parametrize("native,typed", [(SpiceDBClient, TypedClient), (AioClient, AioTypedClient)])
def test_every_native_public_operation_is_explicit(native, typed):
    operations = {name for name, method in inspect.getmembers(native, callable) if not name.startswith("_")}
    assert operations <= set(typed.__dict__)
    for name in operations:
        assert inspect.iscoroutinefunction(getattr(typed, name)) == inspect.iscoroutinefunction(getattr(native, name))


def test_passthrough_signatures_preserve_native_types_and_defaults():
    # from_channel returns the TypedClient wrapper rather than the native client.
    schema_methods = {"check", "lookup_resources", "lookup_subjects", "read_relationships", "from_channel"}
    for native, typed in [(SpiceDBClient, TypedClient), (AioClient, AioTypedClient)]:
        for name, method in inspect.getmembers(native, callable):
            if name.startswith("_") or name in schema_methods:
                continue
            assert inspect.signature(getattr(typed, name)) == inspect.signature(method), name




FLAVORS = [(SpiceDBClient, TypedClient), (AioClient, AioTypedClient)]


async def resolve(value):
    return await value if inspect.isawaitable(value) else value


@pytest.mark.parametrize("native_type,wrapper", FLAVORS)
async def test_native_delete_pagination_options_and_failure_progress(native_type, wrapper):
    native = native_type("localhost:50051", token="key")
    native._ensure_channel = Mock()
    error = UnavailableError("later batch failed")
    # Generated protobuf modules lack static declarations for message classes.
    responses = [p.DeleteRelationshipsResponse(  # pyright: ignore[reportAttributeAccessIssue]
        deleted_at=core.ZedToken(token="ack"), relationships_deleted_count=2,  # pyright: ignore[reportAttributeAccessIssue]
        deletion_progress=2, after_result_cursor=core.Cursor(token="next")), error]  # pyright: ignore
    rpc = AsyncMock(side_effect=responses) if native_type is AioClient else Mock(side_effect=responses)
    native._permissions = Mock(DeleteRelationships=rpc)
    guard = Filter("document", relation="owner")
    with pytest.raises(UnavailableError) as caught:
        await resolve(wrapper(native).delete_all_relationships_non_atomic(
            Filter("document"), page_size=2, must_match=[guard],
            metadata={"origin": "typed"}, timeout=5))
    assert caught.value is error
    assert caught.value.delete_progress == DeleteResult(ZedToken("ack"), 2, DeleteProgress.PARTIAL, "next")
    requests = [call.args[0] for call in rpc.call_args_list]
    assert len(requests) == 2
    assert not requests[0].HasField("optional_cursor")
    assert requests[1].optional_cursor.token == "next"
    for request in requests:
        assert request.optional_limit == 2
        assert request.optional_preconditions[0].filter.optional_relation == "owner"
        assert request.optional_transaction_metadata["origin"] == "typed"


@pytest.mark.parametrize("native_type,wrapper", FLAVORS)
async def test_bounded_delete_returns_native_result(native_type, wrapper):
    native = Mock()
    result = DeleteResult(ZedToken("ack"), 1, DeleteProgress.PARTIAL, "cursor")
    native.delete_relationships = AsyncMock(return_value=result) if native_type is AioClient else Mock(return_value=result)
    filter = Filter("document")
    guard = Filter("document", relation="owner")
    assert await resolve(wrapper(native).delete_relationships(filter, limit=3, must_not_match=[guard], metadata={"key": "value"}, timeout=5)) is result
    native.delete_relationships.assert_called_once_with(filter, limit=3, partial=DeletePartial.ALLOWED, must_match=(), must_not_match=[guard], metadata={"key": "value"}, timeout=5, call_options=None)


class WatchCall:
    def __init__(self):
        self.cancelled = False

    def __iter__(self):
        yield w.WatchResponse(changes_through=core.ZedToken(token="event"))  # pyright: ignore[reportAttributeAccessIssue]

    async def __aiter__(self):
        for response in self:
            yield response

    def cancel(self):
        self.cancelled = True


@pytest.mark.parametrize("native_type,wrapper", FLAVORS)
async def test_watch_preserves_stream_identity_filters_and_cancellation(native_type, wrapper):
    native = native_type("localhost:50051", token="key")
    native._ensure_channel = Mock()
    call = WatchCall()
    rpc = Mock(return_value=call)
    native._watch = Mock(Watch=rpc)
    streams = []
    original = native.watch

    def capture(*args, **kwargs):
        stream = original(*args, **kwargs)
        streams.append(stream)
        return stream

    native.watch = Mock(side_effect=capture)
    filters = [Filter("document", relation="viewer", subject=SubjectFilter("user", relation=SubjectRelationFilter.direct()))]
    stream = wrapper(native).watch(filters=filters, start_revision=ZedToken("start"), content=[WatchContent.RELATIONSHIPS, WatchContent.CHECKPOINTS], timeout=5)
    assert stream is streams[0]
    if native_type is AioClient:
        async with stream:
            assert (await anext(stream)).changes_through == ZedToken("event")
    else:
        with stream:
            assert next(stream).changes_through == ZedToken("event")
    assert call.cancelled
    request = rpc.call_args.args[0]
    assert request.optional_start_cursor.token == "start"
    assert len(request.optional_relationship_filters) == 1
    assert request.optional_relationship_filters[0].optional_relation == "viewer"
    assert request.optional_relationship_filters[0].optional_subject_filter.HasField("optional_relation")


@pytest.mark.parametrize("native_type,wrapper", FLAVORS)
async def test_bulk_import_experimental_and_native_transaction_forwarding(native_type, wrapper):
    native = Mock()
    checks = [PermissionCheck(ObjectRef("document", "one"), "view", SubjectRef(ObjectRef("user", "alice")))]
    results = []
    count = CountResult(4, ZedToken("count"), False)
    write_result = WriteResult(ZedToken("written"))
    for name, result in [("check_bulk", results), ("import_relationships", 7), ("experimental_count_relationships_by_filter", count), ("write", write_result)]:
        setattr(native, name, AsyncMock(return_value=result) if native_type is AioClient else Mock(return_value=result))
    client = wrapper(native)
    consistency = full()
    assert await resolve(client.check_bulk(checks, consistency=consistency, context={"test": True}, tracing=Tracing.ENABLED, item_errors=BulkItemErrors.RETURN, timeout=5)) is results
    native.check_bulk.assert_called_once_with(checks, consistency=consistency, context={"test": True}, tracing=Tracing.ENABLED, item_errors=BulkItemErrors.RETURN, timeout=5, call_options=None)
    producer = iter(())
    assert await resolve(client.import_relationships(producer, timeout=5)) == 7
    assert native.import_relationships.call_args.args[0] is producer
    filter = Filter("document")
    assert await resolve(client.experimental_count_relationships_by_filter(filter, timeout=5)) is count
    native_txn = Transaction()
    assert await resolve(client.write(native_txn, metadata={"test": True}, timeout=5)) is write_result
    assert native.write.call_args.args[0] is native_txn
    assert native.write.call_args.kwargs["metadata"] == {"test": True}
    typed = TypedTransaction().touch(Document("one").viewer(User("alice")))
    assert await resolve(client.write_txn(typed, metadata={"test": True}, timeout=5)) is write_result
    built = native.write.call_args.args[0]
    assert isinstance(built, Transaction) and built is not native_txn
    assert native.write.call_args.kwargs == {"metadata": {"test": True}, "timeout": 5, "call_options": None}


@pytest.mark.parametrize("native_type,wrapper", FLAVORS)
async def test_write_txn_raises_construction_errors_before_any_rpc(native_type, wrapper):
    native = Mock()
    native.write = AsyncMock() if native_type is AioClient else Mock()
    txn = TypedTransaction().touch(Document("").viewer(User("alice")))
    with pytest.raises(InvalidArgumentError):
        await resolve(wrapper(native).write_txn(txn))
    native.write.assert_not_called()


@pytest.mark.parametrize("wrapper", [TypedClient, AioTypedClient])
def test_schema_stream_options_and_materialize_cursor_forwarding(wrapper):
    native = Mock()
    client = wrapper(native)
    consistency = full()
    assert client.lookup_resources(consistency, DOCUMENT_VIEW, User("alice"), page_size=4, limit=7, debug=LookupDebug.ENABLED, context={"x": True}, permissionship_filter=LookupPermissionshipFilter.HAS_PERMISSION_ONLY) is native.lookup_resources.return_value
    options = native.lookup_resources.call_args.kwargs
    assert (options["page_size"], options["limit"], options["debug"], options["context"]) == (4, 7, LookupDebug.ENABLED, {"x": True})
    assert client.lookup_subjects(consistency, Document("one").view, User, page_size=3, limit=6, wildcards=Wildcards.EXCLUDE) is native.lookup_subjects.return_value
    options = native.lookup_subjects.call_args.kwargs
    assert (options["page_size"], options["limit"], options["wildcards"]) == (3, 6, Wildcards.EXCLUDE)
    filter = Filter("document")
    assert client.read_relationships(consistency, filter, page_size=9) is native.read_relationships.return_value
    assert native.read_relationships.call_args.kwargs["page_size"] == 9
    assert client.export_relationships(filter, consistency=consistency, page_size=8) is native.export_relationships.return_value
    assert native.export_relationships.call_args.kwargs["page_size"] == 8
    cursor = PermissionSetCursor(4, ZedToken("revision"), 1, True, "key", "opaque")
    assert client.lookup_permission_sets(limit=4, at_revision=ZedToken("revision"), cursor=cursor, timeout=5) is native.lookup_permission_sets.return_value
    assert native.lookup_permission_sets.call_args.kwargs["cursor"] is cursor


@pytest.mark.parametrize("native,typed", FLAVORS)
def test_schema_helpers_expose_every_native_behavior_option(native, typed):
    # typed method -> (native method, native arguments the typed method adapts)
    schema_arguments = {
        "check": ("check", {"check", "consistency"}),
        "lookup_resources": ("lookup_resources", {"resource_type", "permission", "subject", "consistency"}),
        "lookup_subjects": ("lookup_subjects", {"resource", "permission", "subject_type", "subject_relation", "consistency"}),
        "read_relationships": ("read_relationships", {"filter", "consistency"}),
        "check_dynamic": ("check", {"check", "consistency"}),
        "write_txn": ("write", {"transaction"}),
        "touch": ("write", {"transaction"}),
        "create": ("write", {"transaction"}),
        "delete": ("write", {"transaction"}),
    }
    for name, (native_name, adapted) in schema_arguments.items():
        native_signature = inspect.signature(getattr(native, native_name))
        typed_signature = inspect.signature(getattr(typed, name))
        for parameter, expected in native_signature.parameters.items():
            if parameter in adapted or parameter == "self":
                continue
            actual = typed_signature.parameters[parameter]
            assert (actual.annotation, actual.default, actual.kind) == (expected.annotation, expected.default, expected.kind)
        assert typed_signature.return_annotation == native_signature.return_annotation
