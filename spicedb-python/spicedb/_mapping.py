"""Pure proto -> dataclass mappers shared by the sync and async clients.

MUST NOT import or reference channels, stubs, awaitables, or grpc call types.

The deprecated-field fallbacks here are security-relevant: a wildcard subject
match that loses its excluded_subjects would over-grant access. They live in one
place so both flavors cannot disagree about them.
"""

from __future__ import annotations

from authzed.api.v1 import permission_service_pb2, watch_service_pb2

from spicedb.errors import error_from_status_proto
from spicedb.types import (
    CheckResult,
    LookupResource,
    LookupSubject,
    Permissionship,
    ResolvedSubject,
    Update,
    _check_permissionship_from_proto,
    _partial_caveat_from_proto,
    _permissionship_from_proto,
    _resolved_subject_from_proto,
)


def check_results(
    resp: permission_service_pb2.CheckBulkPermissionsResponse,
) -> list[CheckResult]:
    """Map a bulk-check response to CheckResults, raising on any per-item
    error.

    CheckBulkPermissionsResponse.checked_at is response-level, not
    per-item — CheckBulkPermissionsResponseItem carries no token of its
    own — so the one token on the response is propagated onto every result.
    """
    checked_at = resp.checked_at.token
    results: list[CheckResult] = []
    for pair in resp.pairs:
        if pair.HasField("error"):
            raise error_from_status_proto(pair.error)
        results.append(
            CheckResult(
                permissionship=_check_permissionship_from_proto(
                    pair.item.permissionship
                ),
                missing_context=list(
                    pair.item.partial_caveat_info.missing_required_context
                ),
                checked_at=checked_at,
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
        looked_up_at=resp.looked_up_at.token,
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
        looked_up_at=resp.looked_up_at.token,
    )


def watch_event(resp: watch_service_pb2.WatchResponse) -> tuple[list[Update], str]:
    return (
        [Update._from_proto(u) for u in resp.updates],
        resp.changes_through.token,
    )
