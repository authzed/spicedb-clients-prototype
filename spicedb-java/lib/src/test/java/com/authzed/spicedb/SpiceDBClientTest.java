package com.authzed.spicedb;

import static org.junit.jupiter.api.Assertions.*;

import build.buf.gen.authzed.api.v1.AlgebraicSubjectSet;
import build.buf.gen.authzed.api.v1.DirectSubjectSet;
import build.buf.gen.authzed.api.v1.ObjectReference;
import build.buf.gen.authzed.api.v1.PermissionRelationshipTree;
import build.buf.gen.authzed.api.v1.SubjectReference;
import com.google.protobuf.Value;
import java.time.Instant;
import java.util.Arrays;
import java.util.LinkedHashMap;
import java.util.List;
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

  // toProtoRelationshipPreservesCaveatContextTypes is the regression test for the write-time
  // defect: toProtoRelationship used to stringify every caveatContext value via
  // Object#toString(), including numbers, booleans, null, and nested maps/lists. A caveat like
  // `now < 100` stored against a stringified "50" fails to evaluate, and fails *silently* -- as a
  // conditional result rather than an error. Unlike a bad check-time context (which fails one
  // call), a bad write-time context is persisted: every future check against the relationship
  // mis-evaluates, and re-checking with correct context never repairs it, only rewriting the
  // relationship does. toProtoRelationship must dispatch on type instead, via the same
  // toProtoValue converter the check path already used correctly.
  @Test
  void toProtoRelationshipPreservesCaveatContextTypes() {
    Map<String, Object> context = new LinkedHashMap<>();
    context.put("a_string", "hello");
    context.put("an_int", 42);
    context.put("a_float", 3.5);
    context.put("a_bool", true);
    context.put("a_null", null);
    Map<String, Object> nestedMap = new LinkedHashMap<>();
    nestedMap.put("nested", "value");
    context.put("a_map", nestedMap);
    context.put("a_list", Arrays.asList("one", 2, false));

    Relationship r =
        Relationship.of("document", "doc1", "viewer", "user", "alice")
            .withCaveat("some_caveat", context);
    var proto = SpiceDBClient.toProtoRelationship(r);
    var fields = proto.getOptionalCaveat().getContext().getFieldsMap();

    assertEquals(Value.KindCase.STRING_VALUE, fields.get("a_string").getKindCase());
    assertEquals("hello", fields.get("a_string").getStringValue());

    // google.protobuf.Value.number_value is a double, so an integer legitimately round-trips as
    // a float (42 -> 42.0). That is inherent to the proto, not a defect in this conversion.
    assertEquals(Value.KindCase.NUMBER_VALUE, fields.get("an_int").getKindCase());
    assertEquals(42.0, fields.get("an_int").getNumberValue());

    assertEquals(Value.KindCase.NUMBER_VALUE, fields.get("a_float").getKindCase());
    assertEquals(3.5, fields.get("a_float").getNumberValue());

    assertEquals(Value.KindCase.BOOL_VALUE, fields.get("a_bool").getKindCase());
    assertTrue(fields.get("a_bool").getBoolValue());

    assertEquals(Value.KindCase.NULL_VALUE, fields.get("a_null").getKindCase());

    assertEquals(Value.KindCase.STRUCT_VALUE, fields.get("a_map").getKindCase());
    assertEquals(
        "value", fields.get("a_map").getStructValue().getFieldsMap().get("nested").getStringValue());

    assertEquals(Value.KindCase.LIST_VALUE, fields.get("a_list").getKindCase());
    var listValues = fields.get("a_list").getListValue().getValuesList();
    assertEquals(3, listValues.size());
    assertEquals(Value.KindCase.STRING_VALUE, listValues.get(0).getKindCase());
    assertEquals(Value.KindCase.NUMBER_VALUE, listValues.get(1).getKindCase());
    assertEquals(Value.KindCase.BOOL_VALUE, listValues.get(2).getKindCase());
  }

  // toProtoRelationshipUnrepresentableCaveatContextValueThrows: a value toProtoValue cannot
  // dispatch on any known type/kind case (e.g. a custom class instance) used to fall through to
  // Value.newBuilder().setStringValue(value.toString()), silently stringifying it instead of
  // raising. Caveat context is caller-supplied, so per root DESIGN.md "RULE: A conversion that
  // cannot preserve meaning must fail", clause 1, it must raise a typed error naming the
  // offending key and the unsupported type instead of guessing. Shared converter -- this
  // exercises the write path (toProtoRelationship); CheckContextTest covers the check path.
  private static final class UnrepresentableValue {}

  @Test
  void toProtoRelationshipUnrepresentableCaveatContextValueThrows() {
    Relationship r =
        Relationship.of("document", "doc1", "viewer", "user", "alice")
            .withCaveat("some_caveat", Map.of("bad_key", new UnrepresentableValue()));

    var thrown =
        assertThrows(
            com.authzed.spicedb.errors.InvalidArgumentException.class,
            () -> SpiceDBClient.toProtoRelationship(r));

    assertTrue(thrown.getMessage().contains("bad_key"), thrown.getMessage());
    assertTrue(
        thrown.getMessage().contains(UnrepresentableValue.class.getName()), thrown.getMessage());
  }

  // fromProtoRelationshipPreservesCaveatContextTypes covers the read side, which shares the same
  // toProtoValue/fromProtoValue converters -- a relationship written with a typed caveat context
  // must read back with the same types, not everything collapsed to a String.
  @Test
  void fromProtoRelationshipPreservesCaveatContextTypes() {
    Map<String, Object> context = new LinkedHashMap<>();
    context.put("a_string", "hello");
    context.put("an_int", 42);
    context.put("a_float", 3.5);
    context.put("a_bool", true);
    context.put("a_null", null);
    Map<String, Object> nestedMap = new LinkedHashMap<>();
    nestedMap.put("nested", "value");
    context.put("a_map", nestedMap);
    context.put("a_list", Arrays.asList("one", 2, false));

    Relationship original =
        Relationship.of("document", "doc1", "viewer", "user", "alice")
            .withCaveat("some_caveat", context);
    Relationship restored =
        SpiceDBClient.fromProtoRelationship(SpiceDBClient.toProtoRelationship(original));
    Map<String, Object> rt = restored.caveatContext();

    assertEquals("hello", rt.get("a_string"));
    // 42 -> 42.0: inherent to google.protobuf.Value.number_value being a double, not a defect.
    assertEquals(42.0, rt.get("an_int"));
    assertEquals(3.5, rt.get("a_float"));
    assertEquals(true, rt.get("a_bool"));
    assertTrue(rt.containsKey("a_null"));
    assertNull(rt.get("a_null"));
    assertInstanceOf(Map.class, rt.get("a_map"));
    assertEquals("value", ((Map<?, ?>) rt.get("a_map")).get("nested"));
    assertInstanceOf(List.class, rt.get("a_list"));
    List<?> list = (List<?>) rt.get("a_list");
    assertEquals(List.of("one", 2.0, false), list);
  }

  @Test
  void toProtoRelationshipNeverIncludesCheckContextWhenNoCaveat() {
    // checkContext is CHECK-TIME only; it must never cause a write-time caveat to appear on the
    // proto, even when no caveat was set via withCaveat.
    Relationship r =
        Relationship.of("document", "doc1", "viewer", "user", "alice")
            .withCheckContext(Map.of("secret", "leak-if-present"));
    var proto = SpiceDBClient.toProtoRelationship(r);
    assertFalse(
        proto.hasOptionalCaveat(), "checkContext alone must never produce a write-time caveat");
  }

  @Test
  void toProtoRelationshipNeverIncludesCheckContextWithDisjointCaveatContext() {
    // A relationship carrying BOTH a write-time caveat context and a check-time context with
    // DISJOINT keys -- if toProtoRelationship ever merged them, "secret" would leak into the
    // write-time proto's caveat context and get silently stored in SpiceDB.
    Relationship r =
        Relationship.of("document", "doc1", "viewer", "user", "alice")
            .withCaveat("is_allowed", Map.of("allowed", true))
            .withCheckContext(Map.of("secret", "leak-if-present"));
    var proto = SpiceDBClient.toProtoRelationship(r);
    assertTrue(proto.hasOptionalCaveat());
    assertFalse(
        proto.getOptionalCaveat().getContext().getFieldsMap().containsKey("secret"),
        "check-time context must never leak into the write-time caveat context");
    assertTrue(proto.getOptionalCaveat().getContext().getFieldsMap().containsKey("allowed"));
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
