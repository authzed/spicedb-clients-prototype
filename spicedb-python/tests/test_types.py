"""Unit tests for spicedb.types — no SpiceDB instance needed."""

from datetime import datetime, timezone

import pytest
from authzed.api.v1 import core_pb2, permission_service_pb2

from spicedb.types import (
    CheckResult,
    Filter,
    IntermediateNode,
    LeafNode,
    ObjectRef,
    PartialCaveatInfo,
    Permissionship,
    PermissionTree,
    Relationship,
    ResolvedSubject,
    SubjectRef,
    Transaction,
    TreeOperation,
    Update,
    UpdateOperation,
    _check_permissionship_from_proto,
    _partial_caveat_from_proto,
    _permission_tree_from_proto,
    _permissionship_from_proto,
    _resolved_subject_from_proto,
)


class TestRelationship:
    def test_basic_construction(self):
        r = Relationship(
            resource_type="document",
            resource_id="readme",
            resource_relation="viewer",
            subject_type="user",
            subject_id="alice",
        )
        assert r.resource_type == "document"
        assert r.resource_id == "readme"
        assert r.resource_relation == "viewer"
        assert r.subject_type == "user"
        assert r.subject_id == "alice"
        assert r.subject_relation == ""
        assert r.caveat_name is None
        assert r.caveat_context is None
        assert r.expiration is None

    def test_from_triple(self):
        r = Relationship.from_triple("document:readme", "viewer", "user:alice")
        assert r.resource_type == "document"
        assert r.resource_id == "readme"
        assert r.resource_relation == "viewer"
        assert r.subject_type == "user"
        assert r.subject_id == "alice"
        assert r.subject_relation == ""

    def test_from_triple_with_subject_relation(self):
        r = Relationship.from_triple(
            "document:readme", "viewer", "group:engineers#member"
        )
        assert r.subject_type == "group"
        assert r.subject_id == "engineers"
        assert r.subject_relation == "member"

    def test_from_tuple(self):
        r = Relationship.from_tuple("document:readme#viewer", "user:alice")
        assert r.resource_type == "document"
        assert r.resource_id == "readme"
        assert r.resource_relation == "viewer"
        assert r.subject_type == "user"
        assert r.subject_id == "alice"

    def test_from_triple_with_caveat(self):
        r = Relationship.from_triple(
            "document:readme",
            "viewer",
            "user:alice",
            caveat_name="is_weekday",
            caveat_context={"day": "monday"},
        )
        assert r.caveat_name == "is_weekday"
        assert r.caveat_context == {"day": "monday"}

    def test_from_triple_with_expiration(self):
        exp = datetime(2030, 1, 1, tzinfo=timezone.utc)
        r = Relationship.from_triple(
            "document:readme", "viewer", "user:alice", expiration=exp
        )
        assert r.expiration == exp

    def test_frozen(self):
        r = Relationship.from_triple("document:readme", "viewer", "user:alice")
        try:
            r.resource_type = "other"  # type: ignore[misc]
            assert False, "should have raised"
        except AttributeError:
            pass

    def test_roundtrip_proto(self):
        r = Relationship(
            resource_type="document",
            resource_id="readme",
            resource_relation="viewer",
            subject_type="user",
            subject_id="alice",
            subject_relation="",
            caveat_name="is_weekday",
            caveat_context={"day": "monday"},
        )
        proto = r._to_proto()
        r2 = Relationship._from_proto(proto)
        assert r2.resource_type == r.resource_type
        assert r2.resource_id == r.resource_id
        assert r2.resource_relation == r.resource_relation
        assert r2.subject_type == r.subject_type
        assert r2.subject_id == r.subject_id
        assert r2.subject_relation == r.subject_relation
        assert r2.caveat_name == r.caveat_name
        assert r2.caveat_context == r.caveat_context

    def test_roundtrip_proto_minimal(self):
        r = Relationship.from_triple("document:readme", "viewer", "user:alice")
        proto = r._to_proto()
        r2 = Relationship._from_proto(proto)
        assert r2 == r


class TestFilter:
    def test_basic_filter(self):
        f = Filter(resource_type="document")
        proto = f._to_proto()
        assert proto.resource_type == "document"

    def test_full_filter(self):
        f = Filter(
            resource_type="document",
            resource_id="readme",
            relation="viewer",
            subject_type="user",
            subject_id="alice",
        )
        proto = f._to_proto()
        assert proto.resource_type == "document"
        assert proto.optional_resource_id == "readme"
        assert proto.optional_relation == "viewer"
        assert proto.optional_subject_filter.subject_type == "user"
        assert proto.optional_subject_filter.optional_subject_id == "alice"

    def test_filter_with_subject_relation(self):
        f = Filter(
            resource_type="document",
            subject_type="group",
            subject_relation="member",
        )
        proto = f._to_proto()
        assert proto.optional_subject_filter.optional_relation.relation == "member"


class TestTransaction:
    def test_create(self):
        r = Relationship.from_triple("document:readme", "viewer", "user:alice")
        txn = Transaction()
        txn.create(r)
        assert len(txn._updates) == 1
        assert txn._updates[0].operation == 1  # OPERATION_CREATE

    def test_touch(self):
        r = Relationship.from_triple("document:readme", "viewer", "user:alice")
        txn = Transaction()
        txn.touch(r)
        assert len(txn._updates) == 1
        assert txn._updates[0].operation == 2  # OPERATION_TOUCH

    def test_delete(self):
        r = Relationship.from_triple("document:readme", "viewer", "user:alice")
        txn = Transaction()
        txn.delete(r)
        assert len(txn._updates) == 1
        assert txn._updates[0].operation == 3  # OPERATION_DELETE

    def test_chaining(self):
        r1 = Relationship.from_triple("document:readme", "viewer", "user:alice")
        r2 = Relationship.from_triple("document:readme", "editor", "user:bob")
        txn = Transaction()
        txn.touch(r1).touch(r2)
        assert len(txn._updates) == 2

    def test_preconditions(self):
        f = Filter(resource_type="document", resource_id="readme")
        txn = Transaction()
        txn.must_not_match(f)
        assert len(txn._preconditions) == 1
        assert txn._preconditions[0].operation == 1  # OPERATION_MUST_NOT_MATCH

    def test_must_match(self):
        f = Filter(resource_type="document", resource_id="readme")
        txn = Transaction()
        txn.must_match(f)
        assert len(txn._preconditions) == 1
        assert txn._preconditions[0].operation == 2  # OPERATION_MUST_MATCH


class TestUpdate:
    def _proto_update(self, operation, resource_id="readme"):
        rel = Relationship.from_triple(
            f"document:{resource_id}", "viewer", "user:alice"
        )
        return core_pb2.RelationshipUpdate(
            operation=operation,
            relationship=rel._to_proto(),
        )

    def test_from_proto_create(self):
        proto = self._proto_update(core_pb2.RelationshipUpdate.OPERATION_CREATE)
        update = Update._from_proto(proto)
        assert update.operation == UpdateOperation.CREATE
        assert update.relationship.resource_id == "readme"
        assert update.relationship.subject_id == "alice"

    def test_from_proto_touch(self):
        proto = self._proto_update(core_pb2.RelationshipUpdate.OPERATION_TOUCH)
        update = Update._from_proto(proto)
        assert update.operation == UpdateOperation.TOUCH

    def test_from_proto_delete(self):
        proto = self._proto_update(core_pb2.RelationshipUpdate.OPERATION_DELETE)
        update = Update._from_proto(proto)
        assert update.operation == UpdateOperation.DELETE

    def test_from_proto_unspecified_does_not_raise(self):
        proto = self._proto_update(core_pb2.RelationshipUpdate.OPERATION_UNSPECIFIED)
        update = Update._from_proto(proto)
        assert update.operation == UpdateOperation.UNSPECIFIED

    def test_from_proto_unknown_op_maps_to_unspecified(self):
        # An out-of-range int (not a valid enum value on the wire) must not
        # raise a bare KeyError -- that would kill a live watch() stream with
        # a non-SpiceDBError.
        proto = self._proto_update(core_pb2.RelationshipUpdate.OPERATION_TOUCH)
        proto.operation = 99
        update = Update._from_proto(proto)
        assert update.operation == UpdateOperation.UNSPECIFIED

    def test_frozen(self):
        proto = self._proto_update(core_pb2.RelationshipUpdate.OPERATION_TOUCH)
        update = Update._from_proto(proto)
        try:
            update.operation = UpdateOperation.CREATE  # type: ignore[misc]
            assert False, "should have raised"
        except AttributeError:
            pass

    def test_no_proto_leak(self):
        proto = self._proto_update(core_pb2.RelationshipUpdate.OPERATION_TOUCH)
        update = Update._from_proto(proto)
        assert not isinstance(update.operation, int)
        assert isinstance(update.relationship, Relationship)


class TestPermissionTreeFromProto:
    """Mirrors the Go G3 test coverage in spicedb-go/client/expand_tree_test.go.

    Synthetic shape:
        root: intermediate UNION on document:doc1#view
          - leaf with 2 subjects (one with optional_relation, one without)
          - intermediate INTERSECTION on document:doc1#view
              - leaf with 1 subject
    """

    def _object_ref(self, object_type="document", object_id="doc1"):
        return core_pb2.ObjectReference(object_type=object_type, object_id=object_id)

    def test_nested_tree(self):
        inner_leaf = core_pb2.PermissionRelationshipTree(
            expanded_object=self._object_ref(),
            expanded_relation="view",
            leaf=core_pb2.DirectSubjectSet(
                subjects=[
                    core_pb2.SubjectReference(
                        object=core_pb2.ObjectReference(
                            object_type="user", object_id="carol"
                        ),
                    ),
                ]
            ),
        )

        inner_intermediate = core_pb2.PermissionRelationshipTree(
            expanded_object=self._object_ref(),
            expanded_relation="view",
            intermediate=core_pb2.AlgebraicSubjectSet(
                operation=core_pb2.AlgebraicSubjectSet.OPERATION_INTERSECTION,
                children=[inner_leaf],
            ),
        )

        root_leaf = core_pb2.PermissionRelationshipTree(
            expanded_object=self._object_ref(),
            expanded_relation="view",
            leaf=core_pb2.DirectSubjectSet(
                subjects=[
                    core_pb2.SubjectReference(
                        object=core_pb2.ObjectReference(
                            object_type="user", object_id="alice"
                        ),
                        optional_relation="member",
                    ),
                    core_pb2.SubjectReference(
                        object=core_pb2.ObjectReference(
                            object_type="user", object_id="bob"
                        ),
                    ),
                ]
            ),
        )

        root = core_pb2.PermissionRelationshipTree(
            expanded_object=self._object_ref(),
            expanded_relation="view",
            intermediate=core_pb2.AlgebraicSubjectSet(
                operation=core_pb2.AlgebraicSubjectSet.OPERATION_UNION,
                children=[root_leaf, inner_intermediate],
            ),
        )

        got = _permission_tree_from_proto(root)

        want = PermissionTree(
            expanded_object=ObjectRef(object_type="document", object_id="doc1"),
            expanded_relation="view",
            intermediate=IntermediateNode(
                operation=TreeOperation.UNION,
                children=[
                    PermissionTree(
                        expanded_object=ObjectRef(
                            object_type="document", object_id="doc1"
                        ),
                        expanded_relation="view",
                        leaf=LeafNode(
                            subjects=[
                                SubjectRef(
                                    subject_type="user",
                                    subject_id="alice",
                                    optional_relation="member",
                                ),
                                SubjectRef(
                                    subject_type="user",
                                    subject_id="bob",
                                ),
                            ]
                        ),
                    ),
                    PermissionTree(
                        expanded_object=ObjectRef(
                            object_type="document", object_id="doc1"
                        ),
                        expanded_relation="view",
                        intermediate=IntermediateNode(
                            operation=TreeOperation.INTERSECTION,
                            children=[
                                PermissionTree(
                                    expanded_object=ObjectRef(
                                        object_type="document", object_id="doc1"
                                    ),
                                    expanded_relation="view",
                                    leaf=LeafNode(
                                        subjects=[
                                            SubjectRef(
                                                subject_type="user",
                                                subject_id="carol",
                                            ),
                                        ]
                                    ),
                                ),
                            ],
                        ),
                    ),
                ],
            ),
        )

        assert got == want

    def test_unspecified_operation(self):
        root = core_pb2.PermissionRelationshipTree(
            intermediate=core_pb2.AlgebraicSubjectSet(
                operation=core_pb2.AlgebraicSubjectSet.OPERATION_UNSPECIFIED,
            ),
        )
        got = _permission_tree_from_proto(root)
        assert got.intermediate is not None
        assert got.intermediate.operation == TreeOperation.UNSPECIFIED

    def test_exclusion_operation(self):
        root = core_pb2.PermissionRelationshipTree(
            intermediate=core_pb2.AlgebraicSubjectSet(
                operation=core_pb2.AlgebraicSubjectSet.OPERATION_EXCLUSION,
            ),
        )
        got = _permission_tree_from_proto(root)
        assert got.intermediate is not None
        assert got.intermediate.operation == TreeOperation.EXCLUSION

    def test_frozen(self):
        root = core_pb2.PermissionRelationshipTree(
            expanded_object=self._object_ref(),
            expanded_relation="view",
        )
        tree = _permission_tree_from_proto(root)
        try:
            tree.expanded_relation = "other"  # type: ignore[misc]
            assert False, "should have raised"
        except AttributeError:
            pass


# ── Lookup types / mappers ─────────────────────────────────────────────
#
# Mirrors spicedb-go's lookup_types_test.go (client/lookup_types_test.go):
# permissionshipFromProto, partialCaveatFromProto, resolvedSubjectFromProto.
# Python protobuf submessages are never `None` on attribute access (you get
# a default instance), so — unlike the Go mappers, which take nilable
# pointers — `_partial_caveat_from_proto` takes the *containing* message and
# checks `HasField` to decide presence.


class TestPermissionshipFromProto:
    def test_unspecified(self):
        assert (
            _permissionship_from_proto(
                permission_service_pb2.LOOKUP_PERMISSIONSHIP_UNSPECIFIED
            )
            == Permissionship.UNSPECIFIED
        )

    def test_has_permission(self):
        assert (
            _permissionship_from_proto(
                permission_service_pb2.LOOKUP_PERMISSIONSHIP_HAS_PERMISSION
            )
            == Permissionship.HAS_PERMISSION
        )

    def test_conditional_permission(self):
        assert (
            _permissionship_from_proto(
                permission_service_pb2.LOOKUP_PERMISSIONSHIP_CONDITIONAL_PERMISSION
            )
            == Permissionship.CONDITIONAL_PERMISSION
        )

    def test_unknown_value_maps_to_unspecified(self):
        assert _permissionship_from_proto(99) == Permissionship.UNSPECIFIED


class TestPartialCaveatFromProto:
    def test_unset_field_is_none(self):
        resp = permission_service_pb2.LookupResourcesResponse(resource_object_id="doc1")
        assert _partial_caveat_from_proto(resp) is None

    def test_maps_missing_context(self):
        resp = permission_service_pb2.LookupResourcesResponse(
            resource_object_id="doc1",
            partial_caveat_info=core_pb2.PartialCaveatInfo(
                missing_required_context=["ip_address", "time_of_day"]
            ),
        )
        got = _partial_caveat_from_proto(resp)
        assert got == PartialCaveatInfo(
            missing_required_context=["ip_address", "time_of_day"]
        )


class TestResolvedSubjectFromProto:
    def test_zero_value_is_empty_subject_id(self):
        got = _resolved_subject_from_proto(permission_service_pb2.ResolvedSubject())
        assert got == ResolvedSubject(
            subject_id="",
            permissionship=Permissionship.UNSPECIFIED,
            partial_caveat=None,
        )

    def test_maps_all_fields(self):
        got = _resolved_subject_from_proto(
            permission_service_pb2.ResolvedSubject(
                subject_object_id="*",
                permissionship=permission_service_pb2.LOOKUP_PERMISSIONSHIP_CONDITIONAL_PERMISSION,
                partial_caveat_info=core_pb2.PartialCaveatInfo(
                    missing_required_context=["region"]
                ),
            )
        )
        assert got.subject_id == "*"
        assert got.permissionship == Permissionship.CONDITIONAL_PERMISSION
        assert got.partial_caveat == PartialCaveatInfo(
            missing_required_context=["region"]
        )


# ── Check results / mappers ─────────────────────────────────────────────
#
# Mirrors spicedb-go's client/check_types_test.go. Unlike LookupPermissionship,
# CheckPermissionResponse.Permissionship has a fourth value (NO_PERMISSION),
# since a single check answers a yes/no/conditional question about one
# specific pair rather than streaming only the matches. Lookups never yield
# NO_PERMISSION -- a non-match is simply absent from the stream.


class TestCheckPermissionshipFromProto:
    def test_unspecified(self):
        assert (
            _check_permissionship_from_proto(
                permission_service_pb2.CheckPermissionResponse.PERMISSIONSHIP_UNSPECIFIED
            )
            == Permissionship.UNSPECIFIED
        )

    def test_no_permission(self):
        assert (
            _check_permissionship_from_proto(
                permission_service_pb2.CheckPermissionResponse.PERMISSIONSHIP_NO_PERMISSION
            )
            == Permissionship.NO_PERMISSION
        )

    def test_has_permission(self):
        assert (
            _check_permissionship_from_proto(
                permission_service_pb2.CheckPermissionResponse.PERMISSIONSHIP_HAS_PERMISSION
            )
            == Permissionship.HAS_PERMISSION
        )

    def test_conditional_permission(self):
        assert (
            _check_permissionship_from_proto(
                permission_service_pb2.CheckPermissionResponse.PERMISSIONSHIP_CONDITIONAL_PERMISSION
            )
            == Permissionship.CONDITIONAL_PERMISSION
        )

    def test_unknown_value_maps_to_unspecified(self):
        assert _check_permissionship_from_proto(99) == Permissionship.UNSPECIFIED


class TestCheckResultHasPermission:
    """T1: has_permission must be True ONLY for HAS_PERMISSION -- a
    CONDITIONAL_PERMISSION result is NOT a grant (fail-closed: a caveat the
    server couldn't evaluate must never be treated as satisfied)."""

    @pytest.mark.parametrize(
        "permissionship,expected",
        [
            (Permissionship.UNSPECIFIED, False),
            (Permissionship.NO_PERMISSION, False),
            (Permissionship.HAS_PERMISSION, True),
            (Permissionship.CONDITIONAL_PERMISSION, False),
        ],
    )
    def test_has_permission_true_only_for_has_permission(
        self, permissionship, expected
    ):
        result = CheckResult(
            permissionship=permissionship,
            missing_context=[],
            checked_at="",
        )
        assert result.has_permission is expected

    def test_frozen(self):
        result = CheckResult(
            permissionship=Permissionship.HAS_PERMISSION,
            missing_context=[],
            checked_at="deadbeef",
        )
        try:
            result.permissionship = Permissionship.NO_PERMISSION  # type: ignore[misc]
            assert False, "should have raised"
        except AttributeError:
            pass
