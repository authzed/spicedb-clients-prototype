"""Unit tests for spicedb.types — no SpiceDB instance needed."""

from datetime import datetime, timezone

from authzed.api.v1 import core_pb2

from spicedb.types import (
    Filter,
    IntermediateNode,
    LeafNode,
    ObjectRef,
    PermissionTree,
    Relationship,
    SubjectRef,
    Transaction,
    TreeOperation,
    Update,
    UpdateOperation,
    _permission_tree_from_proto,
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
