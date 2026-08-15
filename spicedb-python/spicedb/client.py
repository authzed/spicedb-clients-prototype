"""Async SpiceDB client wrapping the proto-generated gRPC stubs."""

from __future__ import annotations

import asyncio
from collections.abc import AsyncIterator
from typing import Any

import grpc
import grpc.aio
from authzed.api.v1 import (
    core_pb2,
    experimental_service_pb2,
    permission_service_pb2,
    schema_service_pb2,
    watch_service_pb2,
)
from google.protobuf import struct_pb2

from spicedb.consistency import Consistency
from spicedb.errors import error_from_status_proto, is_transient, to_spicedb_error
from spicedb.types import (
    Filter,
    PermissionTree,
    ReflectSchemaResult,
    Relationship,
    SchemaDiff,
    Transaction,
    Update,
    _permission_tree_from_proto,
    _schema_diff_from_proto,
)

_DEFAULT_PAGE_SIZE = 512
_IMPORT_BATCH_SIZE = 1000
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

    # ── Permission checks ──────────────────────────────────────────

    async def check_permission(
        self,
        consistency: Consistency,
        rel: Relationship,
        *,
        context: dict[str, Any] | None = None,
    ) -> bool:
        """Check a single permission. Returns True if the subject has the permission."""
        results = await self.check_permissions(
            consistency, rel, context=context
        )
        return results[0]

    async def check_permissions(
        self,
        consistency: Consistency,
        *rels: Relationship,
        context: dict[str, Any] | None = None,
    ) -> list[bool]:
        """Check multiple permissions via BulkCheckPermissions. Returns list of bools."""
        ctx_struct = None
        if context is not None:
            ctx_struct = struct_pb2.Struct()
            ctx_struct.update(context)

        items = []
        for rel in rels:
            items.append(
                permission_service_pb2.CheckBulkPermissionsRequestItem(
                    resource=core_pb2.ObjectReference(
                        object_type=rel.resource_type,
                        object_id=rel.resource_id,
                    ),
                    permission=rel.resource_relation,
                    subject=core_pb2.SubjectReference(
                        object=core_pb2.ObjectReference(
                            object_type=rel.subject_type,
                            object_id=rel.subject_id,
                        ),
                        optional_relation=rel.subject_relation,
                    ),
                    context=ctx_struct,
                )
            )

        request = permission_service_pb2.CheckBulkPermissionsRequest(
            consistency=consistency._to_proto(),
            items=items,
        )

        async def _call() -> permission_service_pb2.CheckBulkPermissionsResponse:
            return await self._permissions.CheckBulkPermissions(request)

        resp = await self._with_retry(_call)
        HAS = permission_service_pb2.CheckPermissionResponse.PERMISSIONSHIP_HAS_PERMISSION
        results: list[bool] = []
        for pair in resp.pairs:
            if pair.HasField("error"):
                raise error_from_status_proto(pair.error)
            results.append(pair.item.permissionship == HAS)
        return results

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
            request = permission_service_pb2.ReadRelationshipsRequest(
                consistency=consistency._to_proto(),
                relationship_filter=filter._to_proto(),
                optional_limit=_DEFAULT_PAGE_SIZE,
                optional_cursor=cursor,
            )
            try:
                count = 0
                async for resp in self._permissions.ReadRelationships(request, metadata=self._metadata):
                    yield Relationship._from_proto(resp.relationship)
                    cursor = resp.after_result_cursor
                    count += 1
                if count < _DEFAULT_PAGE_SIZE:
                    return
            except grpc.aio.AioRpcError as e:
                raise to_spicedb_error(e) from e

    async def lookup_resources(
        self,
        resource_type: str,
        permission: str,
        subject: Relationship | tuple[str, str],
        consistency: Consistency,
        *,
        context: dict[str, Any] | None = None,
    ) -> AsyncIterator[str]:
        """Look up resource IDs where the subject has the given permission.

        ``subject`` can be a Relationship (using its subject fields) or a
        tuple of ``("type:id", "optional_relation")``.
        """
        if isinstance(subject, Relationship):
            subj_ref = core_pb2.SubjectReference(
                object=core_pb2.ObjectReference(
                    object_type=subject.subject_type,
                    object_id=subject.subject_id,
                ),
                optional_relation=subject.subject_relation,
            )
        else:
            subj_str, subj_rel = subject
            s_type, s_id = subj_str.split(":", 1)
            subj_ref = core_pb2.SubjectReference(
                object=core_pb2.ObjectReference(
                    object_type=s_type,
                    object_id=s_id,
                ),
                optional_relation=subj_rel,
            )

        ctx_struct = None
        if context is not None:
            ctx_struct = struct_pb2.Struct()
            ctx_struct.update(context)

        cursor = None
        while True:
            request = permission_service_pb2.LookupResourcesRequest(
                consistency=consistency._to_proto(),
                resource_object_type=resource_type,
                permission=permission,
                subject=subj_ref,
                context=ctx_struct,
                optional_limit=_DEFAULT_PAGE_SIZE,
                optional_cursor=cursor,
            )
            try:
                count = 0
                async for resp in self._permissions.LookupResources(request, metadata=self._metadata):
                    yield resp.resource_object_id
                    cursor = resp.after_result_cursor
                    count += 1
                if count < _DEFAULT_PAGE_SIZE:
                    return
            except grpc.aio.AioRpcError as e:
                raise to_spicedb_error(e) from e

    async def lookup_subjects(
        self,
        resource: tuple[str, str],
        permission: str,
        subject_type: str,
        consistency: Consistency,
        *,
        subject_relation: str = "",
        context: dict[str, Any] | None = None,
    ) -> AsyncIterator[str]:
        """Look up subject IDs that have the given permission on the resource.

        ``resource`` is a tuple of ``("type", "id")``.
        """
        res_type, res_id = resource
        ctx_struct = None
        if context is not None:
            ctx_struct = struct_pb2.Struct()
            ctx_struct.update(context)

        request = permission_service_pb2.LookupSubjectsRequest(
            consistency=consistency._to_proto(),
            resource=core_pb2.ObjectReference(
                object_type=res_type,
                object_id=res_id,
            ),
            permission=permission,
            subject_object_type=subject_type,
            optional_subject_relation=subject_relation,
            context=ctx_struct,
        )
        try:
            async for resp in self._permissions.LookupSubjects(request, metadata=self._metadata):
                yield resp.subject.subject_object_id
        except grpc.aio.AioRpcError as e:
            raise to_spicedb_error(e) from e

    # ── Writes ──────────────────────────────────────────────────────

    async def write(self, txn: Transaction) -> str:
        """Execute a transaction and return the revision string."""
        request = permission_service_pb2.WriteRelationshipsRequest(
            updates=txn._updates,
            optional_preconditions=txn._preconditions,
        )

        async def _call() -> permission_service_pb2.WriteRelationshipsResponse:
            return await self._permissions.WriteRelationships(request)

        resp = await self._with_retry(_call)
        return resp.written_at.token

    async def delete_relationships(
        self,
        filter: Filter,
    ) -> str:
        """Delete relationships matching the filter. Returns the revision string."""
        request = permission_service_pb2.DeleteRelationshipsRequest(
            relationship_filter=filter._to_proto(),
        )

        async def _call() -> permission_service_pb2.DeleteRelationshipsResponse:
            return await self._permissions.DeleteRelationships(request)

        resp = await self._with_retry(_call)
        return resp.deleted_at.token

    # ── Schema ──────────────────────────────────────────────────────

    async def read_schema(self) -> str:
        """Read the current schema. Returns the schema text."""
        async def _call() -> schema_service_pb2.ReadSchemaResponse:
            return await self._schema.ReadSchema(
                schema_service_pb2.ReadSchemaRequest()
            )

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
        cursor = None
        if start_revision is not None:
            cursor = core_pb2.ZedToken(token=start_revision)

        request = watch_service_pb2.WatchRequest(
            optional_object_types=object_types or [],
            optional_start_cursor=cursor,
        )
        try:
            async for resp in self._watch.Watch(request, metadata=self._metadata):
                yield [Update._from_proto(u) for u in resp.updates], resp.changes_through.token
        except grpc.aio.AioRpcError as e:
            raise to_spicedb_error(e) from e

    # ── Bulk operations ─────────────────────────────────────────────

    async def import_relationships(
        self, relationships: list[Relationship]
    ) -> int:
        """Import relationships in bulk. Returns the number loaded."""
        async def _request_iter():
            for i in range(0, len(relationships), _IMPORT_BATCH_SIZE):
                batch = relationships[i : i + _IMPORT_BATCH_SIZE]
                yield permission_service_pb2.ImportBulkRelationshipsRequest(
                    relationships=[r._to_proto() for r in batch]
                )

        try:
            resp = await self._permissions.ImportBulkRelationships(_request_iter(), metadata=self._metadata)
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
            request = permission_service_pb2.ExportBulkRelationshipsRequest(
                consistency=consistency._to_proto(),
                optional_limit=_DEFAULT_PAGE_SIZE,
                optional_cursor=cursor,
                optional_relationship_filter=rel_filter,
            )
            try:
                count = 0
                async for resp in self._permissions.ExportBulkRelationships(request, metadata=self._metadata):
                    for proto_rel in resp.relationships:
                        yield Relationship._from_proto(proto_rel)
                        count += 1
                    cursor = resp.after_result_cursor
                if count < _DEFAULT_PAGE_SIZE:
                    return
            except grpc.aio.AioRpcError as e:
                raise to_spicedb_error(e) from e

    # ── Expand ──────────────────────────────────────────────────────

    async def expand_permission_tree(
        self,
        resource: tuple[str, str],
        permission: str,
        consistency: Consistency,
    ) -> tuple[PermissionTree, str]:
        """Expand a permission tree. Returns (tree, revision)."""
        res_type, res_id = resource
        request = permission_service_pb2.ExpandPermissionTreeRequest(
            consistency=consistency._to_proto(),
            resource=core_pb2.ObjectReference(
                object_type=res_type,
                object_id=res_id,
            ),
            permission=permission,
        )

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
        request = experimental_service_pb2.ExperimentalRegisterRelationshipCounterRequest(
            name=name,
            relationship_filter=filter._to_proto(),
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
        request = experimental_service_pb2.ExperimentalUnregisterRelationshipCounterRequest(
            name=name,
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
        request = schema_service_pb2.ReflectSchemaRequest(
            consistency=consistency._to_proto(),
            optional_filters=[
                schema_service_pb2.ReflectionSchemaFilter(
                    optional_definition_name_filter=f,
                )
                for f in (filters or [])
            ],
        )

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
        request = schema_service_pb2.DiffSchemaRequest(
            consistency=consistency._to_proto(),
            comparison_schema=comparison_schema,
        )

        async def _call() -> schema_service_pb2.DiffSchemaResponse:
            return await self._schema.DiffSchema(request)

        resp = await self._with_retry(_call)
        return [_schema_diff_from_proto(d) for d in resp.diffs]


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
