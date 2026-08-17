"""Synchronous SpiceDB client over the proto-generated gRPC stubs.

Mirrors spicedb.aio.client method-for-method. The generated stub classes work
over either a sync `grpc.Channel` or an aio channel, so both flavors share every
request builder and response mapper; only the call and iteration mechanics
differ.

Keep this file in lockstep with spicedb/aio/client.py -- tests/test_parity.py
fails the build if the two surfaces diverge.
"""

from __future__ import annotations

import time
from typing import Any

import grpc
from authzed.api.v1 import (
    experimental_service_pb2,
    permission_service_pb2,
    schema_service_pb2,
)

from spicedb import _mapping, _requests
from spicedb._auth import bearer_metadata
from spicedb.consistency import Consistency
from spicedb.errors import is_transient, to_spicedb_error
from spicedb.types import (
    Filter,
    PermissionTree,
    ReflectSchemaResult,
    RelationReference,
    Relationship,
    SchemaDiff,
    Transaction,
    _permission_tree_from_proto,
    _schema_diff_from_proto,
)

_DEFAULT_MAX_RETRIES = 3


class SpiceDBClient:
    """Idiomatic synchronous Python client for SpiceDB.

    Usage::

        with SpiceDBClient("localhost:50051", token="test", insecure=True) as client:
            ...

    Safe to build once at process start and reuse for the process lifetime;
    there is no event loop to bind to.
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
        self._channel: grpc.Channel | None = None
        self._permissions = None
        self._schema = None
        self._watch = None
        self._experimental = None

    def _ensure_channel(self) -> None:
        """Open the channel on first use.

        Lazy purely to mirror the aio flavor's construction semantics, so the
        two clients behave identically for callers that build early and use
        late. Unlike aio there is no loop to bind to.
        """
        if self._channel is not None:
            return

        if self._insecure:
            self._channel = grpc.insecure_channel(self._endpoint)
        else:
            self._channel = grpc.secure_channel(
                self._endpoint, grpc.ssl_channel_credentials()
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

    def close(self) -> None:
        """Close the underlying gRPC channel. A no-op if never used."""
        if self._channel is not None:
            self._channel.close()
            self._channel = None

    def __enter__(self) -> SpiceDBClient:
        return self

    def __exit__(self, exc_type: Any, exc_val: Any, exc_tb: Any) -> bool:
        self.close()
        return False

    # ── Retry helper ────────────────────────────────────────────────

    def _with_retry(self, fn: Any) -> Any:
        """Call a function with exponential backoff on transient errors."""
        last_err: Exception | None = None
        for attempt in range(self._max_retries + 1):
            try:
                return fn()
            except grpc.RpcError as e:
                if not is_transient(e) or attempt == self._max_retries:
                    raise to_spicedb_error(e) from e
                last_err = e
                time.sleep(min(0.1 * (2**attempt), 5.0))
        raise to_spicedb_error(last_err) from last_err  # type: ignore[arg-type]

    def _should_retry_establishment(self, attempt: int, err: grpc.RpcError) -> bool:
        """Decide whether to retry a streaming RPC's ESTABLISHMENT after a
        transient error, sleeping with the same backoff as `_with_retry`.

        Callers MUST only retry when zero items have been yielded from the
        current stream/page -- retrying after any item has been yielded would
        replay/duplicate it for the caller. This helper only makes the
        transient/attempt-budget decision; the zero-yielded guard is the
        caller's responsibility.
        """
        if not is_transient(err) or attempt == self._max_retries:
            return False
        time.sleep(min(0.1 * (2**attempt), 5.0))
        return True

    # ── Permission checks ──────────────────────────────────────────

    def check_permission(
        self,
        consistency: Consistency,
        rel: Relationship,
        *,
        context: dict[str, Any] | None = None,
    ) -> bool:
        """Check a single permission. Returns True if the subject has the permission."""
        self._ensure_channel()
        results = self.check_permissions(consistency, rel, context=context)
        return results[0]

    def check_permissions(
        self,
        consistency: Consistency,
        *rels: Relationship,
        context: dict[str, Any] | None = None,
    ) -> list[bool]:
        """Check multiple permissions via BulkCheckPermissions. Returns list of bools."""
        self._ensure_channel()
        request = _requests.check_bulk_request(consistency, rels, context)

        def _call() -> permission_service_pb2.CheckBulkPermissionsResponse:
            return self._permissions.CheckBulkPermissions(
                request, metadata=self._metadata
            )

        resp = self._with_retry(_call)
        return _mapping.check_results(resp)

    def check_any(
        self,
        consistency: Consistency,
        *rels: Relationship,
        context: dict[str, Any] | None = None,
    ) -> bool:
        """Return True if any of the permission checks pass."""
        self._ensure_channel()
        results = self.check_permissions(consistency, *rels, context=context)
        return any(results)

    def check_all(
        self,
        consistency: Consistency,
        *rels: Relationship,
        context: dict[str, Any] | None = None,
    ) -> bool:
        """Return True if all of the permission checks pass."""
        self._ensure_channel()
        results = self.check_permissions(consistency, *rels, context=context)
        return all(results)

    # ── Writes ──────────────────────────────────────────────────────

    def write(self, txn: Transaction) -> str:
        """Execute a transaction and return the revision string."""
        self._ensure_channel()
        request = _requests.write_request(txn)

        def _call() -> permission_service_pb2.WriteRelationshipsResponse:
            return self._permissions.WriteRelationships(
                request, metadata=self._metadata
            )

        resp = self._with_retry(_call)
        return resp.written_at.token

    def delete_relationships(
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

        def _call() -> permission_service_pb2.DeleteRelationshipsResponse:
            return self._permissions.DeleteRelationships(
                request, metadata=self._metadata
            )

        resp = self._with_retry(_call)
        return resp.deleted_at.token

    # ── Schema ──────────────────────────────────────────────────────

    def read_schema(self) -> str:
        """Read the current schema. Returns the schema text."""
        self._ensure_channel()

        def _call() -> schema_service_pb2.ReadSchemaResponse:
            return self._schema.ReadSchema(
                schema_service_pb2.ReadSchemaRequest(), metadata=self._metadata
            )

        resp = self._with_retry(_call)
        return resp.schema_text

    def write_schema(self, schema: str) -> str:
        """Write a schema. Returns the revision string."""
        self._ensure_channel()

        def _call() -> schema_service_pb2.WriteSchemaResponse:
            return self._schema.WriteSchema(
                schema_service_pb2.WriteSchemaRequest(schema=schema),
                metadata=self._metadata,
            )

        resp = self._with_retry(_call)
        return resp.written_at.token

    # ── Expand ──────────────────────────────────────────────────────

    def expand_permission_tree(
        self,
        resource: tuple[str, str],
        permission: str,
        consistency: Consistency,
    ) -> tuple[PermissionTree, str]:
        """Expand a permission tree. Returns (tree, revision)."""
        self._ensure_channel()
        request = _requests.expand_request(resource, permission, consistency)

        def _call() -> permission_service_pb2.ExpandPermissionTreeResponse:
            return self._permissions.ExpandPermissionTree(
                request, metadata=self._metadata
            )

        resp = self._with_retry(_call)
        return _permission_tree_from_proto(resp.tree_root), resp.expanded_at.token

    # ── Experimental: Relationship Counters ─────────────────────────

    def register_relationship_counter(
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

        def _call() -> Any:
            return self._experimental.ExperimentalRegisterRelationshipCounter(
                request, metadata=self._metadata
            )

        self._with_retry(_call)

    def count_relationships(
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

        def _call() -> Any:
            return self._experimental.ExperimentalCountRelationships(
                request, metadata=self._metadata
            )

        resp = self._with_retry(_call)
        return resp.counter_result.relationship_count, resp.read_counter_at.token

    def unregister_relationship_counter(
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

        def _call() -> Any:
            return self._experimental.ExperimentalUnregisterRelationshipCounter(
                request, metadata=self._metadata
            )

        self._with_retry(_call)

    # ── Experimental: Schema Reflection ─────────────────────────────

    def reflect_schema(
        self,
        consistency: Consistency,
        *,
        filters: list[str] | None = None,
    ) -> ReflectSchemaResult:
        """Experimental: Reflect the schema, returning its definitions and
        caveats."""
        self._ensure_channel()
        request = _requests.reflect_schema_request(consistency, filters)

        def _call() -> schema_service_pb2.ReflectSchemaResponse:
            return self._schema.ReflectSchema(request, metadata=self._metadata)

        resp = self._with_retry(_call)
        return ReflectSchemaResult._from_proto(resp)

    def diff_schema(
        self,
        consistency: Consistency,
        comparison_schema: str,
    ) -> list[SchemaDiff]:
        """Experimental: Diff two schemas, returning the list of differences."""
        self._ensure_channel()
        request = _requests.diff_schema_request(consistency, comparison_schema)

        def _call() -> schema_service_pb2.DiffSchemaResponse:
            return self._schema.DiffSchema(request, metadata=self._metadata)

        resp = self._with_retry(_call)
        return [_schema_diff_from_proto(d) for d in resp.diffs]

    # ── Schema Introspection: Computable Permissions / Dependent Relations ──

    def computable_permissions(
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

        def _call() -> schema_service_pb2.ComputablePermissionsResponse:
            return self._schema.ComputablePermissions(request, metadata=self._metadata)

        resp = self._with_retry(_call)
        return [RelationReference._from_proto(p) for p in resp.permissions]

    def dependent_relations(
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

        def _call() -> schema_service_pb2.DependentRelationsResponse:
            return self._schema.DependentRelations(request, metadata=self._metadata)

        resp = self._with_retry(_call)
        return [RelationReference._from_proto(r) for r in resp.relations]
