"""spicedb.protoconv: exported native <-> protocol conversions round-trip."""

from __future__ import annotations

from datetime import datetime, timedelta, timezone

import pytest
from authzed.api.v1 import core_pb2, permission_service_pb2 as psp

from spicedb import (
    Caveat,
    CheckResult,
    ExpandResult,
    Filter,
    InvalidArgumentError,
    LookupResource,
    LookupSubject,
    ObjectRef,
    Permissionship,
    PermissionTree,
    Precondition,
    PreconditionOperation,
    ReadRelationship,
    Relationship,
    ResolvedSubject,
    SchemaResult,
    SubjectFilter,
    SubjectRelationFilter,
    SubjectRef,
    Transaction,
    Update,
    UpdateOperation,
    WriteResult,
    ZedToken,
    at_least,
    full,
    min_latency,
    snapshot,
)
from spicedb import protoconv as pc
from spicedb.types import (
    CaveatEvaluation,
    CaveatResult,
    CheckDebugTrace,
    DebugInformation,
    IntermediateNode,
    LeafNode,
    PartialCaveatInfo,
    PermissionType,
    TreeOperation,
)

REL = Relationship(
    ObjectRef("document", "a"),
    "viewer",
    SubjectRef(ObjectRef("team", "t"), "member"),
    Caveat("ip", {"cidr": "10.0.0.0/8", "n": None}),
    datetime(2030, 1, 1, tzinfo=timezone.utc),
)


def test_relationship_object_subject_round_trip():
    assert pc.relationship_from_proto(pc.relationship_to_proto(REL)) == REL
    assert pc.object_from_proto(pc.object_to_proto(REL.resource)) == REL.resource
    assert pc.subject_from_proto(pc.subject_to_proto(REL.subject)) == REL.subject


@pytest.mark.parametrize(
    "f",
    [
        Filter("document"),
        Filter("document", resource_id="a", relation="viewer"),
        Filter("document", resource_id_prefix="a", subject=SubjectFilter("user")),
        Filter("document", subject=SubjectFilter("user", subject_id="u", relation=SubjectRelationFilter.direct())),
        Filter("document", subject=SubjectFilter("team", relation=SubjectRelationFilter.named("member"))),
    ],
)
def test_filter_round_trip_preserves_relation_scope(f):
    assert pc.filter_from_proto(pc.filter_to_proto(f)) == f


def test_precondition_update_transaction_consistency_token():
    p = Precondition(PreconditionOperation.MUST_NOT_MATCH, Filter("document"))
    assert pc.precondition_from_proto(pc.precondition_to_proto(p)) == p
    u = Update(UpdateOperation.TOUCH, REL)
    assert pc.update_from_proto(pc.update_to_proto(u)) == u
    request = pc.transaction_to_proto(Transaction().must_match(Filter("document")).create(REL), metadata={"k": "v"})
    assert len(request.updates) == 1 and len(request.optional_preconditions) == 1
    assert request.optional_transaction_metadata["k"] == "v"
    for c in (full(), min_latency(), at_least(ZedToken("t")), snapshot(ZedToken("s"))):
        assert pc.consistency_from_proto(pc.consistency_to_proto(c)) == c
    with pytest.raises(InvalidArgumentError):
        pc.consistency_from_proto(psp.Consistency())
    assert pc.zed_token_to_proto(ZedToken("")) is None
    assert pc.zed_token_from_proto(None) == ZedToken("")
    assert pc.zed_token_from_proto(pc.zed_token_to_proto(ZedToken("x"))) == ZedToken("x")
    assert pc.context_from_proto(pc.context_to_proto({"a": [1.5, None]})) == {"a": (1.5, None)}


def test_check_result_with_trace_round_trips():
    sub = CheckDebugTrace(
        REL.resource, "viewer", PermissionType.RELATION, SubjectRef(ObjectRef("user", "u")),
        Permissionship.HAS_PERMISSION, None, None, False, None, None, "op2", "src",
    )
    trace = CheckDebugTrace(
        REL.resource, "view", PermissionType.PERMISSION, SubjectRef(ObjectRef("user", "u")),
        Permissionship.CONDITIONAL_PERMISSION,
        CaveatEvaluation("x > 1", CaveatResult.MISSING_SOME_CONTEXT, {"x": 1.0}, ("y",), "c"),
        timedelta(milliseconds=3), None, (sub,), datetime(2031, 1, 1, tzinfo=timezone.utc), "op1", "src",
    )
    result = CheckResult(
        Permissionship.CONDITIONAL_PERMISSION, ("y",), ZedToken("rev"),
        DebugInformation(trace, "schema"), datetime(2030, 1, 1, tzinfo=timezone.utc),
    )
    assert pc.check_result_from_proto(pc.check_result_to_proto(result)) == result
    assert pc.debug_information_from_proto(pc.debug_information_to_proto(result.debug_trace)) == result.debug_trace


def test_lookup_read_expand_and_write_results():
    lr = LookupResource("a", Permissionship.CONDITIONAL_PERMISSION, PartialCaveatInfo(("x",)), ZedToken("r"), "c1")
    assert pc.lookup_resource_from_proto(pc.lookup_resource_to_proto(lr)) == lr
    ls = LookupSubject(
        ResolvedSubject("*", Permissionship.HAS_PERMISSION),
        (ResolvedSubject("bob", Permissionship.HAS_PERMISSION),),
        ZedToken("r"),
    )
    assert pc.lookup_subject_from_proto(pc.lookup_subject_to_proto(ls)) == ls
    rr = ReadRelationship(REL, "c2", ZedToken("r"))
    proto = pc.read_relationship_to_proto(rr)
    assert (proto.after_result_cursor.token, proto.read_at.token) == ("c2", "r")
    tree = PermissionTree(
        REL.resource, "view",
        intermediate=IntermediateNode(TreeOperation.UNION, (
            PermissionTree(REL.resource, "viewer", leaf=LeafNode((SubjectRef(ObjectRef("user", "u")),))),
        )),
    )
    expanded = pc.expand_result_to_proto(ExpandResult(tree, ZedToken("e")))
    assert pc.permission_tree_from_proto(expanded.tree_root) == tree
    assert pc.write_result_to_proto(WriteResult(ZedToken("w"))).written_at.token == "w"
    assert pc.write_schema_result_to_proto(ZedToken("s")).written_at.token == "s"
    assert pc.read_schema_result_to_proto(SchemaResult("definition u {}", ZedToken("r"))).schema_text == "definition u {}"
    assert isinstance(pc.relationship_to_proto(REL), core_pb2.Relationship)
