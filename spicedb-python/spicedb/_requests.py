"""Pure proto-request builders shared by the sync and async clients.

MUST NOT import or reference channels, stubs, awaitables, or grpc call types.
Everything here is a pure function from this package's dataclasses to proto
messages, which is what lets both flavors share it and lets these be tested
once.
"""

from __future__ import annotations

from collections.abc import Iterator
from typing import Any

from authzed.api.v1 import (
    core_pb2,
    permission_service_pb2,
    schema_service_pb2,
    watch_service_pb2,
)
from google.protobuf import struct_pb2

from spicedb.consistency import Consistency
from spicedb.types import Filter, Relationship, Transaction

DEFAULT_PAGE_SIZE = 512
IMPORT_BATCH_SIZE = 1000


def context_struct(context: dict[str, Any] | None) -> struct_pb2.Struct | None:
    """Build a protobuf Struct from caveat context, or None if unset."""
    if context is None:
        return None
    s = struct_pb2.Struct()
    s.update(context)
    return s


def object_reference(obj: tuple[str, str]) -> core_pb2.ObjectReference:
    obj_type, obj_id = obj
    return core_pb2.ObjectReference(object_type=obj_type, object_id=obj_id)


def subject_reference(
    subject: Relationship | tuple[str, str],
) -> core_pb2.SubjectReference:
    """Build a SubjectReference from a Relationship's subject fields, or from a
    ("type:id", "optional_relation") tuple."""
    if isinstance(subject, Relationship):
        return core_pb2.SubjectReference(
            object=core_pb2.ObjectReference(
                object_type=subject.subject_type,
                object_id=subject.subject_id,
            ),
            optional_relation=subject.subject_relation,
        )
    subj_str, subj_rel = subject
    s_type, s_id = subj_str.split(":", 1)
    return core_pb2.SubjectReference(
        object=core_pb2.ObjectReference(object_type=s_type, object_id=s_id),
        optional_relation=subj_rel,
    )


def _merge_check_context(
    call_level: dict[str, Any] | None, item_level: dict[str, Any] | None
) -> dict[str, Any] | None:
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


def check_bulk_request(
    consistency: Consistency,
    rels: tuple[Relationship, ...] | list[Relationship],
    context: dict[str, Any] | None,
) -> permission_service_pb2.CheckBulkPermissionsRequest:
    """Build a CheckBulkPermissionsRequest, one item per relationship.

    ``context`` is the call-level default; it is fanned out onto every item
    because ``CheckBulkPermissionsRequest`` itself has no context field on
    the wire -- only ``CheckBulkPermissionsRequestItem.context`` does (proto
    field 4). Each relationship's own ``check_context`` (if any) is merged
    on top per ``_merge_check_context``: an item with no ``check_context``
    inherits ``context`` unchanged.
    """
    items = [
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
            context=context_struct(_merge_check_context(context, rel.check_context)),
        )
        for rel in rels
    ]
    return permission_service_pb2.CheckBulkPermissionsRequest(
        consistency=consistency._to_proto(),
        items=items,
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
    resource: tuple[str, str],
    permission: str,
    subject_type: str,
    subject_relation: str,
    ctx_struct: struct_pb2.Struct | None,
    consistency: Consistency,
) -> permission_service_pb2.LookupSubjectsRequest:
    res_type, res_id = resource
    return permission_service_pb2.LookupSubjectsRequest(
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
    relationships: list[Relationship], batch_size: int = IMPORT_BATCH_SIZE
) -> Iterator[permission_service_pb2.ImportBulkRelationshipsRequest]:
    """Chunk relationships into ImportBulkRelationships requests.

    A plain generator, so both the sync client and the async client's request
    feed can drive it.
    """
    for i in range(0, len(relationships), batch_size):
        batch = relationships[i : i + batch_size]
        yield permission_service_pb2.ImportBulkRelationshipsRequest(
            relationships=[r._to_proto() for r in batch]
        )


def watch_request(
    object_types: list[str] | None,
    start_revision: str | None,
) -> watch_service_pb2.WatchRequest:
    cursor = None
    if start_revision is not None:
        cursor = core_pb2.ZedToken(token=start_revision)

    return watch_service_pb2.WatchRequest(
        optional_object_types=object_types or [],
        optional_start_cursor=cursor,
    )


def expand_request(
    resource: tuple[str, str],
    permission: str,
    consistency: Consistency,
) -> permission_service_pb2.ExpandPermissionTreeRequest:
    res_type, res_id = resource
    return permission_service_pb2.ExpandPermissionTreeRequest(
        consistency=consistency._to_proto(),
        resource=core_pb2.ObjectReference(
            object_type=res_type,
            object_id=res_id,
        ),
        permission=permission,
    )


def reflect_schema_request(
    consistency: Consistency,
    filters: list[str] | None,
) -> schema_service_pb2.ReflectSchemaRequest:
    return schema_service_pb2.ReflectSchemaRequest(
        consistency=consistency._to_proto(),
        optional_filters=[
            schema_service_pb2.ReflectionSchemaFilter(
                optional_definition_name_filter=f,
            )
            for f in (filters or [])
        ],
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
