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
from spicedb.errors import PermissionDeniedError


def test_check_results_maps_permissionship_to_bool():
    resp = psp.CheckBulkPermissionsResponse(
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
    assert mapping.check_results(resp) == [True, False]


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
        mapping.check_results(resp)


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
