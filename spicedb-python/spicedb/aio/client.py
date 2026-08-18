"""Async SpiceDB client wrapping the proto-generated gRPC stubs."""

from __future__ import annotations

import asyncio
from collections.abc import AsyncIterator
from typing import TYPE_CHECKING, Any

import grpc
import grpc.aio
from authzed.api.v1 import (
    experimental_service_pb2,
    permission_service_pb2,
    schema_service_pb2,
)

if TYPE_CHECKING:
    from authzed.api.v1 import (
        experimental_service_pb2_grpc,
        permission_service_pb2_grpc,
        schema_service_pb2_grpc,
        watch_service_pb2_grpc,
    )

from spicedb import _mapping, _requests
from spicedb._auth import bearer_metadata
from spicedb._requests import DEFAULT_PAGE_SIZE as _DEFAULT_PAGE_SIZE
from spicedb._requests import IMPORT_BATCH_SIZE as _IMPORT_BATCH_SIZE
from spicedb.consistency import Consistency
from spicedb.errors import EventLoopBindingError, is_transient, to_spicedb_error
from spicedb.types import (
    CheckResult,
    Filter,
    LookupResource,
    LookupSubject,
    PermissionTree,
    ReflectSchemaResult,
    RelationReference,
    Relationship,
    SchemaDiff,
    Transaction,
    Update,
    _permission_tree_from_proto,
    _schema_diff_from_proto,
)

_DEFAULT_MAX_RETRIES = 3


class SpiceDBClient:
    """Idiomatic async Python client for SpiceDB.

    Usage::

        async with SpiceDBClient("localhost:50051", token="test", insecure=True) as client:
            ...
    """

    def __init__(
        self,
        endpoint: str,
        token: str,
        *,
        insecure: bool = False,
        max_retries: int = _DEFAULT_MAX_RETRIES,
    ):
        self._endpoint = endpoint
        self._insecure = insecure
        self._max_retries = max_retries
        self._metadata = bearer_metadata(token)
        self._channel: grpc.aio.Channel | None = None
        self._loop: asyncio.AbstractEventLoop | None = None
        self._permissions: permission_service_pb2_grpc.PermissionsServiceStub | None = None
        self._schema: schema_service_pb2_grpc.SchemaServiceStub | None = None
        self._watch: watch_service_pb2_grpc.WatchServiceStub | None = None
        self._experimental: experimental_service_pb2_grpc.ExperimentalServiceStub | None = None

    def _ensure_channel(self) -> None:
        """Open the channel on first use, binding it to the running loop.

        Constructing in __init__ would bind to whatever loop existed at
        construction time -- usually none -- which is why building a client at
        import time and then calling asyncio.run() per request fails.
        """
        loop = asyncio.get_running_loop()
        if self._channel is not None:
            if self._loop is not loop:
                raise EventLoopBindingError(
                    "This SpiceDBClient is bound to a different asyncio event "
                    "loop than the one now running. A grpc.aio channel cannot "
                    "be shared across event loops, so calling asyncio.run() per "
                    "request against a client built at startup will always fail. "
                    "Use spicedb.sync.SpiceDBClient from synchronous code, or "
                    "keep one event loop for the process lifetime."
                )
            return

        if self._insecure:
            self._channel = grpc.aio.insecure_channel(self._endpoint)
        else:
            self._channel = grpc.aio.secure_channel(
                self._endpoint, grpc.ssl_channel_credentials()
            )
        self._loop = loop

        from authzed.api.v1 import (
            experimental_service_pb2_grpc,
            permission_service_pb2_grpc,
            schema_service_pb2_grpc,
            watch_service_pb2_grpc,
        )

        self._permissions = permission_service_pb2_grpc.PermissionsServiceStub(
            self._channel
        )
        self._schema = schema_service_pb2_grpc.SchemaServiceStub(self._channel)
        self._watch = watch_service_pb2_grpc.WatchServiceStub(self._channel)
        self._experimental = experimental_service_pb2_grpc.ExperimentalServiceStub(
            self._channel
        )

    async def close(self) -> None:
        """Close the underlying gRPC channel. A no-op if never used."""
        if self._channel is not None:
            await self._channel.close()
            self._channel = None
            self._loop = None

    async def __aenter__(self) -> SpiceDBClient:
        return self

    async def __aexit__(self, exc_type: Any, exc_val: Any, exc_tb: Any) -> bool:
        await self.close()
        return False

    # ── Retry helper ────────────────────────────────────────────────

    async def _with_retry(self, fn: Any) -> Any:
        """Call an async function with exponential backoff on transient errors."""
        last_err: Exception | None = None
        for attempt in range(self._max_retries + 1):
            try:
                return await fn()
            except grpc.aio.AioRpcError as e:
                if not is_transient(e) or attempt == self._max_retries:
                    raise to_spicedb_error(e) from e
                last_err = e
                await asyncio.sleep(min(0.1 * (2**attempt), 5.0))
        raise to_spicedb_error(last_err) from last_err  # type: ignore[arg-type]

    async def _should_retry_establishment(
        self, attempt: int, err: grpc.aio.AioRpcError
    ) -> bool:
        """Decide whether to retry a streaming RPC's ESTABLISHMENT after a
        transient error, sleeping with the same backoff as `_with_retry`.

        Callers MUST only retry when zero items have been yielded from the
        current stream/page — retrying after any item has been yielded
        would replay/duplicate it for the caller. This helper only makes
        the transient/attempt-budget decision; the zero-yielded guard is
        the caller's responsibility.
        """
        if not is_transient(err) or attempt == self._max_retries:
            return False
        await asyncio.sleep(min(0.1 * (2**attempt), 5.0))
        return True

    # ── Permission checks ──────────────────────────────────────────

    async def check_permission(
        self,
        consistency: Consistency,
        rel: Relationship,
        *,
        context: dict[str, Any] | None = None,
    ) -> CheckResult:
        """Check a single permission. Returns a CheckResult -- use
        `.has_permission` for the common bool case, or inspect
        `.permissionship` directly to distinguish a CONDITIONAL_PERMISSION
        result (caveat context was needed but not supplied) from a real
        denial.

        `context` supplies caveat context for this check. `rel` can also
        carry its own `Relationship.check_context`, which overrides `context`
        key-by-key for this one check (see `check_permissions` below)."""
        self._ensure_channel()
        results = await self.check_permissions(consistency, rel, context=context)
        return results[0]

    async def check_permissions(
        self,
        consistency: Consistency,
        *rels: Relationship,
        context: dict[str, Any] | None = None,
    ) -> list[CheckResult]:
        """Check multiple permissions via BulkCheckPermissions. Returns a
        list of CheckResult, one per relationship, in the same order.

        `context` is a call-level default applied to every relationship's
        check item. A relationship built with its own
        `check_context` (e.g. `Relationship.from_triple(..., check_context=
        {...})`) overrides `context` for that one item -- merged key-by-key
        (this item's keys win on conflict; call-level keys the item doesn't
        mention are retained, NOT replaced wholesale). An item with no
        `check_context` inherits `context` unchanged. `check_context` is
        check-time-only and distinct from `Relationship.caveat_context`,
        which is written into a relationship at write time -- see the
        `Relationship` docstring."""
        self._ensure_channel()
        request = _requests.check_bulk_request(consistency, rels, context)

        async def _call() -> permission_service_pb2.CheckBulkPermissionsResponse:
            return await self._permissions.CheckBulkPermissions(
                request, metadata=self._metadata
            )

        resp = await self._with_retry(_call)
        return _mapping.check_results(resp, len(request.items))

    async def check_any(
        self,
        consistency: Consistency,
        *rels: Relationship,
        context: dict[str, Any] | None = None,
    ) -> bool:
        """Return True if any of the permission checks pass outright. Only
        `CheckResult.has_permission` results count -- a CONDITIONAL_PERMISSION
        result is not a grant, so it can never make this True."""
        self._ensure_channel()
        results = await self.check_permissions(consistency, *rels, context=context)
        return any(r.has_permission for r in results)

    async def check_all(
        self,
        consistency: Consistency,
        *rels: Relationship,
        context: dict[str, Any] | None = None,
    ) -> bool:
        """Return True if all of the permission checks pass outright. Only
        `CheckResult.has_permission` results count -- a CONDITIONAL_PERMISSION
        result is not a grant, so it makes this False.

        Returns False, not the vacuous True `all()` yields on an empty
        iterable, if `rels` is empty -- "no checks to run" is not "all
        checks passed"."""
        self._ensure_channel()
        if not rels:
            return False
        results = await self.check_permissions(consistency, *rels, context=context)
        return all(r.has_permission for r in results)

    # ── Reads ───────────────────────────────────────────────────────

    async def read_relationships(
        self,
        filter: Filter,
        consistency: Consistency,
    ) -> AsyncIterator[Relationship]:
        """Read relationships matching the filter. Handles pagination automatically."""
        self._ensure_channel()
        cursor = None
        while True:
            request = _requests.read_relationships_request(
                filter, consistency, _DEFAULT_PAGE_SIZE, cursor
            )
            attempt = 0
            count = 0
            while True:
                yielded = 0
                try:
                    async for resp in self._permissions.ReadRelationships(
                        request, metadata=self._metadata
                    ):
                        yielded += 1
                        count += 1
                        yield Relationship._from_proto(resp.relationship)
                        cursor = resp.after_result_cursor
                    break
                except grpc.aio.AioRpcError as e:
                    if yielded == 0 and await self._should_retry_establishment(
                        attempt, e
                    ):
                        attempt += 1
                        continue
                    raise to_spicedb_error(e) from e
            if count < _DEFAULT_PAGE_SIZE:
                return

    async def lookup_resources(
        self,
        resource_type: str,
        permission: str,
        subject: Relationship | tuple[str, str],
        consistency: Consistency,
        *,
        context: dict[str, Any] | None = None,
    ) -> AsyncIterator[LookupResource]:
        """Look up resources the subject has the given permission on.

        ``subject`` can be a Relationship (using its subject fields) or a
        tuple of ``("type:id", "optional_relation")``.

        Yields ``LookupResource`` — each result carries the permissionship
        (full grant vs conditional on caveat context) and, for conditional
        results, which caveat context was missing. Callers MUST check
        ``permissionship`` before treating a result as a full grant.
        Handles pagination automatically.
        """
        self._ensure_channel()
        subj_ref = _requests.subject_reference(subject)
        ctx_struct = _requests.context_struct(context)

        cursor = None
        while True:
            request = _requests.lookup_resources_request(
                resource_type,
                permission,
                subj_ref,
                ctx_struct,
                consistency,
                _DEFAULT_PAGE_SIZE,
                cursor,
            )
            attempt = 0
            count = 0
            while True:
                yielded = 0
                try:
                    async for resp in self._permissions.LookupResources(
                        request, metadata=self._metadata
                    ):
                        yielded += 1
                        count += 1
                        yield _mapping.lookup_resource(resp)
                        cursor = resp.after_result_cursor
                    break
                except grpc.aio.AioRpcError as e:
                    if yielded == 0 and await self._should_retry_establishment(
                        attempt, e
                    ):
                        attempt += 1
                        continue
                    raise to_spicedb_error(e) from e
            if count < _DEFAULT_PAGE_SIZE:
                return

    async def lookup_subjects(
        self,
        resource: tuple[str, str],
        permission: str,
        subject_type: str,
        consistency: Consistency,
        *,
        subject_relation: str = "",
        context: dict[str, Any] | None = None,
    ) -> AsyncIterator[LookupSubject]:
        """Look up subjects that have the given permission on the resource.

        ``resource`` is a tuple of ``("type", "id")``.

        Yields ``LookupSubject``. When a yielded ``subject.subject_id`` is
        the wildcard ``"*"``, the server has granted the permission to every
        subject of ``subject_type`` EXCEPT those listed in
        ``excluded_subjects``. Callers MUST check ``excluded_subjects``
        before treating a wildcard match as a blanket grant, or they risk
        granting access to subjects the server explicitly excluded.
        """
        self._ensure_channel()
        ctx_struct = _requests.context_struct(context)
        request = _requests.lookup_subjects_request(
            resource,
            permission,
            subject_type,
            subject_relation,
            ctx_struct,
            consistency,
        )
        attempt = 0
        while True:
            yielded = 0
            try:
                async for resp in self._permissions.LookupSubjects(
                    request, metadata=self._metadata
                ):
                    yielded += 1
                    yield _mapping.lookup_subject(resp)
                return
            except grpc.aio.AioRpcError as e:
                if yielded == 0 and await self._should_retry_establishment(attempt, e):
                    attempt += 1
                    continue
                raise to_spicedb_error(e) from e

    # ── Writes ──────────────────────────────────────────────────────

    async def write(self, txn: Transaction) -> str:
        """Execute a transaction and return the revision string."""
        self._ensure_channel()
        request = _requests.write_request(txn)

        async def _call() -> permission_service_pb2.WriteRelationshipsResponse:
            return await self._permissions.WriteRelationships(
                request, metadata=self._metadata
            )

        resp = await self._with_retry(_call)
        return resp.written_at.token

    async def delete_relationships(
        self,
        filter: Filter,
        *,
        must_match: list[Filter] | None = None,
        must_not_match: list[Filter] | None = None,
        limit: int | None = None,
    ) -> str:
        """Delete relationships matching the filter. Returns the revision string.

        ``must_match``/``must_not_match`` add preconditions that guard the
        delete: if a precondition fails, the server rejects the call and
        deletes nothing. Mirrors spicedb-go's `WithDeleteMustMatch`/
        `WithDeleteMustNotMatch` (client/relationships.go).

        ``limit`` bounds how many relationships this single call deletes. If
        more relationships match the filter than ``limit``, only ``limit`` of
        them are deleted by this call (the server requires
        ``optional_allow_partial_deletions``, which this sets automatically
        whenever ``limit`` is given, to permit that). Unlike spicedb-go's
        `WithDeleteLimit`, this does not auto-page — it does not loop to
        delete every match when the match count exceeds ``limit``; call again
        with the same filter to continue deleting what remains.
        """
        self._ensure_channel()
        request = _requests.delete_relationships_request(
            filter, must_match, must_not_match, limit
        )

        async def _call() -> permission_service_pb2.DeleteRelationshipsResponse:
            return await self._permissions.DeleteRelationships(
                request, metadata=self._metadata
            )

        resp = await self._with_retry(_call)
        return resp.deleted_at.token

    # ── Schema ──────────────────────────────────────────────────────

    async def read_schema(self) -> str:
        """Read the current schema. Returns the schema text."""
        self._ensure_channel()

        async def _call() -> schema_service_pb2.ReadSchemaResponse:
            return await self._schema.ReadSchema(
                schema_service_pb2.ReadSchemaRequest(), metadata=self._metadata
            )

        resp = await self._with_retry(_call)
        return resp.schema_text

    async def write_schema(self, schema: str) -> str:
        """Write a schema. Returns the revision string."""
        self._ensure_channel()

        async def _call() -> schema_service_pb2.WriteSchemaResponse:
            return await self._schema.WriteSchema(
                schema_service_pb2.WriteSchemaRequest(schema=schema),
                metadata=self._metadata,
            )

        resp = await self._with_retry(_call)
        return resp.written_at.token

    # ── Watch ───────────────────────────────────────────────────────

    async def watch(
        self,
        *,
        object_types: list[str] | None = None,
        start_revision: str | None = None,
    ) -> AsyncIterator[tuple[list[Update], str]]:
        """Watch for relationship changes. Yields (updates, revision) tuples."""
        self._ensure_channel()
        request = _requests.watch_request(object_types, start_revision)
        attempt = 0
        while True:
            yielded = 0
            try:
                async for resp in self._watch.Watch(request, metadata=self._metadata):
                    yielded += 1
                    yield _mapping.watch_event(resp)
                return
            except grpc.aio.AioRpcError as e:
                # Retrying is only safe before any update has been yielded
                # (stream ESTABLISHMENT) — never retry mid-watch, since
                # that would replay/duplicate already-delivered updates.
                if yielded == 0 and await self._should_retry_establishment(attempt, e):
                    attempt += 1
                    continue
                raise to_spicedb_error(e) from e

    # ── Bulk operations ─────────────────────────────────────────────

    async def import_relationships(self, relationships: list[Relationship]) -> int:
        """Import relationships in bulk. Returns the number loaded."""
        self._ensure_channel()
        try:
            resp = await self._permissions.ImportBulkRelationships(
                _requests.import_batches(relationships, _IMPORT_BATCH_SIZE),
                metadata=self._metadata,
            )
            return resp.num_loaded
        except grpc.aio.AioRpcError as e:
            raise to_spicedb_error(e) from e

    async def export_relationships(
        self,
        consistency: Consistency,
        *,
        filter: Filter | None = None,
    ) -> AsyncIterator[Relationship]:
        """Export relationships in bulk. Handles pagination automatically."""
        self._ensure_channel()
        cursor = None
        rel_filter = filter._to_proto() if filter else None
        while True:
            request = _requests.export_request(
                consistency, rel_filter, _DEFAULT_PAGE_SIZE, cursor
            )
            attempt = 0
            count = 0
            while True:
                yielded = 0
                try:
                    async for resp in self._permissions.ExportBulkRelationships(
                        request, metadata=self._metadata
                    ):
                        for proto_rel in resp.relationships:
                            yield Relationship._from_proto(proto_rel)
                            yielded += 1
                            count += 1
                        cursor = resp.after_result_cursor
                    break
                except grpc.aio.AioRpcError as e:
                    if yielded == 0 and await self._should_retry_establishment(
                        attempt, e
                    ):
                        attempt += 1
                        continue
                    raise to_spicedb_error(e) from e
            if count < _DEFAULT_PAGE_SIZE:
                return

    # ── Expand ──────────────────────────────────────────────────────

    async def expand_permission_tree(
        self,
        resource: tuple[str, str],
        permission: str,
        consistency: Consistency,
    ) -> tuple[PermissionTree, str]:
        """Expand a permission tree. Returns (tree, revision)."""
        self._ensure_channel()
        request = _requests.expand_request(resource, permission, consistency)

        async def _call() -> permission_service_pb2.ExpandPermissionTreeResponse:
            return await self._permissions.ExpandPermissionTree(
                request, metadata=self._metadata
            )

        resp = await self._with_retry(_call)
        return _permission_tree_from_proto(resp.tree_root), resp.expanded_at.token

    # ── Experimental: Relationship Counters ─────────────────────────

    async def register_relationship_counter(
        self,
        name: str,
        filter: Filter,
    ) -> None:
        """Experimental: Register a relationship counter."""
        self._ensure_channel()
        request = (
            experimental_service_pb2.ExperimentalRegisterRelationshipCounterRequest(
                name=name,
                relationship_filter=filter._to_proto(),
            )
        )

        async def _call() -> Any:
            return await self._experimental.ExperimentalRegisterRelationshipCounter(
                request, metadata=self._metadata
            )

        await self._with_retry(_call)

    async def count_relationships(
        self,
        name: str,
    ) -> tuple[int, str]:
        """Experimental: Count relationships for a registered counter.

        Returns (count, revision).
        """
        self._ensure_channel()
        request = experimental_service_pb2.ExperimentalCountRelationshipsRequest(
            name=name,
        )

        async def _call() -> Any:
            return await self._experimental.ExperimentalCountRelationships(
                request, metadata=self._metadata
            )

        resp = await self._with_retry(_call)
        return resp.counter_result.relationship_count, resp.read_counter_at.token

    async def unregister_relationship_counter(
        self,
        name: str,
    ) -> None:
        """Experimental: Unregister a relationship counter."""
        self._ensure_channel()
        request = (
            experimental_service_pb2.ExperimentalUnregisterRelationshipCounterRequest(
                name=name,
            )
        )

        async def _call() -> Any:
            return await self._experimental.ExperimentalUnregisterRelationshipCounter(
                request, metadata=self._metadata
            )

        await self._with_retry(_call)

    # ── Experimental: Schema Reflection ─────────────────────────────

    async def reflect_schema(
        self,
        consistency: Consistency,
        *,
        filters: list[str] | None = None,
    ) -> ReflectSchemaResult:
        """Experimental: Reflect the schema, returning its definitions and
        caveats."""
        self._ensure_channel()
        request = _requests.reflect_schema_request(consistency, filters)

        async def _call() -> schema_service_pb2.ReflectSchemaResponse:
            return await self._schema.ReflectSchema(request, metadata=self._metadata)

        resp = await self._with_retry(_call)
        return ReflectSchemaResult._from_proto(resp)

    async def diff_schema(
        self,
        consistency: Consistency,
        comparison_schema: str,
    ) -> list[SchemaDiff]:
        """Experimental: Diff two schemas, returning the list of differences."""
        self._ensure_channel()
        request = _requests.diff_schema_request(consistency, comparison_schema)

        async def _call() -> schema_service_pb2.DiffSchemaResponse:
            return await self._schema.DiffSchema(request, metadata=self._metadata)

        resp = await self._with_retry(_call)
        return [_schema_diff_from_proto(d) for d in resp.diffs]

    # ── Schema Introspection: Computable Permissions / Dependent Relations ──

    async def computable_permissions(
        self,
        consistency: Consistency,
        definition_name: str,
        relation_name: str,
    ) -> list[RelationReference]:
        """Return the permissions that are computable for the given relation
        on a definition."""
        self._ensure_channel()
        request = _requests.computable_permissions_request(
            consistency, definition_name, relation_name
        )

        async def _call() -> schema_service_pb2.ComputablePermissionsResponse:
            return await self._schema.ComputablePermissions(
                request, metadata=self._metadata
            )

        resp = await self._with_retry(_call)
        return [RelationReference._from_proto(p) for p in resp.permissions]

    async def dependent_relations(
        self,
        consistency: Consistency,
        definition_name: str,
        permission_name: str,
    ) -> list[RelationReference]:
        """Return the relations that the given permission depends on."""
        self._ensure_channel()
        request = _requests.dependent_relations_request(
            consistency, definition_name, permission_name
        )

        async def _call() -> schema_service_pb2.DependentRelationsResponse:
            return await self._schema.DependentRelations(
                request, metadata=self._metadata
            )

        resp = await self._with_retry(_call)
        return [RelationReference._from_proto(r) for r in resp.relations]
