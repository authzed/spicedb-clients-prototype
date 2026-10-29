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

from spicedb import _wire
from spicedb.errors import error_from_status_proto
from spicedb.types import (
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
    _permission_tree_from_proto,
    _permissionship_from_proto,
    _resolved_subject_from_proto,
)


def _check_permissionship(value: int, what: str) -> Permissionship:
    """A check's permissionship, which the server always sets: an unspecified
    one is a malformed response, never ``Permissionship.UNSPECIFIED``."""
    if value == permission_service_pb2.CheckPermissionResponse.PERMISSIONSHIP_UNSPECIFIED:
        raise _wire.malformed(f"{what}.permissionship is unspecified")
    return _check_permissionship_from_proto(value)


def _lookup_permissionship(value: int, what: str) -> Permissionship:
    """A lookup result's permissionship, which the server always sets."""
    if value == permission_service_pb2.LOOKUP_PERMISSIONSHIP_UNSPECIFIED:
        raise _wire.malformed(f"{what}.permissionship is unspecified")
    return _permissionship_from_proto(value)


def check_results(
    resp: permission_service_pb2.CheckBulkPermissionsResponse,
    expected_items: Sequence[permission_service_pb2.CheckBulkPermissionsRequestItem],
    offset: int = 0,
    return_item_errors: bool = False,
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
        raise _wire.malformed(
            f"check_bulk_permissions returned {len(resp.pairs)} pair(s) for "
            f"{expected_count} request item(s)"
        )

    # Validate every echo before interpreting any item outcome. Protobuf message
    # equality preserves nested JSON values and message-field presence, so absent
    # context cannot silently compare equal to a present empty context.
    for i, (pair, expected) in enumerate(zip(resp.pairs, expected_items)):
        if not pair.HasField("request") or pair.request != expected:
            error = _wire.malformed(
                f"check item {offset + i}: missing or mismatched echoed request"
            )
            error.input_index = offset + i
            raise error

    checked_at = _wire.token(resp, "checked_at", "CheckBulkPermissionsResponse")
    results: list[CheckResult] = []
    for i, pair in enumerate(resp.pairs):
        if pair.HasField("error"):
            error = error_from_status_proto(pair.error)
            error.input_index = offset + i
            if not return_item_errors:
                raise error
            results.append(
                CheckResult(
                    permissionship=Permissionship.UNSPECIFIED,
                    missing_context=(),
                    checked_at=checked_at,
                    error=error,
                )
            )
            continue
        if not pair.HasField("item"):
            # The proto's `response` is a oneof -- a well-behaved server
            # always sets it to `item` or `error`, so this should be
            # unreachable in practice. Mirrors spicedb-rust's
            # malformed-oneof guard: fail loudly instead of falling through
            # to the item field's default (zero) value.
            error = _wire.malformed(
                f"check item {offset + i}: CheckBulkPermissionsPair has "
                "neither item nor error"
            )
            error.input_index = offset + i
            raise error
        if (
            pair.item.permissionship
            == permission_service_pb2.CheckPermissionResponse.PERMISSIONSHIP_UNSPECIFIED
        ):
            error = _wire.malformed(f"check item {offset + i}: permissionship is unspecified")
            error.input_index = offset + i
            raise error
        permissionship = _check_permissionship_from_proto(pair.item.permissionship)
        results.append(
            CheckResult(
                permissionship=permissionship,
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
    resource_id = _wire.text(resp.resource_object_id, "LookupResourcesResponse.resource_object_id")
    looked_up_at = _wire.token(resp, "looked_up_at", "LookupResourcesResponse")
    return LookupResource(
        resource_id=resource_id,
        permissionship=_lookup_permissionship(resp.permissionship, "LookupResourcesResponse"),
        partial_caveat=_partial_caveat_from_proto(resp),
        looked_up_at=looked_up_at,
        after_result_cursor=resp.after_result_cursor.token or None
        if resp.HasField("after_result_cursor")
        else None,
    )


def lookup_subject(
    resp: permission_service_pb2.LookupSubjectsResponse,
) -> LookupSubject:
    subject = _resolved_subject_from_proto(resp.subject)
    wire_permissionship = resp.subject.permissionship
    if not subject.subject_id:
        # Fall back to the deprecated top-level fields for servers that don't
        # yet populate the non-deprecated `subject` field.
        subject = ResolvedSubject(
            subject_id=resp.subject_object_id,
            permissionship=_permissionship_from_proto(resp.permissionship),
            partial_caveat=_partial_caveat_from_proto(resp),
        )
        wire_permissionship = resp.permissionship
    _wire.text(subject.subject_id, "LookupSubjectsResponse.subject.subject_object_id")
    # The found subject's permissionship is always set; excluded subjects keep
    # their existing mapping (the deprecated ID-only form has none at all).
    _lookup_permissionship(wire_permissionship, "LookupSubjectsResponse.subject")

    excluded: list[ResolvedSubject] = []
    if resp.excluded_subjects:
        excluded = [_resolved_subject_from_proto(e) for e in resp.excluded_subjects]
        for value in excluded:
            _wire.text(value.subject_id, "LookupSubjectsResponse.excluded_subjects.subject_object_id")
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
        looked_up_at=_wire.token(resp, "looked_up_at", "LookupSubjectsResponse"),
    )


def _update_operation(value):
    from spicedb.types import _UPDATE_OP_MAP, UpdateOperation

    return _UPDATE_OP_MAP.get(value, UpdateOperation.UNSPECIFIED)


def watch_event(resp: watch_service_pb2.WatchResponse) -> WatchEvent:
    return WatchEvent(
        updates=[
            Update(
                _update_operation(u.operation),
                _wire.relationship(u, "relationship", "WatchResponse.updates"),
            )
            for u in resp.updates
        ],
        changes_through=_wire.token(resp, "changes_through", "WatchResponse"),
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
    checked_at = _wire.token(resp, "checked_at", "CheckPermissionResponse")
    return CheckResult(
        permissionship=_check_permissionship(resp.permissionship, "CheckPermissionResponse"),
        missing_context=list(resp.partial_caveat_info.missing_required_context),
        checked_at=checked_at,
        debug_trace=_debug_information(resp),
        expiration=resp.optional_expires_at.ToDatetime(tzinfo=timezone.utc)
        if resp.HasField("optional_expires_at")
        else None,
    )


def _set_ref(value, field, what):
    ref = _wire.need(value, field, what)
    return PermissionSetRef(
        _wire.object_ref(value, field, what),
        _wire.text(ref.permission_or_relation, f"{what}.{field}.permission_or_relation"),
    )


def permission_set_change(value, what="PermissionSetChange"):
    parent = _set_ref(value, "parent_set", what)
    if value.HasField("child_set"):
        child = _set_ref(value, "child_set", what)
    elif value.HasField("child_member"):
        child = PermissionSetMember(
            _wire.object_ref(value, "child_member", what),
            value.child_member.optional_permission_or_relation or None,
        )
    else:
        raise _wire.malformed(f"{what} has neither child_set nor child_member")
    operation = {
        1: PermissionSetOperation.ADDED,
        2: PermissionSetOperation.REMOVED,
    }.get(value.operation, PermissionSetOperation.UNSPECIFIED)
    return PermissionSetChange(_wire.token(value, "at_revision", what), operation, parent, child)


def permission_set_event(response) -> PermissionSetEvent:
    variant = response.WhichOneof("response")
    if variant == "change":
        return permission_set_change(response.change)
    if variant == "completed_revision":
        return PermissionSetCheckpoint(_wire.token(response, "completed_revision", "WatchPermissionSetsResponse"))
    if variant == "lookup_permission_sets_required":
        return PermissionSetsLookupRequired(
            _wire.token(
                response.lookup_permission_sets_required,
                "required_lookup_at",
                "WatchPermissionSetsResponse.lookup_permission_sets_required",
            )
        )
    if variant == "breaking_schema_change":
        return PermissionSetSchemaChange(
            _wire.token(
                response.breaking_schema_change, "change_at", "WatchPermissionSetsResponse.breaking_schema_change"
            ),
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
    what = "LookupPermissionSetsResponse"
    change = permission_set_change(_wire.need(response, "change", what), f"{what}.change")
    value = _wire.need(response, "cursor", what)
    cursor = PermissionSetCursor(
        value.limit,
        ZedToken(value.token.token) if value.HasField("token") else None,
        value.starting_index,
        value.completed_members,
        value.starting_key,
        value.cursor,
    )
    return PermissionSetLookup(change, cursor)


def permission_set_download(response) -> PermissionSetDownload:
    return PermissionSetDownload(
        tuple(PermissionSetFile(value.name, value.url) for value in response.files),
        response.timestamp.ToDatetime(tzinfo=timezone.utc)
        if response.HasField("timestamp")
        else None,
        _wire.token(response, "at_revision", "DownloadPermissionSetsResponse"),
    )


def _check_tree(node, what: str) -> None:
    _wire.object_ref(node, "expanded_object", what)
    _wire.text(node.expanded_relation, f"{what}.expanded_relation")
    kind = node.WhichOneof("tree_type")
    if kind == "intermediate":
        for i, child in enumerate(node.intermediate.children):
            _check_tree(child, f"{what}.intermediate.children[{i}]")
    elif kind == "leaf":
        for i, subject in enumerate(node.leaf.subjects):
            _wire.object_ref(subject, "object", f"{what}.leaf.subjects[{i}]")
    else:
        raise _wire.malformed(f"{what} has neither intermediate nor leaf")


def permission_tree(response):
    """The expand response's tree, every node checked before any is mapped."""
    root = _wire.need(response, "tree_root", "ExpandPermissionTreeResponse")
    _check_tree(root, "ExpandPermissionTreeResponse.tree_root")
    return _permission_tree_from_proto(root)
