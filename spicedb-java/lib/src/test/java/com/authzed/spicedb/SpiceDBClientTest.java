package com.authzed.spicedb;

import static org.junit.jupiter.api.Assertions.*;

import build.buf.gen.authzed.api.v1.AlgebraicSubjectSet;
import build.buf.gen.authzed.api.v1.DirectSubjectSet;
import build.buf.gen.authzed.api.v1.ObjectReference;
import build.buf.gen.authzed.api.v1.PermissionRelationshipTree;
import build.buf.gen.authzed.api.v1.SubjectReference;
import java.time.Instant;
import java.util.Map;
import org.junit.jupiter.api.Test;

/**
 * Unit tests for SpiceDBClient helper methods (proto conversion, etc.). Integration tests require a
 * running SpiceDB instance and belong in the examples directory.
 */
class SpiceDBClientTest {

  @Test
  void toProtoRelationshipBasic() {
    Relationship r = Relationship.of("document", "doc1", "viewer", "user", "alice");
    var proto = SpiceDBClient.toProtoRelationship(r);
    assertEquals("document", proto.getResource().getObjectType());
    assertEquals("doc1", proto.getResource().getObjectId());
    assertEquals("viewer", proto.getRelation());
    assertEquals("user", proto.getSubject().getObject().getObjectType());
    assertEquals("alice", proto.getSubject().getObject().getObjectId());
  }

  @Test
  void toProtoRelationshipWithSubjectRelation() {
    Relationship r = Relationship.of("document", "doc1", "viewer", "group", "eng", "member");
    var proto = SpiceDBClient.toProtoRelationship(r);
    assertEquals("member", proto.getSubject().getOptionalRelation());
  }

  @Test
  void toProtoRelationshipWithCaveat() {
    Relationship r =
        Relationship.of("document", "doc1", "viewer", "user", "alice")
            .withCaveat("is_allowed", Map.of("allowed", true));
    var proto = SpiceDBClient.toProtoRelationship(r);
    assertTrue(proto.hasOptionalCaveat());
    assertEquals("is_allowed", proto.getOptionalCaveat().getCaveatName());
  }

  @Test
  void toProtoRelationshipWithExpiration() {
    Instant exp = Instant.parse("2026-12-31T23:59:59Z");
    Relationship r =
        Relationship.of("document", "doc1", "viewer", "user", "alice").withExpiration(exp);
    var proto = SpiceDBClient.toProtoRelationship(r);
    assertTrue(proto.hasOptionalExpiresAt());
    assertEquals(exp.getEpochSecond(), proto.getOptionalExpiresAt().getSeconds());
  }

  @Test
  void fromProtoRelationshipRoundTrip() {
    Relationship original =
        Relationship.of("document", "doc1", "viewer", "user", "alice", "member");
    var proto = SpiceDBClient.toProtoRelationship(original);
    Relationship restored = SpiceDBClient.fromProtoRelationship(proto);
    assertEquals(original.resourceType(), restored.resourceType());
    assertEquals(original.resourceID(), restored.resourceID());
    assertEquals(original.resourceRelation(), restored.resourceRelation());
    assertEquals(original.subjectType(), restored.subjectType());
    assertEquals(original.subjectID(), restored.subjectID());
    assertEquals(original.subjectRelation(), restored.subjectRelation());
  }

  @Test
  void fromProtoRelationshipWithCaveatRoundTrip() {
    Relationship original =
        Relationship.of("document", "doc1", "viewer", "user", "alice")
            .withCaveat("test_caveat", Map.of("key", "value"));
    var proto = SpiceDBClient.toProtoRelationship(original);
    Relationship restored = SpiceDBClient.fromProtoRelationship(proto);
    assertEquals("test_caveat", restored.caveatName());
    assertEquals("value", restored.caveatContext().get("key"));
  }

  @Test
  void fromProtoRelationshipWithExpirationRoundTrip() {
    Instant exp = Instant.parse("2026-06-15T12:00:00Z");
    Relationship original =
        Relationship.of("document", "doc1", "viewer", "user", "alice").withExpiration(exp);
    var proto = SpiceDBClient.toProtoRelationship(original);
    Relationship restored = SpiceDBClient.fromProtoRelationship(proto);
    assertEquals(exp, restored.expiration());
  }

  // -----------------------------------------------------------------------
  // toPermissionTree — recursive mapper from PermissionRelationshipTree
  // -----------------------------------------------------------------------

  private static ObjectReference objRef(String type, String id) {
    return ObjectReference.newBuilder().setObjectType(type).setObjectId(id).build();
  }

  private static SubjectReference subjRef(String type, String id, String optionalRelation) {
    return SubjectReference.newBuilder()
        .setObject(objRef(type, id))
        .setOptionalRelation(optionalRelation)
        .build();
  }

  @Test
  void toPermissionTreeMapsLeafNode() {
    PermissionRelationshipTree proto =
        PermissionRelationshipTree.newBuilder()
            .setExpandedObject(objRef("document", "doc1"))
            .setExpandedRelation("viewer")
            .setLeaf(
                DirectSubjectSet.newBuilder()
                    .addSubjects(subjRef("user", "alice", ""))
                    .addSubjects(subjRef("group", "eng", "member"))
                    .build())
            .build();

    PermissionTree tree = SpiceDBClient.toPermissionTree(proto);

    assertEquals("document", tree.expandedObject().objectType());
    assertEquals("doc1", tree.expandedObject().objectID());
    assertEquals("viewer", tree.expandedRelation());
    assertNull(tree.intermediate());
    assertNotNull(tree.leaf());
    assertEquals(2, tree.leaf().subjects().size());
    assertEquals("user", tree.leaf().subjects().get(0).subjectType());
    assertEquals("alice", tree.leaf().subjects().get(0).subjectID());
    assertEquals("", tree.leaf().subjects().get(0).optionalRelation());
    assertEquals("group", tree.leaf().subjects().get(1).subjectType());
    assertEquals("eng", tree.leaf().subjects().get(1).subjectID());
    assertEquals("member", tree.leaf().subjects().get(1).optionalRelation());
  }

  @Test
  void toPermissionTreeMapsNestedIntermediateNodes() {
    // Nested INTERSECTION intermediate with a single leaf child.
    PermissionRelationshipTree nestedIntersection =
        PermissionRelationshipTree.newBuilder()
            .setExpandedObject(objRef("document", "doc1"))
            .setExpandedRelation("editor")
            .setIntermediate(
                AlgebraicSubjectSet.newBuilder()
                    .setOperation(AlgebraicSubjectSet.Operation.OPERATION_INTERSECTION)
                    .addChildren(
                        PermissionRelationshipTree.newBuilder()
                            .setExpandedObject(objRef("document", "doc1"))
                            .setExpandedRelation("editor")
                            .setLeaf(
                                DirectSubjectSet.newBuilder()
                                    .addSubjects(subjRef("user", "bob", ""))
                                    .build())
                            .build())
                    .build())
            .build();

    // Leaf child of the top-level UNION.
    PermissionRelationshipTree leafChild =
        PermissionRelationshipTree.newBuilder()
            .setExpandedObject(objRef("document", "doc1"))
            .setExpandedRelation("viewer")
            .setLeaf(
                DirectSubjectSet.newBuilder()
                    .addSubjects(subjRef("user", "alice", ""))
                    .addSubjects(subjRef("group", "eng", "member"))
                    .build())
            .build();

    PermissionRelationshipTree root =
        PermissionRelationshipTree.newBuilder()
            .setExpandedObject(objRef("document", "doc1"))
            .setExpandedRelation("view")
            .setIntermediate(
                AlgebraicSubjectSet.newBuilder()
                    .setOperation(AlgebraicSubjectSet.Operation.OPERATION_UNION)
                    .addChildren(leafChild)
                    .addChildren(nestedIntersection)
                    .build())
            .build();

    PermissionTree tree = SpiceDBClient.toPermissionTree(root);

    assertEquals("document", tree.expandedObject().objectType());
    assertEquals("doc1", tree.expandedObject().objectID());
    assertEquals("view", tree.expandedRelation());
    assertNull(tree.leaf());
    assertNotNull(tree.intermediate());
    assertEquals(PermissionTree.Operation.UNION, tree.intermediate().operation());
    assertEquals(2, tree.intermediate().children().size());

    PermissionTree mappedLeafChild = tree.intermediate().children().get(0);
    assertNull(mappedLeafChild.intermediate());
    assertNotNull(mappedLeafChild.leaf());
    assertEquals(2, mappedLeafChild.leaf().subjects().size());
    assertEquals("alice", mappedLeafChild.leaf().subjects().get(0).subjectID());
    assertEquals("member", mappedLeafChild.leaf().subjects().get(1).optionalRelation());

    PermissionTree mappedIntersection = tree.intermediate().children().get(1);
    assertEquals("editor", mappedIntersection.expandedRelation());
    assertNull(mappedIntersection.leaf());
    assertNotNull(mappedIntersection.intermediate());
    assertEquals(
        PermissionTree.Operation.INTERSECTION, mappedIntersection.intermediate().operation());
    assertEquals(1, mappedIntersection.intermediate().children().size());
    assertEquals(
        "bob",
        mappedIntersection.intermediate().children().get(0).leaf().subjects().get(0).subjectID());
  }

  @Test
  void toPermissionTreeMapsUnspecifiedOperation() {
    PermissionRelationshipTree proto =
        PermissionRelationshipTree.newBuilder()
            .setExpandedObject(objRef("document", "doc1"))
            .setExpandedRelation("view")
            .setIntermediate(
                AlgebraicSubjectSet.newBuilder()
                    .setOperation(AlgebraicSubjectSet.Operation.OPERATION_UNSPECIFIED)
                    .build())
            .build();

    PermissionTree tree = SpiceDBClient.toPermissionTree(proto);

    assertEquals(PermissionTree.Operation.UNSPECIFIED, tree.intermediate().operation());
    assertEquals(0, tree.intermediate().children().size());
  }

  @Test
  void toPermissionTreeHandlesNullInput() {
    PermissionTree tree = SpiceDBClient.toPermissionTree(null);
    assertNull(tree.intermediate());
    assertNull(tree.leaf());
    assertEquals("", tree.expandedObject().objectType());
    assertEquals("", tree.expandedObject().objectID());
    assertEquals("", tree.expandedRelation());
  }

  @Test
  void expandResultExposesNoProtoType() {
    for (var component : SpiceDBClient.ExpandResult.class.getRecordComponents()) {
      assertFalse(
          component.getType().getPackageName().startsWith("build.buf.gen"),
          "ExpandResult must not expose proto types, found: " + component.getType());
    }
  }
}
