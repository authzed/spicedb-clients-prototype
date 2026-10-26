"""Pure proto -> dataclass mappers shared by the sync and async clients.

MUST NOT import or reference channels, stubs, awaitables, or grpc call types.

The deprecated-field fallbacks here are security-relevant: a wildcard subject
match that loses its excluded_subjects would over-grant access. They live in one
place so both flavors cannot disagree about them.
"""

from __future__ import annotations
from spicedb.token import ZedToken

from datetime import timezone
from collections.abc import Sequence

from authzed.api.v1 import permission_service_pb2, watch_service_pb2

from spicedb.errors import SpiceDBError, error_from_status_proto
from spicedb.types import (
    ObjectRef,
    PermissionSetCursor,
    PermissionSetRef,
    PermissionSetMember,
    PermissionSetOperation,
    PermissionSetChange,
    PermissionSetCheckpoint,
    PermissionSetsLookupRequired,
    WatchedPermission,
    PermissionSetSchemaChange,
    UnknownPermissionSetEvent,
    PermissionSetEvent,
    PermissionSetLookup,
    PermissionSetFile,
    PermissionSetDownload,
    CheckResult,
    LookupResource,
    LookupSubject,
    Permissionship,
    ResolvedSubject,
    Update,
    WatchEvent,
    _check_permissionship_from_proto,
    _debug_information,
    context_from_struct,
    _partial_caveat_from_proto,
    _permissionship_from_proto,
    _resolved_subject_from_proto,
)


def check_results(
    resp: permission_service_pb2.CheckBulkPermissionsResponse,
    expected_items: Sequence[permission_service_pb2.CheckBulkPermissionsRequestItem],
    offset: int = 0,
) -> list[CheckResult]:
    """Map a bulk-check response to CheckResults, raising on any per-item
    error.

    CheckBulkPermissionsResponse.checked_at is response-level, not
    per-item — CheckBulkPermissionsResponseItem carries no token of its
    own — so the one token on the response is propagated onto every result.

    ``expected_items`` are the exact items sent in the request. The proto
    guarantees pairs are returned in request order but says nothing about
    count, so a response with a different number of pairs would otherwise
    silently desync results[i] from relationships[i] for every item after
    the gap -- one resource's answer attributed to another. Raise loudly
    instead of returning a misaligned-but-"successful" list.

    ``offset`` is this response's start index within the caller's full
    relationship list, since a large check is split across several requests.
    The "check item N" message below reports ``offset + i``, not ``i``: the
    index a caller sees must be the one they can use to look up their own
    relationship. Reporting the chunk-relative index would attribute the
    failing item to a different resource entirely.
    """
    expected_count = len(expected_items)
    if len(resp.pairs) != expected_count:
        raise SpiceDBError(
            f"check_bulk_permissions returned {len(resp.pairs)} pair(s) for "
            f"{expected_count} request item(s)"
        )

    # Validate every echo before interpreting any item outcome. Protobuf message
    # equality preserves nested JSON values and message-field presence, so absent
    # context cannot silently compare equal to a present empty context.
    for i, (pair, expected) in enumerate(zip(resp.pairs, expected_items)):
        if not pair.HasField("request") or pair.request != expected:
            error = SpiceDBError(
                f"check item {offset + i}: missing or mismatched echoed request"
            )
            error.input_index = offset + i
            raise error

    checked_at = ZedToken(resp.checked_at.token)
    results: list[CheckResult] = []
    for i, pair in enumerate(resp.pairs):
        if pair.HasField("error"):
            error = error_from_status_proto(pair.error)
            error.input_index = offset + i
            raise error
        if not pair.HasField("item"):
            # The proto's `response` is a oneof -- a well-behaved server
            # always sets it to `item` or `error`, so this should be
            # unreachable in practice. Mirrors spicedb-rust's
            # malformed-oneof guard: fail loudly instead of falling through
            # to the item field's default (zero) value.
            error = SpiceDBError(
                f"check item {offset + i}: malformed CheckBulkPermissionsPair "
                "(neither item nor error set)"
            )
            error.input_index = offset + i
            raise error
        results.append(
            CheckResult(
                permissionship=_check_permissionship_from_proto(
                    pair.item.permissionship
                ),
                missing_context=list(
                    pair.item.partial_caveat_info.missing_required_context
                ),
                checked_at=checked_at,
                debug_trace=_debug_information(pair.item),
            )
        )
    return results


def lookup_resource(
    resp: permission_service_pb2.LookupResourcesResponse,
) -> LookupResource:
    return LookupResource(
        resource_id=resp.resource_object_id,
        permissionship=_permissionship_from_proto(resp.permissionship),
        partial_caveat=_partial_caveat_from_proto(resp),
        looked_up_at=ZedToken(resp.looked_up_at.token),
    )


def lookup_subject(
    resp: permission_service_pb2.LookupSubjectsResponse,
) -> LookupSubject:
    subject = _resolved_subject_from_proto(resp.subject)
    if not subject.subject_id:
        # Fall back to the deprecated top-level fields for servers that don't
        # yet populate the non-deprecated `subject` field.
        subject = ResolvedSubject(
            subject_id=resp.subject_object_id,
            permissionship=_permissionship_from_proto(resp.permissionship),
            partial_caveat=_partial_caveat_from_proto(resp),
        )

    excluded: list[ResolvedSubject] = []
    if resp.excluded_subjects:
        excluded = [_resolved_subject_from_proto(e) for e in resp.excluded_subjects]
    elif resp.excluded_subject_ids:
        # Deprecated field: IDs only, no permissionship/caveat info.
        excluded = [
            ResolvedSubject(
                subject_id=subject_id,
                permissionship=Permissionship.UNSPECIFIED,
            )
            for subject_id in resp.excluded_subject_ids
        ]

    return LookupSubject(
        subject=subject,
        excluded_subjects=excluded,
        looked_up_at=ZedToken(resp.looked_up_at.token),
    )


def watch_event(resp: watch_service_pb2.WatchResponse) -> WatchEvent:
    return WatchEvent(
        updates=[Update._from_proto(u) for u in resp.updates],
        changes_through=ZedToken(resp.changes_through.token),
        is_checkpoint=resp.is_checkpoint,
        transaction_metadata=context_from_struct(resp.optional_transaction_metadata)
        if resp.HasField("optional_transaction_metadata")
        else None,
        schema_updated=resp.schema_updated,
        full_revision_metadata=tuple(
            context_from_struct(value) for value in resp.full_revision_metadata
        ),
    )


def check_result(resp):
    return CheckResult(
        permissionship=_check_permissionship_from_proto(resp.permissionship),
        missing_context=list(resp.partial_caveat_info.missing_required_context),
        checked_at=ZedToken(resp.checked_at.token),
        debug_trace=_debug_information(resp),
        expiration=resp.optional_expires_at.ToDatetime(tzinfo=timezone.utc)
        if resp.HasField("optional_expires_at")
        else None,
    )


def permission_set_change(value):
    parent = (
        PermissionSetRef(
            ObjectRef(value.parent_set.object_type, value.parent_set.object_id),
            value.parent_set.permission_or_relation,
        )
        if value.HasField("parent_set")
        else None
    )
    child = None
    if value.HasField("child_set"):
        child = PermissionSetRef(
            ObjectRef(value.child_set.object_type, value.child_set.object_id),
            value.child_set.permission_or_relation,
        )
    elif value.HasField("child_member"):
        child = PermissionSetMember(
            ObjectRef(value.child_member.object_type, value.child_member.object_id),
            value.child_member.optional_permission_or_relation or None,
        )
    operation = {
        1: PermissionSetOperation.ADDED,
        2: PermissionSetOperation.REMOVED,
    }.get(value.operation, PermissionSetOperation.UNSPECIFIED)
    return PermissionSetChange(ZedToken(value.at_revision.token), operation, parent, child)


def permission_set_event(response) -> PermissionSetEvent:
    variant = response.WhichOneof("response")
    if variant == "change":
        return permission_set_change(response.change)
    if variant == "completed_revision":
        return PermissionSetCheckpoint(ZedToken(response.completed_revision.token))
    if variant == "lookup_permission_sets_required":
        return PermissionSetsLookupRequired(
            ZedToken(response.lookup_permission_sets_required.required_lookup_at.token)
        )
    if variant == "breaking_schema_change":
        return PermissionSetSchemaChange(
            ZedToken(response.breaking_schema_change.change_at.token),
            tuple(
                WatchedPermission(
                    value.resource_type,
                    value.permission,
                    value.subject_type,
                    value.optional_subject_relation or None,
                )
                for value in response.breaking_schema_change.affected_permissions
            ),
        )
    return UnknownPermissionSetEvent()


def permission_set_lookup(response) -> PermissionSetLookup:
    value = response.cursor
    cursor = (
        PermissionSetCursor(
            value.limit,
            ZedToken(value.token.token) if value.HasField("token") else None,
            value.starting_index,
            value.completed_members,
            value.starting_key,
            value.cursor,
        )
        if response.HasField("cursor")
        else None
    )
    return PermissionSetLookup(
        permission_set_change(response.change) if response.HasField("change") else None,
        cursor,
    )


def permission_set_download(response) -> PermissionSetDownload:
    return PermissionSetDownload(
        tuple(PermissionSetFile(value.name, value.url) for value in response.files),
        response.timestamp.ToDatetime(tzinfo=timezone.utc)
        if response.HasField("timestamp")
        else None,
        ZedToken(response.at_revision.token),
    )
