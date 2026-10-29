"""Protocols naming the native clients' full public surface.

:class:`ClientProtocol` is every public method of
:class:`spicedb.sync.SpiceDBClient`; :class:`AsyncClientProtocol` is the same
for :class:`spicedb.aio.SpiceDBClient`. Each real client satisfies its
protocol, and so does the matching :mod:`spicedb.testing` double
(:class:`~spicedb.testing.InMemoryClient`,
:class:`~spicedb.testing.AsyncInMemoryClient`), so code typed against a
protocol accepts either. A test keeps each protocol identical to its client's
public methods (names, parameters, defaults and return annotations).

Depend on the protocol where tests substitute the double, or define a smaller
``Protocol`` of your own naming only the methods you call.
"""

from __future__ import annotations

from collections.abc import AsyncIterable, Callable, Iterable, Sequence
from typing import Any, Protocol

from spicedb._streams import AsyncStream, Stream
from spicedb.consistency import Consistency
from spicedb.options import (
    BulkItemErrors,
    CallOptions,
    DeletePartial,
    LookupDebug,
    LookupPermissionshipFilter,
    Paging,
    Tracing,
    WatchContent,
    Wildcards,
)
from spicedb.raw import RawGrpc
from spicedb.token import ZedToken
from spicedb.types import (
    CheckResult,
    CountResult,
    DeleteResult,
    ExpandResult,
    ExportedRelationship,
    Filter,
    JSONObject,
    LookupResource,
    LookupSubject,
    ObjectRef,
    PermissionCheck,
    PermissionSetCursor,
    PermissionSetDownload,
    PermissionSetEvent,
    PermissionSetLookup,
    ReadRelationship,
    ReflectSchemaResult,
    Relationship,
    RelationsResult,
    RoaringLookupResourcesResult,
    SchemaDiffResult,
    SchemaFilter,
    SchemaResult,
    SubjectRef,
    Transaction,
    WatchEvent,
    WriteResult,
)

__all__ = ["ClientProtocol", "AsyncClientProtocol"]


class ClientProtocol(Protocol):
    """Every public method of :class:`spicedb.sync.SpiceDBClient`."""

    def raw_grpc(self) -> RawGrpc:
        """Return this client's authenticated channel; raw calls own their options."""
        ...

    def connect(self, *, timeout: float | None = None) -> None:
        """Wait until the channel is connected, as a startup readiness check."""
        ...

    def close(self) -> None:
        """Close streams and, unless it was supplied by the caller, the channel."""
        ...

    def __enter__(self) -> ClientProtocol:
        ...

    def __exit__(self, exc_type, exc_value, traceback) -> None:
        ...

    def check(
        self,
        check: PermissionCheck,
        *,
        consistency: Consistency,
        context: JSONObject | None = None,
        tracing: Tracing = Tracing.DISABLED,
        timeout: float | None = None,
        call_options: CallOptions | None = None,
    ) -> CheckResult:
        """Check."""
        ...

    def check_bulk(
        self,
        checks: Sequence[PermissionCheck],
        *,
        consistency: Consistency,
        context: JSONObject | None = None,
        tracing: Tracing = Tracing.DISABLED,
        item_errors: BulkItemErrors = BulkItemErrors.RAISE,
        timeout: float | None = None,
        call_options: CallOptions | None = None,
    ) -> list[CheckResult]:
        """Check bulk."""
        ...

    def write(
        self,
        transaction: Transaction,
        *,
        metadata: JSONObject | None = None,
        timeout: float | None = None,
        call_options: CallOptions | None = None,
    ) -> WriteResult:
        """Write the transaction atomically."""
        ...

    def delete_relationships(
        self,
        filter: Filter,
        *,
        limit: int | None = None,
        partial: DeletePartial = DeletePartial.ALLOWED,
        must_match: Sequence[Filter] = (),
        must_not_match: Sequence[Filter] = (),
        metadata: JSONObject | None = None,
        timeout: float | None = None,
        call_options: CallOptions | None = None,
    ) -> DeleteResult:
        """Delete relationships in one request."""
        ...

    def delete_all_relationships_non_atomic(
        self,
        filter: Filter,
        *,
        page_size: int = 1000,
        must_match: Sequence[Filter] = (),
        must_not_match: Sequence[Filter] = (),
        metadata: JSONObject | None = None,
        on_progress: Callable[[DeleteResult], Any] | None = None,
        timeout: float | None = None,
        call_options: CallOptions | None = None,
    ) -> DeleteResult:
        """Delete matching relationships in individually transactional batches."""
        ...

    def read_schema(
        self, *, timeout: float | None = None, call_options: CallOptions | None = None
    ) -> SchemaResult:
        """Read schema."""
        ...

    def write_schema(
        self, schema: str, *, timeout: float | None = None, call_options: CallOptions | None = None
    ) -> ZedToken:
        """Write schema. Idempotent, so it may opt in to retries with ``CallOptions(retry_policy=...)``."""
        ...

    def expand_permission_tree(
        self,
        *,
        resource: ObjectRef,
        permission: str,
        consistency: Consistency,
        timeout: float | None = None,
        call_options: CallOptions | None = None,
    ) -> ExpandResult:
        """Expand permission tree."""
        ...

    def reflect_schema(
        self,
        *,
        consistency: Consistency,
        filters: Sequence[SchemaFilter] = (),
        timeout: float | None = None,
        call_options: CallOptions | None = None,
    ) -> ReflectSchemaResult:
        """Reflect schema."""
        ...

    def computable_permissions(
        self,
        *,
        definition_name: str,
        relation_name: str,
        consistency: Consistency,
        definition_name_filter: str | None = None,
        timeout: float | None = None,
        call_options: CallOptions | None = None,
    ) -> RelationsResult:
        """Computable permissions."""
        ...

    def dependent_relations(
        self,
        *,
        definition_name: str,
        permission_name: str,
        consistency: Consistency,
        timeout: float | None = None,
        call_options: CallOptions | None = None,
    ) -> RelationsResult:
        """Dependent relations."""
        ...

    def diff_schema(
        self,
        comparison_schema: str,
        *,
        consistency: Consistency,
        timeout: float | None = None,
        call_options: CallOptions | None = None,
    ) -> SchemaDiffResult:
        """Diff schema."""
        ...

    def experimental_register_relationship_counter(
        self, name: str, filter: Filter, *, timeout: float | None = None, call_options: CallOptions | None = None
    ) -> None:
        """Experimental: register a stored relationship counter."""
        ...

    def experimental_unregister_relationship_counter(
        self, name: str, *, timeout: float | None = None, call_options: CallOptions | None = None
    ) -> None:
        """Experimental: unregister a stored relationship counter."""
        ...

    def experimental_count_relationships(
        self, name: str, *, timeout: float | None = None, call_options: CallOptions | None = None
    ) -> CountResult:
        """Experimental: inspect a stored relationship counter."""
        ...

    def experimental_roaring_lookup_resources(
        self,
        *,
        resource_type: str,
        permission: str,
        subject: SubjectRef,
        consistency: Consistency,
        timeout: float | None = None,
        call_options: CallOptions | None = None,
    ) -> RoaringLookupResourcesResult:
        """Experimental: return the server's opaque roaring64 bitmap."""
        ...

    def read_relationships(
        self,
        filter: Filter,
        *,
        consistency: Consistency,
        page_size: int = 1000,
        cursor: str | None = None,
        paging: Paging = Paging.AUTOMATIC,
        timeout: float | None = None,
        call_options: CallOptions | None = None,
    ) -> Stream[ReadRelationship]:
        """Read relationships, each with its after-result cursor and read-at revision."""
        ...

    def lookup_resources(
        self,
        *,
        resource_type: str,
        permission: str,
        subject: SubjectRef,
        consistency: Consistency,
        context: JSONObject | None = None,
        permissionship_filter: LookupPermissionshipFilter = LookupPermissionshipFilter.ALL,
        limit: int | None = None,
        page_size: int = 1000,
        debug: LookupDebug = LookupDebug.DISABLED,
        cursor: str | None = None,
        paging: Paging = Paging.AUTOMATIC,
        timeout: float | None = None,
        call_options: CallOptions | None = None,
    ) -> Stream[LookupResource]:
        """Lookup resources, including conditional matches by default."""
        ...

    def lookup_subjects(
        self,
        *,
        resource: ObjectRef,
        permission: str,
        subject_type: str,
        consistency: Consistency,
        subject_relation: str | None = None,
        context: JSONObject | None = None,
        page_size: int | None = None,
        limit: int | None = None,
        wildcards: Wildcards = Wildcards.DEFAULT,
        timeout: float | None = None,
        call_options: CallOptions | None = None,
    ) -> Stream[LookupSubject]:
        """Lookup subjects."""
        ...

    def export_relationships(
        self,
        filter: Filter | None = None,
        *,
        consistency: Consistency,
        page_size: int = 1000,
        cursor: str | None = None,
        timeout: float | None = None,
        call_options: CallOptions | None = None,
    ) -> Stream[ExportedRelationship]:
        """Export relationships, each with its response's after-result cursor."""
        ...

    def watch(
        self,
        object_types: Sequence[str] = (),
        *,
        start_revision: ZedToken | None = None,
        content: Sequence[WatchContent] = (WatchContent.RELATIONSHIPS,),
        filters: Sequence[Filter] = (),
        timeout: float | None = None,
        call_options: CallOptions | None = None,
    ) -> Stream[WatchEvent]:
        """Watch changes matching any supplied relationship filter."""
        ...

    def import_relationships(
        self,
        relationships: Iterable[Relationship],
        *,
        batch_size: int = 1000,
        timeout: float | None = None,
        call_options: CallOptions | None = None,
    ) -> int:
        """Import relationships in one stream and one transaction."""
        ...

    def experimental_count_relationships_by_filter(
        self, filter: Filter, *, timeout: float | None = None, call_options: CallOptions | None = None
    ) -> CountResult:
        """Experimental: count matching tuples without registering a counter."""
        ...

    def watch_permission_sets(
        self,
        *,
        start_revision: ZedToken | None = None,
        timeout: float | None = None,
        call_options: CallOptions | None = None,
    ) -> Stream[PermissionSetEvent]:
        """Materialize: watch normalized set changes; never auto-reconnects."""
        ...

    def lookup_permission_sets(
        self,
        *,
        limit: int = 1000,
        at_revision: ZedToken | None = None,
        cursor: PermissionSetCursor | None = None,
        timeout: float | None = None,
        call_options: CallOptions | None = None,
    ) -> Stream[PermissionSetLookup]:
        """Materialize: one bounded stream; resume explicitly with its native cursor."""
        ...

    def download_permission_sets(
        self,
        *,
        at_revision: ZedToken | None = None,
        timeout: float | None = None,
        call_options: CallOptions | None = None,
    ) -> PermissionSetDownload:
        """Materialize: return native file URLs without downloading their contents."""
        ...


class AsyncClientProtocol(Protocol):
    """Every public method of :class:`spicedb.aio.SpiceDBClient`."""

    async def raw_grpc(self) -> RawGrpc:
        """Return this client's authenticated channel; raw calls own their options."""
        ...

    async def connect(self, *, timeout: float | None = None) -> None:
        """Wait until the channel is connected, as a startup readiness check."""
        ...

    async def close(self) -> None:
        """Close streams and, unless it was supplied by the caller, the channel."""
        ...

    async def __aenter__(self) -> AsyncClientProtocol:
        ...

    async def __aexit__(self, exc_type, exc_value, traceback) -> None:
        ...

    async def check(
        self,
        check: PermissionCheck,
        *,
        consistency: Consistency,
        context: JSONObject | None = None,
        tracing: Tracing = Tracing.DISABLED,
        timeout: float | None = None,
        call_options: CallOptions | None = None,
    ) -> CheckResult:
        """Check."""
        ...

    async def check_bulk(
        self,
        checks: Sequence[PermissionCheck],
        *,
        consistency: Consistency,
        context: JSONObject | None = None,
        tracing: Tracing = Tracing.DISABLED,
        item_errors: BulkItemErrors = BulkItemErrors.RAISE,
        timeout: float | None = None,
        call_options: CallOptions | None = None,
    ) -> list[CheckResult]:
        """Check bulk."""
        ...

    async def write(
        self,
        transaction: Transaction,
        *,
        metadata: JSONObject | None = None,
        timeout: float | None = None,
        call_options: CallOptions | None = None,
    ) -> WriteResult:
        """Write the transaction atomically."""
        ...

    async def delete_relationships(
        self,
        filter: Filter,
        *,
        limit: int | None = None,
        partial: DeletePartial = DeletePartial.ALLOWED,
        must_match: Sequence[Filter] = (),
        must_not_match: Sequence[Filter] = (),
        metadata: JSONObject | None = None,
        timeout: float | None = None,
        call_options: CallOptions | None = None,
    ) -> DeleteResult:
        """Delete relationships in one request."""
        ...

    async def delete_all_relationships_non_atomic(
        self,
        filter: Filter,
        *,
        page_size: int = 1000,
        must_match: Sequence[Filter] = (),
        must_not_match: Sequence[Filter] = (),
        metadata: JSONObject | None = None,
        on_progress: Callable[[DeleteResult], Any] | None = None,
        timeout: float | None = None,
        call_options: CallOptions | None = None,
    ) -> DeleteResult:
        """Delete matching relationships in individually transactional batches."""
        ...

    async def read_schema(
        self, *, timeout: float | None = None, call_options: CallOptions | None = None
    ) -> SchemaResult:
        """Read schema."""
        ...

    async def write_schema(
        self, schema: str, *, timeout: float | None = None, call_options: CallOptions | None = None
    ) -> ZedToken:
        """Write schema. Idempotent, so it may opt in to retries with ``CallOptions(retry_policy=...)``."""
        ...

    async def expand_permission_tree(
        self,
        *,
        resource: ObjectRef,
        permission: str,
        consistency: Consistency,
        timeout: float | None = None,
        call_options: CallOptions | None = None,
    ) -> ExpandResult:
        """Expand permission tree."""
        ...

    async def reflect_schema(
        self,
        *,
        consistency: Consistency,
        filters: Sequence[SchemaFilter] = (),
        timeout: float | None = None,
        call_options: CallOptions | None = None,
    ) -> ReflectSchemaResult:
        """Reflect schema."""
        ...

    async def computable_permissions(
        self,
        *,
        definition_name: str,
        relation_name: str,
        consistency: Consistency,
        definition_name_filter: str | None = None,
        timeout: float | None = None,
        call_options: CallOptions | None = None,
    ) -> RelationsResult:
        """Computable permissions."""
        ...

    async def dependent_relations(
        self,
        *,
        definition_name: str,
        permission_name: str,
        consistency: Consistency,
        timeout: float | None = None,
        call_options: CallOptions | None = None,
    ) -> RelationsResult:
        """Dependent relations."""
        ...

    async def diff_schema(
        self,
        comparison_schema: str,
        *,
        consistency: Consistency,
        timeout: float | None = None,
        call_options: CallOptions | None = None,
    ) -> SchemaDiffResult:
        """Diff schema."""
        ...

    async def experimental_register_relationship_counter(
        self, name: str, filter: Filter, *, timeout: float | None = None, call_options: CallOptions | None = None
    ) -> None:
        """Experimental: register a stored relationship counter."""
        ...

    async def experimental_unregister_relationship_counter(
        self, name: str, *, timeout: float | None = None, call_options: CallOptions | None = None
    ) -> None:
        """Experimental: unregister a stored relationship counter."""
        ...

    async def experimental_count_relationships(
        self, name: str, *, timeout: float | None = None, call_options: CallOptions | None = None
    ) -> CountResult:
        """Experimental: inspect a stored relationship counter."""
        ...

    async def experimental_roaring_lookup_resources(
        self,
        *,
        resource_type: str,
        permission: str,
        subject: SubjectRef,
        consistency: Consistency,
        timeout: float | None = None,
        call_options: CallOptions | None = None,
    ) -> RoaringLookupResourcesResult:
        """Experimental: return the server's opaque roaring64 bitmap."""
        ...

    def read_relationships(
        self,
        filter: Filter,
        *,
        consistency: Consistency,
        page_size: int = 1000,
        cursor: str | None = None,
        paging: Paging = Paging.AUTOMATIC,
        timeout: float | None = None,
        call_options: CallOptions | None = None,
    ) -> AsyncStream[ReadRelationship]:
        """Read relationships, each with its after-result cursor and read-at revision."""
        ...

    def lookup_resources(
        self,
        *,
        resource_type: str,
        permission: str,
        subject: SubjectRef,
        consistency: Consistency,
        context: JSONObject | None = None,
        permissionship_filter: LookupPermissionshipFilter = LookupPermissionshipFilter.ALL,
        limit: int | None = None,
        page_size: int = 1000,
        debug: LookupDebug = LookupDebug.DISABLED,
        cursor: str | None = None,
        paging: Paging = Paging.AUTOMATIC,
        timeout: float | None = None,
        call_options: CallOptions | None = None,
    ) -> AsyncStream[LookupResource]:
        """Lookup resources, including conditional matches by default."""
        ...

    def lookup_subjects(
        self,
        *,
        resource: ObjectRef,
        permission: str,
        subject_type: str,
        consistency: Consistency,
        subject_relation: str | None = None,
        context: JSONObject | None = None,
        page_size: int | None = None,
        limit: int | None = None,
        wildcards: Wildcards = Wildcards.DEFAULT,
        timeout: float | None = None,
        call_options: CallOptions | None = None,
    ) -> AsyncStream[LookupSubject]:
        """Lookup subjects."""
        ...

    def export_relationships(
        self,
        filter: Filter | None = None,
        *,
        consistency: Consistency,
        page_size: int = 1000,
        cursor: str | None = None,
        timeout: float | None = None,
        call_options: CallOptions | None = None,
    ) -> AsyncStream[ExportedRelationship]:
        """Export relationships, each with its response's after-result cursor."""
        ...

    def watch(
        self,
        object_types: Sequence[str] = (),
        *,
        start_revision: ZedToken | None = None,
        content: Sequence[WatchContent] = (WatchContent.RELATIONSHIPS,),
        filters: Sequence[Filter] = (),
        timeout: float | None = None,
        call_options: CallOptions | None = None,
    ) -> AsyncStream[WatchEvent]:
        """Watch changes matching any supplied relationship filter."""
        ...

    async def import_relationships(
        self,
        relationships: Iterable[Relationship] | AsyncIterable[Relationship],
        *,
        batch_size: int = 1000,
        timeout: float | None = None,
        call_options: CallOptions | None = None,
    ) -> int:
        """Import relationships in one stream and one transaction."""
        ...

    async def experimental_count_relationships_by_filter(
        self, filter: Filter, *, timeout: float | None = None, call_options: CallOptions | None = None
    ) -> CountResult:
        """Experimental: count matching tuples without registering a counter."""
        ...

    def watch_permission_sets(
        self,
        *,
        start_revision: ZedToken | None = None,
        timeout: float | None = None,
        call_options: CallOptions | None = None,
    ) -> AsyncStream[PermissionSetEvent]:
        """Materialize: watch normalized set changes; never auto-reconnects."""
        ...

    def lookup_permission_sets(
        self,
        *,
        limit: int = 1000,
        at_revision: ZedToken | None = None,
        cursor: PermissionSetCursor | None = None,
        timeout: float | None = None,
        call_options: CallOptions | None = None,
    ) -> AsyncStream[PermissionSetLookup]:
        """Materialize: one bounded stream; resume explicitly with its native cursor."""
        ...

    async def download_permission_sets(
        self,
        *,
        at_revision: ZedToken | None = None,
        timeout: float | None = None,
        call_options: CallOptions | None = None,
    ) -> PermissionSetDownload:
        """Materialize: return native file URLs without downloading their contents."""
        ...
