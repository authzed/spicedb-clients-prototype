"""Unit tests for SpiceDBClient construction — no SpiceDB instance needed."""

import inspect
from unittest.mock import AsyncMock

import pytest
from authzed.api.v1 import core_pb2, permission_service_pb2, schema_service_pb2
from google.rpc import status_pb2

from spicedb import Relationship, RelationReference, SpiceDBClient, full
from spicedb.errors import InvalidArgumentError, SpiceDBError
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
