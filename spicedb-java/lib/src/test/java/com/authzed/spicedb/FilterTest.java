package com.authzed.spicedb;

import static org.junit.jupiter.api.Assertions.*;

import org.junit.jupiter.api.Test;

class FilterTest {

  @Test
  void ofCreatesFilter() {
    Filter f = Filter.of("document");
    assertEquals("document", f.resourceType());
    assertEquals("", f.resourceID());
    assertEquals("", f.relation());
    assertEquals("", f.subjectType());
    assertEquals("", f.subjectID());
    assertEquals("", f.subjectRelation());
    assertEquals("", f.resourceIDPrefix());
  }

  @Test
  void ofRejectsNull() {
    assertThrows(NullPointerException.class, () -> Filter.of(null));
  }

  @Test
  void ofRejectsEmpty() {
    assertThrows(IllegalArgumentException.class, () -> Filter.of(""));
  }

  @Test
  void withResourceIDReturnsNewFilter() {
    Filter original = Filter.of("document");
    Filter withID = original.withResourceID("doc1");
    assertEquals("", original.resourceID());
    assertEquals("doc1", withID.resourceID());
    assertEquals("document", withID.resourceType());
  }

  @Test
  void withResourceIDPrefixReturnsNewFilter() {
    Filter f = Filter.of("document").withResourceIDPrefix("doc");
    assertEquals("doc", f.resourceIDPrefix());
  }

  @Test
  void withRelationReturnsNewFilter() {
    Filter f = Filter.of("document").withRelation("viewer");
    assertEquals("viewer", f.relation());
  }

  @Test
  void withSubjectTypeReturnsNewFilter() {
    Filter f = Filter.of("document").withSubjectType("user");
    assertEquals("user", f.subjectType());
  }

  @Test
  void withSubjectIDReturnsNewFilter() {
    Filter f = Filter.of("document").withSubjectType("user").withSubjectID("alice");
    assertEquals("alice", f.subjectID());
  }

  @Test
  void withSubjectRelationReturnsNewFilter() {
    Filter f = Filter.of("document").withSubjectType("group").withSubjectRelation("member");
    assertEquals("member", f.subjectRelation());
  }

  @Test
  void chainingPreservesAllFields() {
    Filter f =
        Filter.of("document")
            .withResourceID("doc1")
            .withRelation("viewer")
            .withSubjectType("user")
            .withSubjectID("alice")
            .withSubjectRelation("member");

    assertEquals("document", f.resourceType());
    assertEquals("doc1", f.resourceID());
    assertEquals("viewer", f.relation());
    assertEquals("user", f.subjectType());
    assertEquals("alice", f.subjectID());
    assertEquals("member", f.subjectRelation());
  }

  @Test
  void immutabilityPreserved() {
    Filter base = Filter.of("document").withRelation("viewer");
    Filter narrowed = base.withSubjectType("user");

    assertEquals("", base.subjectType());
    assertEquals("user", narrowed.subjectType());
    assertEquals("viewer", narrowed.relation());
  }
}
