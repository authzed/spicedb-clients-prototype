package com.authzed.spicedb;

import static org.junit.jupiter.api.Assertions.*;

import java.time.Instant;
import java.util.Map;
import org.junit.jupiter.api.Test;

class RelationshipTest {

  @Test
  void ofCreatesRelationship() {
    Relationship r =
        Relationship.of(
            new ObjectRef("document", "doc1"),
            "viewer",
            SubjectRef.direct(new ObjectRef("user", "alice")));
    assertEquals("document", r.resource().objectType());
    assertEquals("doc1", r.resource().objectId());
    assertEquals("viewer", r.relation());
    assertEquals("user", r.subject().object().objectType());
    assertEquals("alice", r.subject().object().objectId());
    assertNull(r.subject().relation());
    assertNull(r.caveatName());
    assertNull(r.caveatContext());
    assertNull(r.expiration());
    assertFalse(
        java.util.Arrays.stream(Relationship.class.getRecordComponents())
            .anyMatch(c -> c.getName().equals("checkContext")));
  }

  @Test
  void ofWithoutSubjectRelation() {
    Relationship r =
        Relationship.of(
            new ObjectRef("document", "doc1"),
            "viewer",
            SubjectRef.direct(new ObjectRef("user", "alice")));
    assertNull(r.subject().relation());
  }

  @Test
  void ofRejectsEmptyResourceType() {
    assertThrows(
        com.authzed.spicedb.errors.InvalidArgumentException.class,
        () ->
            Relationship.of(
                new ObjectRef("", "doc1"),
                "viewer",
                SubjectRef.direct(new ObjectRef("user", "alice"))));
  }

  @Test
  void ofRejectsNullResourceID() {
    assertThrows(
        com.authzed.spicedb.errors.InvalidArgumentException.class,
        () ->
            Relationship.of(
                new ObjectRef("document", null),
                "viewer",
                SubjectRef.direct(new ObjectRef("user", "alice"))));
  }

  @Test
  void ofRejectsEmptySubjectType() {
    assertThrows(
        com.authzed.spicedb.errors.InvalidArgumentException.class,
        () ->
            Relationship.of(
                new ObjectRef("document", "doc1"),
                "viewer",
                SubjectRef.direct(new ObjectRef("", "alice"))));
  }

  @Test
  void ofRejectsNullSubjectID() {
    assertThrows(
        com.authzed.spicedb.errors.InvalidArgumentException.class,
        () ->
            Relationship.of(
                new ObjectRef("document", "doc1"),
                "viewer",
                SubjectRef.direct(new ObjectRef("user", null))));
  }

  @Test
  void fromTupleBasic() {
    Relationship r = Relationship.fromTuple("document:doc1#viewer@user:alice");
    assertEquals("document", r.resource().objectType());
    assertEquals("doc1", r.resource().objectId());
    assertEquals("viewer", r.relation());
    assertEquals("user", r.subject().object().objectType());
    assertEquals("alice", r.subject().object().objectId());
    assertNull(r.subject().relation());
  }

  @Test
  void fromTupleWithSubjectRelation() {
    Relationship r = Relationship.fromTuple("document:doc1#viewer@group:eng#member");
    assertEquals("group", r.subject().object().objectType());
    assertEquals("eng", r.subject().object().objectId());
    assertEquals("member", r.subject().relation());
  }

  @Test
  void fromTupleRejectsMissingAt() {
    assertThrows(
        com.authzed.spicedb.errors.InvalidArgumentException.class,
        () -> Relationship.fromTuple("document:doc1#viewer"));
  }

  @Test
  void fromTupleRejectsMissingHash() {
    assertThrows(
        com.authzed.spicedb.errors.InvalidArgumentException.class,
        () -> Relationship.fromTuple("document:doc1@user:alice"));
  }

  @Test
  void toStringBasic() {
    Relationship r =
        Relationship.of(
            new ObjectRef("document", "doc1"),
            "viewer",
            SubjectRef.direct(new ObjectRef("user", "alice")));
    assertEquals("document:doc1#viewer@user:alice", r.toString());
  }

  @Test
  void toStringWithSubjectRelation() {
    Relationship r =
        Relationship.of(
            new ObjectRef("document", "doc1"),
            "viewer",
            SubjectRef.named(new ObjectRef("group", "eng"), "member"));
    assertEquals("document:doc1#viewer@group:eng#member", r.toString());
  }

  @Test
  void withCaveatReturnsNewInstance() {
    Relationship original =
        Relationship.of(
            new ObjectRef("document", "doc1"),
            "viewer",
            SubjectRef.direct(new ObjectRef("user", "alice")));
    Map<String, Object> ctx = Map.of("allowed", true);
    Relationship withCaveat = original.withCaveat("is_allowed", ctx);

    assertNull(original.caveatName());
    assertEquals("is_allowed", withCaveat.caveatName());
    assertEquals(ctx, withCaveat.caveatContext());
    // Original fields preserved
    assertEquals("document", withCaveat.resource().objectType());
  }

  @Test
  void withExpirationReturnsNewInstance() {
    Relationship original =
        Relationship.of(
            new ObjectRef("document", "doc1"),
            "viewer",
            SubjectRef.direct(new ObjectRef("user", "alice")));
    Instant exp = Instant.parse("2026-12-31T23:59:59Z");
    Relationship withExp = original.withExpiration(exp);

    assertNull(original.expiration());
    assertEquals(exp, withExp.expiration());
  }

  @Test
  void withCheckContextReturnsNewInstance() {
    Relationship original =
        Relationship.of(
            new ObjectRef("document", "doc1"),
            "viewer",
            SubjectRef.direct(new ObjectRef("user", "alice")));
    Map<String, Object> ctx = Map.of("now", 42);
    PermissionCheck withCheckContext = TestRequests.withContext(original, ctx);

    assertNull(TestRequests.check(original, "view", Consistency.full()).context());
    assertEquals(ctx, withCheckContext.context());
    // Original fields preserved
    assertEquals("document", withCheckContext.resource().objectType());
  }

  @Test
  void checkContextIsDistinctFromCaveatContext() {
    // Same relationship, DISJOINT keys in each context -- proves the two fields are independent,
    // not aliases of one another. caveatContext is write-time (stored WITH the relationship);
    // checkContext is check-time only (supplied fresh on each check call, never written).
    Relationship r =
        Relationship.of(
                new ObjectRef("document", "doc1"),
                "viewer",
                SubjectRef.direct(new ObjectRef("user", "alice")))
            .withCaveat("is_allowed", Map.of("write_time_key", "w"));

    assertEquals(Map.of("write_time_key", "w"), r.caveatContext());
    var request = TestRequests.withContext(r, Map.of("check_time_key", "c"));
    assertEquals(Map.of("check_time_key", "c"), request.context());
  }

  @Test
  void toFilterCreatesMatchingFilter() {
    Relationship r =
        Relationship.of(
            new ObjectRef("document", "doc1"),
            "viewer",
            SubjectRef.direct(new ObjectRef("user", "alice")));
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
