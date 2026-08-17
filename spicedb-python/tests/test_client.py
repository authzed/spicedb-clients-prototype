"""Unit tests for SpiceDBClient construction — no SpiceDB instance needed."""

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
        c = SpiceDBClient("localhost:50051", token="testtoken", insecure=True, **kw)
        c._ensure_channel()
        created.append(c)
        return c

    yield _make
    for c in created:
        await c.close()


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
        return _gen()

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
        return _gen()

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
        return _gen()

    _call.calls = []
    return _call


async def test_constructor_insecure_opens_channel_on_ensure(make_client):
    """The channel binds lazily -- exercise `_ensure_channel()` explicitly to
    confirm the insecure code path still produces a real channel."""
    c = make_client()
    assert c._channel is not None


async def test_context_manager():
    async with SpiceDBClient("localhost:50051", token="testtoken", insecure=True) as c:
        c._ensure_channel()
        assert c._channel is not None


class TestSchemaReflectionSignatures:
    """`reflect_schema`/`diff_schema` must return native types, not proto
    (NOT-1: no proto in the public API). `client.py` uses
    `from __future__ import annotations`, so annotations are unevaluated
    strings — check them directly rather than importing proto types here.
    """

    def test_reflect_schema_returns_native_result(self):
        sig = inspect.signature(SpiceDBClient.reflect_schema)
        assert sig.return_annotation == "ReflectSchemaResult"

    def test_diff_schema_returns_native_list(self):
        sig = inspect.signature(SpiceDBClient.diff_schema)
        assert sig.return_annotation == "list[SchemaDiff]"

    def test_computable_permissions_returns_native_list(self):
        sig = inspect.signature(SpiceDBClient.computable_permissions)
        assert sig.return_annotation == "list[RelationReference]"

    def test_dependent_relations_returns_native_list(self):
        sig = inspect.signature(SpiceDBClient.dependent_relations)
        assert sig.return_annotation == "list[RelationReference]"


class TestComputablePermissions:
    """`computable_permissions` mirrors spicedb-go's ComputablePermissions
    (client/schema.go): request {consistency, definition_name, relation_name},
    response .permissions (list of RelationReference) + .read_at."""

    async def test_maps_permissions_field_by_field(self, make_client):
        client = make_client()
        response = schema_service_pb2.ComputablePermissionsResponse(
            permissions=[
                schema_service_pb2.ReflectionRelationReference(
                    definition_name="document",
                    relation_name="view",
                    is_permission=True,
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

        result = await client.computable_permissions(full(), "document", "viewer")

        assert result == [
            RelationReference(
                definition_name="document", relation_name="view", is_permission=True
            ),
            RelationReference(
                definition_name="document",
                relation_name="view_and_edit",
                is_permission=True,
            ),
        ]

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

        result = await client.dependent_relations(full(), "document", "view")

        assert result == [
            RelationReference(
                definition_name="document", relation_name="viewer", is_permission=False
            ),
            RelationReference(
                definition_name="document", relation_name="editor", is_permission=False
            ),
        ]

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
        response = permission_service_pb2.CheckBulkPermissionsResponse(
            checked_at=core_pb2.ZedToken(token="deadbeef"),
            pairs=[
                permission_service_pb2.CheckBulkPermissionsPair(
                    item=permission_service_pb2.CheckBulkPermissionsResponseItem(
                        permissionship=permission_service_pb2.CheckPermissionResponse.PERMISSIONSHIP_HAS_PERMISSION
                    )
                )
            ],
        )
        client._permissions.CheckBulkPermissions = AsyncMock(return_value=response)

        rel = Relationship.from_triple("document:readme", "view", "user:alice")
        result = await client.check_permission(full(), rel)

        assert result == CheckResult(
            permissionship=Permissionship.HAS_PERMISSION,
            missing_context=[],
            checked_at="deadbeef",
        )
        assert result.has_permission is True

    async def test_check_permission_conditional_has_permission_false(
        self, make_client
    ):
        client = make_client()
        response = permission_service_pb2.CheckBulkPermissionsResponse(
            checked_at=core_pb2.ZedToken(token="deadbeef"),
            pairs=[
                permission_service_pb2.CheckBulkPermissionsPair(
                    item=permission_service_pb2.CheckBulkPermissionsResponseItem(
                        permissionship=permission_service_pb2.CheckPermissionResponse.PERMISSIONSHIP_CONDITIONAL_PERMISSION,
                        partial_caveat_info=core_pb2.PartialCaveatInfo(
                            missing_required_context=["now"]
                        ),
                    )
                )
            ],
        )
        client._permissions.CheckBulkPermissions = AsyncMock(return_value=response)

        rel = Relationship.from_triple("document:readme", "conditional_view", "user:alice")
        result = await client.check_permission(full(), rel)

        assert result.permissionship == Permissionship.CONDITIONAL_PERMISSION
        assert result.has_permission is False
        assert result.missing_context == ["now"]

    async def test_check_any_and_check_all_do_not_count_conditional(
        self, make_client
    ):
        """check_any/check_all stay boolean but must count ONLY
        HasPermission -- a Conditional result is not a grant, so it must not
        flip either to True."""
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
        client._permissions.CheckBulkPermissions = AsyncMock(return_value=response)

        rel = Relationship.from_triple("document:readme", "conditional_view", "user:alice")
        assert await client.check_any(full(), rel) is False
        assert await client.check_all(full(), rel) is False


class TestBulkCheckPerItemErrorFidelity:
    """check_permissions must surface the real per-item google.rpc.Status
    from a CheckBulkPermissions pair as a typed SpiceDBError, not fabricate
    a generic INTERNAL error (CI-2)."""

    async def test_per_item_invalid_argument_surfaces_as_typed_error(self, make_client):
        client = make_client()
        pair = permission_service_pb2.CheckBulkPermissionsPair(
            error=status_pb2.Status(code=3, message="bad item"),  # INVALID_ARGUMENT
        )
        response = permission_service_pb2.CheckBulkPermissionsResponse(pairs=[pair])
        client._permissions.CheckBulkPermissions = AsyncMock(return_value=response)

        rel = Relationship(
            resource_type="document",
            resource_id="1",
            resource_relation="view",
            subject_type="user",
            subject_id="alice",
        )

        with pytest.raises(InvalidArgumentError) as exc_info:
            await client.check_permissions(full(), rel)

        # Must not be downgraded to a generic base SpiceDBError (e.g. the
        # old fabricated INTERNAL path).
        assert type(exc_info.value) is not SpiceDBError
        assert str(exc_info.value) == "bad item"


# ── Lookup: rich result types ───────────────────────────────────────────
#
# Mirrors spicedb-go's client/lookup_test.go: LookupResources/LookupSubjects
# must yield native LookupResource/LookupSubject structs (permissionship,
# partial caveat info, and — the over-grant-risk fix — excluded_subjects for
# wildcard "*" matches) instead of bare ID strings.


class TestLookupResourcesYieldsPermissionshipAndPartialCaveat:
    """LookupResources surfaces the proto's permissionship and partial
    caveat info instead of dropping them, so a CONDITIONAL match is
    distinguishable from a full HAS_PERMISSION grant."""

    async def test_maps_has_and_conditional_results(self, make_client):
        client = make_client()
        client._permissions.LookupResources = _async_stream(
            permission_service_pb2.LookupResourcesResponse(
                resource_object_id="doc1",
                permissionship=permission_service_pb2.LOOKUP_PERMISSIONSHIP_HAS_PERMISSION,
            ),
            permission_service_pb2.LookupResourcesResponse(
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
                "document", "view", ("user:alice", ""), full()
            )
        ]

        assert len(got) == 2
        assert got[0] == LookupResource(
            resource_id="doc1",
            permissionship=Permissionship.HAS_PERMISSION,
            partial_caveat=None,
        )
        assert got[1].resource_id == "doc2"
        assert got[1].permissionship == Permissionship.CONDITIONAL_PERMISSION
        assert got[1].partial_caveat is not None
        assert got[1].partial_caveat.missing_required_context == ["ip_address"]


class TestLookupSubjectsWildcardSubjectExposesExcludedSubjects:
    """The key over-grant-fix assertion: when LookupSubjects resolves a
    wildcard "*" subject, the excluded_subjects the server attaches to that
    wildcard MUST be surfaced to the caller, since dropping them would make
    a wildcard match look like an unconditional grant to every subject
    including the excluded ones."""

    async def test_wildcard_excluded_subjects_surfaced(self, make_client):
        client = make_client()
        client._permissions.LookupSubjects = _async_stream(
            permission_service_pb2.LookupSubjectsResponse(
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
                ("document", "doc1"), "view", "user", full()
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
            permission_service_pb2.LookupSubjectsResponse(
                subject=permission_service_pb2.ResolvedSubject(
                    subject_object_id="alice",
                    permissionship=permission_service_pb2.LOOKUP_PERMISSIONSHIP_HAS_PERMISSION,
                ),
            )
        )

        got = [
            s
            async for s in client.lookup_subjects(
                ("document", "doc1"), "view", "user", full()
            )
        ]

        assert len(got) == 1
        assert got[0].subject.subject_id == "alice"
        assert got[0].excluded_subjects == []


class TestLookupSignatures:
    """lookup_resources/lookup_subjects must return native types, not bare
    strings (RB2: over-grant risk from dropped excluded_subjects/
    permissionship). `client.py` uses `from __future__ import annotations`,
    so annotations are unevaluated strings — check them directly."""

    def test_lookup_resources_returns_lookup_resource_iterator(self):
        sig = inspect.signature(SpiceDBClient.lookup_resources)
        assert sig.return_annotation == "AsyncIterator[LookupResource]"

    def test_lookup_subjects_returns_lookup_subject_iterator(self):
        sig = inspect.signature(SpiceDBClient.lookup_subjects)
        assert sig.return_annotation == "AsyncIterator[LookupSubject]"


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
        )
        client._permissions.DeleteRelationships = AsyncMock(return_value=response)
        return client

    async def test_no_options_preserves_default_behavior(self, make_client):
        client = self._client(make_client)
        f = Filter(resource_type="document", resource_id="1")

        revision = await client.delete_relationships(f)

        assert revision == "deadbeef"
        client._permissions.DeleteRelationships.assert_awaited_once()
        request = client._permissions.DeleteRelationships.await_args.args[0]
        assert request.relationship_filter == f._to_proto()
        assert list(request.optional_preconditions) == []
        assert request.optional_limit == 0
        assert request.optional_allow_partial_deletions is False

    async def test_must_match_sets_precondition(self, make_client):
        client = self._client(make_client)
        f = Filter(resource_type="document", resource_id="1")
        guard = Filter(resource_type="document", resource_id="1", relation="owner")

        await client.delete_relationships(f, must_match=[guard])

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

        await client.delete_relationships(f, must_not_match=[guard])

        request = client._permissions.DeleteRelationships.await_args.args[0]
        assert list(request.optional_preconditions) == [
            permission_service_pb2.Precondition(
                operation=permission_service_pb2.Precondition.OPERATION_MUST_NOT_MATCH,
                filter=guard._to_proto(),
            )
        ]

    async def test_must_match_and_must_not_match_accumulate_in_order(
        self, make_client
    ):
        client = self._client(make_client)
        f = Filter(resource_type="document", resource_id="1")
        match_guard = Filter(
            resource_type="document", resource_id="1", relation="owner"
        )
        not_match_guard = Filter(
            resource_type="document", resource_id="1", relation="banned"
        )

        await client.delete_relationships(
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
        # The server rejects a limited delete outright if more relationships
        # match than the limit unless partial deletions are allowed — so
        # supplying `limit` must also flip this on, or `limit` would only
        # ever work when the caller already knows the exact match count.
        assert request.optional_allow_partial_deletions is True

    async def test_limit_none_leaves_optional_limit_unset(self, make_client):
        client = self._client(make_client)
        f = Filter(resource_type="document", resource_id="1")

        await client.delete_relationships(f, limit=None)

        request = client._permissions.DeleteRelationships.await_args.args[0]
        assert request.optional_limit == 0
        assert request.optional_allow_partial_deletions is False


# ── Streaming establishment retry (RB4) ─────────────────────────────────
#
# DESIGN.md mandates automatic retry for transient errors with no
# streaming carve-out. The streaming methods must retry stream/page
# ESTABLISHMENT on a transient error, but must NEVER retry once any item
# has been yielded from the current stream/page — doing so would
# replay/duplicate items for the caller. Each class below asserts both
# halves: establishment retry succeeds, and a post-yield transient error
# is surfaced as-is (not retried, not replayed).


def _rel_response(doc_id: str, cursor_token: str):
    return permission_service_pb2.ReadRelationshipsResponse(
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
            _rel_response("doc1", "c1"),
        )

        got = [
            r
            async for r in client.read_relationships(
                Filter(resource_type="document"), full()
            )
        ]

        assert [r.resource_id for r in got] == ["doc1"]
        # One failed open + one successful open == 2 calls to the stub.
        assert len(client._permissions.ReadRelationships.calls) == 2

    async def test_transient_error_after_yielding_is_not_retried(self, make_client):
        client = make_client()
        client._permissions.ReadRelationships = _stream_fails_after_yielding(
            _rel_response("doc1", "c1"),
        )

        got = []
        with pytest.raises(UnavailableError):
            async for r in client.read_relationships(
                Filter(resource_type="document"), full()
            ):
                got.append(r)

        # The item that streamed before the failure was yielded exactly
        # once — a retry here would have replayed it.
        assert [r.resource_id for r in got] == ["doc1"]
        assert len(client._permissions.ReadRelationships.calls) == 1


class TestLookupResourcesEstablishmentRetry:
    def _response(self, doc_id: str, cursor_token: str):
        return permission_service_pb2.LookupResourcesResponse(
            resource_object_id=doc_id,
            permissionship=permission_service_pb2.LOOKUP_PERMISSIONSHIP_HAS_PERMISSION,
            after_result_cursor=core_pb2.Cursor(token=cursor_token),
        )

    async def test_retries_establishment_on_first_open_transient_error(
        self, make_client
    ):
        client = make_client()
        client._permissions.LookupResources = _stream_open_fails_then_succeeds(
            self._response("doc1", "c1"),
        )

        got = [
            r
            async for r in client.lookup_resources(
                "document", "view", ("user:alice", ""), full()
            )
        ]

        assert [r.resource_id for r in got] == ["doc1"]
        assert len(client._permissions.LookupResources.calls) == 2

    async def test_transient_error_after_yielding_is_not_retried(self, make_client):
        client = make_client()
        client._permissions.LookupResources = _stream_fails_after_yielding(
            self._response("doc1", "c1"),
        )

        got = []
        with pytest.raises(UnavailableError):
            async for r in client.lookup_resources(
                "document", "view", ("user:alice", ""), full()
            ):
                got.append(r)

        assert [r.resource_id for r in got] == ["doc1"]
        assert len(client._permissions.LookupResources.calls) == 1


class TestLookupSubjectsEstablishmentRetry:
    def _response(self, subject_id: str):
        return permission_service_pb2.LookupSubjectsResponse(
            subject=permission_service_pb2.ResolvedSubject(
                subject_object_id=subject_id,
                permissionship=permission_service_pb2.LOOKUP_PERMISSIONSHIP_HAS_PERMISSION,
            ),
        )

    async def test_retries_establishment_on_first_open_transient_error(
        self, make_client
    ):
        client = make_client()
        client._permissions.LookupSubjects = _stream_open_fails_then_succeeds(
            self._response("alice"),
        )

        got = [
            s
            async for s in client.lookup_subjects(
                ("document", "doc1"), "view", "user", full()
            )
        ]

        assert [s.subject.subject_id for s in got] == ["alice"]
        assert len(client._permissions.LookupSubjects.calls) == 2

    async def test_transient_error_after_yielding_is_not_retried(self, make_client):
        client = make_client()
        client._permissions.LookupSubjects = _stream_fails_after_yielding(
            self._response("alice"),
        )

        got = []
        with pytest.raises(UnavailableError):
            async for s in client.lookup_subjects(
                ("document", "doc1"), "view", "user", full()
            ):
                got.append(s)

        assert [s.subject.subject_id for s in got] == ["alice"]
        assert len(client._permissions.LookupSubjects.calls) == 1


class TestWatchEstablishmentRetry:
    def _response(self, token: str):
        return watch_service_pb2.WatchResponse(
            updates=[],
            changes_through=core_pb2.ZedToken(token=token),
        )

    async def test_retries_establishment_on_first_open_transient_error(
        self, make_client
    ):
        client = make_client()
        client._watch.Watch = _stream_open_fails_then_succeeds(
            self._response("rev1"),
        )

        got = [rev async for _updates, rev in client.watch()]

        assert got == ["rev1"]
        assert len(client._watch.Watch.calls) == 2

    async def test_transient_error_after_yielding_is_not_retried(self, make_client):
        client = make_client()
        client._watch.Watch = _stream_fails_after_yielding(
            self._response("rev1"),
        )

        got = []
        with pytest.raises(UnavailableError):
            async for _updates, rev in client.watch():
                got.append(rev)

        assert got == ["rev1"]
        assert len(client._watch.Watch.calls) == 1


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
            self._response("doc1", "c1"),
        )

        got = [r async for r in client.export_relationships(full())]

        assert [r.resource_id for r in got] == ["doc1"]
        assert len(client._permissions.ExportBulkRelationships.calls) == 2

    async def test_transient_error_after_yielding_is_not_retried(self, make_client):
        client = make_client()
        client._permissions.ExportBulkRelationships = _stream_fails_after_yielding(
            self._response("doc1", "c1"),
        )

        got = []
        with pytest.raises(UnavailableError):
            async for r in client.export_relationships(full()):
                got.append(r)

        assert [r.resource_id for r in got] == ["doc1"]
        assert len(client._permissions.ExportBulkRelationships.calls) == 1


# ── Lazy channel binding (D6) ───────────────────────────────────────────
#
# The reported bug: a client built once at startup (no running loop), then
# driven by `asyncio.run(...)` per call. A `grpc.aio` channel binds to the
# event loop present when it is constructed, so building eagerly in
# `__init__` breaks the very "build once, reuse" pattern callers expect from
# every other client. The channel must instead open on first *use*, and
# reuse from a second loop must raise a typed, actionable error rather than
# an opaque grpc/asyncio failure.


def test_construction_outside_a_loop_does_not_raise():
    """The reported failure: build once at startup, no loop running."""
    client = SpiceDBClient("localhost:50051", token="t", insecure=True)
    assert client is not None


def test_construction_opens_no_channel():
    client = SpiceDBClient("localhost:50051", token="t", insecure=True)
    assert client._channel is None, "channel must bind lazily, at first use"


def test_close_before_any_call_is_a_noop():
    client = SpiceDBClient("localhost:50051", token="t", insecure=True)
    asyncio.run(client.close())  # must not raise


def test_reusing_a_client_across_event_loops_raises_a_clear_error():
    """asyncio.run() per call is exactly what the bug report did."""
    client = SpiceDBClient("localhost:50051", token="t", insecure=True)

    async def _bind():
        client._ensure_channel()

    asyncio.run(_bind())          # binds to loop #1, which then closes

    async def _use_again():
        client._ensure_channel()  # loop #2

    with pytest.raises(EventLoopBindingError) as excinfo:
        asyncio.run(_use_again())

    msg = str(excinfo.value)
    assert "spicedb.sync" in msg, "the error must point at the sync client"
    assert "event loop" in msg.lower()

    # Clean up the channel bound to loop #1 -- by this point the assertions
    # above have already fired, so closing here cannot affect what the test
    # verifies. Left open, the abandoned grpc.aio channel's GC teardown
    # produces a ResourceWarning: unclosed event loop.
    asyncio.run(client.close())
