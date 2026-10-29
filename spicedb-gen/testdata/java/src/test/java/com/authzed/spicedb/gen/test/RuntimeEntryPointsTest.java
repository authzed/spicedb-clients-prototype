package com.authzed.spicedb.gen.test;

import static com.authzed.spicedb.gen.test.Permissions.*;
import static org.junit.jupiter.api.Assertions.*;

import com.authzed.spicedb.ClientOperation;
import com.authzed.spicedb.Consistency;
import com.authzed.spicedb.LookupOptions;
import com.authzed.spicedb.LookupResult.LookupResource;
import com.authzed.spicedb.LookupResult.LookupSubject;
import com.authzed.spicedb.Relationship;
import com.authzed.spicedb.SpiceDBClient;
import com.authzed.spicedb.errors.InvalidArgumentException;
import com.authzed.spicedb.testing.InMemorySpiceDB;
import java.time.Instant;
import java.util.List;
import java.util.Map;
import java.util.stream.Stream;
import org.junit.jupiter.api.Test;

/**
 * String-keyed entry points: objects, subject sets and relationships from names and tuple text,
 * checks and lookups in both directions by name, each validated against the schema. The last test
 * needs a server (SPICEDB_ENDPOINT, default localhost:50051).
 */
class RuntimeEntryPointsTest {

  private static <T> List<T> drain(Stream<T> stream) {
    try (stream) {
      return stream.toList();
    }
  }

  @Test
  void objectsFromNamesAndText() {
    assertEquals(Document("readme"), objectByName("document", "readme"));
    assertEquals(Organization("acme"), parseObject("organization:acme"));
    DefinitionRef any = parseObject("user:alice");
    assertEquals("user", any.definitionName());
    assertThrows(NotInSchemaException.class, () -> objectByName("folder", "x"));
    assertThrows(NotInSchemaException.class, () -> parseObject("folder:x"));
    assertThrows(InvalidArgumentException.class, () -> objectByName("document", ""));
    for (var bad : List.of("document", "document:", ":x")) {
      assertThrows(InvalidArgumentException.class, () -> parseObject(bad), bad);
    }
  }

  @Test
  void subjectSetsFromNames() {
    assertEquals(Team("eng").member(), subjectSetByName("team", "eng", "member"));
    assertEquals(
        Organization("acme").canAdminSubject(),
        subjectSetByName("organization", "acme", "can_admin"));
    assertEquals(
        Document("d").viewer(Team("eng").member()),
        Document("d").relationshipByName("viewer", subjectSetByName("team", "eng", "member")));
    assertThrows(NotInSchemaException.class, () -> subjectSetByName("folder", "x", "member"));
    assertThrows(NotInSchemaException.class, () -> subjectSetByName("team", "x", "owner"));
    var e =
        assertThrows(NotInSchemaException.class, () -> subjectSetByName("document", "x", "viewer"));
    assertTrue(e.getMessage().contains("not a subject anywhere"), e.getMessage());
    assertThrows(InvalidArgumentException.class, () -> subjectSetByName("team", "", "member"));
  }

  @Test
  void relationshipsFromTupleText() {
    assertEquals(
        Document("readme").viewer(User("alice")),
        relationshipFromTriple("document:readme", "viewer", "user:alice"));
    assertEquals(
        Document("readme").viewer(Team("eng").member()),
        relationshipFromTriple("document:readme", "viewer", "team:eng#member"));
    assertEquals(
        Organization("acme").creator(UserWildcard()),
        relationshipFromTriple("organization:acme", "creator", "user:*"));
    assertEquals(
        Document("readme").editor(User("bob")),
        relationshipFromTriple("document:readme", "editor", "user:bob#..."));
    var at = Instant.parse("2030-01-02T03:04:05Z");
    assertEquals(
        Invitation("i1").inviter(User("alice").withExpiration(at)),
        relationshipFromTriple("invitation:i1", "inviter", "user:alice", null, null, at));
    assertEquals(
        Relationship.of(
                new com.authzed.spicedb.ObjectRef("document", "readme"),
                "viewer",
                com.authzed.spicedb.SubjectRef.direct(
                    new com.authzed.spicedb.ObjectRef("user", "alice")))
            .withCaveat("ip_range", Map.of("allowed_cidr", "10.0.0.0/8")),
        relationshipFromTriple(
                "document:readme",
                "viewer",
                "user:alice",
                "ip_range",
                Map.of("allowed_cidr", "10.0.0.0/8"),
                null)
            .toRelationship());

    assertThrows(
        NotInSchemaException.class, () -> relationshipFromTriple("folder:x", "viewer", "user:a"));
    assertThrows(
        NotInSchemaException.class, () -> relationshipFromTriple("document:x", "viewer", "robot:r"));
    assertThrows(
        NotInSchemaException.class, () -> relationshipFromTriple("document:x", "view", "user:a"));
    for (var bad :
        List.of(
            new String[] {"document:x", "editor", "team:eng#member"},
            new String[] {"document:x", "viewer", "user:*"},
            new String[] {"organization:x", "creator", "user:alice"},
            new String[] {"invitation:x", "inviter", "user:alice"})) {
      assertThrows(
          SubjectNotAllowedException.class,
          () -> relationshipFromTriple(bad[0], bad[1], bad[2]),
          String.join(" ", bad));
    }
    assertThrows(
        SubjectNotAllowedException.class,
        () -> relationshipFromTriple("document:x", "editor", "user:a", null, null, at));
    assertThrows(
        SubjectNotAllowedException.class,
        () -> relationshipFromTriple("document:x", "editor", "user:a", "ip_range", null, null));
    assertThrows(
        InvalidArgumentException.class,
        () -> relationshipFromTriple("document", "viewer", "user:alice"));
    assertThrows(
        InvalidArgumentException.class, () -> relationshipFromTriple("document:x", "viewer", "a"));
    assertThrows(
        InvalidArgumentException.class,
        () -> relationshipFromTriple("document:x", "viewer", "team:eng#"));
  }

  @Test
  void checksByName() {
    var db = new InMemorySpiceDB();
    try (var typed = new TypedClient(db)) {
      typed.touch(List.of(Document("readme").viewer(User("alice"))));
      var c = Consistency.full();
      assertTrue(typed.checkByName(c, "document", "readme", "viewer", User("alice")).hasPermission());
      assertFalse(typed.checkByName(c, "document", "readme", "viewer", User("bob")).hasPermission());
      var results =
          typed.checkBulkByName(
              c,
              List.of(
                  new NamedCheck("document", "readme", "viewer", User("alice")),
                  new NamedCheck("document", "readme", "viewer", User("bob"))));
      assertEquals(
          List.of(true, false), results.stream().map(r -> r.hasPermission()).toList());
      var calls = db.calls().size();

      assertThrows(
          NotInSchemaException.class,
          () -> typed.checkByName(c, "document", "readme", "share", User("alice")));
      assertThrows(
          SubjectNotAllowedException.class,
          () -> typed.checkByName(c, "document", "readme", "view", Organization("o")));
      assertThrows(
          SubjectNotAllowedException.class,
          () -> typed.checkByName(c, "organization", "o", "view", UserWildcard()));
      assertThrows(
          NotInSchemaException.class,
          () ->
              typed.checkBulkByName(
                  c,
                  List.of(
                      new NamedCheck("document", "readme", "view", User("alice")),
                      new NamedCheck("document", "readme", "share", User("alice")))));
      assertEquals(calls, db.calls().size(), "invalid names fail before any call");
    }
  }

  @Test
  void lookupsByNameValidateAndShortCircuit() {
    var db = new InMemorySpiceDB();
    try (var typed = new TypedClient(db)) {
      typed.touch(List.of(Document("readme").viewer(User("alice"))));
      var c = Consistency.full();
      assertEquals(
          List.of("readme"),
          drain(typed.lookupResources(c, "document", "viewer", User("alice")))
              .stream()
              .map(LookupResource::resourceId)
              .toList());
      assertEquals(List.of(), drain(typed.lookupResources(c, "document", "view", Team("eng"))));
      assertEquals(1, db.callsOf(ClientOperation.LOOKUP_RESOURCES).size());

      assertEquals(
          List.of("alice"),
          drain(typed.lookupSubjects(c, "document", "readme", "viewer", "user")).stream()
              .map(s -> s.subject().subjectId())
              .toList());
      drain(typed.lookupSubjects(c, "document", "readme", "viewer", "user", "..."));
      assertEquals(2, db.callsOf(ClientOperation.LOOKUP_SUBJECTS).size());
      for (var st : List.of(new String[] {"organization", null}, new String[] {"team", null})) {
        assertEquals(
            List.<LookupSubject>of(),
            drain(typed.lookupSubjects(c, "document", "readme", "view", st[0], st[1])));
      }
      assertEquals(
          List.<LookupSubject>of(),
          drain(typed.lookupSubjects(c, "document", "readme", "view", "organization", "member")));
      assertEquals(2, db.callsOf(ClientOperation.LOOKUP_SUBJECTS).size());

      assertThrows(
          NotInSchemaException.class,
          () -> typed.lookupSubjects(c, "folder", "readme", "view", "user"));
      assertThrows(
          NotInSchemaException.class,
          () -> typed.lookupSubjects(c, "document", "readme", "share", "user"));
      assertThrows(
          NotInSchemaException.class,
          () -> typed.lookupSubjects(c, "document", "readme", "view", "folder"));
      assertThrows(
          NotInSchemaException.class,
          () -> typed.lookupSubjects(c, "document", "readme", "view", "team", "owner"));
      assertThrows(
          InvalidArgumentException.class,
          () ->
              typed.lookupSubjects(
                  c,
                  "document",
                  "readme",
                  "view",
                  "team",
                  "member",
                  LookupOptions.builder().subjectRelation("other").build()));
      assertEquals(2, db.callsOf(ClientOperation.LOOKUP_SUBJECTS).size());
    }
  }

  @Test
  void runtimeEntryPointsAgainstServer() {
    var endpoint = System.getenv().getOrDefault("SPICEDB_ENDPOINT", "localhost:50051");
    try (var raw = SpiceDBClient.createPlaintext(endpoint, "somerandomkeyhere");
        var tc = new TypedClient(raw)) {
      tc.writeSchema(Permissions.SCHEMA);
      var c = Consistency.full();
      var alice = parseObject("user:rn-alice");
      var eng = subjectSetByName("team", "rn-eng", "member");
      tc.touch(
          List.of(
              relationshipFromTriple("document:rn-doc", "viewer", "team:rn-eng#member"),
              objectByName("team", "rn-eng").relationshipByName("member", alice)));

      assertTrue(tc.checkByName(c, "document", "rn-doc", "view", alice).hasPermission());
      assertEquals(
          List.of(true, false),
          tc
              .checkBulkByName(
                  c,
                  List.of(
                      new NamedCheck("document", "rn-doc", "viewer", eng),
                      new NamedCheck("document", "rn-doc", "edit", alice)))
              .stream()
              .map(r -> r.hasPermission())
              .toList());
      assertEquals(
          List.of("rn-doc"),
          drain(tc.lookupResources(c, "document", "viewer", eng)).stream()
              .map(LookupResource::resourceId)
              .toList());
      assertEquals(
          List.of("rn-eng"),
          drain(tc.lookupSubjects(c, "document", "rn-doc", "viewer", "team", "member")).stream()
              .map(s -> s.subject().subjectId())
              .distinct() // SpiceDB may report a subject once per path
              .toList());
      assertEquals(
          List.of("rn-alice"),
          drain(tc.lookupSubjects(c, "document", "rn-doc", "view", "user")).stream()
              .map(s -> s.subject().subjectId())
              .toList());
      assertEquals(
          List.of(), drain(tc.lookupSubjects(c, "document", "rn-doc", "view", "organization")));
      assertEquals(
          List.of(), drain(tc.lookupResources(c, "document", "view", Organization("rn-org"))));
    }
  }
}
