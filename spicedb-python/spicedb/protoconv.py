"""Native <-> protocol conversions (secondary API).

For tools that print or persist protocol-shaped data (``--json`` output,
backup files, debug traces) so they reuse the client's own conversions
instead of reimplementing them. Ordinary operations never need this module.
The messages are the ``authzed.api.v1`` classes the client itself uses; there
is no stability promise beyond theirs.

``*_to_proto`` functions validate and raise ``InvalidArgumentError`` for a
value the protocol cannot represent. ``*_from_proto`` functions accept what a
server sends.
"""

from __future__ import annotations

from collections.abc import Sequence
from datetime import datetime
from typing import Any

from authzed.api.v1 import core_pb2, debug_pb2, permission_service_pb2, schema_service_pb2
from google.protobuf import duration_pb2, struct_pb2, timestamp_pb2
from google.rpc import error_details_pb2, status_pb2

from spicedb._mapping import check_result as _check_result
from spicedb._mapping import lookup_resource as _lookup_resource
from spicedb._mapping import lookup_subject as _lookup_subject
from spicedb.consistency import Consistency, ConsistencyMode
from spicedb._requests import check_fields as _check_fields
from spicedb.errors import InvalidArgumentError, SpiceDBError
from spicedb.token import ZedToken, _token_string
from spicedb.types import (
    CheckDebugTrace,
    CheckResult,
    DebugInformation,
    ExpandResult,
    Filter,
    JSONObject,
    LookupResource,
    LookupSubject,
    ObjectRef,
    Permissionship,
    PermissionCheck,
    PermissionTree,
    Precondition,
    PreconditionOperation,
    ReadRelationship,
    Relationship,
    ResolvedSubject,
    SchemaResult,
    SubjectFilter,
    SubjectRef,
    SubjectRelationFilter,
    Transaction,
    TreeOperation,
    Update,
    WriteResult,
    _debug_information,
    _permission_tree_from_proto,
    context_from_struct,
    context_to_struct,
)

__all__ = [
    "object_to_proto",
    "object_from_proto",
    "subject_to_proto",
    "subject_from_proto",
    "relationship_to_proto",
    "relationship_from_proto",
    "filter_to_proto",
    "filter_from_proto",
    "precondition_to_proto",
    "precondition_from_proto",
    "update_to_proto",
    "update_from_proto",
    "transaction_to_proto",
    "consistency_to_proto",
    "consistency_from_proto",
    "zed_token_to_proto",
    "zed_token_from_proto",
    "context_to_proto",
    "context_from_proto",
    "check_result_to_proto",
    "check_result_from_proto",
    "check_request_to_proto",
    "check_bulk_to_proto",
    "debug_information_to_proto",
    "debug_information_from_proto",
    "lookup_resource_to_proto",
    "lookup_resource_from_proto",
    "lookup_subject_to_proto",
    "lookup_subject_from_proto",
    "read_relationship_to_proto",
    "expand_result_to_proto",
    "permission_tree_to_proto",
    "permission_tree_from_proto",
    "write_result_to_proto",
    "write_schema_result_to_proto",
    "read_schema_result_to_proto",
]


def object_to_proto(value: ObjectRef) -> core_pb2.ObjectReference:
    return value._to_proto()


def object_from_proto(value: core_pb2.ObjectReference) -> ObjectRef:
    return ObjectRef(value.object_type, value.object_id)


def subject_to_proto(value: SubjectRef) -> core_pb2.SubjectReference:
    return value._to_proto()


def subject_from_proto(value: core_pb2.SubjectReference) -> SubjectRef:
    return SubjectRef(object_from_proto(value.object), value.optional_relation or None)


def relationship_to_proto(value: Relationship) -> core_pb2.Relationship:
    """Includes caveat (with context) and expiration."""
    return value._to_proto()


def relationship_from_proto(value: core_pb2.Relationship) -> Relationship:
    return Relationship._from_proto(value)


def filter_to_proto(value: Filter) -> permission_service_pb2.RelationshipFilter:
    return value._to_proto()


def filter_from_proto(value: permission_service_pb2.RelationshipFilter) -> Filter:
    """Absent subject relation filter is Any; a present empty one is Direct."""
    subject = None
    if value.HasField("optional_subject_filter"):
        sf = value.optional_subject_filter
        if not sf.HasField("optional_relation"):
            relation = SubjectRelationFilter.any()
        elif sf.optional_relation.relation:
            relation = SubjectRelationFilter.named(sf.optional_relation.relation)
        else:
            relation = SubjectRelationFilter.direct()
        subject = SubjectFilter(
            sf.subject_type,
            subject_id=sf.optional_subject_id or None,
            relation=relation,
        )
    return Filter(
        value.resource_type,
        resource_id=value.optional_resource_id or None,
        resource_id_prefix=value.optional_resource_id_prefix or None,
        relation=value.optional_relation or None,
        subject=subject,
    )


def precondition_to_proto(value: Precondition) -> permission_service_pb2.Precondition:
    return value._to_proto()


def precondition_from_proto(value: permission_service_pb2.Precondition) -> Precondition:
    operation = {
        permission_service_pb2.Precondition.OPERATION_MUST_MATCH: PreconditionOperation.MUST_MATCH,
        permission_service_pb2.Precondition.OPERATION_MUST_NOT_MATCH: PreconditionOperation.MUST_NOT_MATCH,
    }.get(value.operation)
    if operation is None:
        raise InvalidArgumentError("precondition.operation: unspecified or unknown")
    return Precondition(operation, filter_from_proto(value.filter))


def update_to_proto(value: Update) -> core_pb2.RelationshipUpdate:
    return value._to_proto()


def update_from_proto(value: core_pb2.RelationshipUpdate) -> Update:
    return Update._from_proto(value)


def transaction_to_proto(
    value: Transaction, *, metadata: JSONObject | None = None
) -> permission_service_pb2.WriteRelationshipsRequest:
    """The ``WriteRelationshipsRequest`` that ``write(value, metadata=...)`` sends."""
    request = permission_service_pb2.WriteRelationshipsRequest(
        updates=value._update_protos(),
        optional_preconditions=value._precondition_protos(),
    )
    if metadata is not None:
        request.optional_transaction_metadata.CopyFrom(context_to_proto(metadata))
    return request


def consistency_to_proto(value: Consistency) -> permission_service_pb2.Consistency:
    return value._to_proto()


def consistency_from_proto(value: permission_service_pb2.Consistency) -> Consistency:
    kind = value.WhichOneof("requirement")
    if kind == "fully_consistent":
        return Consistency(ConsistencyMode.FULL)
    if kind == "minimize_latency":
        return Consistency(ConsistencyMode.MIN_LATENCY)
    if kind == "at_least_as_fresh":
        return Consistency(ConsistencyMode.AT_LEAST, ZedToken(value.at_least_as_fresh.token))
    if kind == "at_exact_snapshot":
        return Consistency(ConsistencyMode.SNAPSHOT, ZedToken(value.at_exact_snapshot.token))
    raise InvalidArgumentError("consistency: no requirement set")


def zed_token_to_proto(value: ZedToken | None) -> core_pb2.ZedToken | None:
    """None and the empty token convert to None (absent)."""
    if value is None or not _token_string(value):
        return None
    return core_pb2.ZedToken(token=str(value))


def zed_token_from_proto(value: core_pb2.ZedToken | None) -> ZedToken:
    """None converts to the empty token."""
    return ZedToken(value.token if value is not None else "")


def context_to_proto(value: JSONObject | None) -> struct_pb2.Struct | None:
    if value is None:
        return None
    return context_to_struct(value, "context")


def context_from_proto(value: struct_pb2.Struct | None) -> JSONObject | None:
    return None if value is None else context_from_struct(value)


_CHECK_PERMISSIONSHIP = {
    Permissionship.HAS_PERMISSION: permission_service_pb2.CheckPermissionResponse.PERMISSIONSHIP_HAS_PERMISSION,
    Permissionship.CONDITIONAL_PERMISSION: permission_service_pb2.CheckPermissionResponse.PERMISSIONSHIP_CONDITIONAL_PERMISSION,
    Permissionship.NO_PERMISSION: permission_service_pb2.CheckPermissionResponse.PERMISSIONSHIP_NO_PERMISSION,
}
_LOOKUP_PERMISSIONSHIP = {
    Permissionship.HAS_PERMISSION: permission_service_pb2.LOOKUP_PERMISSIONSHIP_HAS_PERMISSION,
    Permissionship.CONDITIONAL_PERMISSION: permission_service_pb2.LOOKUP_PERMISSIONSHIP_CONDITIONAL_PERMISSION,
}


def _timestamp(value: datetime | None) -> timestamp_pb2.Timestamp | None:
    if value is None:
        return None
    result = timestamp_pb2.Timestamp()
    result.FromDatetime(value)
    return result


def _partial(missing) -> core_pb2.PartialCaveatInfo | None:
    return core_pb2.PartialCaveatInfo(missing_required_context=list(missing)) if missing else None


def check_result_to_proto(value: CheckResult) -> permission_service_pb2.CheckPermissionResponse:
    """Includes the debug trace and expiration. A per-item ``error`` has no
    place in ``CheckPermissionResponse`` and is rejected."""
    if value.error is not None:
        raise InvalidArgumentError("check_result: a result carrying an error has no CheckPermissionResponse form")
    return permission_service_pb2.CheckPermissionResponse(
        checked_at=zed_token_to_proto(value.checked_at),
        permissionship=_CHECK_PERMISSIONSHIP.get(value.permissionship, 0),
        partial_caveat_info=_partial(value.missing_context),
        debug_trace=debug_information_to_proto(value.debug_trace),
        optional_expires_at=_timestamp(value.expiration),
    )


def check_result_from_proto(value: permission_service_pb2.CheckPermissionResponse) -> CheckResult:
    return _check_result(value)


def check_request_to_proto(
    value: PermissionCheck, *, context: JSONObject | None = None
) -> permission_service_pb2.CheckBulkPermissionsRequestItem:
    """The bulk request item ``check_bulk([value], context=...)`` sends:
    call-level ``context`` merged under the check's own, item keys winning."""
    fields: dict[str, Any] = _check_fields(value, context)
    return permission_service_pb2.CheckBulkPermissionsRequestItem(**fields)


def _status_to_proto(error: BaseException) -> status_pb2.Status:
    """The ``google.rpc.Status`` for a per-item error: the server's own when
    the error came from one, else built from its code, message and reason."""
    original = getattr(error, "status", None)
    if isinstance(original, status_pb2.Status):
        return status_pb2.Status.FromString(original.SerializeToString())
    if not isinstance(error, SpiceDBError):
        return status_pb2.Status(code=2, message=str(error))  # UNKNOWN
    status = status_pb2.Status(code=error.code().value[0], message=error.details())
    if error.reason:
        status.details.add().Pack(
            error_details_pb2.ErrorInfo(
                reason=error.reason, domain=error.reason_domain, metadata=error.reason_metadata
            )
        )
    return status


def check_bulk_to_proto(
    requests: Sequence[PermissionCheck],
    results: Sequence[CheckResult],
    *,
    context: JSONObject | None = None,
) -> permission_service_pb2.CheckBulkPermissionsResponse:
    """The server's response for a bulk check: ``requests`` and the results
    ``check_bulk(requests, context=...)`` returned for them, in order.

    Each pair echoes its request item and carries either the result or, for a
    result with ``error`` set (``item_errors=BulkItemErrors.RETURN``), that
    error's status. The response's ``checked_at`` is the last result's
    revision; ``check_bulk`` may span several requests, each with its own.
    Raises ``InvalidArgumentError`` when the two sequences differ in length.
    """
    if len(requests) != len(results):
        raise InvalidArgumentError(
            f"check_bulk: {len(requests)} requests but {len(results)} results"
        )
    response = permission_service_pb2.CheckBulkPermissionsResponse()
    for i, (request, result) in enumerate(zip(requests, results)):
        pair = response.pairs.add()
        try:
            pair.request.CopyFrom(check_request_to_proto(request, context=context))
        except InvalidArgumentError as error:
            raise InvalidArgumentError(f"check_bulk item {i}: {error}") from error
        if result.error is not None:
            pair.error.CopyFrom(_status_to_proto(result.error))
        else:
            single = check_result_to_proto(result)
            pair.item.CopyFrom(
                permission_service_pb2.CheckBulkPermissionsResponseItem(
                    permissionship=single.permissionship,
                    partial_caveat_info=single.partial_caveat_info
                    if single.HasField("partial_caveat_info")
                    else None,
                    debug_trace=single.debug_trace if single.HasField("debug_trace") else None,
                )
            )
        token = zed_token_to_proto(result.checked_at)
        if token is not None:
            response.checked_at.CopyFrom(token)
    return response


def _trace_to_proto(value: CheckDebugTrace) -> debug_pb2.CheckDebugTrace:
    result = debug_pb2.CheckDebugTrace(
        resource=value.resource._to_proto(),
        permission=value.permission,
        permission_type=value.permission_type.value,
        subject=value.subject._to_proto(),
        result=_CHECK_PERMISSIONSHIP.get(value.result, 0),
        optional_expires_at=_timestamp(value.expiration),
        trace_operation_id=value.trace_operation_id,
        source=value.source,
    )
    if value.caveat_evaluation is not None:
        c = value.caveat_evaluation
        result.caveat_evaluation_info.CopyFrom(
            debug_pb2.CaveatEvalInfo(
                expression=c.expression,
                result=c.result.value,
                context=context_to_proto(c.context),
                partial_caveat_info=_partial(c.missing_context),
                caveat_name=c.caveat_name,
            )
        )
    if value.duration is not None:
        duration = duration_pb2.Duration()
        duration.FromTimedelta(value.duration)
        result.duration.CopyFrom(duration)
    if value.was_cached_result is not None:
        result.was_cached_result = value.was_cached_result
    if value.sub_problems is not None:
        result.sub_problems.CopyFrom(
            debug_pb2.CheckDebugTrace.SubProblems(
                traces=[_trace_to_proto(x) for x in value.sub_problems]
            )
        )
    return result


def debug_information_to_proto(value: DebugInformation | None) -> debug_pb2.DebugInformation | None:
    if value is None:
        return None
    return debug_pb2.DebugInformation(
        check=_trace_to_proto(value.check) if value.check is not None else None,
        schema_used=value.schema_used,
    )


def debug_information_from_proto(value: debug_pb2.DebugInformation | None) -> DebugInformation | None:
    if value is None:
        return None
    holder = permission_service_pb2.CheckPermissionResponse(debug_trace=value)
    return _debug_information(holder)


def lookup_resource_to_proto(value: LookupResource) -> permission_service_pb2.LookupResourcesResponse:
    """Includes the looked-up-at revision and after-result cursor."""
    return permission_service_pb2.LookupResourcesResponse(
        looked_up_at=zed_token_to_proto(value.looked_up_at),
        resource_object_id=value.resource_id,
        permissionship=_LOOKUP_PERMISSIONSHIP.get(value.permissionship, 0),
        partial_caveat_info=_partial(
            value.partial_caveat.missing_required_context if value.partial_caveat else ()
        ),
        after_result_cursor=core_pb2.Cursor(token=value.after_result_cursor)
        if value.after_result_cursor
        else None,
    )


def lookup_resource_from_proto(value: permission_service_pb2.LookupResourcesResponse) -> LookupResource:
    return _lookup_resource(value)


def _resolved(value: ResolvedSubject) -> permission_service_pb2.ResolvedSubject:
    return permission_service_pb2.ResolvedSubject(
        subject_object_id=value.subject_id,
        permissionship=_LOOKUP_PERMISSIONSHIP.get(value.permissionship, 0),
        partial_caveat_info=_partial(
            value.partial_caveat.missing_required_context if value.partial_caveat else ()
        ),
    )


def lookup_subject_to_proto(value: LookupSubject) -> permission_service_pb2.LookupSubjectsResponse:
    """Includes wildcard exclusions, and fills the deprecated top-level fields
    (subject ID, excluded IDs, permissionship and partial caveat info) that
    SpiceDB still sends, so the message matches the server's response."""
    subject = _resolved(value.subject)
    return permission_service_pb2.LookupSubjectsResponse(
        looked_up_at=zed_token_to_proto(value.looked_up_at),
        subject=subject,
        excluded_subjects=[_resolved(x) for x in value.excluded_subjects],
        subject_object_id=subject.subject_object_id,
        excluded_subject_ids=[x.subject_id for x in value.excluded_subjects],
        permissionship=subject.permissionship,
        partial_caveat_info=subject.partial_caveat_info
        if subject.HasField("partial_caveat_info")
        else None,
    )


def lookup_subject_from_proto(value: permission_service_pb2.LookupSubjectsResponse) -> LookupSubject:
    return _lookup_subject(value)


def read_relationship_to_proto(value: ReadRelationship) -> permission_service_pb2.ReadRelationshipsResponse:
    """Includes the read-at revision and after-result cursor."""
    return permission_service_pb2.ReadRelationshipsResponse(
        read_at=zed_token_to_proto(value.read_at),
        relationship=value.relationship._to_proto(),
        after_result_cursor=core_pb2.Cursor(token=value.after_result_cursor)
        if value.after_result_cursor
        else None,
    )


_TREE_OPERATION = {
    TreeOperation.UNSPECIFIED: core_pb2.AlgebraicSubjectSet.OPERATION_UNSPECIFIED,
    TreeOperation.UNION: core_pb2.AlgebraicSubjectSet.OPERATION_UNION,
    TreeOperation.INTERSECTION: core_pb2.AlgebraicSubjectSet.OPERATION_INTERSECTION,
    TreeOperation.EXCLUSION: core_pb2.AlgebraicSubjectSet.OPERATION_EXCLUSION,
}


def permission_tree_to_proto(value: PermissionTree) -> core_pb2.PermissionRelationshipTree:
    result = core_pb2.PermissionRelationshipTree(
        expanded_object=value.expanded_object._to_proto(),
        expanded_relation=value.expanded_relation,
    )
    if value.intermediate is not None:
        result.intermediate.CopyFrom(
            core_pb2.AlgebraicSubjectSet(
                operation=_TREE_OPERATION[value.intermediate.operation],
                children=[permission_tree_to_proto(x) for x in value.intermediate.children],
            )
        )
    elif value.leaf is not None:
        result.leaf.CopyFrom(
            core_pb2.DirectSubjectSet(subjects=[x._to_proto() for x in value.leaf.subjects])
        )
    return result


def permission_tree_from_proto(value: core_pb2.PermissionRelationshipTree) -> PermissionTree:
    return _permission_tree_from_proto(value)


def expand_result_to_proto(value: ExpandResult) -> permission_service_pb2.ExpandPermissionTreeResponse:
    return permission_service_pb2.ExpandPermissionTreeResponse(
        expanded_at=zed_token_to_proto(value.revision),
        tree_root=permission_tree_to_proto(value.tree),
    )


def write_result_to_proto(value: WriteResult) -> permission_service_pb2.WriteRelationshipsResponse:
    return permission_service_pb2.WriteRelationshipsResponse(written_at=zed_token_to_proto(value.revision))


def write_schema_result_to_proto(value: ZedToken) -> schema_service_pb2.WriteSchemaResponse:
    """``write_schema`` returns its revision token directly."""
    return schema_service_pb2.WriteSchemaResponse(written_at=zed_token_to_proto(value))


def read_schema_result_to_proto(value: SchemaResult) -> schema_service_pb2.ReadSchemaResponse:
    return schema_service_pb2.ReadSchemaResponse(
        schema_text=value.schema, read_at=zed_token_to_proto(value.revision)
    )
