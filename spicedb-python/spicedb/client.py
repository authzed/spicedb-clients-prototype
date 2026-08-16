"""Async SpiceDB client wrapping the proto-generated gRPC stubs."""

from __future__ import annotations

import asyncio
from collections.abc import AsyncIterator
from typing import Any

import grpc
import grpc.aio
from authzed.api.v1 import (
    experimental_service_pb2,
    permission_service_pb2,
    schema_service_pb2,
)

from spicedb import _mapping, _requests
from spicedb._requests import DEFAULT_PAGE_SIZE as _DEFAULT_PAGE_SIZE
from spicedb._requests import IMPORT_BATCH_SIZE as _IMPORT_BATCH_SIZE
from spicedb.consistency import Consistency
from spicedb.errors import is_transient, to_spicedb_error
from spicedb.types import (
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
        self._max_retries = max_retries
        self._metadata = [("authorization", f"Bearer {token}")]
        interceptors = [_BearerTokenInterceptor(token)]
        if insecure:
            self._channel = grpc.aio.insecure_channel(
                endpoint, interceptors=interceptors
            )
        else:
            call_creds = grpc.access_token_call_credentials(token)
            channel_creds = grpc.ssl_channel_credentials()
            composite_creds = grpc.composite_channel_credentials(
                channel_creds, call_creds
            )
            self._channel = grpc.aio.secure_channel(
                endpoint, composite_creds, interceptors=interceptors
            )

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
        """Close the underlying gRPC channel."""
        await self._channel.close()

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
    ) -> bool:
        """Check a single permission. Returns True if the subject has the permission."""
        results = await self.check_permissions(consistency, rel, context=context)
        return results[0]

    async def check_permissions(
        self,
        consistency: Consistency,
        *rels: Relationship,
        context: dict[str, Any] | None = None,
    ) -> list[bool]:
        """Check multiple permissions via BulkCheckPermissions. Returns list of bools."""
        request = _requests.check_bulk_request(consistency, rels, context)

        async def _call() -> permission_service_pb2.CheckBulkPermissionsResponse:
            return await self._permissions.CheckBulkPermissions(request)

        resp = await self._with_retry(_call)
        return _mapping.check_results(resp)

    async def check_any(
        self,
        consistency: Consistency,
        *rels: Relationship,
        context: dict[str, Any] | None = None,
    ) -> bool:
        """Return True if any of the permission checks pass."""
        results = await self.check_permissions(consistency, *rels, context=context)
        return any(results)

    async def check_all(
        self,
        consistency: Consistency,
        *rels: Relationship,
        context: dict[str, Any] | None = None,
    ) -> bool:
        """Return True if all of the permission checks pass."""
        results = await self.check_permissions(consistency, *rels, context=context)
        return all(results)

    # ── Reads ───────────────────────────────────────────────────────

    async def read_relationships(
        self,
        filter: Filter,
        consistency: Consistency,
    ) -> AsyncIterator[Relationship]:
        """Read relationships matching the filter. Handles pagination automatically."""
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
        request = _requests.write_request(txn)

        async def _call() -> permission_service_pb2.WriteRelationshipsResponse:
            return await self._permissions.WriteRelationships(request)

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
        request = _requests.delete_relationships_request(
            filter, must_match, must_not_match, limit
        )

        async def _call() -> permission_service_pb2.DeleteRelationshipsResponse:
            return await self._permissions.DeleteRelationships(request)

        resp = await self._with_retry(_call)
        return resp.deleted_at.token

    # ── Schema ──────────────────────────────────────────────────────

    async def read_schema(self) -> str:
        """Read the current schema. Returns the schema text."""

        async def _call() -> schema_service_pb2.ReadSchemaResponse:
            return await self._schema.ReadSchema(schema_service_pb2.ReadSchemaRequest())

        resp = await self._with_retry(_call)
        return resp.schema_text

    async def write_schema(self, schema: str) -> str:
        """Write a schema. Returns the revision string."""

        async def _call() -> schema_service_pb2.WriteSchemaResponse:
            return await self._schema.WriteSchema(
                schema_service_pb2.WriteSchemaRequest(schema=schema)
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
        request = _requests.expand_request(resource, permission, consistency)

        async def _call() -> permission_service_pb2.ExpandPermissionTreeResponse:
            return await self._permissions.ExpandPermissionTree(request)

        resp = await self._with_retry(_call)
        return _permission_tree_from_proto(resp.tree_root), resp.expanded_at.token

    # ── Experimental: Relationship Counters ─────────────────────────

    async def register_relationship_counter(
        self,
        name: str,
        filter: Filter,
    ) -> None:
        """Experimental: Register a relationship counter."""
        request = (
            experimental_service_pb2.ExperimentalRegisterRelationshipCounterRequest(
                name=name,
                relationship_filter=filter._to_proto(),
            )
        )

        async def _call() -> Any:
            return await self._experimental.ExperimentalRegisterRelationshipCounter(
                request
            )

        await self._with_retry(_call)

    async def count_relationships(
        self,
        name: str,
    ) -> tuple[int, str]:
        """Experimental: Count relationships for a registered counter.

        Returns (count, revision).
        """
        request = experimental_service_pb2.ExperimentalCountRelationshipsRequest(
            name=name,
        )

        async def _call() -> Any:
            return await self._experimental.ExperimentalCountRelationships(request)

        resp = await self._with_retry(_call)
        return resp.counter_result.relationship_count, resp.read_counter_at.token

    async def unregister_relationship_counter(
        self,
        name: str,
    ) -> None:
        """Experimental: Unregister a relationship counter."""
        request = (
            experimental_service_pb2.ExperimentalUnregisterRelationshipCounterRequest(
                name=name,
            )
        )

        async def _call() -> Any:
            return await self._experimental.ExperimentalUnregisterRelationshipCounter(
                request
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
        request = _requests.reflect_schema_request(consistency, filters)

        async def _call() -> schema_service_pb2.ReflectSchemaResponse:
            return await self._schema.ReflectSchema(request)

        resp = await self._with_retry(_call)
        return ReflectSchemaResult._from_proto(resp)

    async def diff_schema(
        self,
        consistency: Consistency,
        comparison_schema: str,
    ) -> list[SchemaDiff]:
        """Experimental: Diff two schemas, returning the list of differences."""
        request = _requests.diff_schema_request(consistency, comparison_schema)

        async def _call() -> schema_service_pb2.DiffSchemaResponse:
            return await self._schema.DiffSchema(request)

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
        request = _requests.computable_permissions_request(
            consistency, definition_name, relation_name
        )

        async def _call() -> schema_service_pb2.ComputablePermissionsResponse:
            return await self._schema.ComputablePermissions(request)

        resp = await self._with_retry(_call)
        return [RelationReference._from_proto(p) for p in resp.permissions]

    async def dependent_relations(
        self,
        consistency: Consistency,
        definition_name: str,
        permission_name: str,
    ) -> list[RelationReference]:
        """Return the relations that the given permission depends on."""
        request = _requests.dependent_relations_request(
            consistency, definition_name, permission_name
        )

        async def _call() -> schema_service_pb2.DependentRelationsResponse:
            return await self._schema.DependentRelations(request)

        resp = await self._with_retry(_call)
        return [RelationReference._from_proto(r) for r in resp.relations]


class _BearerTokenInterceptor(
    grpc.aio.UnaryUnaryClientInterceptor,
    grpc.aio.UnaryStreamClientInterceptor,
    grpc.aio.StreamUnaryClientInterceptor,
    grpc.aio.StreamStreamClientInterceptor,
):
    def __init__(self, token: str):
        self._metadata = (("authorization", f"Bearer {token}"),)

    def _add_metadata(
        self, client_call_details: grpc.aio.ClientCallDetails
    ) -> grpc.aio.ClientCallDetails:
        metadata = list(client_call_details.metadata or [])
        metadata.extend(self._metadata)
        return grpc.aio.ClientCallDetails(
            client_call_details.method,
            client_call_details.timeout,
            metadata,
            client_call_details.credentials,
            client_call_details.wait_for_ready,
        )

    async def intercept_unary_unary(self, continuation, client_call_details, request):
        return await continuation(self._add_metadata(client_call_details), request)

    async def intercept_unary_stream(self, continuation, client_call_details, request):
        return await continuation(self._add_metadata(client_call_details), request)

    async def intercept_stream_unary(
        self, continuation, client_call_details, request_iterator
    ):
        return await continuation(
            self._add_metadata(client_call_details), request_iterator
        )

    async def intercept_stream_stream(
        self, continuation, client_call_details, request_iterator
    ):
        return await continuation(
            self._add_metadata(client_call_details), request_iterator
        )
