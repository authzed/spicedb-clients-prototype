"""Unit tests for SpiceDBClient construction — no SpiceDB instance needed."""

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

from spicedb import Filter, Relationship, RelationReference, SpiceDBClient, full
from spicedb.errors import InvalidArgumentError, SpiceDBError, UnavailableError
from spicedb.types import LookupResource, Permissionship, ResolvedSubject


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


def _transient_error(code: grpc.StatusCode = grpc.StatusCode.UNAVAILABLE) -> grpc.aio.AioRpcError:
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


def test_constructor_insecure():
    c = SpiceDBClient("localhost:50051", token="testtoken", insecure=True)
    assert c._channel is not None


async def test_context_manager():
    async with SpiceDBClient(
        "localhost:50051", token="testtoken", insecure=True
    ) as c:
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

    async def test_maps_permissions_field_by_field(self):
        client = SpiceDBClient("localhost:50051", token="testtoken", insecure=True)
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

    async def test_maps_relations_field_by_field(self):
        client = SpiceDBClient("localhost:50051", token="testtoken", insecure=True)
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


class TestBulkCheckPerItemErrorFidelity:
    """check_permissions must surface the real per-item google.rpc.Status
    from a CheckBulkPermissions pair as a typed SpiceDBError, not fabricate
    a generic INTERNAL error (CI-2)."""

    async def test_per_item_invalid_argument_surfaces_as_typed_error(self):
        client = SpiceDBClient("localhost:50051", token="testtoken", insecure=True)
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

    async def test_maps_has_and_conditional_results(self):
        client = SpiceDBClient("localhost:50051", token="testtoken", insecure=True)
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

    async def test_wildcard_excluded_subjects_surfaced(self):
        client = SpiceDBClient("localhost:50051", token="testtoken", insecure=True)
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
            subject_id="*", permissionship=Permissionship.HAS_PERMISSION, partial_caveat=None
        )
        assert len(got[0].excluded_subjects) == 2
        excluded_ids = {e.subject_id for e in got[0].excluded_subjects}
        assert excluded_ids == {"eve", "mallory"}


class TestLookupSubjectsNonWildcardHasNoExcludedSubjects:
    """A plain (non-wildcard) match doesn't spuriously populate
    excluded_subjects."""

    async def test_no_excluded_subjects(self):
        client = SpiceDBClient("localhost:50051", token="testtoken", insecure=True)
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


class TestLookupSubjectsFallsBackToDeprecatedFields:
    """When a server only populates the deprecated top-level
    subject_object_id (leaving the non-deprecated `subject` field unset),
    the client still surfaces a usable ResolvedSubject rather than an empty
    subject_id."""

    async def test_falls_back_to_deprecated_subject_fields(self):
        client = SpiceDBClient("localhost:50051", token="testtoken", insecure=True)
        client._permissions.LookupSubjects = _async_stream(
            permission_service_pb2.LookupSubjectsResponse(
                subject_object_id="bob",
                permissionship=permission_service_pb2.LOOKUP_PERMISSIONSHIP_HAS_PERMISSION,
            )
        )

        got = [
            s
            async for s in client.lookup_subjects(
                ("document", "doc1"), "view", "user", full()
            )
        ]

        assert len(got) == 1
        assert got[0].subject.subject_id == "bob"
        assert got[0].subject.permissionship == Permissionship.HAS_PERMISSION


class TestLookupSubjectsExcludedSubjectsFallsBackToDeprecatedIds:
    """When a server populates a wildcard "*" match's exclusions ONLY via
    the deprecated top-level excluded_subject_ids field (leaving the
    non-deprecated excluded_subjects list empty), the client still surfaces
    those exclusions as ResolvedSubjects. This is security-relevant:
    dropping exclusions from an older-wire-format server would silently
    over-grant access to the excluded subjects."""

    async def test_falls_back_to_deprecated_excluded_ids(self):
        client = SpiceDBClient("localhost:50051", token="testtoken", insecure=True)
        client._permissions.LookupSubjects = _async_stream(
            permission_service_pb2.LookupSubjectsResponse(
                subject=permission_service_pb2.ResolvedSubject(
                    subject_object_id="*",
                    permissionship=permission_service_pb2.LOOKUP_PERMISSIONSHIP_HAS_PERMISSION,
                ),
                # Non-deprecated excluded_subjects deliberately left empty;
                # only the deprecated excluded_subject_ids is populated.
                excluded_subject_ids=["eve", "mallory"],
            )
        )

        got = [
            s
            async for s in client.lookup_subjects(
                ("document", "doc1"), "view", "user", full()
            )
        ]

        assert len(got) == 1
        assert got[0].subject.subject_id == "*"
        assert got[0].excluded_subjects == [
            ResolvedSubject(subject_id="eve", permissionship=Permissionship.UNSPECIFIED),
            ResolvedSubject(subject_id="mallory", permissionship=Permissionship.UNSPECIFIED),
        ]


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

    def _client(self) -> SpiceDBClient:
        client = SpiceDBClient("localhost:50051", token="testtoken", insecure=True)
        response = permission_service_pb2.DeleteRelationshipsResponse(
            deleted_at=core_pb2.ZedToken(token="deadbeef"),
        )
        client._permissions.DeleteRelationships = AsyncMock(return_value=response)
        return client

    async def test_no_options_preserves_default_behavior(self):
        client = self._client()
        f = Filter(resource_type="document", resource_id="1")

        revision = await client.delete_relationships(f)

        assert revision == "deadbeef"
        client._permissions.DeleteRelationships.assert_awaited_once()
        request = client._permissions.DeleteRelationships.await_args.args[0]
        assert request.relationship_filter == f._to_proto()
        assert list(request.optional_preconditions) == []
        assert request.optional_limit == 0
        assert request.optional_allow_partial_deletions is False

    async def test_must_match_sets_precondition(self):
        client = self._client()
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

    async def test_must_not_match_sets_precondition(self):
        client = self._client()
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

    async def test_must_match_and_must_not_match_accumulate_in_order(self):
        client = self._client()
        f = Filter(resource_type="document", resource_id="1")
        match_guard = Filter(resource_type="document", resource_id="1", relation="owner")
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

    async def test_limit_sets_optional_limit(self):
        client = self._client()
        f = Filter(resource_type="document", resource_id="1")

        await client.delete_relationships(f, limit=50)

        request = client._permissions.DeleteRelationships.await_args.args[0]
        assert request.optional_limit == 50
        # The server rejects a limited delete outright if more relationships
        # match than the limit unless partial deletions are allowed — so
        # supplying `limit` must also flip this on, or `limit` would only
        # ever work when the caller already knows the exact match count.
        assert request.optional_allow_partial_deletions is True

    async def test_limit_none_leaves_optional_limit_unset(self):
        client = self._client()
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
    async def test_retries_establishment_on_first_open_transient_error(self):
        client = SpiceDBClient("localhost:50051", token="testtoken", insecure=True)
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

    async def test_transient_error_after_yielding_is_not_retried(self):
        client = SpiceDBClient("localhost:50051", token="testtoken", insecure=True)
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

    async def test_retries_establishment_on_first_open_transient_error(self):
        client = SpiceDBClient("localhost:50051", token="testtoken", insecure=True)
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

    async def test_transient_error_after_yielding_is_not_retried(self):
        client = SpiceDBClient("localhost:50051", token="testtoken", insecure=True)
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

    async def test_retries_establishment_on_first_open_transient_error(self):
        client = SpiceDBClient("localhost:50051", token="testtoken", insecure=True)
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

    async def test_transient_error_after_yielding_is_not_retried(self):
        client = SpiceDBClient("localhost:50051", token="testtoken", insecure=True)
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

    async def test_retries_establishment_on_first_open_transient_error(self):
        client = SpiceDBClient("localhost:50051", token="testtoken", insecure=True)
        client._watch.Watch = _stream_open_fails_then_succeeds(
            self._response("rev1"),
        )

        got = [rev async for _updates, rev in client.watch()]

        assert got == ["rev1"]
        assert len(client._watch.Watch.calls) == 2

    async def test_transient_error_after_yielding_is_not_retried(self):
        client = SpiceDBClient("localhost:50051", token="testtoken", insecure=True)
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

    async def test_retries_establishment_on_first_open_transient_error(self):
        client = SpiceDBClient("localhost:50051", token="testtoken", insecure=True)
        client._permissions.ExportBulkRelationships = _stream_open_fails_then_succeeds(
            self._response("doc1", "c1"),
        )

        got = [r async for r in client.export_relationships(full())]

        assert [r.resource_id for r in got] == ["doc1"]
        assert len(client._permissions.ExportBulkRelationships.calls) == 2

    async def test_transient_error_after_yielding_is_not_retried(self):
        client = SpiceDBClient("localhost:50051", token="testtoken", insecure=True)
        client._permissions.ExportBulkRelationships = _stream_fails_after_yielding(
            self._response("doc1", "c1"),
        )

        got = []
        with pytest.raises(UnavailableError):
            async for r in client.export_relationships(full()):
                got.append(r)

        assert [r.resource_id for r in got] == ["doc1"]
        assert len(client._permissions.ExportBulkRelationships.calls) == 1
