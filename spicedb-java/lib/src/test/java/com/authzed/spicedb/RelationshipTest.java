package com.authzed.spicedb;

import static org.junit.jupiter.api.Assertions.*;

import java.time.Instant;
import java.util.Map;
import org.junit.jupiter.api.Test;

class RelationshipTest {

  @Test
  void ofCreatesRelationship() {
    Relationship r = Relationship.of("document", "doc1", "viewer", "user", "alice", "");
    assertEquals("document", r.resourceType());
    assertEquals("doc1", r.resourceID());
    assertEquals("viewer", r.resourceRelation());
    assertEquals("user", r.subjectType());
    assertEquals("alice", r.subjectID());
    assertEquals("", r.subjectRelation());
    assertNull(r.caveatName());
    assertNull(r.caveatContext());
    assertNull(r.expiration());
  }

  @Test
  void ofWithoutSubjectRelation() {
    Relationship r = Relationship.of("document", "doc1", "viewer", "user", "alice");
    assertEquals("", r.subjectRelation());
  }

  @Test
  void ofRejectsEmptyResourceType() {
    assertThrows(
        IllegalArgumentException.class,
        () -> Relationship.of("", "doc1", "viewer", "user", "alice"));
  }

  @Test
  void ofRejectsNullResourceID() {
    assertThrows(
        IllegalArgumentException.class,
        () -> Relationship.of("document", null, "viewer", "user", "alice"));
  }

  @Test
  void ofRejectsEmptySubjectType() {
    assertThrows(
        IllegalArgumentException.class,
        () -> Relationship.of("document", "doc1", "viewer", "", "alice"));
  }

  @Test
  void ofRejectsNullSubjectID() {
    assertThrows(
        IllegalArgumentException.class,
        () -> Relationship.of("document", "doc1", "viewer", "user", null));
  }

  @Test
  void fromTupleBasic() {
    Relationship r = Relationship.fromTuple("document:doc1#viewer@user:alice");
    assertEquals("document", r.resourceType());
    assertEquals("doc1", r.resourceID());
    assertEquals("viewer", r.resourceRelation());
    assertEquals("user", r.subjectType());
    assertEquals("alice", r.subjectID());
    assertEquals("", r.subjectRelation());
  }

  @Test
  void fromTupleWithSubjectRelation() {
    Relationship r = Relationship.fromTuple("document:doc1#viewer@group:eng#member");
    assertEquals("group", r.subjectType());
    assertEquals("eng", r.subjectID());
    assertEquals("member", r.subjectRelation());
  }

  @Test
  void fromTupleRejectsMissingAt() {
    assertThrows(
        IllegalArgumentException.class, () -> Relationship.fromTuple("document:doc1#viewer"));
  }

  @Test
  void fromTupleRejectsMissingHash() {
    assertThrows(
        IllegalArgumentException.class, () -> Relationship.fromTuple("document:doc1@user:alice"));
  }

  @Test
  void toStringBasic() {
    Relationship r = Relationship.of("document", "doc1", "viewer", "user", "alice");
    assertEquals("document:doc1#viewer@user:alice", r.toString());
  }

  @Test
  void toStringWithSubjectRelation() {
    Relationship r = Relationship.of("document", "doc1", "viewer", "group", "eng", "member");
    assertEquals("document:doc1#viewer@group:eng#member", r.toString());
  }

  @Test
  void withCaveatReturnsNewInstance() {
    Relationship original = Relationship.of("document", "doc1", "viewer", "user", "alice");
    Map<String, Object> ctx = Map.of("allowed", true);
    Relationship withCaveat = original.withCaveat("is_allowed", ctx);

    assertNull(original.caveatName());
    assertEquals("is_allowed", withCaveat.caveatName());
    assertEquals(ctx, withCaveat.caveatContext());
    // Original fields preserved
    assertEquals("document", withCaveat.resourceType());
  }

  @Test
  void withExpirationReturnsNewInstance() {
    Relationship original = Relationship.of("document", "doc1", "viewer", "user", "alice");
    Instant exp = Instant.parse("2026-12-31T23:59:59Z");
    Relationship withExp = original.withExpiration(exp);

    assertNull(original.expiration());
    assertEquals(exp, withExp.expiration());
  }

  @Test
  void toFilterCreatesMatchingFilter() {
    Relationship r = Relationship.of("document", "doc1", "viewer", "user", "alice");
    Filter f = r.toFilter();
    assertEquals("document", f.resourceType());
    assertEquals("doc1", f.resourceID());
    assertEquals("viewer", f.relation());
    assertEquals("user", f.subjectType());
    assertEquals("alice", f.subjectID());
  }

  @Test
  void roundTripTupleString() {
    String tuple = "document:doc1#viewer@user:alice";
    Relationship r = Relationship.fromTuple(tuple);
    assertEquals(tuple, r.toString());
  }

  @Test
  void roundTripTupleStringWithSubjectRelation() {
    String tuple = "document:doc1#viewer@group:eng#member";
    Relationship r = Relationship.fromTuple(tuple);
    assertEquals(tuple, r.toString());
  }
}
