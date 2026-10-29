"""Unit tests for SpiceDBClient construction — no SpiceDB instance needed."""
from authzed.api.v1 import core_pb2 as _core_tok
from spicedb import ZedToken
from spicedb import TransportSecurity, WatchContent, RetryPolicy

from spicedb import ObjectRef, SubjectRef, PermissionCheck, SubjectFilter
import asyncio
import inspect
from unittest.mock import AsyncMock
import grpc
import pytest
from authzed.api.v1 import (
    core_pb2,
    permission_service_pb2,
    schema_service_pb2,
    watch_service_pb2,
)
from google.rpc import status_pb2
from spicedb import Filter, Relationship, RelationReference, full
from spicedb.aio import SpiceDBClient
from spicedb.errors import (
    EventLoopBindingError,
    InvalidArgumentError,
    SpiceDBError,
    UnavailableError,
)
from spicedb.types import CheckResult, LookupResource, Permissionship, ResolvedSubject


@pytest.fixture
async def make_client():
    """Construct SpiceDBClient(s) for a test and close them all on teardown.

    A factory (not a plain fixture) because several tests vary
    ``max_retries`` -- a fixed fixture would force those to keep
    constructing by hand, which is how the leaked-channel debt this fixture
    fixes originally persisted.
    """
    created = []

    def _make(**kw):
        c = SpiceDBClient("localhost:50051", token="testtoken", transport_security=TransportSecurity.PLAINTEXT, **kw)
        c._ensure_channel()
        created.append(c)
        return c

    yield _make
    for c in created:
        await c.close()


class _FakeAioCall:
    """Stand-in for the call object a ``grpc.aio`` streaming stub returns.

    A real stub hands back one object that is both the response iterator and
    the RPC handle, and the client now cancels that handle on the way out of
    every stream (root DESIGN.md, "RULE: Abandoning a stream must release
    it"). A bare async generator has no ``cancel()``, so a stub returning one
    is not the shape the client sees in production -- and a test built on
    that shape would go green while the shipped code raised
    ``AttributeError``. ``cancelled`` additionally lets a test assert the
    client released the stream.
    """

    def __init__(self, gen):
        self._gen = gen
        self.cancelled = False

    def __aiter__(self):
        return self._gen.__aiter__()

    def cancel(self) -> bool:
        self.cancelled = True
        return True


def _async_stream(*responses):
    """Build a stub for a grpc.aio server-streaming call.

    ``grpc.aio`` stream stubs return an async-iterable *call object*
    synchronously when invoked (not a coroutine) — the caller then does
    ``async for resp in stub.Method(request, ...)`` without ever awaiting the
    call itself. That means this can't be an ``AsyncMock``: calling an
    ``AsyncMock`` returns a coroutine, and ``async for`` over a coroutine
    raises ``TypeError``. This returns a plain callable that, when invoked,
    returns a fresh async generator yielding ``responses``.
    """

    async def _gen(*_args, **_kwargs):
        for r in responses:
            yield r

    def _call(*args, **kwargs):
        _call.calls.append((args, kwargs))
        return _FakeAioCall(_gen())

    _call.calls = []
    return _call


def _transient_error(
    code: grpc.StatusCode = grpc.StatusCode.UNAVAILABLE,
) -> grpc.aio.AioRpcError:
    return grpc.aio.AioRpcError(
        code, grpc.aio.Metadata(), grpc.aio.Metadata(), details="transient failure"
    )


def _stream_open_fails_then_succeeds(*responses, fail_times: int = 1):
    """Stub whose first `fail_times` invocations raise a transient
    ``AioRpcError`` immediately — before yielding anything — simulating a
    stream that fails at ESTABLISHMENT. The next invocation succeeds,
    yielding ``responses``. Used to assert establishment retry happens:
    since nothing was ever yielded by the failed attempt(s), retrying
    cannot replay anything.
    """
    state = {"opens": 0}

    async def _gen(*_args, **_kwargs):
        state["opens"] += 1
        if state["opens"] <= fail_times:
            raise _transient_error()
        for r in responses:
            yield r

    def _call(*args, **kwargs):
        _call.calls.append((args, kwargs))
        return _FakeAioCall(_gen())

    _call.calls = []
    return _call


def _stream_fails_after_yielding(*responses):
    """Stub that yields ``responses`` and then raises a transient
    ``AioRpcError``. Used to assert a transient error occurring AFTER at
    least one item has been yielded is NEVER retried — retrying would
    replay ``responses`` to the caller a second time.
    """

    async def _gen(*_args, **_kwargs):
        for r in responses:
            yield r
        raise _transient_error()

    def _call(*args, **kwargs):
        _call.calls.append((args, kwargs))
        return _FakeAioCall(_gen())

    _call.calls = []
    return _call


async def test_constructor_insecure_opens_channel_on_ensure(make_client):
    """The channel binds lazily -- exercise `_ensure_channel()` explicitly to
    confirm the insecure code path still produces a real channel."""
    c = make_client()
    assert c._channel is not None


async def test_context_manager():
    async with SpiceDBClient("localhost:50051", token="testtoken", transport_security=TransportSecurity.PLAINTEXT) as c:
        c._ensure_channel()
        assert c._channel is not None


class TestSchemaReflectionSignatures:
    """`reflect_schema`/`diff_schema` must return native types, not proto
    (NOT-1: no proto in the public API). `client.py` uses
    `from __future__ import annotations`, so annotations are unevaluated
from spicedb import ZedToken, TransportSecurity, WatchContent
    strings — check them directly rather than importing proto types here.
    """

    def test_reflect_schema_returns_native_result(self):
        sig = inspect.signature(SpiceDBClient.reflect_schema)
        assert sig.return_annotation == "ReflectSchemaResult"

    def test_diff_schema_returns_native_list(self):
        sig = inspect.signature(SpiceDBClient.diff_schema)
        assert sig.return_annotation == "SchemaDiffResult"

    def test_computable_permissions_returns_native_list(self):
        sig = inspect.signature(SpiceDBClient.computable_permissions)
        assert sig.return_annotation == "RelationsResult"

    def test_dependent_relations_returns_native_list(self):
        sig = inspect.signature(SpiceDBClient.dependent_relations)
        assert sig.return_annotation == "RelationsResult"


class TestComputablePermissions:
    """`computable_permissions` mirrors spicedb-go's ComputablePermissions
    (client/schema.go): request {consistency, definition_name, relation_name},
    response .permissions (list of RelationReference) + .read_at."""

    async def test_maps_permissions_field_by_field(self, make_client):
        client = make_client()
        response = schema_service_pb2.ComputablePermissionsResponse(
            permissions=[
                schema_service_pb2.ReflectionRelationReference(
                    definition_name="document", relation_name="view", is_permission=True
                ),
                schema_service_pb2.ReflectionRelationReference(
                    definition_name="document",
                    relation_name="view_and_edit",
                    is_permission=True,
                ),
            ],
            read_at=core_pb2.ZedToken(token="deadbeef"),
        )
        client._schema.ComputablePermissions = AsyncMock(return_value=response)
        result = await client.computable_permissions(
            consistency=full(), definition_name="document", relation_name="viewer"
        )
        assert result.relations == (
            RelationReference(
                definition_name="document", relation_name="view", is_permission=True
            ),
            RelationReference(
                definition_name="document",
                relation_name="view_and_edit",
                is_permission=True,
            ),
        )
        client._schema.ComputablePermissions.assert_awaited_once()
        request = client._schema.ComputablePermissions.await_args.args[0]
        assert request.definition_name == "document"
        assert request.relation_name == "viewer"


class TestDependentRelations:
    """`dependent_relations` mirrors spicedb-go's DependentRelations
    (client/schema.go): request {consistency, definition_name,
    permission_name}, response .relations (list of RelationReference) +
    .read_at."""

    async def test_maps_relations_field_by_field(self, make_client):
        client = make_client()
        response = schema_service_pb2.DependentRelationsResponse(
            relations=[
                schema_service_pb2.ReflectionRelationReference(
                    definition_name="document",
                    relation_name="viewer",
                    is_permission=False,
                ),
                schema_service_pb2.ReflectionRelationReference(
                    definition_name="document",
                    relation_name="editor",
                    is_permission=False,
                ),
            ],
            read_at=core_pb2.ZedToken(token="cafebabe"),
        )
        client._schema.DependentRelations = AsyncMock(return_value=response)
        result = await client.dependent_relations(
            consistency=full(), definition_name="document", permission_name="view"
        )
        assert result.relations == (
            RelationReference(
                definition_name="document", relation_name="viewer", is_permission=False
            ),
            RelationReference(
                definition_name="document", relation_name="editor", is_permission=False
            ),
        )
        client._schema.DependentRelations.assert_awaited_once()
        request = client._schema.DependentRelations.await_args.args[0]
        assert request.definition_name == "document"
        assert request.permission_name == "view"


class TestCheckPermissionReturnsCheckResult:
    """check_permission/check_permissions return CheckResult (permissionship,
    missing_context, checked_at, has_permission) instead of a bare bool, so a
    caveated relationship whose context wasn't supplied is distinguishable
    from a real denial."""

    async def test_check_permission_returns_check_result(self, make_client):
        client = make_client()
        response = permission_service_pb2.CheckPermissionResponse(
            checked_at=core_pb2.ZedToken(token="deadbeef"),
            permissionship=permission_service_pb2.CheckPermissionResponse.PERMISSIONSHIP_HAS_PERMISSION,
        )
        client._permissions.CheckPermission = AsyncMock(return_value=response)
        rel = Relationship.from_triple("document:readme", "view", "user:alice")
        result = await client.check(_check(rel), consistency=full())
        assert result == CheckResult(
            permissionship=Permissionship.HAS_PERMISSION,
            missing_context=[],
            checked_at=ZedToken("deadbeef"),
        )
        assert result.has_permission is True

    async def test_check_permission_conditional_has_permission_false(self, make_client):
        client = make_client()
        response = permission_service_pb2.CheckPermissionResponse(
            checked_at=core_pb2.ZedToken(token="deadbeef"),
            permissionship=permission_service_pb2.CheckPermissionResponse.PERMISSIONSHIP_CONDITIONAL_PERMISSION,
            partial_caveat_info=core_pb2.PartialCaveatInfo(
                missing_required_context=["now"]
            ),
        )
        client._permissions.CheckPermission = AsyncMock(return_value=response)
        rel = Relationship.from_triple(
            "document:readme", "conditional_view", "user:alice"
        )
        result = await client.check(_check(rel), consistency=full())
        assert result.permissionship == Permissionship.CONDITIONAL_PERMISSION
        assert result.has_permission is False
        assert result.missing_context == ("now",)

    async def test_check_any_and_check_all_do_not_count_conditional(self, make_client):
        """check_any/check_all stay boolean but must count ONLY
        HasPermission -- a Conditional result is not a grant, so it must not
        flip either to True.

        check_any/check_all are implemented independently in each flavor's
        client.py (not shared via _mapping.py), so this must stay in
        lockstep with the sync-flavor counterpart,
        tests/test_client_sync.py::test_check_any_and_check_all_do_not_count_conditional
        -- test_parity.py only compares signatures and would not catch one
        flavor alone regressing here."""
        client = make_client()
        response = permission_service_pb2.CheckBulkPermissionsResponse(
            checked_at=core_pb2.ZedToken(token="deadbeef"),
            pairs=[
                permission_service_pb2.CheckBulkPermissionsPair(
                    item=permission_service_pb2.CheckBulkPermissionsResponseItem(
                        permissionship=permission_service_pb2.CheckPermissionResponse.PERMISSIONSHIP_CONDITIONAL_PERMISSION
                    )
                )
            ],
        )
        client._permissions.CheckBulkPermissions = AsyncMock(
            side_effect=lambda request, **kwargs: _echo_response(response, request)
        )
        rel = Relationship.from_triple(
            "document:readme", "conditional_view", "user:alice"
        )
        assert (
            _aggregate(
                await client.check_bulk([_check(rel)], consistency=full()), False
            )
            is False
        )
        assert (
            _aggregate(await client.check_bulk([_check(rel)], consistency=full()), True)
            is False
        )

    async def test_check_all_with_zero_relationships_returns_false(self, make_client):
        """Python's builtin all() is vacuously True over an empty iterable,
        so check_all(cs, "edit") with no relationships would otherwise return
        True -- a fail-open authorization gate. This matters because the
        idiomatic call site is a gate over a *derived* collection
        (check_all(cs, "edit", *docs_to_rels(docs))), and any ordinary way
        that collection comes up empty (a filter matching nothing, an
        upstream returning []) must not silently open the gate. Root
        DESIGN.md: "An aggregate over zero checks is not a grant."

        check_any/check_all are implemented independently in each flavor's
        client.py, so this must stay in lockstep with the sync-flavor
        counterpart, tests/test_client_sync.py::
        test_check_all_with_zero_relationships_returns_false."""
        client = make_client()
        response = permission_service_pb2.CheckBulkPermissionsResponse(
            checked_at=core_pb2.ZedToken(token="deadbeef"), pairs=[]
        )
        client._permissions.CheckBulkPermissions = AsyncMock(
            side_effect=lambda request, **kwargs: _echo_response(response, request)
        )
        assert (
            _aggregate(await client.check_bulk([], consistency=full()), True) is False
        )


class TestBulkCheckPerItemErrorFidelity:
    """check_permissions must surface the real per-item google.rpc.Status
    from a CheckBulkPermissions pair as a typed SpiceDBError, not fabricate
    a generic INTERNAL error (CI-2)."""

    async def test_per_item_invalid_argument_surfaces_as_typed_error(self, make_client):
        client = make_client()
        pair = permission_service_pb2.CheckBulkPermissionsPair(
            error=status_pb2.Status(code=3, message="bad item")
        )
        response = permission_service_pb2.CheckBulkPermissionsResponse(checked_at=_core_tok.ZedToken(token="t"), pairs=[pair])
        client._permissions.CheckBulkPermissions = AsyncMock(
            side_effect=lambda request, **kwargs: _echo_response(response, request)
        )
        rel = Relationship(
            ObjectRef("document", "1"),
            "view",
            SubjectRef(ObjectRef("user", "alice"), relation=None),
        )
        with pytest.raises(InvalidArgumentError) as exc_info:
            await client.check_bulk([_check(rel)], consistency=full())
        assert type(exc_info.value) is not SpiceDBError
        assert str(exc_info.value) == "bad item"


class TestLookupResourcesYieldsPermissionshipAndPartialCaveat:
    """LookupResources surfaces the proto's permissionship and partial
    caveat info instead of dropping them, so a CONDITIONAL match is
    distinguishable from a full HAS_PERMISSION grant."""

    async def test_maps_has_and_conditional_results(self, make_client):
        client = make_client()
        client._permissions.LookupResources = _async_stream(
            permission_service_pb2.LookupResourcesResponse(looked_up_at=_core_tok.ZedToken(token="t"), 
                resource_object_id="doc1",
                permissionship=permission_service_pb2.LOOKUP_PERMISSIONSHIP_HAS_PERMISSION,
            ),
            permission_service_pb2.LookupResourcesResponse(looked_up_at=_core_tok.ZedToken(token="t"), 
                resource_object_id="doc2",
                permissionship=permission_service_pb2.LOOKUP_PERMISSIONSHIP_CONDITIONAL_PERMISSION,
                partial_caveat_info=core_pb2.PartialCaveatInfo(
                    missing_required_context=["ip_address"]
                ),
            ),
        )
        got = [
            r
            async for r in client.lookup_resources(
                resource_type="document",
                permission="view",
                subject=_subject("user:alice", ""),
                consistency=full(),
            )
        ]
        assert len(got) == 2
        assert got[0] == LookupResource(
            resource_id="doc1",
            permissionship=Permissionship.HAS_PERMISSION,
            partial_caveat=None,
            looked_up_at=ZedToken("t"),
        )
        assert got[1].resource_id == "doc2"
        assert got[1].permissionship == Permissionship.CONDITIONAL_PERMISSION
        assert got[1].partial_caveat is not None
        assert got[1].partial_caveat.missing_required_context == ("ip_address",)


class TestLookupSubjectsWildcardSubjectExposesExcludedSubjects:
    """The key over-grant-fix assertion: when LookupSubjects resolves a
    wildcard "*" subject, the excluded_subjects the server attaches to that
    wildcard MUST be surfaced to the caller, since dropping them would make
    a wildcard match look like an unconditional grant to every subject
    including the excluded ones."""

    async def test_wildcard_excluded_subjects_surfaced(self, make_client):
        client = make_client()
        client._permissions.LookupSubjects = _async_stream(
            permission_service_pb2.LookupSubjectsResponse(looked_up_at=_core_tok.ZedToken(token="t"), 
                subject=permission_service_pb2.ResolvedSubject(
                    subject_object_id="*",
                    permissionship=permission_service_pb2.LOOKUP_PERMISSIONSHIP_HAS_PERMISSION,
                ),
                excluded_subjects=[
                    permission_service_pb2.ResolvedSubject(
                        subject_object_id="eve",
                        permissionship=permission_service_pb2.LOOKUP_PERMISSIONSHIP_HAS_PERMISSION,
                    ),
                    permission_service_pb2.ResolvedSubject(
                        subject_object_id="mallory",
                        permissionship=permission_service_pb2.LOOKUP_PERMISSIONSHIP_HAS_PERMISSION,
                    ),
                ],
            )
        )
        got = [
            s
            async for s in client.lookup_subjects(
                resource=ObjectRef("document", "doc1"),
                permission="view",
                subject_type="user",
                consistency=full(),
            )
        ]
        assert len(got) == 1
        assert got[0].subject == ResolvedSubject(
            subject_id="*",
            permissionship=Permissionship.HAS_PERMISSION,
            partial_caveat=None,
        )
        assert len(got[0].excluded_subjects) == 2
        excluded_ids = {e.subject_id for e in got[0].excluded_subjects}
        assert excluded_ids == {"eve", "mallory"}


class TestLookupSubjectsNonWildcardHasNoExcludedSubjects:
    """A plain (non-wildcard) match doesn't spuriously populate
    excluded_subjects."""

    async def test_no_excluded_subjects(self, make_client):
        client = make_client()
        client._permissions.LookupSubjects = _async_stream(
            permission_service_pb2.LookupSubjectsResponse(looked_up_at=_core_tok.ZedToken(token="t"), 
                subject=permission_service_pb2.ResolvedSubject(
                    subject_object_id="alice",
                    permissionship=permission_service_pb2.LOOKUP_PERMISSIONSHIP_HAS_PERMISSION,
                )
            )
        )
        got = [
            s
            async for s in client.lookup_subjects(
                resource=ObjectRef("document", "doc1"),
                permission="view",
                subject_type="user",
                consistency=full(),
            )
        ]
        assert len(got) == 1
        assert got[0].subject.subject_id == "alice"
        assert got[0].excluded_subjects == ()


class TestLookupSignatures:
    """lookup_resources/lookup_subjects must return native types, not bare
    strings (RB2: over-grant risk from dropped excluded_subjects/
    permissionship). `client.py` uses `from __future__ import annotations`,
    so annotations are unevaluated strings — check them directly."""

    def test_lookup_resources_returns_lookup_resource_iterator(self):
        sig = inspect.signature(SpiceDBClient.lookup_resources)
        assert sig.return_annotation == "AsyncStream[LookupResource]"

    def test_lookup_subjects_returns_lookup_subject_iterator(self):
        sig = inspect.signature(SpiceDBClient.lookup_subjects)
        assert sig.return_annotation == "AsyncStream[LookupSubject]"


class TestDeleteRelationships:
    """delete_relationships mirrors spicedb-go's DeleteRelationships
    (client/relationships.go) `WithDeleteMustMatch`/`WithDeleteMustNotMatch`/
    `WithDeleteLimit` options: optional keyword args build
    DeleteRelationshipsRequest.optional_preconditions/optional_limit. Additive
    — the no-kwargs call must keep sending the same request as before."""

    def _client(self, make_client) -> SpiceDBClient:
        client = make_client()
        response = permission_service_pb2.DeleteRelationshipsResponse(
            deleted_at=core_pb2.ZedToken(token="deadbeef"),
            deletion_progress=1,
            relationships_deleted_count=2,
        )
        client._permissions.DeleteRelationships = AsyncMock(return_value=response)
        return client

    async def test_no_options_preserves_default_behavior(self, make_client):
        client = self._client(make_client)
        f = Filter(resource_type="document", resource_id="1")
        revision = await client.delete_all_relationships_non_atomic(f)
        assert revision.revision == ZedToken("deadbeef")
        client._permissions.DeleteRelationships.assert_awaited_once()
        request = client._permissions.DeleteRelationships.await_args.args[0]
        assert request.relationship_filter == f._to_proto()
        assert list(request.optional_preconditions) == []
        assert request.optional_limit == 1000
        assert request.optional_allow_partial_deletions is True

    async def test_filter_subject_id_without_subject_type_raises_before_rpc(
        self, make_client
    ):
        client = self._client(make_client)
        with pytest.raises(InvalidArgumentError, match="subject_type"):
            await client.delete_relationships(
                Filter("document", subject=SubjectFilter("", subject_id="alice")),
                limit=10,
            )
        client._permissions.DeleteRelationships.assert_not_awaited()

    async def test_must_match_sets_precondition(self, make_client):
        client = self._client(make_client)
        f = Filter(resource_type="document", resource_id="1")
        guard = Filter(resource_type="document", resource_id="1", relation="owner")
        await client.delete_all_relationships_non_atomic(f, must_match=[guard])
        request = client._permissions.DeleteRelationships.await_args.args[0]
        assert list(request.optional_preconditions) == [
            permission_service_pb2.Precondition(
                operation=permission_service_pb2.Precondition.OPERATION_MUST_MATCH,
                filter=guard._to_proto(),
            )
        ]

    async def test_must_not_match_sets_precondition(self, make_client):
        client = self._client(make_client)
        f = Filter(resource_type="document", resource_id="1")
        guard = Filter(resource_type="document", resource_id="1", relation="banned")
        await client.delete_all_relationships_non_atomic(f, must_not_match=[guard])
        request = client._permissions.DeleteRelationships.await_args.args[0]
        assert list(request.optional_preconditions) == [
            permission_service_pb2.Precondition(
                operation=permission_service_pb2.Precondition.OPERATION_MUST_NOT_MATCH,
                filter=guard._to_proto(),
            )
        ]

    async def test_must_match_and_must_not_match_accumulate_in_order(self, make_client):
        client = self._client(make_client)
        f = Filter(resource_type="document", resource_id="1")
        match_guard = Filter(
            resource_type="document", resource_id="1", relation="owner"
        )
        not_match_guard = Filter(
            resource_type="document", resource_id="1", relation="banned"
        )
        await client.delete_all_relationships_non_atomic(
            f, must_match=[match_guard], must_not_match=[not_match_guard]
        )
        request = client._permissions.DeleteRelationships.await_args.args[0]
        assert list(request.optional_preconditions) == [
            permission_service_pb2.Precondition(
                operation=permission_service_pb2.Precondition.OPERATION_MUST_MATCH,
                filter=match_guard._to_proto(),
            ),
            permission_service_pb2.Precondition(
                operation=permission_service_pb2.Precondition.OPERATION_MUST_NOT_MATCH,
                filter=not_match_guard._to_proto(),
            ),
        ]

    async def test_limit_sets_optional_limit(self, make_client):
        client = self._client(make_client)
        f = Filter(resource_type="document", resource_id="1")
        await client.delete_relationships(f, limit=50)
        request = client._permissions.DeleteRelationships.await_args.args[0]
        assert request.optional_limit == 50
        assert request.optional_allow_partial_deletions is True

    async def test_limit_none_is_rejected(self, make_client):
        client = self._client(make_client)
        with pytest.raises(InvalidArgumentError, match="limit"):
            await client.delete_relationships(Filter("document"), limit=None)
        client._permissions.DeleteRelationships.assert_not_awaited()

    async def test_auto_page_false_makes_a_single_call_on_partial_progress(
        self, make_client
    ):
        """Default behavior (auto_page=False) must be unchanged: even a
        PARTIAL response is returned as-is, with no follow-up call."""
        client = make_client()
        response = permission_service_pb2.DeleteRelationshipsResponse(
            deleted_at=core_pb2.ZedToken(token="page1"),
            deletion_progress=permission_service_pb2.DeleteRelationshipsResponse.DELETION_PROGRESS_PARTIAL,
        )
        client._permissions.DeleteRelationships = AsyncMock(return_value=response)
        f = Filter(resource_type="document", resource_id="1")
        revision = await client.delete_relationships(f, limit=10)
        assert revision.revision == ZedToken("page1")
        client._permissions.DeleteRelationships.assert_awaited_once()

    async def test_auto_page_loops_until_complete_using_cursor(self, make_client):
        client = make_client()
        cursor_a = core_pb2.Cursor(token="a")
        responses = [
            permission_service_pb2.DeleteRelationshipsResponse(
                deleted_at=core_pb2.ZedToken(token="page1"),
                deletion_progress=permission_service_pb2.DeleteRelationshipsResponse.DELETION_PROGRESS_PARTIAL,
                after_result_cursor=cursor_a,
            ),
            permission_service_pb2.DeleteRelationshipsResponse(
                deleted_at=core_pb2.ZedToken(token="page2"),
                deletion_progress=permission_service_pb2.DeleteRelationshipsResponse.DELETION_PROGRESS_COMPLETE,
            ),
        ]
        client._permissions.DeleteRelationships = AsyncMock(side_effect=responses)
        f = Filter(resource_type="document", resource_id="1")
        revision = await client.delete_all_relationships_non_atomic(f, page_size=5)
        assert revision.revision == ZedToken("page2")
        assert client._permissions.DeleteRelationships.await_count == 2
        first_request = client._permissions.DeleteRelationships.await_args_list[0].args[
            0
        ]
        second_request = client._permissions.DeleteRelationships.await_args_list[
            1
        ].args[0]
        assert first_request.optional_limit == 5
        assert first_request.optional_allow_partial_deletions is True
        assert not first_request.HasField("optional_cursor")
        assert second_request.optional_cursor == cursor_a

    async def test_auto_page_defaults_page_size_when_no_limit_given(self, make_client):
        client = self._client(make_client)
        f = Filter(resource_type="document", resource_id="1")
        await client.delete_all_relationships_non_atomic(f)
        request = client._permissions.DeleteRelationships.await_args.args[0]
        assert request.optional_limit == 1000
        assert request.optional_allow_partial_deletions is True


def _rel_response(doc_id: str, cursor_token: str):
    return permission_service_pb2.ReadRelationshipsResponse(read_at=_core_tok.ZedToken(token="t"), 
        relationship=core_pb2.Relationship(
            resource=core_pb2.ObjectReference(object_type="document", object_id=doc_id),
            relation="viewer",
            subject=core_pb2.SubjectReference(
                object=core_pb2.ObjectReference(object_type="user", object_id="alice")
            ),
        ),
        after_result_cursor=core_pb2.Cursor(token=cursor_token),
    )


class TestReadRelationshipsEstablishmentRetry:
    async def test_retries_establishment_on_first_open_transient_error(
        self, make_client
    ):
        client = make_client()
        client._permissions.ReadRelationships = _stream_open_fails_then_succeeds(
            _rel_response("doc1", "c1")
        )
        got = [
            r
            async for r in client.read_relationships(
                Filter(resource_type="document"), consistency=full()
            )
        ]
        assert [r.relationship.resource.object_id for r in got] == ["doc1"]
        assert len(client._permissions.ReadRelationships.calls) == 2

    async def test_transient_error_after_yielding_is_not_retried(self, make_client):
        client = make_client()
        client._permissions.ReadRelationships = _stream_fails_after_yielding(
            _rel_response("doc1", "c1")
        )
        got = []
        with pytest.raises(UnavailableError):
            async for r in client.read_relationships(
                Filter(resource_type="document"), consistency=full()
            ):
                got.append(r)
        assert [r.relationship.resource.object_id for r in got] == ["doc1"]
        assert len(client._permissions.ReadRelationships.calls) == 1


class TestLookupResourcesEstablishmentRetry:
    def _response(self, doc_id: str, cursor_token: str):
        return permission_service_pb2.LookupResourcesResponse(looked_up_at=_core_tok.ZedToken(token="t"), 
            resource_object_id=doc_id,
            permissionship=permission_service_pb2.LOOKUP_PERMISSIONSHIP_HAS_PERMISSION,
            after_result_cursor=core_pb2.Cursor(token=cursor_token),
        )

    async def test_retries_establishment_on_first_open_transient_error(
        self, make_client
    ):
        client = make_client()
        client._permissions.LookupResources = _stream_open_fails_then_succeeds(
            self._response("doc1", "c1")
        )
        got = [
            r
            async for r in client.lookup_resources(
                resource_type="document",
                permission="view",
                subject=_subject("user:alice", ""),
                consistency=full(),
            )
        ]
        assert [r.resource_id for r in got] == ["doc1"]
        assert len(client._permissions.LookupResources.calls) == 2

    async def test_transient_error_after_yielding_is_not_retried(self, make_client):
        client = make_client()
        client._permissions.LookupResources = _stream_fails_after_yielding(
            self._response("doc1", "c1")
        )
        got = []
        with pytest.raises(UnavailableError):
            async for r in client.lookup_resources(
                resource_type="document",
                permission="view",
                subject=_subject("user:alice", ""),
                consistency=full(),
            ):
                got.append(r)
        assert [r.resource_id for r in got] == ["doc1"]
        assert len(client._permissions.LookupResources.calls) == 1


class TestLookupSubjectsEstablishmentRetry:
    def _response(self, subject_id: str):
        return permission_service_pb2.LookupSubjectsResponse(looked_up_at=_core_tok.ZedToken(token="t"), 
            subject=permission_service_pb2.ResolvedSubject(
                subject_object_id=subject_id,
                permissionship=permission_service_pb2.LOOKUP_PERMISSIONSHIP_HAS_PERMISSION,
            )
        )

    async def test_retries_establishment_on_first_open_transient_error(
        self, make_client
    ):
        client = make_client()
        client._permissions.LookupSubjects = _stream_open_fails_then_succeeds(
            self._response("alice")
        )
        got = [
            s
            async for s in client.lookup_subjects(
                resource=ObjectRef("document", "doc1"),
                permission="view",
                subject_type="user",
                consistency=full(),
            )
        ]
        assert [s.subject.subject_id for s in got] == ["alice"]
        assert len(client._permissions.LookupSubjects.calls) == 2

    async def test_transient_error_after_yielding_is_not_retried(self, make_client):
        client = make_client()
        client._permissions.LookupSubjects = _stream_fails_after_yielding(
            self._response("alice")
        )
        got = []
        with pytest.raises(UnavailableError):
            async for s in client.lookup_subjects(
                resource=ObjectRef("document", "doc1"),
                permission="view",
                subject_type="user",
                consistency=full(),
            ):
                got.append(s)
        assert [s.subject.subject_id for s in got] == ["alice"]
        assert len(client._permissions.LookupSubjects.calls) == 1


class TestWatchEstablishmentRetry:
    def _response(self, token: str, *, is_checkpoint: bool = False):
        return watch_service_pb2.WatchResponse(
            updates=[],
            changes_through=core_pb2.ZedToken(token=token),
            is_checkpoint=is_checkpoint,
        )

    async def test_watch_never_reconnects(self, make_client):
        client = make_client()
        client._watch.Watch = _stream_open_fails_then_succeeds(self._response("rev"))
        with pytest.raises(UnavailableError):
            async for event in client.watch():
                pass
        assert len(client._watch.Watch.calls) == 1

    async def test_checkpoint_event_distinguishable_from_update_event(
        self, make_client
    ):
        client = make_client()
        client._watch.Watch = _async_stream(
            self._response("checkpoint-rev", is_checkpoint=True)
        )
        [event] = [event async for event in client.watch(content=(WatchContent.RELATIONSHIPS, WatchContent.CHECKPOINTS))]
        assert event.is_checkpoint is True
        assert event.updates == ()
        assert event.changes_through == ZedToken("checkpoint-rev")


class TestExportRelationshipsEstablishmentRetry:
    def _response(self, doc_id: str, cursor_token: str):
        return permission_service_pb2.ExportBulkRelationshipsResponse(
            relationships=[
                core_pb2.Relationship(
                    resource=core_pb2.ObjectReference(
                        object_type="document", object_id=doc_id
                    ),
                    relation="viewer",
                    subject=core_pb2.SubjectReference(
                        object=core_pb2.ObjectReference(
                            object_type="user", object_id="alice"
                        )
                    ),
                )
            ],
            after_result_cursor=core_pb2.Cursor(token=cursor_token),
        )

    async def test_retries_establishment_on_first_open_transient_error(
        self, make_client
    ):
        client = make_client()
        client._permissions.ExportBulkRelationships = _stream_open_fails_then_succeeds(
            self._response("doc1", "c1")
        )
        got = [r async for r in client.export_relationships(consistency=full())]
        assert [r.relationship.resource.object_id for r in got] == ["doc1"]
        assert len(client._permissions.ExportBulkRelationships.calls) == 2

    async def test_transient_error_after_yielding_is_not_retried(self, make_client):
        client = make_client()
        client._permissions.ExportBulkRelationships = _stream_fails_after_yielding(
            self._response("doc1", "c1")
        )
        got = []
        with pytest.raises(UnavailableError):
            async for r in client.export_relationships(consistency=full()):
                got.append(r)
        assert [r.relationship.resource.object_id for r in got] == ["doc1"]
        assert len(client._permissions.ExportBulkRelationships.calls) == 1


def test_construction_outside_a_loop_does_not_raise():
    """The reported failure: build once at startup, no loop running."""
    client = SpiceDBClient("localhost:50051", token="t", transport_security=TransportSecurity.PLAINTEXT)
    assert client is not None


def test_construction_opens_no_channel():
    client = SpiceDBClient("localhost:50051", token="t", transport_security=TransportSecurity.PLAINTEXT)
    assert client._channel is None, "channel must bind lazily, at first use"


def test_close_before_any_call_is_a_noop():
    client = SpiceDBClient("localhost:50051", token="t", transport_security=TransportSecurity.PLAINTEXT)
    asyncio.run(client.close())


def test_reusing_a_client_across_event_loops_raises_a_clear_error():
    """asyncio.run() per call is exactly what the bug report did."""
    client = SpiceDBClient("localhost:50051", token="t", transport_security=TransportSecurity.PLAINTEXT)

    async def _bind():
        client._ensure_channel()

    asyncio.run(_bind())

    async def _use_again():
        client._ensure_channel()

    with pytest.raises(EventLoopBindingError) as excinfo:
        asyncio.run(_use_again())
    msg = str(excinfo.value)
    assert "one client per event loop" in msg, "the primary fix is a client per loop"
    assert "spicedb.sync" in msg, "synchronous callers are still pointed at the sync client"
    assert "if the calling code is synchronous" in msg, "the sync client is conditional advice"
    asyncio.run(client.close())


async def test_read_transient_error_is_retried_then_succeeds(make_client):
    from authzed.api.v1 import schema_service_pb2 as ssp

    client = make_client(retry_policy=RetryPolicy(max_retries=2))
    ok = ssp.ReadSchemaResponse(read_at=_core_tok.ZedToken(token="t"), schema_text="definition user {}")
    client._schema.ReadSchema = AsyncMock(side_effect=[_transient_error(), ok])
    result = await client.read_schema()
    assert result.schema == "definition user {}"
    assert client._schema.ReadSchema.await_count == 2, (
        "a transient error must be retried"
    )


async def test_mutation_transient_error_is_attempted_once_only(make_client):
    from spicedb import Transaction

    client = make_client(retry_policy=RetryPolicy(max_retries=3))
    client._permissions.WriteRelationships = AsyncMock(side_effect=_transient_error())
    txn = Transaction()
    txn.create(Relationship.from_triple("document:a", "viewer", "user:jimmy"))
    with pytest.raises(UnavailableError):
        await client.write(txn)
    assert client._permissions.WriteRelationships.await_count == 1, (
        "mutations must be attempted exactly once"
    )


async def test_resource_exhausted_is_never_retried(make_client):
    from spicedb.errors import ResourceExhaustedError

    client = make_client(retry_policy=RetryPolicy(max_retries=3))
    client._schema.ReadSchema = AsyncMock(
        side_effect=_transient_error(grpc.StatusCode.RESOURCE_EXHAUSTED)
    )
    with pytest.raises(ResourceExhaustedError):
        await client.read_schema()
    assert client._schema.ReadSchema.await_count == 1, (
        "RESOURCE_EXHAUSTED must never be retried"
    )


def test_backoff_has_jitter():
    """Backoff must vary between runs -- assert the jitter exists, not an
    exact value. Without jitter every client in a fleet retries on the
    same schedule after a server restart (thundering herd).

    `_backoff_seconds` reads only the client's retry policy, so this does
    not need a channel."""
    client = SpiceDBClient("localhost:50051", token="t", transport_security=TransportSecurity.PLAINTEXT)
    seen = {client._backoff_seconds(client._retry_policy, 2) for _ in range(50)}
    assert len(seen) > 1, "backoff should vary between calls"
    assert all((0 <= v <= 0.4 for v in seen))


def _check(value):
    if isinstance(value, PermissionCheck):
        return value
    return PermissionCheck(value.resource, value.relation, value.subject)


def _aggregate(values, every=False):
    return bool(values) and (all(values) if every else any(values))


def _subject(value, relation=None):
    typ, identifier = value.split(":", 1)
    return SubjectRef(ObjectRef(typ, identifier), relation or None)


def _echo_response(response, request):
    response = type(response).FromString(response.SerializeToString())
    for pair, item in zip(response.pairs, request.items):
        pair.request.CopyFrom(item)
    return response
