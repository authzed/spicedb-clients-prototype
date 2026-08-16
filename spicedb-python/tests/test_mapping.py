"""Tests for the flavor-free response mappers.

The deprecated-field fallbacks below are the reason this logic is worth
extracting: they are subtle, they protect against over-granting access, and
duplicating them per flavor would be a security-relevant drift risk.
"""

import pytest
from authzed.api.v1 import permission_service_pb2 as psp

from spicedb import Permissionship
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
    from authzed.api.v1 import core_pb2

    resp = psp.LookupSubjectsResponse(
        subject=psp.ResolvedSubject(
            subject_object_id="modern",
            permissionship=psp.LookupPermissionship.LOOKUP_PERMISSIONSHIP_HAS_PERMISSION,
        ),
        subject_object_id="deprecated",
    )
    assert mapping.lookup_subject(resp).subject.subject_id == "modern"
    assert core_pb2 is not None
