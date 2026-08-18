"""Tests for the flavor-free response mappers.

The deprecated-field fallbacks below are the reason this logic is worth
extracting: they are subtle, they protect against over-granting access, and
duplicating them per flavor would be a security-relevant drift risk.
"""

import pytest
from authzed.api.v1 import (
    core_pb2,
    permission_service_pb2 as psp,
    watch_service_pb2 as wsp,
)

from spicedb import Permissionship, UpdateOperation
from spicedb import _mapping as mapping
from spicedb.errors import PermissionDeniedError, SpiceDBError
from spicedb.types import CheckResult


def test_check_results_maps_permissionship_per_item():
    resp = psp.CheckBulkPermissionsResponse(
        checked_at=core_pb2.ZedToken(token="deadbeef"),
        pairs=[
            psp.CheckBulkPermissionsPair(
                item=psp.CheckBulkPermissionsResponseItem(
                    permissionship=psp.CheckPermissionResponse.PERMISSIONSHIP_HAS_PERMISSION
                )
            ),
            psp.CheckBulkPermissionsPair(
                item=psp.CheckBulkPermissionsResponseItem(
                    permissionship=psp.CheckPermissionResponse.PERMISSIONSHIP_NO_PERMISSION
                )
            ),
        ]
    )
    results = mapping.check_results(resp, 2)
    assert results == [
        CheckResult(
            permissionship=Permissionship.HAS_PERMISSION,
            missing_context=[],
            checked_at="deadbeef",
        ),
        CheckResult(
            permissionship=Permissionship.NO_PERMISSION,
            missing_context=[],
            checked_at="deadbeef",
        ),
    ]
    assert [r.has_permission for r in results] == [True, False]


def test_check_results_conditional_has_permission_is_false():
    """T1 (mapper angle): CONDITIONAL_PERMISSION must map to has_permission ==
    False, never True -- a conditional result is not a grant."""
    resp = psp.CheckBulkPermissionsResponse(
        pairs=[
            psp.CheckBulkPermissionsPair(
                item=psp.CheckBulkPermissionsResponseItem(
                    permissionship=psp.CheckPermissionResponse.PERMISSIONSHIP_CONDITIONAL_PERMISSION
                )
            )
        ]
    )
    results = mapping.check_results(resp, 1)
    assert results[0].permissionship == Permissionship.CONDITIONAL_PERMISSION
    assert results[0].has_permission is False


def test_check_results_missing_context_carries_server_values():
    """T2: missing_context must carry the server's exact
    missing_required_context contents, not merely be non-empty."""
    resp = psp.CheckBulkPermissionsResponse(
        pairs=[
            psp.CheckBulkPermissionsPair(
                item=psp.CheckBulkPermissionsResponseItem(
                    permissionship=psp.CheckPermissionResponse.PERMISSIONSHIP_CONDITIONAL_PERMISSION,
                    partial_caveat_info=core_pb2.PartialCaveatInfo(
                        missing_required_context=["ip_address", "time_of_day"]
                    ),
                )
            )
        ]
    )
    results = mapping.check_results(resp, 1)
    assert results[0].missing_context == ["ip_address", "time_of_day"]


def test_check_results_missing_context_empty_when_not_conditional():
    resp = psp.CheckBulkPermissionsResponse(
        pairs=[
            psp.CheckBulkPermissionsPair(
                item=psp.CheckBulkPermissionsResponseItem(
                    permissionship=psp.CheckPermissionResponse.PERMISSIONSHIP_HAS_PERMISSION
                )
            )
        ]
    )
    results = mapping.check_results(resp, 1)
    assert results[0].missing_context == []


def test_check_results_checked_at_populated_from_response():
    """T3: checked_at comes from the response-level token --
    CheckBulkPermissionsResponseItem has no per-item token of its own, so the
    one token on the response is propagated onto every result."""
    resp = psp.CheckBulkPermissionsResponse(
        checked_at=core_pb2.ZedToken(token="cafebabe"),
        pairs=[
            psp.CheckBulkPermissionsPair(
                item=psp.CheckBulkPermissionsResponseItem(
                    permissionship=psp.CheckPermissionResponse.PERMISSIONSHIP_HAS_PERMISSION
                )
            ),
            psp.CheckBulkPermissionsPair(
                item=psp.CheckBulkPermissionsResponseItem(
                    permissionship=psp.CheckPermissionResponse.PERMISSIONSHIP_NO_PERMISSION
                )
            ),
        ]
    )
    results = mapping.check_results(resp, 2)
    assert [r.checked_at for r in results] == ["cafebabe", "cafebabe"]


def test_check_results_raises_typed_error_on_item_error():
    from google.rpc import status_pb2

    resp = psp.CheckBulkPermissionsResponse(
        pairs=[
            psp.CheckBulkPermissionsPair(
                error=status_pb2.Status(code=7, message="nope")  # 7 = PERMISSION_DENIED
            )
        ]
    )
    with pytest.raises(PermissionDeniedError, match="nope"):
        mapping.check_results(resp, 1)


def test_check_results_raises_when_response_has_fewer_pairs_than_request_items():
    """The proto guarantees pairs are returned in request order but says
    nothing about count. A short response would otherwise silently desync
    results[i] from relationships[i] for every item after the gap -- one
    resource's answer attributed to another."""
    resp = psp.CheckBulkPermissionsResponse(
        pairs=[
            psp.CheckBulkPermissionsPair(
                item=psp.CheckBulkPermissionsResponseItem(
                    permissionship=psp.CheckPermissionResponse.PERMISSIONSHIP_HAS_PERMISSION
                )
            ),
        ]
    )
    with pytest.raises(SpiceDBError, match=r"1 pair.*2 request item"):
        mapping.check_results(resp, 2)


def test_check_results_raises_on_malformed_pair_instead_of_shrinking_results():
    """A CheckBulkPermissionsPair whose `response` oneof is unset (neither
    `item` nor `error`) must fail loudly, not silently fall through to the
    default (zero-value) item."""
    resp = psp.CheckBulkPermissionsResponse(
        pairs=[psp.CheckBulkPermissionsPair()]
    )
    with pytest.raises(SpiceDBError, match="check item 0"):
        mapping.check_results(resp, 1)


def test_lookup_subject_falls_back_to_deprecated_top_level_fields():
    """Servers that don't populate `subject` must still map correctly."""
    resp = psp.LookupSubjectsResponse(
        subject_object_id="jimmy",
        permissionship=psp.LookupPermissionship.LOOKUP_PERMISSIONSHIP_HAS_PERMISSION,
    )
    got = mapping.lookup_subject(resp)
    assert got.subject.subject_id == "jimmy"
    assert got.subject.permissionship == Permissionship.HAS_PERMISSION


def test_lookup_subject_falls_back_to_deprecated_excluded_ids():
    """excluded_subject_ids carries no permissionship, so it maps to UNSPECIFIED.

    Callers MUST still see the exclusions -- dropping them would over-grant on a
    wildcard match.
    """
    resp = psp.LookupSubjectsResponse(
        subject_object_id="*",
        permissionship=psp.LookupPermissionship.LOOKUP_PERMISSIONSHIP_HAS_PERMISSION,
        excluded_subject_ids=["banned"],
    )
    got = mapping.lookup_subject(resp)
    assert got.subject.subject_id == "*"
    assert [e.subject_id for e in got.excluded_subjects] == ["banned"]
    assert got.excluded_subjects[0].permissionship == Permissionship.UNSPECIFIED


def test_lookup_subject_prefers_modern_fields_over_deprecated():
    resp = psp.LookupSubjectsResponse(
        subject=psp.ResolvedSubject(
            subject_object_id="modern",
            permissionship=psp.LookupPermissionship.LOOKUP_PERMISSIONSHIP_HAS_PERMISSION,
        ),
        subject_object_id="deprecated",
    )
    assert mapping.lookup_subject(resp).subject.subject_id == "modern"


def test_lookup_resource_maps_has_permission_with_no_partial_caveat():
    resp = psp.LookupResourcesResponse(
        resource_object_id="doc1",
        permissionship=psp.LookupPermissionship.LOOKUP_PERMISSIONSHIP_HAS_PERMISSION,
    )
    got = mapping.lookup_resource(resp)
    assert got.resource_id == "doc1"
    assert got.permissionship == Permissionship.HAS_PERMISSION
    assert got.partial_caveat is None


def test_lookup_resource_conditional_permission_carries_partial_caveat():
    """A conditional result is NOT a full grant -- a caller that loses
    `partial_caveat` here would treat it as one.
    """
    resp = psp.LookupResourcesResponse(
        resource_object_id="doc2",
        permissionship=psp.LookupPermissionship.LOOKUP_PERMISSIONSHIP_CONDITIONAL_PERMISSION,
        partial_caveat_info=core_pb2.PartialCaveatInfo(
            missing_required_context=["ip_address", "time_of_day"]
        ),
    )
    got = mapping.lookup_resource(resp)
    assert got.resource_id == "doc2"
    assert got.permissionship == Permissionship.CONDITIONAL_PERMISSION
    assert got.partial_caveat is not None
    assert got.partial_caveat.missing_required_context == [
        "ip_address",
        "time_of_day",
    ]


def test_lookup_resource_carries_looked_up_at():
    """The looked_up_at token is what makes read-your-writes reachable for
    lookups -- previously dropped entirely."""
    resp = psp.LookupResourcesResponse(
        resource_object_id="doc1",
        permissionship=psp.LookupPermissionship.LOOKUP_PERMISSIONSHIP_HAS_PERMISSION,
        looked_up_at=core_pb2.ZedToken(token="deadbeef"),
    )
    got = mapping.lookup_resource(resp)
    assert got.looked_up_at == "deadbeef"


def test_lookup_subject_carries_looked_up_at():
    resp = psp.LookupSubjectsResponse(
        subject=psp.ResolvedSubject(
            subject_object_id="alice",
            permissionship=psp.LookupPermissionship.LOOKUP_PERMISSIONSHIP_HAS_PERMISSION,
        ),
        looked_up_at=core_pb2.ZedToken(token="cafebabe"),
    )
    got = mapping.lookup_subject(resp)
    assert got.looked_up_at == "cafebabe"


def test_watch_event_maps_multiple_updates_and_revision():
    resp = wsp.WatchResponse(
        updates=[
            core_pb2.RelationshipUpdate(
                operation=core_pb2.RelationshipUpdate.OPERATION_TOUCH,
                relationship=core_pb2.Relationship(
                    resource=core_pb2.ObjectReference(
                        object_type="document", object_id="1"
                    ),
                    relation="viewer",
                    subject=core_pb2.SubjectReference(
                        object=core_pb2.ObjectReference(
                            object_type="user", object_id="alice"
                        )
                    ),
                ),
            ),
            core_pb2.RelationshipUpdate(
                operation=core_pb2.RelationshipUpdate.OPERATION_DELETE,
                relationship=core_pb2.Relationship(
                    resource=core_pb2.ObjectReference(
                        object_type="document", object_id="2"
                    ),
                    relation="viewer",
                    subject=core_pb2.SubjectReference(
                        object=core_pb2.ObjectReference(
                            object_type="user", object_id="bob"
                        )
                    ),
                ),
            ),
        ],
        changes_through=core_pb2.ZedToken(token="deadbeef"),
    )
    updates, revision = mapping.watch_event(resp)
    assert [u.operation for u in updates] == [
        UpdateOperation.TOUCH,
        UpdateOperation.DELETE,
    ]
    assert [u.relationship.resource_id for u in updates] == ["1", "2"]
    assert [u.relationship.subject_id for u in updates] == ["alice", "bob"]
    assert revision == "deadbeef"


def test_watch_event_empty_updates_returns_empty_list_and_token():
    resp = wsp.WatchResponse(changes_through=core_pb2.ZedToken(token="cafebabe"))
    updates, revision = mapping.watch_event(resp)
    assert updates == []
    assert revision == "cafebabe"
