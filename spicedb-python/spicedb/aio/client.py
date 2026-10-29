"""Canonical native SpiceDB client. RawGrpc is the secondary transport API."""

from __future__ import annotations
from spicedb.options import (
    Tracing,
    LookupPermissionshipFilter,
    LookupDebug,
    Wildcards,
    WatchContent,
    TransportSecurity,
    RemoteCredentials,
    DefaultDeadline,
    RetryPolicy,
    Retries,
    StreamRetries,
    BulkItemErrors,
    DeletePartial,
    Paging,
    CallOptions,
    DEFAULT_RETRY_POLICY,
    Operation,
    _option,
)
from spicedb.token import ZedToken, _token_string

import asyncio
import time
import inspect
from typing import Any
from collections.abc import Callable, Iterable, AsyncIterable, Mapping, Sequence
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
from spicedb import _mapping, _requests, _wire
from spicedb._auth import bearer_metadata, require_insecure_transport_allowed
from spicedb._endpoint import parse_endpoint
from spicedb._tls import channel_credentials, require_tls_material_usable
from spicedb._budget import Budget
from spicedb._client_base import (
    MESSAGE,
    PAGE,
    PROBE_METHOD,
    PROBE_TIMEOUT,
    ClientConfig,
    _positive,
    capture_async,
    not_ready_error,
    response_target,
    stalled_error,
    write_is_idempotent,
)
from spicedb._streams import AsyncStream
from spicedb.consistency import Consistency
from spicedb.errors import (
    LocalValidationError,
    SpiceDBError,
    EventLoopBindingError,
    DeadlineExceededError,
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
    ReadRelationship,
    ExportedRelationship,
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
    _snapshot,
    _schema_diff_from_proto,
)


def _cursor(value: str | None, field: str):
    if value is None:
        return None
    if not isinstance(value, str) or not value:
        raise LocalValidationError(f"{field}: expected nonempty cursor string")
    return core.Cursor(token=value)


def _response_cursor(response) -> str | None:
    if response.HasField("after_result_cursor") and response.after_result_cursor.token:
        return response.after_result_cursor.token
    return None


class SpiceDBClient(ClientConfig):
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
        default_timeout: float | DefaultDeadline = 30.0,
        retry_policy: RetryPolicy | Retries = DEFAULT_RETRY_POLICY,
        stream_retries: StreamRetries = StreamRetries.ENABLED,
        operation_retry_policies: Mapping[Operation, RetryPolicy | Retries] | None = None,
    ) -> None:
        insecure = _option(transport_security, TransportSecurity) == "plaintext"
        allow_insecure_remote_credentials = _option(remote_credentials, RemoteCredentials) == "allow"
        # Malformed endpoints fail first, so the remote-plaintext refusal
        # below only ever describes a well-formed remote host.
        target = parse_endpoint(endpoint, insecure=insecure)
        require_insecure_transport_allowed(
            target,
            insecure=insecure,
            allow_insecure_remote_credentials=allow_insecure_remote_credentials,
        )
        require_tls_material_usable(
            insecure=insecure,
            ca_cert=ca_cert,
            client_cert=client_cert,
            client_key=client_key,
        )
        self._configure(default_timeout, retry_policy, stream_retries, operation_retry_policies)
        self._endpoint = target
        self._insecure = insecure
        self._ca_cert = ca_cert
        self._client_cert = client_cert
        self._client_key = client_key
        self._metadata = bearer_metadata(token)
        self._owns_channel = True
        self._channel = None
        self._stubs_for = None
        self._loop: asyncio.AbstractEventLoop | None = None
        self._streams: set[Any] = set()

    @classmethod
    def from_channel(
        cls,
        channel: grpc.aio.Channel,
        *,
        default_timeout: float | DefaultDeadline = 30.0,
        retry_policy: RetryPolicy | Retries = DEFAULT_RETRY_POLICY,
        stream_retries: StreamRetries = StreamRetries.ENABLED,
        operation_retry_policies: Mapping[Operation, RetryPolicy | Retries] | None = None,
    ) -> SpiceDBClient:
        """Build a client over a caller-owned ``grpc.aio.Channel``.

        For in-memory, buffered or otherwise pre-built connections. The
        channel already carries its transport security and credentials (for
        example ``grpc.composite_channel_credentials`` with call credentials),
        so this client adds no token and no TLS options apply. Operation
        options (default deadline, retry policy, per-call ``call_options``)
        still apply. ``close()`` never closes the channel.
        """
        if not isinstance(channel, grpc.aio.Channel):
            raise LocalValidationError("channel: expected grpc.aio.Channel")
        client = cls.__new__(cls)
        client._configure(default_timeout, retry_policy, stream_retries, operation_retry_policies)
        client._endpoint = None
        client._insecure = False
        client._ca_cert = client._client_cert = client._client_key = None
        client._metadata = []
        client._owns_channel = False
        client._channel = channel
        client._stubs_for = None
        client._loop = None
        client._streams = set()
        return client

    def _ensure_channel(self) -> None:
        loop = asyncio.get_running_loop()
        if self._loop is not None and loop is not self._loop:
            raise EventLoopBindingError(
                "this spicedb.aio client is bound to the event loop it was first used on; "
                "create one client per event loop (for example in each loop's startup "
                "or lifespan hook), or use spicedb.sync.SpiceDBClient if the calling "
                "code is synchronous"
            )
        self._loop = loop
        if self._channel is None:
            self._channel = (
                grpc.aio.insecure_channel(self._endpoint)
                if self._insecure
                else grpc.aio.secure_channel(
                    self._endpoint,
                    channel_credentials(self._ca_cert, self._client_cert, self._client_key),
                )
            )
        if self._stubs_for is self._channel:
            return
        self._stubs_for = self._channel
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

    async def ready(self, *, timeout: float | None = None) -> None:
        """Wait until the channel is connected, as a startup readiness check.

        The client otherwise connects lazily on its first call, so a wrong or
        unreachable endpoint would only show up then. ``timeout`` defaults to
        the client's default deadline. Raises ``UnavailableError`` naming the
        endpoint and the transport's cause (refused, DNS, TLS) when the
        connection failed, or ``DeadlineExceededError`` when it was still
        connecting at the deadline. Sends no credentials and calls no SpiceDB
        API, so it does not check the token.
        """
        budget = self._budget(timeout)
        self._ensure_channel()
        channel = self._channel
        waited = budget.remaining()
        channel.get_state(try_to_connect=True)
        try:
            await asyncio.wait_for(channel.channel_ready(), waited)
            return
        except asyncio.TimeoutError:
            pass
        probe_error = None
        if channel.get_state() is grpc.ChannelConnectivity.TRANSIENT_FAILURE:
            try:
                await channel.unary_unary(PROBE_METHOD)(b"", timeout=PROBE_TIMEOUT)
            except grpc.RpcError as error:
                probe_error = error
            else:
                return
        limit = self._default_timeout if timeout is None else timeout
        failure = not_ready_error(self._endpoint, limit, probe_error)
        if failure is not None:
            raise failure

    async def close(self) -> None:
        """Close streams and, unless it was supplied by the caller, the channel."""
        for stream in tuple(self._streams):
            await stream.aclose()
        if self._channel is not None and self._owns_channel:
            await self._channel.close()
            self._channel = None
            self._stubs_for = None

    async def __aenter__(self) -> SpiceDBClient:
        self._ensure_channel()
        return self

    async def __aexit__(self, exc_type, exc_value, traceback) -> None:
        await self.close()

    async def _rpc(self, rpc, request, budget: Budget, retry: Operation | RetryPolicy | bool = False, call_options: CallOptions | None = None):
        policy = self._policy(retry, call_options)
        kwargs = self._call_kwargs(call_options)
        target = response_target(call_options)
        attempt = 0
        while True:
            call = None
            try:
                call = rpc(request, timeout=budget.remaining(), **kwargs)
                response = await call
                await capture_async(target, call)
                return response
            except asyncio.CancelledError:
                if call is not None and hasattr(call, "cancel"):
                    call.cancel()
                raise
            except SpiceDBError:
                raise
            except grpc.RpcError as error:
                await capture_async(target, error)
                converted = to_spicedb_error(error)
                if not self._should_retry(policy, error, attempt):
                    raise converted from error
                delay = budget.delay(self._backoff_seconds(policy, attempt))
                notified = self._retry_attempt(policy, rpc, attempt, converted, delay)
                if inspect.isawaitable(notified):
                    await notified
                attempt += 1
                await asyncio.sleep(delay)
                budget.remaining()

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
        budget = self._budget(timeout)
        request = _requests.check_request(check, consistency, context, tracing)
        self._ensure_channel()
        return _mapping.check_result(
            await self._rpc(self._permissions.CheckPermission, request, budget, Operation.CHECK, call_options)
        )

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
        """Check bulk.

        ``item_errors=BulkItemErrors.RETURN`` puts each failing item's error on
        ``CheckResult.error`` instead of raising the first one.
        """
        _option(tracing, Tracing)
        return_errors = _option(item_errors, BulkItemErrors) == "return"
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
                self._permissions.CheckBulkPermissions, request, budget, Operation.CHECK_BULK, call_options
            )
            results.extend(
                _mapping.check_results(response, request.items, i * 1000, return_errors)
            )
        return results

    async def write(
        self,
        transaction: Transaction,
        *,
        metadata: JSONObject | None = None,
        timeout: float | None = None,
        call_options: CallOptions | None = None,
    ) -> WriteResult:
        """Write the transaction atomically.

        An empty transaction is sent as is: SpiceDB evaluates its
        preconditions and returns the current revision. A transaction holding
        only touches and no preconditions may opt in to retries with
        ``CallOptions(retry_policy=...)``.
        """
        budget = self._budget(timeout)
        policy = self._mutation_policy(call_options, write_is_idempotent(transaction), "write", Operation.WRITE)
        request = _requests.write_request(transaction)
        if metadata is not None:
            request.optional_transaction_metadata.CopyFrom(
                _requests.context_struct(metadata)
            )
        self._ensure_channel()
        response = await self._rpc(
            self._permissions.WriteRelationships, request, budget, policy, call_options
        )
        return WriteResult(revision=_wire.token(response, "written_at", "WriteRelationshipsResponse"))

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
        """Delete relationships in one request.

        DeletePartial.ALLOWED (default) requires a positive ``limit`` and may
        delete part of the matches. DeletePartial.DISALLOWED is all-or-nothing:
        the server rejects a delete exceeding its limit; omit ``limit`` to use
        the server's default.
        """
        allow_partial = _option(partial, DeletePartial) == "allowed"
        budget = self._budget(timeout)
        self._mutation_policy(call_options, False, "delete relationships", Operation.DELETE_RELATIONSHIPS)
        if allow_partial or limit is not None:
            _positive(limit, "limit")
        self._ensure_channel()
        return await self._delete_page(
            filter, limit, must_match, must_not_match, metadata, None, budget,
            allow_partial, call_options,
        )

    async def _delete_page(
        self, filter, limit, must_match, must_not_match, metadata, cursor, budget,
        allow_partial=True, call_options=None,
    ):
        request = _requests.delete_relationships_request(
            filter,
            must_match,
            must_not_match,
            limit,
            core.Cursor(token=cursor) if cursor else None,
            allow_partial,
        )
        if metadata is not None:
            request.optional_transaction_metadata.CopyFrom(
                _requests.context_struct(metadata)
            )
        response = await self._rpc(
            self._permissions.DeleteRelationships, request, budget, False, call_options
        )
        progress = {1: DeleteProgress.COMPLETE, 2: DeleteProgress.PARTIAL}.get(
            response.deletion_progress, DeleteProgress.UNSPECIFIED
        )
        return DeleteResult(
            _wire.token(response, "deleted_at", "DeleteRelationshipsResponse"),
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
        on_progress: Callable[[DeleteResult], Any] | None = None,
        timeout: float | None = None,
        call_options: CallOptions | None = None,
    ) -> DeleteResult:
        """Delete matching relationships in individually transactional batches.

        The entire operation is non-atomic: completed batches remain deleted
        if a later batch fails. Error delete_progress carries acknowledged
        counts and the latest revision. ``on_progress`` (sync or async) is
        called after each acknowledged batch with the cumulative result.
        """
        budget = self._budget(timeout)
        self._mutation_policy(call_options, False, "delete relationships", Operation.DELETE_ALL_RELATIONSHIPS_NON_ATOMIC)
        must_match = tuple(must_match)
        must_not_match = tuple(must_not_match)
        metadata = _snapshot(metadata)
        _positive(page_size, "page_size")
        if on_progress is not None and not callable(on_progress):
            raise LocalValidationError("on_progress: expected callable")
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
                    True,
                    call_options,
                )
                count += page.deleted_count
                acknowledged = DeleteResult(
                    page.revision, count, page.progress, page.cursor
                )
                if on_progress is not None:
                    reported = on_progress(acknowledged)
                    if inspect.isawaitable(reported):
                        await reported
                if page.progress == DeleteProgress.COMPLETE:
                    return acknowledged
                if page.progress == DeleteProgress.UNSPECIFIED:
                    raise _wire.malformed("DeleteRelationshipsResponse.deletion_progress is unspecified")
                cursor = page.cursor
        except BaseException as error:
            if acknowledged is not None:
                setattr(error, "delete_progress", acknowledged)
            raise

    async def read_schema(
        self, *, timeout: float | None = None, call_options: CallOptions | None = None
    ) -> SchemaResult:
        """Read schema."""
        budget = self._budget(timeout)
        self._ensure_channel()
        response = await self._rpc(
            self._schema.ReadSchema, s.ReadSchemaRequest(), budget, Operation.READ_SCHEMA, call_options
        )
        return SchemaResult(response.schema_text, _wire.token(response, "read_at", "ReadSchemaResponse"))

    async def write_schema(
        self, schema: str, *, timeout: float | None = None, call_options: CallOptions | None = None
    ) -> ZedToken:
        """Write schema. Idempotent, so it may opt in to retries with ``CallOptions(retry_policy=...)``."""
        budget = self._budget(timeout)
        policy = self._mutation_policy(call_options, True, "write schema", Operation.WRITE_SCHEMA)
        self._ensure_channel()
        response = await self._rpc(
            self._schema.WriteSchema, s.WriteSchemaRequest(schema=schema), budget, policy, call_options
        )
        return _wire.token(response, "written_at", "WriteSchemaResponse")

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
        budget = self._budget(timeout)
        self._ensure_channel()
        response = await self._rpc(
            self._permissions.ExpandPermissionTree,
            _requests.expand_request(resource, permission, consistency),
            budget,
            Operation.EXPAND_PERMISSION_TREE,
            call_options,
        )
        return ExpandResult(
            _mapping.permission_tree(response), _wire.token(response, "expanded_at", "ExpandPermissionTreeResponse")
        )

    async def reflect_schema(
        self,
        *,
        consistency: Consistency,
        filters: Sequence[SchemaFilter] = (),
        timeout: float | None = None,
        call_options: CallOptions | None = None,
    ) -> ReflectSchemaResult:
        """Reflect schema."""
        budget = self._budget(timeout)
        self._ensure_channel()
        response = await self._rpc(
            self._schema.ReflectSchema,
            _requests.reflect_schema_request(consistency, filters),
            budget,
            Operation.REFLECT_SCHEMA,
            call_options,
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
        call_options: CallOptions | None = None,
    ) -> RelationsResult:
        """Computable permissions."""
        budget = self._budget(timeout)
        self._ensure_channel()
        request = _requests.computable_permissions_request(
            consistency, definition_name, relation_name
        )
        request.optional_definition_name_filter = definition_name_filter or ""
        response = await self._rpc(
            self._schema.ComputablePermissions, request, budget, Operation.COMPUTABLE_PERMISSIONS, call_options
        )
        return RelationsResult(
            [RelationReference._from_proto(x) for x in response.permissions],
            _wire.token(response, "read_at", "ComputablePermissionsResponse"),
        )

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
        budget = self._budget(timeout)
        self._ensure_channel()
        response = await self._rpc(
            self._schema.DependentRelations,
            _requests.dependent_relations_request(
                consistency, definition_name, permission_name
            ),
            budget,
            Operation.DEPENDENT_RELATIONS,
            call_options,
        )
        return RelationsResult(
            [RelationReference._from_proto(x) for x in response.relations],
            _wire.token(response, "read_at", "DependentRelationsResponse"),
        )

    async def diff_schema(
        self,
        comparison_schema: str,
        *,
        consistency: Consistency,
        timeout: float | None = None,
        call_options: CallOptions | None = None,
    ) -> SchemaDiffResult:
        """Diff schema."""
        budget = self._budget(timeout)
        self._ensure_channel()
        response = await self._rpc(
            self._schema.DiffSchema,
            _requests.diff_schema_request(consistency, comparison_schema),
            budget,
            Operation.DIFF_SCHEMA,
            call_options,
        )
        return SchemaDiffResult(
            [_schema_diff_from_proto(x) for x in response.diffs], _wire.token(response, "read_at", "DiffSchemaResponse")
        )

    async def experimental_register_relationship_counter(
        self, name: str, filter: Filter, *, timeout: float | None = None, call_options: CallOptions | None = None
    ) -> None:
        """Experimental: register a stored relationship counter."""
        budget = self._budget(timeout)
        self._mutation_policy(call_options, False, "register relationship counter", Operation.EXPERIMENTAL_REGISTER_RELATIONSHIP_COUNTER)
        self._ensure_channel()
        await self._rpc(
            self._experimental.ExperimentalRegisterRelationshipCounter,
            e.ExperimentalRegisterRelationshipCounterRequest(
                name=name, relationship_filter=filter._to_proto()
            ),
            budget,
            False,
            call_options,
        )

    async def experimental_unregister_relationship_counter(
        self, name: str, *, timeout: float | None = None, call_options: CallOptions | None = None
    ) -> None:
        """Experimental: unregister a stored relationship counter."""
        budget = self._budget(timeout)
        self._mutation_policy(call_options, False, "unregister relationship counter", Operation.EXPERIMENTAL_UNREGISTER_RELATIONSHIP_COUNTER)
        self._ensure_channel()
        await self._rpc(
            self._experimental.ExperimentalUnregisterRelationshipCounter,
            e.ExperimentalUnregisterRelationshipCounterRequest(name=name),
            budget,
            False,
            call_options,
        )

    async def experimental_count_relationships(
        self, name: str, *, timeout: float | None = None, call_options: CallOptions | None = None
    ) -> CountResult:
        """Experimental: inspect a stored relationship counter."""
        budget = self._budget(timeout)
        self._ensure_channel()
        response = await self._rpc(
            self._experimental.ExperimentalCountRelationships,
            e.ExperimentalCountRelationshipsRequest(name=name),
            budget,
            Operation.EXPERIMENTAL_COUNT_RELATIONSHIPS,
            call_options,
        )
        if response.HasField("read_counter_value"):
            return CountResult(
                response.read_counter_value.relationship_count,
                _wire.token(
                    response.read_counter_value, "read_at", "ExperimentalCountRelationshipsResponse.read_counter_value"
                ),
            )
        if not response.counter_still_calculating:
            raise _wire.malformed(
                "ExperimentalCountRelationshipsResponse has neither read_counter_value nor counter_still_calculating"
            )
        return CountResult(None, None, True)

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
        budget = self._budget(timeout)
        self._ensure_channel()
        request = roaring.ExperimentalRoaringLookupResourcesRequest(
            resource_object_type=resource_type,
            permission=permission,
            subject=subject._to_proto(),
            consistency=consistency._to_proto(),
        )
        response = await self._rpc(
            self._materialize.ExperimentalRoaringLookupResources, request, budget, Operation.EXPERIMENTAL_ROARING_LOOKUP_RESOURCES, call_options
        )
        return RoaringLookupResourcesResult(
            response.bitmap, response.cardinality, _wire.token(response, "at_revision", "RoaringLookupResourcesResponse")
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
        call_options=None,
        start_cursor=None,
        single_request=False,
        operation=None,
        deadline=None,
    ):
        """Stream ``mapper``'s items over one or more requests.

        ``deadline`` is PAGE, MESSAGE or None; see ``spicedb._client_base``.
        """
        # Validate options before any transport work.
        kwargs = self._call_kwargs(call_options)
        target = response_target(call_options)
        policy = self._stream_policy(watch, call_options, operation)
        page_default = self._default_timeout if deadline is not None else None
        per_page = deadline == PAGE

        async def factory(owner):
            """Factory."""
            self._ensure_channel()
            cursor = start_cursor
            delivered = 0
            try:
                while not owner._closed:
                    # Every page repeats the caller's consistency and ONE fixed
                    # page size: SpiceDB hashes both into the cursor and rejects
                    # a cursor sent with different arguments. Limits are
                    # enforced client-side below, never by shrinking a page.
                    # Single-request mode sends one request, as the raw
                    # API does: its limit is the total limit (0 means
                    # unbounded) and no follow-up page is fetched.
                    page_start = cursor
                    request = request_factory(cursor, (limit or 0) if single_request else page_size)
                    attempt = 0
                    while True:
                        # Count per attempt: a retry re-reads the page from
                        # its start, so items read by a failed attempt (all
                        # skipped by the include filter, or the retry would
                        # not be allowed) must not count toward this page.
                        page_count = 0
                        page_cursor = None
                        call = None
                        # Server-wait time left for this page (PAGE) or for
                        # each message (MESSAGE). Measured only around the
                        # wait for the server, never while the caller holds
                        # an item, so a slow consumer never fails the read.
                        page_left = page_default
                        try:
                            call = rpc_factory()(
                                request,
                                timeout=budget.remaining(),
                                **kwargs,
                            )
                            owner._set_call(call)
                            responses = call.__aiter__()
                            while True:
                                try:
                                    if page_default is None:
                                        response = await anext(responses)
                                    else:
                                        bound = page_left if per_page else page_default
                                        waited = time.monotonic()
                                        try:
                                            response = await asyncio.wait_for(anext(responses), max(bound, 0))
                                        finally:
                                            if per_page:
                                                page_left -= time.monotonic() - waited
                                except StopAsyncIteration:
                                    break
                                except TimeoutError as error:
                                    raise stalled_error(page_default, per_page) from error
                                budget.remaining()
                                page_cursor = (
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
                            await capture_async(target, call)
                            cursor = page_cursor
                            break
                        except SpiceDBError:
                            raise
                        except grpc.RpcError as error:
                            await capture_async(target, error)
                            converted = to_spicedb_error(error)
                            if delivered or not self._should_retry(policy, error, attempt):
                                raise converted from error
                            delay = budget.delay(self._backoff_seconds(policy, attempt))
                            notified = self._retry_attempt(policy, rpc_factory(), attempt, converted, delay)
                            if inspect.isawaitable(notified):
                                await notified
                            attempt += 1
                            await asyncio.sleep(delay)
                            budget.remaining()
                        finally:
                            if call is not None:
                                call.cancel()
                    if single_request or page_size is None or page_count < page_size or cursor is None:
                        return
                    if page_start is not None and cursor.token == page_start.token:
                        # Re-requesting the same cursor would loop forever.
                        raise _wire.malformed(
                            "a full page ended at the cursor it started from; the cursor does not advance"
                        )
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
        cursor: str | None = None,
        paging: Paging = Paging.AUTOMATIC,
        timeout: float | None = None,
        call_options: CallOptions | None = None,
    ) -> AsyncStream[ReadRelationship]:
        """Read relationships, each with its after-result cursor and read-at revision.

        To resume, pass an item's ``after_result_cursor`` as ``cursor`` with
        the same filter, consistency and page size.
        ``paging=Paging.SINGLE_REQUEST`` sends exactly one unbounded request,
        as the raw API does.
        """
        single = _option(paging, Paging) == "single_request"
        budget = Budget(timeout)
        _positive(page_size, "page_size")
        filter._to_proto()
        start = _cursor(cursor, "cursor")
        return self._stream(
            lambda: self._permissions.ReadRelationships,
            lambda cursor, size: _requests.read_relationships_request(
                filter, consistency, size, cursor
            ),
            lambda response: [
                ReadRelationship(
                    _wire.relationship(response, "relationship", "ReadRelationshipsResponse"),
                    _response_cursor(response),
                    _wire.token(response, "read_at", "ReadRelationshipsResponse"),
                )
            ],
            budget,
            page_size,
            call_options=call_options,
            start_cursor=start,
            single_request=single,
            operation=Operation.READ_RELATIONSHIPS,
            deadline=MESSAGE if single else PAGE,
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
        cursor: str | None = None,
        paging: Paging = Paging.AUTOMATIC,
        timeout: float | None = None,
        call_options: CallOptions | None = None,
    ) -> AsyncStream[LookupResource]:
        """Lookup resources, including conditional matches by default.

        HAS_PERMISSION_ONLY filters client-side to unconditional grants; limit
        counts yielded matches and skipped results still advance pagination.
        Resume from an item's ``after_result_cursor`` via ``cursor`` with the
        same request and page size. ``paging=Paging.SINGLE_REQUEST`` sends
        exactly one request, as the raw API does: its limit is ``limit``
        (unbounded without one) and ``page_size`` is ignored.
        """
        single = _option(paging, Paging) == "single_request"
        _option(permissionship_filter, LookupPermissionshipFilter)
        _option(debug, LookupDebug)
        budget = Budget(timeout)
        _positive(page_size, "page_size")
        if limit is not None:
            _positive(limit, "limit")
        subj = subject._to_proto()
        ctx = _requests.context_struct(context)
        start = _cursor(cursor, "cursor")

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
            call_options=call_options,
            start_cursor=start,
            single_request=single,
            operation=Operation.LOOKUP_RESOURCES,
            deadline=MESSAGE if single else PAGE,
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
        call_options: CallOptions | None = None,
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
            call_options=call_options,
            operation=Operation.LOOKUP_SUBJECTS,
            deadline=MESSAGE if page_size is None else PAGE,
        )

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
        """Export relationships, each with its response's after-result cursor.

        Every relationship in one response batch shares that cursor; resume
        with ``cursor`` and the same filter, consistency and page size.
        """
        budget = Budget(timeout)
        _positive(page_size, "page_size")
        rel_filter = filter._to_proto() if filter is not None else None
        start = _cursor(cursor, "cursor")

        def items(response):
            after = _response_cursor(response)
            return [
                ExportedRelationship(_wire.relationship(x, None, "ExportBulkRelationshipsResponse.relationships"), after)
                for x in response.relationships
            ]

        return self._stream(
            lambda: self._permissions.ExportBulkRelationships,
            lambda cursor, size: _requests.export_request(
                consistency, rel_filter, size, cursor
            ),
            items,
            budget,
            page_size,
            call_options=call_options,
            start_cursor=start,
            operation=Operation.EXPORT_RELATIONSHIPS,
            deadline=MESSAGE,
        )

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
        """Watch changes matching any supplied relationship filter.

        Empty filters watch all relationship changes. Nonempty object_types
        and filters are mutually exclusive. Inputs are copied before streaming.
        Add WatchContent.SCHEMA (combinable with CHECKPOINTS) to receive
        schema-change events, reported by ``WatchEvent.schema_updated``.
        Watch is never retried or reconnected automatically.
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
            call_options=call_options,
            operation=Operation.WATCH,
        )

    async def import_relationships(
        self,
        relationships: Iterable[Relationship] | AsyncIterable[Relationship],
        *,
        batch_size: int = 1000,
        timeout: float | None = None,
        call_options: CallOptions | None = None,
    ) -> int:
        """Import relationships in one stream and one transaction.

        ``batch_size`` sets relationships per message. If the server aborts the
        stream (for example ALREADY_EXISTS for a duplicate), its status is
        raised. Never retried.
        """
        budget = Budget(timeout)
        _positive(batch_size, "batch_size")
        self._mutation_policy(call_options, False, "import relationships", Operation.IMPORT_RELATIONSHIPS)
        kwargs = self._call_kwargs(call_options)
        target = response_target(call_options)
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
                if len(batch) == batch_size:
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
                    response = await self._rpc(rpc, producer, budget, False, call_options)
                else:
                    call = rpc(timeout=budget.remaining(), **kwargs)

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
                    # together. A failed send after the server aborted the
                    # stream is not the error to report: the call's final
                    # status (e.g. ALREADY_EXISTS) is, so it is awaited below.
                    send_failed = (
                        sender.done()
                        and not sender.cancelled()
                        and sender.exception() is not None
                    )
                    if send_failed and producer_error is not None:
                        await sender
                    response = await receiver
                    if send_failed:
                        await sender
                    await capture_async(target, call)
                return response.num_loaded
        except BaseException as error:
            if call is not None:
                call.cancel()
            if isinstance(error, TimeoutError) and deadline.expired():
                raise DeadlineExceededError("operation deadline exceeded") from error
            if isinstance(error, grpc.RpcError) and not isinstance(error, SpiceDBError):
                await capture_async(target, error)
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
        self, filter: Filter, *, timeout: float | None = None, call_options: CallOptions | None = None
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
            Operation.EXPERIMENTAL_COUNT_RELATIONSHIPS_BY_FILTER,
            call_options,
        )
        return CountResult(response.relationship_count, _wire.token(response, "read_at", "CountRelationshipsResponse"))

    def watch_permission_sets(
        self,
        *,
        start_revision: ZedToken | None = None,
        timeout: float | None = None,
        call_options: CallOptions | None = None,
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
            call_options=call_options,
            operation=Operation.WATCH_PERMISSION_SETS,
        )

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
        budget = Budget(timeout)
        _positive(limit, "limit")
        if limit > 0xFFFFFFFF:
            raise LocalValidationError("limit: exceeds uint32")
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
            call_options=call_options,
            operation=Operation.LOOKUP_PERMISSION_SETS,
            deadline=MESSAGE,
        )

    async def download_permission_sets(
        self,
        *,
        at_revision: ZedToken | None = None,
        timeout: float | None = None,
        call_options: CallOptions | None = None,
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
            self._permission_sets.DownloadPermissionSets, request, budget, Operation.DOWNLOAD_PERMISSION_SETS, call_options
        )
        return _mapping.permission_set_download(response)
