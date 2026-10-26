"""Pure proto-request builders shared by the sync and async clients.

MUST NOT import or reference channels, stubs, awaitables, or grpc call types.
Everything here is a pure function from this package's dataclasses to proto
messages, which is what lets both flavors share it and lets these be tested
once.
"""

from __future__ import annotations
from spicedb.token import ZedToken, _token_string
from spicedb.options import Tracing, WatchContent, _option

import itertools
from collections.abc import Iterable, Iterator
from collections.abc import Sequence

from authzed.api.v1 import (
    core_pb2,
    permission_service_pb2,
    schema_service_pb2,
    watch_service_pb2,
)
from google.protobuf import struct_pb2

from spicedb.errors import InvalidArgumentError
from spicedb.consistency import Consistency
from spicedb.types import (
    JSONObject,
    Filter,
    Relationship,
    PermissionCheck,
    ObjectRef,
    SubjectRef,
    SchemaFilter,
    Transaction,
    context_to_struct,
)

DEFAULT_PAGE_SIZE = 1000
IMPORT_BATCH_SIZE = 1000

# Per-page size for delete_all_relationships_non_atomic, mirroring
# spicedb-go's defaultDeletePageSize (client/relationships.go).
DEFAULT_DELETE_PAGE_SIZE = 1000

# How many items go into a single CheckBulkPermissions request.
#
# SpiceDB rejects a request carrying more items than ``maxBulkCheckCount``
# -- 10,000, a hard-coded const in ``internal/services/v1/bulkcheck.go``
# with no flag to raise or lower it -- with
# ``ERROR_REASON_TOO_MANY_CHECKS_IN_REQUEST``. Nothing in the proto enforces
# this: ``CheckBulkPermissionsRequest.items`` carries only a per-item
# ``required`` rule, not a collection-size rule, so the limit lives solely
# in server code and a client that forwards the caller's arguments unchanged
# fails on large inputs. 1,000 leaves ten times' headroom and matches
# ``IMPORT_BATCH_SIZE`` and the other clients' check batch size.
CHECK_BATCH_SIZE = 1000


def context_struct(context: JSONObject | None) -> struct_pb2.Struct | None:
    """Build a protobuf Struct from caveat context, or None if unset."""
    if context is None:
        return None
    return context_to_struct(context, "caveat context")


def object_reference(obj: ObjectRef) -> core_pb2.ObjectReference:
    return obj._to_proto()


def subject_reference(subject: SubjectRef) -> core_pb2.SubjectReference:
    return subject._to_proto()


def _merge_check_context(
    call_level: JSONObject | None, item_level: JSONObject | None
) -> JSONObject | None:
    """Merge call-level and per-item caveat context for one check item.

    Key-level, item wins: ``{**call_level, **item_level}``. Item keys
    override call-level keys on conflict; call-level keys the item doesn't
    mention are retained -- this is NOT wholesale replacement. An item that
    supplies only one key must not silently drop every other call-level key,
    or a caveat would fail for context the caller believed it had already
    supplied (spec D3b).

    Returns None -- so the wire request sets no ``context`` field at all,
    rather than an empty Struct -- only when both inputs are None (neither
    call-level nor per-item context was supplied).
    """
    if call_level is None and item_level is None:
        return None
    return {**(call_level or {}), **(item_level or {})}


def check_fields(check: PermissionCheck, context):
    if not isinstance(check, PermissionCheck):
        from spicedb.errors import InvalidArgumentError

        raise InvalidArgumentError("check: expected PermissionCheck")
    return dict(
        resource=check.resource._to_proto(),
        permission=check.permission,
        subject=check.subject._to_proto(),
        context=context_struct(_merge_check_context(context, check.context)),
    )


def check_request(
    check: PermissionCheck, consistency: Consistency, context=None, tracing: Tracing = Tracing.DISABLED
):
    return permission_service_pb2.CheckPermissionRequest(
        consistency=consistency._to_proto(),
        with_tracing=_option(tracing, Tracing),
        **check_fields(check, context),
    )


def check_bulk_request(
    consistency: Consistency,
    checks: Sequence[PermissionCheck],
    context=None,
    tracing: Tracing = Tracing.DISABLED,
):
    return permission_service_pb2.CheckBulkPermissionsRequest(
        consistency=consistency._to_proto(),
        with_tracing=_option(tracing, Tracing),
        items=[
            permission_service_pb2.CheckBulkPermissionsRequestItem(
                **check_fields(check, context)
            )
            for check in checks
        ],
    )


def read_relationships_request(
    filter: Filter,
    consistency: Consistency,
    limit: int,
    cursor: core_pb2.Cursor | None,
) -> permission_service_pb2.ReadRelationshipsRequest:
    return permission_service_pb2.ReadRelationshipsRequest(
        consistency=consistency._to_proto(),
        relationship_filter=filter._to_proto(),
        optional_limit=limit,
        optional_cursor=cursor,
    )


def lookup_resources_request(
    resource_type: str,
    permission: str,
    subj_ref: core_pb2.SubjectReference,
    ctx_struct: struct_pb2.Struct | None,
    consistency: Consistency,
    limit: int,
    cursor: core_pb2.Cursor | None,
) -> permission_service_pb2.LookupResourcesRequest:
    return permission_service_pb2.LookupResourcesRequest(
        consistency=consistency._to_proto(),
        resource_object_type=resource_type,
        permission=permission,
        subject=subj_ref,
        context=ctx_struct,
        optional_limit=limit,
        optional_cursor=cursor,
    )


def lookup_subjects_request(
    resource: ObjectRef,
    permission: str,
    subject_type: str,
    subject_relation: str,
    ctx_struct: struct_pb2.Struct | None,
    consistency: Consistency,
) -> permission_service_pb2.LookupSubjectsRequest:
    return permission_service_pb2.LookupSubjectsRequest(
        consistency=consistency._to_proto(),
        resource=resource._to_proto(),
        permission=permission,
        subject_object_type=subject_type,
        optional_subject_relation=subject_relation,
        context=ctx_struct,
    )


def write_request(
    txn: Transaction,
) -> permission_service_pb2.WriteRelationshipsRequest:
    return permission_service_pb2.WriteRelationshipsRequest(
        updates=txn._updates,
        optional_preconditions=txn._preconditions,
    )


def delete_relationships_request(
    filter: Filter,
    must_match: list[Filter] | None,
    must_not_match: list[Filter] | None,
    limit: int | None,
    cursor: core_pb2.Cursor | None = None,
) -> permission_service_pb2.DeleteRelationshipsRequest:
    preconditions = [
        permission_service_pb2.Precondition(
            operation=permission_service_pb2.Precondition.OPERATION_MUST_MATCH,
            filter=f._to_proto(),
        )
        for f in (must_match or [])
    ] + [
        permission_service_pb2.Precondition(
            operation=permission_service_pb2.Precondition.OPERATION_MUST_NOT_MATCH,
            filter=f._to_proto(),
        )
        for f in (must_not_match or [])
    ]
    return permission_service_pb2.DeleteRelationshipsRequest(
        relationship_filter=filter._to_proto(),
        optional_preconditions=preconditions,
        optional_limit=limit if limit is not None else 0,
        optional_allow_partial_deletions=limit is not None,
        optional_cursor=cursor,
    )


def export_request(
    consistency: Consistency,
    rel_filter: permission_service_pb2.RelationshipFilter | None,
    limit: int,
    cursor: core_pb2.Cursor | None,
) -> permission_service_pb2.ExportBulkRelationshipsRequest:
    return permission_service_pb2.ExportBulkRelationshipsRequest(
        consistency=consistency._to_proto(),
        optional_limit=limit,
        optional_cursor=cursor,
        optional_relationship_filter=rel_filter,
    )


def import_batches(
    relationships: Iterable[Relationship], batch_size: int = IMPORT_BATCH_SIZE
) -> Iterator[permission_service_pb2.ImportBulkRelationshipsRequest]:
    """Chunk relationships into ImportBulkRelationships requests.

    `relationships` is consumed lazily via a plain iterator, not indexed --
    unlike every other paginated/bulk RPC, ImportBulkRelationships is
    client-streaming: the caller is the one producing an unbounded amount of
    data, and a caller streaming in millions of relationships from a
    generator (`File.foreach`-equivalent, a DB cursor) should never be forced
    to materialize the whole thing into a `list` first just to hand it to
    this client. Only one batch (`batch_size` relationships) is ever held in
    memory at a time.

    A plain generator, so both the sync client and the async client's request
    feed can drive it.
    """
    it = iter(relationships)
    while True:
        batch = [r._to_proto() for r in itertools.islice(it, batch_size)]
        if not batch:
            return
        yield permission_service_pb2.ImportBulkRelationshipsRequest(relationships=batch)


def watch_request(
    object_types: Sequence[str] | None,
    start_revision: ZedToken | None,
    content: Sequence[WatchContent] = (WatchContent.RELATIONSHIPS,),
    *,
    filters: Sequence[Filter] = (),
) -> watch_service_pb2.WatchRequest:
    if object_types and filters:
        raise InvalidArgumentError("watch object_types and filters are mutually exclusive")
    cursor = None
    if start_revision is not None:
        cursor = core_pb2.ZedToken(token=_token_string(start_revision))

    update_kinds = [_option(value, WatchContent) for value in content]

    return watch_service_pb2.WatchRequest(
        optional_object_types=object_types or [],
        optional_relationship_filters=[value._to_proto() for value in filters],
        optional_start_cursor=cursor,
        optional_update_kinds=update_kinds,
    )


def expand_request(
    resource: ObjectRef,
    permission: str,
    consistency: Consistency,
) -> permission_service_pb2.ExpandPermissionTreeRequest:
    return permission_service_pb2.ExpandPermissionTreeRequest(
        consistency=consistency._to_proto(),
        resource=resource._to_proto(),
        permission=permission,
    )


def reflect_schema_request(consistency: Consistency, filters: Sequence[SchemaFilter]):
    return schema_service_pb2.ReflectSchemaRequest(
        consistency=consistency._to_proto(),
        optional_filters=[f._to_proto() for f in filters],
    )


def diff_schema_request(
    consistency: Consistency,
    comparison_schema: str,
) -> schema_service_pb2.DiffSchemaRequest:
    return schema_service_pb2.DiffSchemaRequest(
        consistency=consistency._to_proto(),
        comparison_schema=comparison_schema,
    )


def computable_permissions_request(
    consistency: Consistency,
    definition_name: str,
    relation_name: str,
) -> schema_service_pb2.ComputablePermissionsRequest:
    return schema_service_pb2.ComputablePermissionsRequest(
        consistency=consistency._to_proto(),
        definition_name=definition_name,
        relation_name=relation_name,
    )


def dependent_relations_request(
    consistency: Consistency,
    definition_name: str,
    permission_name: str,
) -> schema_service_pb2.DependentRelationsRequest:
    return schema_service_pb2.DependentRelationsRequest(
        consistency=consistency._to_proto(),
        definition_name=definition_name,
        permission_name=permission_name,
    )
