package com.authzed.spicedb;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertTrue;

import com.authzed.spicedb.protoconv.ProtoConv;
import java.time.Duration;
import java.time.Instant;
import java.util.List;
import java.util.Map;
import org.junit.jupiter.api.Test;

/** Exported native↔protocol conversions round-trip. */
class ProtoConvTest {

  @Test
  void relationshipsFiltersPreconditionsAndConsistencyRoundTrip() {
    var r =
        Relationship.fromTuple("document:d#viewer@team:eng#member")
            .withCaveat("c1", Map.of("k", List.of(1.0, "x")))
            .withExpiration(Instant.parse("2030-01-02T03:04:05.5Z"));
    assertEquals(r, ProtoConv.fromProto(ProtoConv.toProto(r)));

    var f =
        Filter.of("document")
            .withResourceIdPrefix("d")
            .withRelation("viewer")
            .withSubjectType("team")
            .withSubjectRelation(SubjectRelationFilter.named("member"));
    assertEquals(f, ProtoConv.fromProto(ProtoConv.toProto(f)));
    var direct =
        Filter.of("document")
            .withSubjectType("user")
            .withSubjectRelation(SubjectRelationFilter.direct());
    assertEquals(direct, ProtoConv.fromProto(ProtoConv.toProto(direct)));

    var p = new Transaction.Precondition(Transaction.PreconditionOperation.MUST_MATCH, f);
    assertEquals(p, ProtoConv.fromProto(ProtoConv.toProto(p)));

    for (var c :
        List.of(
            Consistency.full(),
            Consistency.minLatency(),
            Consistency.atLeast(new ZedToken("a")),
            Consistency.snapshot(new ZedToken("s")))) {
      assertEquals(c, ProtoConv.fromProto(ProtoConv.toProto(c)));
      assertEquals(c.toProto(), ProtoConv.toProto(c));
    }

    var txn = new Transaction();
    txn.mustNotMatch(f);
    txn.create(r);
    var req = ProtoConv.toProto(txn);
    assertEquals(1, req.getUpdatesCount());
    assertEquals(1, req.getOptionalPreconditionsCount());
  }

  @Test
  void resultsConvertToTheirResponseShapesIncludingTraces() {
    var trace =
        new DebugInformation(
            "schema",
            new DebugInformation.CheckTrace(
                new ObjectRef("document", "d"),
                "view",
                DebugInformation.PermissionType.PERMISSION,
                SubjectRef.direct(new ObjectRef("user", "u")),
                Permissionship.HAS_PERMISSION,
                new DebugInformation.CaveatEvaluation(
                    "a == 1", DebugInformation.CaveatResult.TRUE, Map.of("a", 1.0), List.of(), "c"),
                Duration.ofMillis(3),
                new DebugInformation.Subproblems(
                    List.of(
                        new DebugInformation.CheckTrace(
                            new ObjectRef("document", "d"),
                            "viewer",
                            DebugInformation.PermissionType.RELATION,
                            SubjectRef.direct(new ObjectRef("user", "u")),
                            Permissionship.HAS_PERMISSION,
                            null,
                            null,
                            new DebugInformation.Cached(false),
                            null,
                            "op",
                            "src"))),
                null,
                "op1",
                "spicedb:dispatch"));
    assertEquals(trace, ProtoConv.fromProto(ProtoConv.toProto(trace)));

    var result =
        new CheckResult(
            Permissionship.CONDITIONAL_PERMISSION, List.of("ip"), new ZedToken("r"), trace, null);
    var proto = ProtoConv.toProto(result);
    assertEquals("r", proto.getCheckedAt().getToken());
    assertEquals(List.of("ip"), proto.getPartialCaveatInfo().getMissingRequiredContextList());
    assertTrue(proto.hasDebugTrace());

    var lr =
        new LookupResult.LookupResource(
            "d", Permissionship.HAS_PERMISSION, null, new ZedToken("r"), "cur");
    assertEquals(lr, ProtoConv.fromProto(ProtoConv.toProto(lr)));
    var ls =
        new LookupResult.LookupSubject(
            new LookupResult.ResolvedSubject("*", Permissionship.HAS_PERMISSION, null),
            List.of(new LookupResult.ResolvedSubject("bob", Permissionship.HAS_PERMISSION, null)),
            new ZedToken("r"));
    assertEquals(ls, ProtoConv.fromProto(ProtoConv.toProto(ls)));
    var rr =
        new ReadRelationship(
            Relationship.fromTuple("document:d#viewer@user:u"), "c", new ZedToken("r"));
    assertEquals(rr, ProtoConv.fromProto(ProtoConv.toProto(rr)));
    var tree =
        new PermissionTree(
            new PermissionTree.ObjectRef("document", "d"),
            "view",
            new PermissionTree.IntermediateNode(
                PermissionTree.Operation.UNION,
                List.of(
                    new PermissionTree(
                        new PermissionTree.ObjectRef("document", "d"),
                        "viewer",
                        null,
                        new PermissionTree.LeafNode(
                            List.of(new PermissionTree.SubjectRef("user", "u", "")))))),
            null);
    assertEquals(tree, ProtoConv.fromProto(ProtoConv.toProto(tree)));
    assertEquals(
        "w", ProtoConv.toProto(new WriteResult(new ZedToken("w"))).getWrittenAt().getToken());
  }
}
