package com.authzed.spicedb;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertNotEquals;
import static org.junit.jupiter.api.Assertions.assertNull;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

import com.authzed.spicedb.errors.InvalidArgumentException;
import java.time.Instant;
import java.util.List;
import java.util.Map;
import org.junit.jupiter.api.Test;

/** Filter matching, string/JSON forms and size, tuples and copy helpers, accessors. */
class NativeValuesTest {

  private static Relationship rel(String tuple) {
    return Relationship.fromTuple(tuple);
  }

  @Test
  void filterMatchesWithServerSemantics() {
    var direct = rel("document:readme#viewer@user:alice");
    var userset = rel("document:readme#viewer@team:eng#member");
    var other = rel("folder:readme#viewer@user:alice");

    assertTrue(Filter.of("document").matches(direct));
    assertFalse(Filter.of("document").matches(other));
    assertTrue(Filter.of("document").withResourceId("readme").matches(direct));
    assertFalse(Filter.of("document").withResourceId("read").matches(direct));
    assertTrue(Filter.of("document").withResourceIdPrefix("read").matches(direct));
    assertFalse(Filter.of("document").withRelation("editor").matches(direct));
    assertTrue(
        Filter.of("document").withSubjectType("user").withSubjectId("alice").matches(direct));
    assertFalse(Filter.of("document").withSubjectType("user").withSubjectId("bob").matches(direct));
    // Any (default) matches both direct and userset subjects.
    assertTrue(Filter.of("document").withSubjectType("team").matches(userset));
    // Direct excludes usersets; Named requires that relation.
    var directOnly =
        Filter.of("document")
            .withSubjectType("team")
            .withSubjectRelation(SubjectRelationFilter.direct());
    assertFalse(directOnly.matches(userset));
    assertTrue(
        Filter.of("document")
            .withSubjectType("user")
            .withSubjectRelation(SubjectRelationFilter.direct())
            .matches(direct));
    assertTrue(
        Filter.of("document")
            .withSubjectType("team")
            .withSubjectRelation(SubjectRelationFilter.named("member"))
            .matches(userset));
    assertFalse(
        Filter.of("document")
            .withSubjectType("team")
            .withSubjectRelation(SubjectRelationFilter.named("admin"))
            .matches(userset));
    // A relationship's exact filter matches it regardless of caveat/expiration.
    var caveated = direct.withCaveat("only", Map.of("x", 1)).withExpiration(Instant.EPOCH);
    assertTrue(direct.toFilter().matches(caveated));
  }

  @Test
  void valuesHaveReadableAndStructuredForms() {
    var r = rel("document:readme#viewer@team:eng#member");
    assertEquals("document:readme", r.resource().toString());
    assertEquals("team:eng#member", r.subject().toString());
    assertEquals(
        "{\"resourceType\":\"document\",\"resourceId\":\"readme\",\"relation\":\"viewer\","
            + "\"subjectType\":\"team\",\"subjectId\":\"eng\",\"subjectRelation\":\"member\"}",
        r.toJson());

    var f =
        Filter.of("document")
            .withResourceIdPrefix("re")
            .withSubjectType("user")
            .withSubjectRelation(SubjectRelationFilter.direct());
    assertEquals("document:re%*#*@user:*#...", f.toString());
    assertEquals(
        "{\"resourceType\":\"document\",\"resourceIdPrefix\":\"re\",\"subjectType\":\"user\","
            + "\"subjectRelation\":\"...\"}",
        f.toJson());

    var txn = new Transaction();
    assertEquals(0, txn.size());
    txn.mustMatch(Filter.of("document").withResourceId("readme"));
    txn.touch(r);
    txn.delete(rel("document:readme#viewer@user:bob"));
    assertEquals(2, txn.size());
    assertEquals(
        "[MUST_MATCH(document:readme#*@*), TOUCH(document:readme#viewer@team:eng#member),"
            + " DELETE(document:readme#viewer@user:bob)]",
        txn.toString());
    assertTrue(txn.toJson().contains("\"operation\":\"TOUCH\""), txn.toJson());
    assertTrue(txn.toJson().contains("\"operation\":\"MUST_MATCH\""), txn.toJson());
    assertNotEquals("{}", txn.mutations().get(0).toJson());
    assertEquals("atLeast(tok)", Consistency.atLeast(new ZedToken("tok")).toString());
  }

  @Test
  void shortAccessorsAndCopyHelpers() {
    var r = rel("document:readme#viewer@team:eng#member");
    assertEquals("document", r.resourceType());
    assertEquals("readme", r.resourceId());
    assertEquals("team", r.subjectType());
    assertEquals("eng", r.subjectId());
    assertEquals("member", r.subjectRelation());
    assertNull(rel("document:readme#viewer@user:alice").subjectRelation());

    var moved = r.withResource(new ObjectRef("document", "other"));
    assertEquals("document:other#viewer@team:eng#member", moved.toString());
    assertEquals(
        "document:readme#viewer@user:bob",
        r.withSubject(SubjectRef.direct(new ObjectRef("user", "bob"))).toString());
    assertEquals("document:readme#editor@team:eng#member", r.withRelation("editor").toString());
    var caveated = r.withCaveat("c1", Map.of("k", "v"));
    assertEquals("c1", caveated.withResource(new ObjectRef("a", "b")).caveatName());
  }

  /**
   * Inputs and the canonical form SpiceDB's own parser and formatter ({@code pkg/tuple}) produce
   * for them, recorded by running SpiceDB's {@code tuple.Parse} + {@code tuple.String} on each
   * input (the vectors spicedb-gen/tuplecompat uses for Go).
   */
  private static final List<String[]> SPICEDB_TUPLES =
      List.of(
          new String[] {"document:readme#viewer@user:alice", "document:readme#viewer@user:alice"},
          new String[] {
            "document:readme#viewer@team:eng#member", "document:readme#viewer@team:eng#member"
          },
          new String[] {"document:readme#viewer@user:*", "document:readme#viewer@user:*"},
          new String[] {
            "document:readme#viewer@user:alice[only_name]",
            "document:readme#viewer@user:alice[only_name]"
          },
          new String[] {
            "document:readme#viewer@user:alice[ip_range:{\"cidr\":\"10.0.0.0/8\",\"n\":1.5,"
                + "\"list\":[\"a\",\"]\"],\"nested\":{\"b\":true}}]",
            "document:readme#viewer@user:alice[ip_range:{\"cidr\":\"10.0.0.0/8\", \"list\":[\"a\","
                + " \"]\"], \"n\":1.5, \"nested\":{\"b\":true}}]"
          },
          new String[] {
            "document:readme#viewer@user:alice[expiration:2030-01-02T03:04:05Z]",
            "document:readme#viewer@user:alice[expiration:2030-01-02T03:04:05Z]"
          },
          new String[] {
            "document:readme#viewer@user:alice[expiration:2030-01-02T03:04:05.123456789Z]",
            "document:readme#viewer@user:alice[expiration:2030-01-02T03:04:05.123456789Z]"
          },
          new String[] {
            "document:readme#viewer@user:alice[expiration:2030-01-02T03:04:05.5Z]",
            "document:readme#viewer@user:alice[expiration:2030-01-02T03:04:05.5Z]"
          },
          new String[] {
            "document:readme#viewer@team:eng#member[ip_range:{\"cidr\":\"x\"}]"
                + "[expiration:2030-01-02T03:04:05Z]",
            "document:readme#viewer@team:eng#member[ip_range:{\"cidr\":\"x\"}]"
                + "[expiration:2030-01-02T03:04:05Z]"
          },
          new String[] {"document:readme#viewer@team:eng#...", "document:readme#viewer@team:eng"});

  @Test
  void tupleSyntaxMatchesSpiceDBsParserAndFormatter() {
    for (var c : SPICEDB_TUPLES) {
      var parsed = Relationship.fromTuple(c[0]);
      assertEquals(c[1], parsed.toString(), c[0]);
      assertEquals(parsed, Relationship.fromTuple(parsed.toString()), "round trip " + c[1]);
    }
    var r =
        Relationship.fromTuple(
            "document:readme#viewer@team:eng#member[ip_range:{\"cidr\":\"x\",\"n\":2}]"
                + "[expiration:2030-01-02T03:04:05Z]");
    assertEquals("ip_range", r.caveatName());
    assertEquals(Map.of("cidr", "x", "n", 2.0), r.caveatContext());
    assertEquals(Instant.parse("2030-01-02T03:04:05Z"), r.expiration());
    assertEquals("member", r.subjectRelation());
  }

  @Test
  void tupleParsingRejectsWhatSpiceDBRejects() {
    for (var bad :
        List.of(
            "document:readme#viewer",
            "document:readme#viewer@user:alice[",
            "document:readme#viewer@user:alice[expiration:nope]",
            "document:readme#viewer@user:alice[c:{]",
            "document:readme#viewer@user:alice[c:[1]]",
            "documentreadme#viewer@user:alice")) {
      assertThrows(InvalidArgumentException.class, () -> Relationship.fromTuple(bad), bad);
    }
  }
}
