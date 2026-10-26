"""Canonical native SpiceDB client. RawGrpc is the secondary transport API."""

from __future__ import annotations
from spicedb.options import Tracing, LookupPermissionshipFilter, LookupDebug, Wildcards, WatchContent, TransportSecurity, RemoteCredentials, _option
from spicedb.token import ZedToken, _token_string

import asyncio
from typing import Any
import random
from collections.abc import Iterable, AsyncIterable, Sequence
import grpc
import grpc.aio
from authzed.api.v1 import (
    core_pb2 as core,
    permission_service_pb2 as p,
    schema_service_pb2 as s,
    experimental_service_pb2 as e,
)
from authzed.api.v1 import (
    permission_service_pb2_grpc,
    schema_service_pb2_grpc,
    experimental_service_pb2_grpc,
    watch_service_pb2_grpc,
)
from authzed.api.materialize.v0 import (
    roaringlookupresources_pb2 as roaring,
    roaringlookupresources_pb2_grpc,
)
from authzed.api.materialize.v0 import (
    relationships_pb2 as materialize_relationships,
    relationships_pb2_grpc,
)
from authzed.api.materialize.v0 import (
    watchpermissionsets_pb2 as permission_sets,
    watchpermissionsets_pb2_grpc,
)
from spicedb import _mapping, _requests
from spicedb._auth import bearer_metadata, require_insecure_transport_allowed
from spicedb._tls import channel_credentials, require_tls_material_usable
from spicedb._budget import Budget
from spicedb._streams import AsyncStream
from spicedb.consistency import Consistency
from spicedb.errors import (
    InvalidArgumentError,
    SpiceDBError,
    EventLoopBindingError,
    DeadlineExceededError,
    is_transient,
    to_spicedb_error,
)
from spicedb.raw import RawGrpc
from spicedb.types import (
    PermissionSetCursor,
    PermissionSetEvent,
    PermissionSetLookup,
    PermissionSetDownload,
    ObjectRef,
    SubjectRef,
    PermissionCheck,
    CheckResult,
    JSONObject,
    Filter,
    Relationship,
    Transaction,
    SchemaFilter,
    SchemaResult,
    ExpandResult,
    ReflectSchemaResult,
    RelationsResult,
    RelationReference,
    SchemaDiffResult,
    CountResult,
    WriteResult,
    DeleteResult,
    DeleteProgress,
    RoaringLookupResourcesResult,
    Permissionship,
    LookupResource,
    LookupSubject,
    WatchEvent,
    _permission_tree_from_proto,
    _snapshot,
    _schema_diff_from_proto,
)


def _positive(value, field):
    if isinstance(value, bool) or not isinstance(value, int) or value <= 0:
        raise InvalidArgumentError(f"{field}: expected positive integer")
    return value


class SpiceDBClient:
    def __init__(
        self,
        endpoint: str,
        *,
        token: str,
        transport_security: TransportSecurity = TransportSecurity.TLS,
        remote_credentials: RemoteCredentials = RemoteCredentials.REJECT,
        ca_cert: bytes | None = None,
        client_cert: bytes | None = None,
        client_key: bytes | None = None,
        default_timeout: float = 30.0,
        max_retries: int = 3,
    ) -> None:
        insecure = _option(transport_security, TransportSecurity) == "plaintext"
        allow_insecure_remote_credentials = _option(remote_credentials, RemoteCredentials) == "allow"
        require_insecure_transport_allowed(
            endpoint,
            insecure=insecure,
            allow_insecure_remote_credentials=allow_insecure_remote_credentials,
        )
        require_tls_material_usable(
            insecure=insecure,
            ca_cert=ca_cert,
            client_cert=client_cert,
            client_key=client_key,
        )
        Budget(default_timeout)
        if default_timeout is None:
            raise InvalidArgumentError(
                "default_timeout: expected finite positive number"
            )
        if (
            isinstance(max_retries, bool)
            or not isinstance(max_retries, int)
            or (not 0 <= max_retries <= 3)
        ):
            raise InvalidArgumentError("max_retries: expected integer between 0 and 3")
        self._endpoint = endpoint
        self._insecure = insecure
        self._ca_cert = ca_cert
        self._client_cert = client_cert
        self._client_key = client_key
        self._metadata = bearer_metadata(token)
        self._default_timeout = default_timeout
        self._max_retries = max_retries
        self._channel = None
        self._loop: asyncio.AbstractEventLoop | None = None
        self._streams: set[Any] = set()

    def _ensure_channel(self) -> None:
        loop = asyncio.get_running_loop()
        if self._loop is not None and loop is not self._loop:
            raise EventLoopBindingError(
                "client is bound to another event loop; use spicedb.sync for synchronous code"
            )
        self._loop = loop
        if self._channel is not None:
            return
        self._channel = (
            grpc.aio.insecure_channel(self._endpoint)
            if self._insecure
            else grpc.aio.secure_channel(
                self._endpoint,
                channel_credentials(self._ca_cert, self._client_cert, self._client_key),
            )
        )
        self._permissions = permission_service_pb2_grpc.PermissionsServiceStub(
            self._channel
        )
        self._schema = schema_service_pb2_grpc.SchemaServiceStub(self._channel)
        self._watch = watch_service_pb2_grpc.WatchServiceStub(self._channel)
        self._experimental = experimental_service_pb2_grpc.ExperimentalServiceStub(
            self._channel
        )
        self._materialize = (
            roaringlookupresources_pb2_grpc.RoaringLookupResourcesServiceStub(
                self._channel
            )
        )

        self._materialize_relationships = (
            relationships_pb2_grpc.RelationshipsServiceStub(self._channel)
        )
        self._permission_sets = (
            watchpermissionsets_pb2_grpc.WatchPermissionSetsServiceStub(self._channel)
        )

    async def raw_grpc(self) -> RawGrpc:
        """Return this client's authenticated channel; raw calls own their options."""
        self._ensure_channel()
        return RawGrpc(channel=self._channel, metadata=tuple(self._metadata))

    async def close(self) -> None:
        """Close."""
        for stream in tuple(self._streams):
            await stream.aclose()
        if self._channel is not None:
            await self._channel.close()
            self._channel = None

    async def __aenter__(self) -> SpiceDBClient:
        self._ensure_channel()
        return self

    async def __aexit__(self, exc_type, exc_value, traceback) -> None:
        await self.close()

    @staticmethod
    def _backoff_seconds(attempt: int) -> float:
        return random.uniform(0, min(0.1 * 2**attempt, 5.0))

    async def _rpc(self, rpc, request, budget: Budget, retry: bool = False):
        for attempt in range(self._max_retries + 1):
            call = None
            try:
                call = rpc(request, metadata=self._metadata, timeout=budget.remaining())
                return await call
            except asyncio.CancelledError:
                if call is not None and hasattr(call, "cancel"):
                    call.cancel()
                raise
            except grpc.RpcError as error:
                if not retry or not is_transient(error) or attempt == self._max_retries:
                    raise to_spicedb_error(error) from error
                await asyncio.sleep(budget.delay(self._backoff_seconds(attempt)))
                budget.remaining()

    def _budget(self, timeout):
        return Budget(self._default_timeout if timeout is None else timeout)

    async def check(
        self,
        check: PermissionCheck,
        *,
        consistency: Consistency,
        context: JSONObject | None = None,
        tracing: Tracing = Tracing.DISABLED,
        timeout: float | None = None,
    ) -> CheckResult:
        """Check."""
        budget = self._budget(timeout)
        request = _requests.check_request(check, consistency, context, tracing)
        self._ensure_channel()
        return _mapping.check_result(
            await self._rpc(self._permissions.CheckPermission, request, budget, True)
        )

    async def check_bulk(
        self,
        checks: Sequence[PermissionCheck],
        *,
        consistency: Consistency,
        context: JSONObject | None = None,
        tracing: Tracing = Tracing.DISABLED,
        timeout: float | None = None,
    ) -> list[CheckResult]:
        """Check bulk."""
        _option(tracing, Tracing)
        budget = self._budget(timeout)
        requests = [
            _requests.check_bulk_request(
                consistency, checks[i : i + 1000], context, tracing
            )
            for i in range(0, len(checks), 1000)
        ]
        if not requests:
            return []
        self._ensure_channel()
        results = []
        for i, request in enumerate(requests):
            response = await self._rpc(
                self._permissions.CheckBulkPermissions, request, budget, True
            )
            results.extend(_mapping.check_results(response, request.items, i * 1000))
        return results

    async def write(
        self,
        transaction: Transaction,
        *,
        metadata: JSONObject | None = None,
        timeout: float | None = None,
    ) -> WriteResult:
        """Write."""
        budget = self._budget(timeout)
        request = _requests.write_request(transaction)
        if metadata is not None:
            request.optional_transaction_metadata.CopyFrom(
                _requests.context_struct(metadata)
            )
        self._ensure_channel()
        response = await self._rpc(
            self._permissions.WriteRelationships, request, budget
        )
        return WriteResult(revision=ZedToken(response.written_at.token))

    async def delete_relationships(
        self,
        filter: Filter,
        *,
        limit: int,
        must_match: Sequence[Filter] = (),
        must_not_match: Sequence[Filter] = (),
        metadata: JSONObject | None = None,
        timeout: float | None = None,
    ) -> DeleteResult:
        """Delete relationships."""
        budget = self._budget(timeout)
        _positive(limit, "limit")
        self._ensure_channel()
        return await self._delete_page(
            filter, limit, must_match, must_not_match, metadata, None, budget
        )

    async def _delete_page(
        self, filter, limit, must_match, must_not_match, metadata, cursor, budget
    ):
        request = _requests.delete_relationships_request(
            filter,
            must_match,
            must_not_match,
            limit,
            core.Cursor(token=cursor) if cursor else None,
        )
        if metadata is not None:
            request.optional_transaction_metadata.CopyFrom(
                _requests.context_struct(metadata)
            )
        response = await self._rpc(
            self._permissions.DeleteRelationships, request, budget
        )
        progress = {1: DeleteProgress.COMPLETE, 2: DeleteProgress.PARTIAL}.get(
            response.deletion_progress, DeleteProgress.UNSPECIFIED
        )
        return DeleteResult(
            ZedToken(response.deleted_at.token),
            response.relationships_deleted_count,
            progress,
            response.after_result_cursor.token
            if response.HasField("after_result_cursor")
            else None,
        )

    async def delete_all_relationships_non_atomic(
        self,
        filter: Filter,
        *,
        page_size: int = 1000,
        must_match: Sequence[Filter] = (),
        must_not_match: Sequence[Filter] = (),
        metadata: JSONObject | None = None,
        timeout: float | None = None,
    ) -> DeleteResult:
        """Delete matching relationships in individually transactional batches.

        The entire operation is non-atomic: completed batches remain deleted
        if a later batch fails. Error delete_progress carries acknowledged
        counts and the latest revision.
        """
        budget = self._budget(timeout)
        must_match = tuple(must_match)
        must_not_match = tuple(must_not_match)
        metadata = _snapshot(metadata)
        _positive(page_size, "page_size")
        self._ensure_channel()
        acknowledged = None
        cursor = None
        count = 0
        try:
            while True:
                page = await self._delete_page(
                    filter,
                    page_size,
                    must_match,
                    must_not_match,
                    metadata,
                    cursor,
                    budget,
                )
                count += page.deleted_count
                acknowledged = DeleteResult(
                    page.revision, count, page.progress, page.cursor
                )
                if page.progress == DeleteProgress.COMPLETE:
                    return acknowledged
                if page.progress == DeleteProgress.UNSPECIFIED:
                    raise SpiceDBError("delete-all received unspecified progress")
                cursor = page.cursor
        except BaseException as error:
            if acknowledged is not None:
                setattr(error, "delete_progress", acknowledged)
            raise

    async def read_schema(self, *, timeout: float | None = None) -> SchemaResult:
        """Read schema."""
        budget = self._budget(timeout)
        self._ensure_channel()
        response = await self._rpc(
            self._schema.ReadSchema, s.ReadSchemaRequest(), budget, True
        )
        return SchemaResult(response.schema_text, ZedToken(response.read_at.token))

    async def write_schema(self, schema: str, *, timeout: float | None = None) -> ZedToken:
        """Write schema."""
        budget = self._budget(timeout)
        self._ensure_channel()
        response = await self._rpc(
            self._schema.WriteSchema, s.WriteSchemaRequest(schema=schema), budget
        )
        return ZedToken(response.written_at.token)

    async def expand_permission_tree(
        self,
        *,
        resource: ObjectRef,
        permission: str,
        consistency: Consistency,
        timeout: float | None = None,
    ) -> ExpandResult:
        """Expand permission tree."""
        budget = self._budget(timeout)
        self._ensure_channel()
        response = await self._rpc(
            self._permissions.ExpandPermissionTree,
            _requests.expand_request(resource, permission, consistency),
            budget,
            True,
        )
        return ExpandResult(
            _permission_tree_from_proto(response.tree_root), ZedToken(response.expanded_at.token)
        )

    async def reflect_schema(
        self,
        *,
        consistency: Consistency,
        filters: Sequence[SchemaFilter] = (),
        timeout: float | None = None,
    ) -> ReflectSchemaResult:
        """Reflect schema."""
        budget = self._budget(timeout)
        self._ensure_channel()
        response = await self._rpc(
            self._schema.ReflectSchema,
            _requests.reflect_schema_request(consistency, filters),
            budget,
            True,
        )
        return ReflectSchemaResult._from_proto(response)

    async def computable_permissions(
        self,
        *,
        definition_name: str,
        relation_name: str,
        consistency: Consistency,
        definition_name_filter: str | None = None,
        timeout: float | None = None,
    ) -> RelationsResult:
        """Computable permissions."""
        budget = self._budget(timeout)
        self._ensure_channel()
        request = _requests.computable_permissions_request(
            consistency, definition_name, relation_name
        )
        request.optional_definition_name_filter = definition_name_filter or ""
        response = await self._rpc(
            self._schema.ComputablePermissions, request, budget, True
        )
        return RelationsResult(
            [RelationReference._from_proto(x) for x in response.permissions],
            ZedToken(response.read_at.token),
        )

    async def dependent_relations(
        self,
        *,
        definition_name: str,
        permission_name: str,
        consistency: Consistency,
        timeout: float | None = None,
    ) -> RelationsResult:
        """Dependent relations."""
        budget = self._budget(timeout)
        self._ensure_channel()
        response = await self._rpc(
            self._schema.DependentRelations,
            _requests.dependent_relations_request(
                consistency, definition_name, permission_name
            ),
            budget,
            True,
        )
        return RelationsResult(
            [RelationReference._from_proto(x) for x in response.relations],
            ZedToken(response.read_at.token),
        )

    async def diff_schema(
        self,
        comparison_schema: str,
        *,
        consistency: Consistency,
        timeout: float | None = None,
    ) -> SchemaDiffResult:
        """Diff schema."""
        budget = self._budget(timeout)
        self._ensure_channel()
        response = await self._rpc(
            self._schema.DiffSchema,
            _requests.diff_schema_request(consistency, comparison_schema),
            budget,
            True,
        )
        return SchemaDiffResult(
            [_schema_diff_from_proto(x) for x in response.diffs], ZedToken(response.read_at.token)
        )

    async def experimental_register_relationship_counter(
        self, name: str, filter: Filter, *, timeout: float | None = None
    ) -> None:
        """Experimental: register a stored relationship counter."""
        budget = self._budget(timeout)
        self._ensure_channel()
        await self._rpc(
            self._experimental.ExperimentalRegisterRelationshipCounter,
            e.ExperimentalRegisterRelationshipCounterRequest(
                name=name, relationship_filter=filter._to_proto()
            ),
            budget,
        )

    async def experimental_unregister_relationship_counter(
        self, name: str, *, timeout: float | None = None
    ) -> None:
        """Experimental: unregister a stored relationship counter."""
        budget = self._budget(timeout)
        self._ensure_channel()
        await self._rpc(
            self._experimental.ExperimentalUnregisterRelationshipCounter,
            e.ExperimentalUnregisterRelationshipCounterRequest(name=name),
            budget,
        )

    async def experimental_count_relationships(
        self, name: str, *, timeout: float | None = None
    ) -> CountResult:
        """Experimental: inspect a stored relationship counter."""
        budget = self._budget(timeout)
        self._ensure_channel()
        response = await self._rpc(
            self._experimental.ExperimentalCountRelationships,
            e.ExperimentalCountRelationshipsRequest(name=name),
            budget,
            True,
        )
        if response.HasField("read_counter_value"):
            return CountResult(
                response.read_counter_value.relationship_count,
                ZedToken(response.read_counter_value.read_at.token),
            )
        return CountResult(None, None, response.counter_still_calculating)

    async def experimental_roaring_lookup_resources(
        self,
        *,
        resource_type: str,
        permission: str,
        subject: SubjectRef,
        consistency: Consistency,
        timeout: float | None = None,
    ) -> RoaringLookupResourcesResult:
        """Experimental: return the server's opaque roaring64 bitmap."""
        budget = self._budget(timeout)
        self._ensure_channel()
        request = roaring.ExperimentalRoaringLookupResourcesRequest(
            resource_object_type=resource_type,
            permission=permission,
            subject=subject._to_proto(),
            consistency=consistency._to_proto(),
        )
        response = await self._rpc(
            self._materialize.ExperimentalRoaringLookupResources, request, budget, True
        )
        return RoaringLookupResourcesResult(
            response.bitmap, response.cardinality, ZedToken(response.at_revision.token)
        )

    def _stream(
        self,
        rpc_factory,
        request_factory,
        mapper,
        budget,
        page_size=None,
        limit=None,
        watch=False,
        include=None,
    ):

        async def factory(owner):
            """Factory."""
            self._ensure_channel()
            cursor = None
            delivered = 0
            try:
                while not owner._closed:
                    page_count = 0
                    requested_size = min(page_size, limit - delivered) if page_size and limit is not None else page_size
                    request = request_factory(cursor, requested_size)
                    for attempt in range(self._max_retries + 1):
                        call = None
                        try:
                            call = rpc_factory()(
                                request,
                                metadata=self._metadata,
                                timeout=budget.remaining(),
                            )
                            owner._set_call(call)
                            async for response in call:
                                budget.remaining()
                                cursor = (
                                    response.after_result_cursor
                                    if hasattr(response, "after_result_cursor")
                                    and response.HasField("after_result_cursor")
                                    else None
                                )
                                for item in mapper(response):
                                    if owner._closed:
                                        return
                                    page_count += 1
                                    if include is not None and not include(item):
                                        continue
                                    delivered += 1
                                    yield item
                                    if limit is not None and delivered >= limit:
                                        return
                            break
                        except grpc.RpcError as error:
                            if (
                                watch
                                or delivered
                                or (not is_transient(error))
                                or (attempt == self._max_retries)
                            ):
                                raise to_spicedb_error(error) from error
                            await asyncio.sleep(
                                budget.delay(self._backoff_seconds(attempt))
                            )
                            budget.remaining()
                        finally:
                            if call is not None:
                                call.cancel()
                    if page_size is None or page_count < requested_size or cursor is None:
                        return
            finally:
                if owner._call is not None:
                    owner._call.cancel()

        result = AsyncStream(factory, self._streams.discard)
        self._streams.add(result)
        return result

    def read_relationships(
        self,
        filter: Filter,
        *,
        consistency: Consistency,
        page_size: int = 1000,
        timeout: float | None = None,
    ) -> AsyncStream[Relationship]:
        """Read relationships."""
        budget = Budget(timeout)
        _positive(page_size, "page_size")
        filter._to_proto()
        return self._stream(
            lambda: self._permissions.ReadRelationships,
            lambda cursor, size: _requests.read_relationships_request(
                filter, consistency, size, cursor
            ),
            lambda response: [Relationship._from_proto(response.relationship)],
            budget,
            page_size,
        )

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
        timeout: float | None = None,
    ) -> AsyncStream[LookupResource]:
        """Lookup resources, including conditional matches by default.

        HAS_PERMISSION_ONLY filters client-side to unconditional grants; limit
        counts yielded matches and skipped results still advance pagination.
        """
        _option(permissionship_filter, LookupPermissionshipFilter)
        _option(debug, LookupDebug)
        budget = Budget(timeout)
        _positive(page_size, "page_size")
        if limit is not None:
            _positive(limit, "limit")
        subj = subject._to_proto()
        ctx = _requests.context_struct(context)

        def request(cursor, size):
            """Request."""
            value = _requests.lookup_resources_request(
                resource_type, permission, subj, ctx, consistency, size, cursor
            )
            value.with_debug = _option(debug, LookupDebug)
            return value

        return self._stream(
            lambda: self._permissions.LookupResources,
            request,
            lambda response: [_mapping.lookup_resource(response)],
            budget,
            page_size,
            limit,
            include=(lambda result: result.permissionship is Permissionship.HAS_PERMISSION)
            if permissionship_filter is LookupPermissionshipFilter.HAS_PERMISSION_ONLY else None,
        )

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
    ) -> AsyncStream[LookupSubject]:
        """Lookup subjects."""
        _option(wildcards, Wildcards)
        budget = Budget(timeout)
        if page_size is not None:
            _positive(page_size, "page_size")
        if limit is not None:
            _positive(limit, "limit")
        ctx = _requests.context_struct(context)

        def request(cursor, size):
            """Request."""
            value = _requests.lookup_subjects_request(
                resource,
                permission,
                subject_type,
                subject_relation or "",
                ctx,
                consistency,
            )
            if size is not None:
                value.optional_concrete_limit = size
            if cursor is not None:
                value.optional_cursor.CopyFrom(cursor)
            value.wildcard_option = _option(wildcards, Wildcards)
            return value

        return self._stream(
            lambda: self._permissions.LookupSubjects,
            request,
            lambda response: [_mapping.lookup_subject(response)],
            budget,
            page_size,
            limit,
        )

    def export_relationships(
        self,
        filter: Filter | None = None,
        *,
        consistency: Consistency,
        page_size: int = 1000,
        timeout: float | None = None,
    ) -> AsyncStream[Relationship]:
        """Export relationships."""
        budget = Budget(timeout)
        _positive(page_size, "page_size")
        rel_filter = filter._to_proto() if filter is not None else None
        return self._stream(
            lambda: self._permissions.ExportBulkRelationships,
            lambda cursor, size: _requests.export_request(
                consistency, rel_filter, size, cursor
            ),
            lambda response: [
                Relationship._from_proto(x) for x in response.relationships
            ],
            budget,
            page_size,
        )

    def watch(
        self,
        object_types: Sequence[str] = (),
        *,
        start_revision: ZedToken | None = None,
        content: Sequence[WatchContent] = (WatchContent.RELATIONSHIPS,),
        filters: Sequence[Filter] = (),
        timeout: float | None = None,
    ) -> AsyncStream[WatchEvent]:
        """Watch changes matching any supplied relationship filter.

        Empty filters watch all relationship changes. Nonempty object_types
        and filters are mutually exclusive. Inputs are copied before streaming.
        """
        budget = Budget(timeout)
        request = _requests.watch_request(
            tuple(object_types), start_revision, content, filters=tuple(filters)
        )
        return self._stream(
            lambda: self._watch.Watch,
            lambda cursor, size: request,
            lambda response: [_mapping.watch_event(response)],
            budget,
            watch=True,
        )

    async def import_relationships(
        self,
        relationships: Iterable[Relationship] | AsyncIterable[Relationship],
        *,
        timeout: float | None = None,
    ) -> int:
        """Import relationships."""
        budget = Budget(timeout)
        self._ensure_channel()

        async def batches():
            """Batches."""
            batch = []

            async def source():
                """Source."""
                if isinstance(relationships, AsyncIterable):
                    async for item in relationships:
                        yield item
                else:
                    for item in relationships:
                        yield item

            async for item in source():
                batch.append(item._to_proto())
                if len(batch) == 1000:
                    yield p.ImportBulkRelationshipsRequest(relationships=batch)
                    batch = []
            if batch:
                yield p.ImportBulkRelationshipsRequest(relationships=batch)

        rpc = self._permissions.ImportBulkRelationships
        call = None
        producer = batches()
        tasks: list[asyncio.Task] = []
        deadline = asyncio.timeout(budget.remaining())
        try:
            async with deadline:
                if not isinstance(rpc, grpc.aio.StreamUnaryMultiCallable):
                    response = await self._rpc(rpc, producer, budget)
                else:
                    call = rpc(metadata=self._metadata, timeout=budget.remaining())

                    producer_error = None

                    async def send():
                        nonlocal producer_error
                        while True:
                            try:
                                batch = await anext(producer)
                            except StopAsyncIteration:
                                break
                            except BaseException as error:
                                producer_error = error
                                raise
                            budget.remaining()
                            await call.write(batch)
                        await call.done_writing()

                    async def receive():
                        return await call

                    sender = asyncio.create_task(send())
                    receiver = asyncio.create_task(receive())
                    tasks.extend((sender, receiver))
                    await asyncio.wait(tasks, return_when=asyncio.FIRST_COMPLETED)
                    # Keep a producer's own failure when both sides terminate
                    # together; otherwise observe early server rejection even
                    # while the next source item is still blocked.
                    if (
                        sender.done()
                        and not sender.cancelled()
                        and sender.exception() is not None
                    ):
                        if producer_error is not None or not call.done():
                            await sender
                    response = await receiver
                return response.num_loaded
        except BaseException as error:
            if call is not None:
                call.cancel()
            if isinstance(error, TimeoutError) and deadline.expired():
                raise DeadlineExceededError("operation deadline exceeded") from error
            if isinstance(error, grpc.RpcError):
                raise to_spicedb_error(error) from error
            raise
        finally:
            for task in tasks:
                if not task.done():
                    task.cancel()
            if tasks:
                await asyncio.gather(*tasks, return_exceptions=True)
            await producer.aclose()
            close = getattr(relationships, "aclose", None)
            if close is not None:
                await close()

    async def experimental_count_relationships_by_filter(
        self, filter: Filter, *, timeout: float | None = None
    ) -> CountResult:
        """Experimental: count matching tuples without registering a counter."""
        budget = self._budget(timeout)
        request = (
            materialize_relationships.ExperimentalCountRelationshipsByFilterRequest(
                relationship_filter=filter._to_proto()
            )
        )
        self._ensure_channel()
        response = await self._rpc(
            self._materialize_relationships.ExperimentalCountRelationshipsByFilter,
            request,
            budget,
            True,
        )
        return CountResult(response.relationship_count, ZedToken(response.read_at.token))

    def watch_permission_sets(
        self, *, start_revision: ZedToken | None = None, timeout: float | None = None
    ) -> AsyncStream[PermissionSetEvent]:
        """Materialize: watch normalized set changes; never auto-reconnects."""
        budget = Budget(timeout)
        request = permission_sets.WatchPermissionSetsRequest(
            optional_starting_after=core.ZedToken(token=_token_string(start_revision))
            if start_revision is not None
            else None
        )
        return self._stream(
            lambda: self._permission_sets.WatchPermissionSets,
            lambda cursor, size: request,
            lambda response: [_mapping.permission_set_event(response)],
            budget,
            watch=True,
        )

    def lookup_permission_sets(
        self,
        *,
        limit: int = 1000,
        at_revision: ZedToken | None = None,
        cursor: PermissionSetCursor | None = None,
        timeout: float | None = None,
    ) -> AsyncStream[PermissionSetLookup]:
        """Materialize: one bounded stream; resume explicitly with its native cursor."""
        budget = Budget(timeout)
        _positive(limit, "limit")
        if limit > 0xFFFFFFFF:
            raise InvalidArgumentError("limit: exceeds uint32")
        request = permission_sets.LookupPermissionSetsRequest(
            limit=limit,
            optional_at_revision=core.ZedToken(token=_token_string(at_revision))
            if at_revision is not None
            else None,
            optional_starting_after_cursor=cursor._to_proto()
            if cursor is not None
            else None,
        )
        return self._stream(
            lambda: self._permission_sets.LookupPermissionSets,
            lambda cursor, size: request,
            lambda response: [_mapping.permission_set_lookup(response)],
            budget,
            limit=limit,
        )

    async def download_permission_sets(
        self, *, at_revision: ZedToken | None = None, timeout: float | None = None
    ) -> PermissionSetDownload:
        """Materialize: return native file URLs without downloading their contents."""
        budget = self._budget(timeout)
        request = permission_sets.DownloadPermissionSetsRequest(
            optional_at_revision=core.ZedToken(token=_token_string(at_revision))
            if at_revision is not None
            else None
        )
        self._ensure_channel()
        response = await self._rpc(
            self._permission_sets.DownloadPermissionSets, request, budget, True
        )
        return _mapping.permission_set_download(response)
