"""spicedb.testing: in-memory doubles with the real clients' surface and semantics."""

from __future__ import annotations

import inspect

import grpc
import pytest

from spicedb import (
    AlreadyExistsError,
    CallOptions,
    CheckResult,
    NotFoundError,
    ReflectSchemaResult,
    ResponseMetadata,
    RetryPolicy,
    SchemaDefinition,
    TransportSecurity,
    min_latency,
    DeletePartial,
    DeleteProgress,
    FailedPreconditionError,
    Filter,
    InvalidArgumentError,
    ObjectRef,
    PermissionCheck,
    Permissionship,
    Relationship,
    SpiceDBError,
    SubjectFilter,
    SubjectRelationFilter,
    SubjectRef,
    Transaction,
    ZedToken,
    full,
)
from spicedb.aio import SpiceDBClient as AioClient
from spicedb.sync import SpiceDBClient as SyncClient
from spicedb import Operation, UnavailableError
from spicedb.testing import (
    AsyncInMemoryClient,
    Call,
    Fault,
    FaultStage,
    InMemoryClient,
    SchemaRevision,
    cursor_after,
)

SHARED = [
    "write",
    "check",
    "check_bulk",
    "read_relationships",
    "delete_relationships",
    "delete_all_relationships_non_atomic",
    "import_relationships",
    "export_relationships",
    "read_schema",
    "write_schema",
    "reflect_schema",
]


def _shape(fn):
    sig = inspect.signature(fn)
    return [(p.name, p.kind, p.default) for p in sig.parameters.values()], str(sig.return_annotation)


@pytest.mark.parametrize("name", SHARED)
@pytest.mark.parametrize("double, real", [(InMemoryClient, SyncClient), (AsyncInMemoryClient, AioClient)])
def test_double_signatures_match_the_real_client(name, double, real):
    assert _shape(getattr(double, name)) == _shape(getattr(real, name))
    assert inspect.iscoroutinefunction(getattr(double, name)) == inspect.iscoroutinefunction(getattr(real, name))


def rel(text: str) -> Relationship:
    return Relationship.from_tuple(text)


def test_write_semantics():
    store = InMemoryClient([rel("document:a#viewer@user:alice")])
    with pytest.raises(AlreadyExistsError) as caught:
        store.write(Transaction().touch(rel("document:b#viewer@user:bob")).create(rel("document:a#viewer@user:alice")))
    assert caught.value.reason == "ERROR_REASON_ATTEMPT_TO_RECREATE_RELATIONSHIP"
    assert caught.value.code() is grpc.StatusCode.ALREADY_EXISTS
    assert store.relationships() == [rel("document:a#viewer@user:alice")]  # atomic

    with pytest.raises(FailedPreconditionError) as caught:
        store.write(Transaction().must_not_match(Filter("document", resource_id="a")).touch(rel("document:c#viewer@user:c")))
    assert caught.value.reason == "ERROR_REASON_WRITE_OR_DELETE_PRECONDITION_FAILURE"

    result = store.write(
        Transaction().must_match(Filter("document", resource_id="a"))
        .touch(rel("document:a#viewer@user:alice[expiration:2030-01-01T00:00:00Z]"))
        .delete(rel("document:missing#viewer@user:x"))
        .create(rel("document:b#viewer@team:t#member"))
    )
    assert result.revision == ZedToken("1")
    assert [str(r) for r in store.relationships()] == [
        "document:a#viewer@user:alice[expiration:2030-01-01T00:00:00Z]",
        "document:b#viewer@team:t#member",
    ]
    # An empty write is sent like SpiceDB's: preconditions only, current revision.
    assert store.write(Transaction()).revision == ZedToken("1")
    with pytest.raises(FailedPreconditionError):
        store.write(Transaction().must_match(Filter("folder")))


def test_read_and_delete_semantics():
    store = InMemoryClient([rel(f"document:{i}#viewer@user:u") for i in range(5)] + [rel("document:9#viewer@team:t#member")])
    direct = Filter("document", subject=SubjectFilter("user", relation=SubjectRelationFilter.direct()))
    items = list(store.read_relationships(direct, consistency=full()))
    assert [i.relationship.resource_id for i in items] == ["0", "1", "2", "3", "4"]
    assert items[0].read_at == ZedToken("0")
    resumed = list(store.read_relationships(direct, consistency=full(), cursor=items[1].after_result_cursor))
    assert [i.relationship.resource_id for i in resumed] == ["2", "3", "4"]

    with pytest.raises(InvalidArgumentError):
        store.delete_relationships(direct)
    with pytest.raises(SpiceDBError) as caught:
        store.delete_relationships(direct, limit=2, partial=DeletePartial.DISALLOWED)
    assert caught.value.code() is grpc.StatusCode.INVALID_ARGUMENT
    assert len(store.relationships()) == 6

    partial = store.delete_relationships(direct, limit=2)
    assert (partial.deleted_count, partial.progress) == (2, DeleteProgress.PARTIAL)
    progress = []
    done = store.delete_all_relationships_non_atomic(direct, page_size=2, on_progress=progress.append)
    assert [p.deleted_count for p in progress] == [2, 3] and done.progress is DeleteProgress.COMPLETE
    assert store.relationships() == [rel("document:9#viewer@team:t#member")]
    with pytest.raises(FailedPreconditionError):
        store.delete_relationships(Filter("document"), partial=DeletePartial.DISALLOWED, must_match=[direct])


def test_registered_check_results():
    store = InMemoryClient()
    alice = SubjectRef(ObjectRef("user", "alice"))
    doc = ObjectRef("document", "a")
    store.set_check_result(doc, "view", alice, Permissionship.HAS_PERMISSION)
    store.set_check_result(doc, "edit", alice, CheckResult(Permissionship.CONDITIONAL_PERMISSION, ("ip",), ZedToken("")))
    view = store.check(PermissionCheck(doc, "view", alice), consistency=full())
    edit, other = store.check_bulk(
        [PermissionCheck(doc, "edit", alice), PermissionCheck(doc, "delete", alice)], consistency=full()
    )
    assert view.has_permission and view.checked_at == ZedToken("0")
    assert not edit and edit.missing_context == ("ip",)
    assert other.permissionship is Permissionship.NO_PERMISSION


async def test_async_double_shares_semantics():
    store = AsyncInMemoryClient()
    await store.write(Transaction().create(rel("document:a#viewer@user:u")))
    with pytest.raises(AlreadyExistsError):
        await store.write(Transaction().create(rel("document:a#viewer@user:u")))
    async with store.read_relationships(Filter("document"), consistency=full()) as stream:
        assert [i.relationship async for i in stream] == [rel("document:a#viewer@user:u")]
    seen = []

    async def record(progress):
        seen.append(progress.deleted_count)

    await store.delete_all_relationships_non_atomic(Filter("document"), on_progress=record)
    assert seen == [1] and store.relationships() == []


def test_errors_have_the_real_clients_shape():
    store = InMemoryClient([rel("org:acme#admin@user:alice")])
    with pytest.raises(FailedPreconditionError) as caught:
        store.write(Transaction().must_match(Filter("org", relation="owner")))
    # A server failure: the server's text, verbatim, as for a real server error.
    assert str(caught.value).startswith("unable to satisfy write precondition `")
    assert caught.value.server_message == str(caught.value) == caught.value.details()

    # Local validation runs the real clients' request builders: same class,
    # same message, no server message.
    def message(call):
        with pytest.raises(InvalidArgumentError) as error:
            call()
        assert error.value.server_message is None
        return str(error.value)

    real = SyncClient("localhost:1", token="t", transport_security=TransportSecurity.PLAINTEXT)
    check = PermissionCheck(ObjectRef("document", "a"), "view", SubjectRef(ObjectRef("user", "u")))
    for call in (
        lambda c: c.read_relationships(Filter("document"), consistency=full(), page_size=0),
        lambda c: c.delete_relationships(Filter("document")),
        lambda c: c.check(check, consistency=full(), context={"bad": object()}),
        lambda c: c.write_schema("definition user {}", timeout=-1),
    ):
        assert message(lambda: call(store)) == message(lambda: call(real))

    # Mutations that cannot replay refuse a per-call retry opt-in, as the real client does.
    retry = CallOptions(retry_policy=RetryPolicy(max_retries=1))
    for call in (
        lambda c: c.write(Transaction().create(rel("document:x#viewer@user:u")), call_options=retry),
        lambda c: c.delete_relationships(Filter("document"), limit=1, call_options=retry),
        lambda c: c.import_relationships([rel("document:x#viewer@user:u")], call_options=retry),
    ):
        assert message(lambda: call(store)) == message(lambda: call(real))
    store.write(Transaction().touch(rel("document:x#viewer@user:u")), call_options=retry)
    store.write_schema("definition user {}", call_options=retry)


def test_import_export_and_schema_operations():
    store = InMemoryClient()
    tuples = ["doc:a#viewer@user:x", "doc:b#viewer@user:x", "doc:c#viewer@user:x", "folder:f#viewer@user:x"]
    assert store.import_relationships(rel(t) for t in tuples) == 4
    with pytest.raises(AlreadyExistsError):
        store.import_relationships([rel("doc:z#viewer@user:x"), rel("doc:a#viewer@user:x")])
    assert len(store.relationships()) == 4, "a failed import adds nothing"

    first = list(store.read_relationships(Filter("doc"), consistency=full()))
    rest = [r.relationship.resource_id for r in store.read_relationships(
        Filter("doc"), consistency=full(), cursor=first[1].after_result_cursor)]
    assert rest == ["c"]
    with pytest.raises(InvalidArgumentError) as caught:
        list(store.read_relationships(Filter("doc"), consistency=full(), cursor="not-a-cursor"))
    assert caught.value.server_message is not None, "a bad cursor is the server's rejection"

    exported = [str(r.relationship) for r in store.export_relationships(Filter("folder"), consistency=full())]
    assert exported == ["folder:f#viewer@user:x"]
    everything = list(store.export_relationships(consistency=full()))
    resumed = list(store.export_relationships(consistency=full(), cursor=everything[2].after_result_cursor))
    assert [r.relationship for r in resumed] == [rel("folder:f#viewer@user:x")]

    with pytest.raises(NotFoundError):
        store.read_schema()
    written = store.write_schema("definition user {}")
    schema = store.read_schema()
    assert schema.schema == "definition user {}" and schema.revision == written

    with pytest.raises(NotFoundError):
        store.reflect_schema(consistency=min_latency())
    store.set_reflect_schema_result(ReflectSchemaResult([SchemaDefinition("user", "", [], [])], [], ZedToken("")))
    reflected = store.reflect_schema(consistency=min_latency())
    assert reflected.definitions[0].name == "user" and reflected.revision == written


async def test_doubles_fill_response_metadata():
    store = AsyncInMemoryClient()
    store.set_response_metadata([("io.spicedb.respmeta.version", "v1.99")], [("t", "1")])
    await store.write_schema("definition user {}")
    captured = ResponseMetadata()
    await store.read_schema(call_options=CallOptions(response_metadata=captured))
    assert captured.headers == (("io.spicedb.respmeta.version", "v1.99"),)
    assert captured.trailers == (("t", "1"),)

    async def produce():
        yield rel("doc:a#viewer@user:x")

    assert await store.import_relationships(produce()) == 1
    async with store.export_relationships(consistency=full()) as stream:
        assert [r.relationship async for r in stream] == [rel("doc:a#viewer@user:x")]


# --- call recording and fault injection ------------------------------------


def _tuples(n: int) -> list[Relationship]:
    return [Relationship.from_tuple(f"document:d{i}#viewer@user:x") for i in range(n)]


def _unavailable(message: str = "unavailable") -> SpiceDBError:
    return SpiceDBError.from_code(grpc.StatusCode.UNAVAILABLE, message)


def test_fault_injection_import_batches_commit_and_stream_items():
    client = InMemoryClient()

    # A restore test: the third import batch fails with ALREADY_EXISTS and
    # nothing is applied.
    seen: list[Fault] = []

    def batch_two(fault: Fault):
        seen.append(fault)
        if fault.stage is FaultStage.IMPORT_BATCH and fault.item == 2:
            return SpiceDBError.from_code(grpc.StatusCode.ALREADY_EXISTS, "duplicate relationship")
        return None

    client.set_fault_hook(batch_two)
    with pytest.raises(AlreadyExistsError) as raised:
        client.import_relationships(_tuples(10), batch_size=3)
    assert str(raised.value) == "duplicate relationship"
    assert raised.value.server_message == "duplicate relationship"
    assert client.relationships() == []
    assert [(f.stage, f.item) for f in seen] == [
        (FaultStage.CALL, 0),
        (FaultStage.IMPORT_BATCH, 0),
        (FaultStage.IMPORT_BATCH, 1),
        (FaultStage.IMPORT_BATCH, 2),
    ]

    # The commit itself can fail, and nothing is applied.
    client.set_fault_hook(lambda f: _unavailable("lost") if f.stage is FaultStage.IMPORT_COMMIT else None)
    with pytest.raises(UnavailableError):
        client.import_relationships(_tuples(4))
    assert client.relationships() == []

    # A backup test: the export fails after K items.
    client.set_fault_hook(None)
    assert client.import_relationships(_tuples(5)) == 5
    client.set_fault_hook(
        lambda f: _unavailable("stream reset")
        if f.call.operation is Operation.EXPORT_RELATIONSHIPS and f.stage is FaultStage.ITEM and f.item == 3
        else None
    )
    got = []
    with pytest.raises(UnavailableError):
        for item in client.export_relationships(consistency=full()):
            got.append(item)
    assert len(got) == 3

    # Reads fail at items too.
    client.set_fault_hook(lambda f: _unavailable() if f.stage is FaultStage.ITEM and f.item == 1 else None)
    got = []
    with pytest.raises(UnavailableError):
        for item in client.read_relationships(Filter("document"), consistency=full()):
            got.append(item)
    assert len(got) == 1

    # A failed write applies nothing.
    client.fail_next(Operation.WRITE, _unavailable())
    with pytest.raises(UnavailableError):
        client.write(Transaction().touch(Relationship.from_tuple("document:new#viewer@user:x")))
    assert len(client.relationships()) == 5
    with pytest.raises(InvalidArgumentError):
        client.set_fault_hook(lambda f: "not an exception")  # type: ignore[arg-type,return-value]
        client.read_schema()


def test_fail_next_affects_exactly_one_call():
    client = InMemoryClient()
    check = PermissionCheck(ObjectRef("document", "d0"), "view", SubjectRef(ObjectRef("user", "x")))
    client.fail_next(Operation.CHECK, _unavailable("down"))
    with pytest.raises(NotFoundError):
        client.read_schema()  # another operation is unaffected
    with pytest.raises(UnavailableError) as raised:
        client.check(check, consistency=full())
    assert raised.value.code() is grpc.StatusCode.UNAVAILABLE
    assert client.check(check, consistency=full()).permissionship is Permissionship.NO_PERMISSION
    assert client.check(check, consistency=full()).permissionship is Permissionship.NO_PERMISSION

    # A stream's call fault surfaces on iteration, as a server error does.
    client.fail_next(Operation.READ_RELATIONSHIPS, _unavailable("down"))
    stream = client.read_relationships(Filter("document"), consistency=full())
    with pytest.raises(UnavailableError):
        list(stream)
    assert list(client.read_relationships(Filter("document"), consistency=full())) == []
    with pytest.raises(InvalidArgumentError):
        client.fail_next("check", _unavailable())  # type: ignore[arg-type]


def test_calls_are_recorded_with_their_options():
    client = InMemoryClient()
    options = CallOptions(metadata=(("x-test", "1"),))
    list(client.read_relationships(
        Filter("document"), consistency=full(), cursor="2", page_size=5, call_options=options))
    list(client.read_relationships(Filter("folder"), consistency=min_latency()))
    client.write_schema("definition user {}")

    reads = client.calls_of(Operation.READ_RELATIONSHIPS)
    assert [c.index for c in reads] == [0, 1]
    assert reads[0].options["cursor"] == "2"
    assert reads[0].options["page_size"] == 5
    assert reads[0].options["call_options"] is options
    assert reads[1].options["cursor"] is None
    assert reads[1].options["consistency"] == min_latency()
    assert reads[1].request == Filter("folder")
    assert [c.operation for c in client.calls()] == [
        Operation.READ_RELATIONSHIPS, Operation.READ_RELATIONSHIPS, Operation.WRITE_SCHEMA,
    ]
    schema_call = client.calls()[2]
    assert isinstance(schema_call, Call)
    assert (schema_call.index, schema_call.request) == (0, "definition user {}")
    with pytest.raises(TypeError):
        reads[0].options["cursor"] = "3"  # type: ignore[index]


async def test_async_double_records_and_injects():
    client = AsyncInMemoryClient()

    async def source():
        for r in _tuples(4):
            yield r

    client.set_fault_hook(
        lambda f: SpiceDBError.from_code(grpc.StatusCode.ALREADY_EXISTS, "dup")
        if f.stage is FaultStage.IMPORT_BATCH and f.item == 1 else None
    )
    with pytest.raises(AlreadyExistsError):
        await client.import_relationships(source(), batch_size=2)
    assert client.relationships() == []
    client.set_fault_hook(None)
    assert await client.import_relationships(source(), batch_size=2) == 4
    client.fail_next(Operation.EXPORT_RELATIONSHIPS, _unavailable())
    with pytest.raises(UnavailableError):
        [x async for x in client.export_relationships(consistency=full())]
    assert len([x async for x in client.export_relationships(consistency=full())]) == 4
    imports = client.calls_of(Operation.IMPORT_RELATIONSHIPS)
    assert [(c.index, c.options["batch_size"], len(c.request)) for c in imports] == [(0, 2, 4), (1, 2, 4)]
    assert client.calls() == client._sync.calls()


def test_double_covers_the_whole_client():
    client = InMemoryClient(_tuples(2))

    # Operations without in-memory semantics fail like a server lacking them.
    with pytest.raises(SpiceDBError) as raised:
        client.expand_permission_tree(resource=ObjectRef("document", "d0"), permission="view", consistency=full())
    assert raised.value.code() is grpc.StatusCode.UNIMPLEMENTED
    assert str(raised.value) == "expand_permission_tree: not implemented by the spicedb.testing in-memory double"
    assert raised.value.server_message == str(raised.value)
    expand = client.calls_of(Operation.EXPAND_PERMISSION_TREE)
    assert len(expand) == 1 and expand[0].request is None
    assert expand[0].options["resource"] == ObjectRef("document", "d0")

    # Streams record the call and fail on iteration.
    stream = client.watch(["document"])
    assert client.calls_of(Operation.WATCH)[0].request == ["document"]
    with pytest.raises(SpiceDBError) as raised:
        list(stream)
    assert raised.value.code() is grpc.StatusCode.UNIMPLEMENTED

    # They consult the fault hook first, so a test can still script them.
    client.fail_next(Operation.DIFF_SCHEMA, _unavailable("down"))
    with pytest.raises(UnavailableError):
        client.diff_schema("definition user {}", consistency=full())
    client.fail_next(Operation.LOOKUP_RESOURCES, _unavailable("down"))
    with pytest.raises(UnavailableError):
        list(client.lookup_resources(
            resource_type="document", permission="view",
            subject=SubjectRef(ObjectRef("user", "x")), consistency=full(),
        ))
    with pytest.raises(SpiceDBError) as raised:
        client.experimental_register_relationship_counter("docs", Filter("document"))
    assert raised.value.code() is grpc.StatusCode.UNIMPLEMENTED
    assert client.calls_of(Operation.EXPERIMENTAL_REGISTER_RELATIONSHIP_COUNTER)[0].request == (
        "docs", Filter("document"),
    )
    with pytest.raises(InvalidArgumentError):
        client.experimental_count_relationships("docs", call_options="nope")  # type: ignore[arg-type]

    # The double has no channel; close and the context manager work in memory.
    with pytest.raises(SpiceDBError) as raised:
        client.raw_grpc()
    assert raised.value.code() is grpc.StatusCode.UNIMPLEMENTED
    with client as entered:
        assert entered is client
    client.close()
    assert len(client.relationships()) == 2, "the double stays usable after close"


async def test_async_double_covers_the_whole_client():
    client = AsyncInMemoryClient()
    with pytest.raises(SpiceDBError) as raised:
        await client.download_permission_sets()
    assert raised.value.code() is grpc.StatusCode.UNIMPLEMENTED
    with pytest.raises(SpiceDBError) as raised:
        [x async for x in client.watch(["document"])]
    assert raised.value.code() is grpc.StatusCode.UNIMPLEMENTED
    # Lookups work in memory (see test_testing_double_resolution.py).
    assert [x async for x in client.lookup_subjects(
        resource=ObjectRef("document", "d0"), permission="view", subject_type="user", consistency=full(),
    )] == []
    with pytest.raises(SpiceDBError):
        await client.raw_grpc()
    async with client as entered:
        assert entered is client
    await client.close()
    assert [c.operation for c in client.calls()] == [
        Operation.DOWNLOAD_PERMISSION_SETS, Operation.WATCH, Operation.LOOKUP_SUBJECTS,
    ]


def test_read_schema_without_revision():
    client = InMemoryClient()
    client.write_schema("definition user {}")
    assert str(client.read_schema().revision) != ""

    client.set_read_schema_revision(SchemaRevision.OMITTED)
    omitted = client.read_schema()
    assert omitted.revision == ZedToken("")
    assert omitted.schema == "definition user {}"

    client.set_read_schema_revision(SchemaRevision.INCLUDED)
    assert str(client.read_schema().revision) != ""
    with pytest.raises(InvalidArgumentError):
        client.set_read_schema_revision("omitted")  # type: ignore[arg-type]


async def test_async_read_schema_without_revision():
    client = AsyncInMemoryClient()
    await client.write_schema("definition user {}")
    client.set_read_schema_revision(SchemaRevision.OMITTED)
    assert (await client.read_schema()).revision == ZedToken("")


def test_fault_sees_import_batch_contents():
    client = InMemoryClient()
    batches: list[list[str]] = []

    def hook(fault: Fault):
        if fault.stage is not FaultStage.IMPORT_BATCH:
            assert fault.batch == ()
            return None
        ids = [r.resource_id for r in fault.batch]
        if "d4" in ids:
            return SpiceDBError.from_code(grpc.StatusCode.ALREADY_EXISTS, "duplicate d4")
        batches.append(ids)
        return None

    client.set_fault_hook(hook)
    with pytest.raises(AlreadyExistsError):
        client.import_relationships(_tuples(7), batch_size=3)
    assert batches == [["d0", "d1", "d2"]], "the hook saw the first batch, then failed on the one holding d4"
    assert client.relationships() == []

    batches.clear()
    client.set_fault_hook(lambda f: batches.append([r.resource_id for r in f.batch]) if f.batch else None)
    assert client.import_relationships(_tuples(7), batch_size=3) == 7
    assert batches == [["d0", "d1", "d2"], ["d3", "d4", "d5"], ["d6"]]


def test_cursor_after_resumes_like_a_double_cursor():
    client = InMemoryClient()
    client.import_relationships(_tuples(5))
    cursors = [item.after_result_cursor for item in client.export_relationships(consistency=full())]
    assert cursors[1] == cursor_after(2), "cursor_after(n) is the cursor on the n-th item"
    rest = [item.relationship.resource_id
            for item in client.export_relationships(consistency=full(), cursor=cursor_after(3))]
    assert rest == ["d3", "d4"]
    reads = [item.relationship.resource_id
             for item in client.read_relationships(Filter("document"), consistency=full(), cursor=cursor_after(4))]
    assert reads == ["d4"]
    assert cursor_after(0) == "0"
    for bad in (-1, True, "2"):
        with pytest.raises(InvalidArgumentError):
            cursor_after(bad)  # type: ignore[arg-type]
