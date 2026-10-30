package com.authzed.spicedb;

import static org.junit.jupiter.api.Assertions.*;

import com.authzed.spicedb.errors.ErrorMapper;
import com.authzed.spicedb.errors.InvalidArgumentException;
import com.authzed.spicedb.errors.LocalValidationException;
import com.authzed.spicedb.errors.NotFoundException;
import com.authzed.spicedb.errors.UnavailableException;
import com.authzed.spicedb.testing.InMemorySpiceDB;
import io.grpc.Status;
import java.time.Clock;
import java.time.Instant;
import java.time.ZoneOffset;
import java.util.List;
import java.util.Map;
import java.util.UUID;
import java.util.concurrent.atomic.AtomicInteger;
import org.junit.jupiter.api.Test;

/**
 * The double's check resolution (registered result, then resolver, then stored relationships, then
 * no permission), its lookups over that resolution, and the filter-only delete.
 */
class InMemoryResolutionTest {

  private static final ObjectRef DOC = new ObjectRef("document", "a");
  private static final SubjectRef ALICE = SubjectRef.direct(new ObjectRef("user", "alice"));
  private static final SubjectRef BOB = SubjectRef.direct(new ObjectRef("user", "bob"));

  private static void write(InMemorySpiceDB db, String... tuples) {
    var txn = new Transaction();
    for (var t : tuples) txn.touch(Relationship.fromTuple(t));
    db.write(txn);
  }

  private static Permissionship check(
      InMemorySpiceDB db, ObjectRef resource, String permission, SubjectRef subject) {
    return db.check(new PermissionCheck(resource, permission, subject, Consistency.full()))
        .permissionship();
  }

  private static List<String> resourceIds(InMemorySpiceDB db, String type, SubjectRef subject) {
    return resources(db, type, subject, LookupOptions.builder().build()).stream()
        .map(LookupResult.LookupResource::resourceId)
        .toList();
  }

  private static List<LookupResult.LookupResource> resources(
      InMemorySpiceDB db, String type, SubjectRef subject, LookupOptions options) {
    try (var s = db.lookupResources(Consistency.full(), type, "viewer", subject, options)) {
      return s.toList();
    }
  }

  private static List<String> subjectIds(InMemorySpiceDB db, ObjectRef resource, String type) {
    try (var s = db.lookupSubjects(Consistency.full(), resource, "viewer", type)) {
      return s.map(r -> r.subject().subjectId()).toList();
    }
  }

  @Test
  void storedRelationshipsGrantByExactSubjectAndWildcard() {
    var now = Instant.parse("2030-01-01T00:00:00Z");
    var db = new InMemorySpiceDB(Clock.fixed(now, ZoneOffset.UTC));
    write(db, "document:a#viewer@user:alice", "document:b#viewer@user:*");
    write(db, "document:c#viewer@team:eng#member", "document:d#viewer@user:bob[only_weekdays]");
    var expired = new Transaction();
    expired.touch(
        Relationship.fromTuple("document:e#viewer@user:alice").withExpiration(now.minusSeconds(1)));
    db.write(expired);

    assertEquals(Permissionship.HAS_PERMISSION, check(db, DOC, "viewer", ALICE));
    assertEquals(Permissionship.NO_PERMISSION, check(db, DOC, "viewer", BOB));
    assertEquals(Permissionship.NO_PERMISSION, check(db, DOC, "editor", ALICE), "other relation");
    var b = new ObjectRef("document", "b");
    assertEquals(Permissionship.HAS_PERMISSION, check(db, b, "viewer", BOB), "wildcard");
    var eng = SubjectRef.named(new ObjectRef("team", "eng"), "member");
    var c = new ObjectRef("document", "c");
    assertEquals(Permissionship.HAS_PERMISSION, check(db, c, "viewer", eng), "userset subject");
    assertEquals(
        Permissionship.NO_PERMISSION,
        check(db, c, "viewer", SubjectRef.direct(new ObjectRef("team", "eng"))),
        "a userset does not grant the direct subject");
    var d = new ObjectRef("document", "d");
    assertEquals(Permissionship.CONDITIONAL_PERMISSION, check(db, d, "viewer", BOB), "caveated");
    var e = new ObjectRef("document", "e");
    assertEquals(Permissionship.NO_PERMISSION, check(db, e, "viewer", ALICE), "expired");
  }

  @Test
  void resolutionOrderIsRegisteredThenResolverThenStored() {
    var db = new InMemorySpiceDB();
    write(db, "document:a#viewer@user:alice");
    var asked = new AtomicInteger();
    db.setCheckResolver(
        c -> {
          asked.incrementAndGet();
          if (c.permission().equals("edit")) return result(Permissionship.NO_PERMISSION);
          if (c.subject().equals(BOB)) return result(Permissionship.HAS_PERMISSION);
          return null; // fall through to the stored relationships
        });
    db.setCheckResult(DOC, "viewer", ALICE, Permissionship.NO_PERMISSION);

    assertEquals(Permissionship.NO_PERMISSION, check(db, DOC, "viewer", ALICE), "registered wins");
    assertEquals(0, asked.get(), "the resolver is not consulted for a registered question");
    assertEquals(Permissionship.HAS_PERMISSION, check(db, DOC, "viewer", BOB), "resolver");
    assertEquals(Permissionship.NO_PERMISSION, check(db, DOC, "edit", BOB), "resolver");
    var other = new ObjectRef("document", "z");
    write(db, "document:z#viewer@user:carol");
    var carol = SubjectRef.direct(new ObjectRef("user", "carol"));
    assertEquals(Permissionship.HAS_PERMISSION, check(db, other, "viewer", carol), "stored");

    db.setCheckResolver(null);
    assertEquals(Permissionship.NO_PERMISSION, check(db, DOC, "viewer", BOB), "resolver removed");
  }

  @Test
  void grantAllAnswersGeneratedIdsAndBulkChecks() {
    var db = new InMemorySpiceDB();
    db.setCheckResolver(InMemorySpiceDB.grantAll());
    var id = UUID.randomUUID().toString();
    var doc = new ObjectRef("document", id);
    db.setCheckResult(doc, "delete", ALICE, Permissionship.NO_PERMISSION);
    assertTrue(
        db.check(new PermissionCheck(doc, "view", ALICE, Consistency.full())).hasPermission());
    assertTrue(
        db.checkAny(List.of(new PermissionCheck(doc, "edit", BOB, Consistency.minLatency()))));
    assertFalse(
        db.check(new PermissionCheck(doc, "delete", ALICE, Consistency.full())).hasPermission());
  }

  @Test
  void lookupResourcesReturnsGrantedObjectsOfTheType() {
    var db = new InMemorySpiceDB();
    write(
        db,
        "document:a#viewer@user:alice",
        "document:b#viewer@user:bob",
        "document:c#viewer@user:*",
        "folder:f#viewer@user:alice",
        "document:d#parent@folder:f");
    assertEquals(List.of("a", "c"), resourceIds(db, "document", ALICE));
    assertEquals(List.of("f"), resourceIds(db, "folder", ALICE));

    // A grant-all resolver returns every document the store mentions, including one that only
    // appears in another relation.
    db.setCheckResolver(InMemorySpiceDB.grantAll());
    assertEquals(List.of("a", "b", "c", "d"), resourceIds(db, "document", ALICE));

    // Objects that only appear as subjects are candidates too.
    assertEquals(List.of("f"), resourceIds(db, "folder", BOB));

    var lookups = db.callsOf(ClientOperation.LOOKUP_RESOURCES);
    assertEquals(4, lookups.size());
    assertEquals(
        List.of(Consistency.full(), "document", "viewer", ALICE), lookups.get(0).arguments());
  }

  @Test
  void lookupResourcesHonorsFilterLimitAndCursor() {
    var db = new InMemorySpiceDB();
    write(
        db,
        "document:a#viewer@user:alice",
        "document:b#viewer@user:alice[only_weekdays]",
        "document:c#viewer@user:alice",
        "document:d#viewer@user:alice");
    var all = resources(db, "document", ALICE, LookupOptions.builder().build());
    assertEquals(4, all.size());
    var conditional = all.get(1);
    assertEquals(Permissionship.CONDITIONAL_PERMISSION, conditional.permissionship());
    assertNotNull(conditional.partialCaveat());
    assertNull(all.get(0).partialCaveat());
    assertEquals(InMemorySpiceDB.cursorAfter(1), all.get(0).afterResultCursor());
    assertNotNull(all.get(0).lookedUpAt());

    var onlyFull =
        resources(
            db,
            "document",
            ALICE,
            LookupOptions.builder()
                .permissionshipFilter(LookupPermissionshipFilter.HAS_PERMISSION_ONLY)
                .build());
    assertEquals(
        List.of("a", "c", "d"),
        onlyFull.stream().map(LookupResult.LookupResource::resourceId).toList());
    // A skipped object still advances the cursor.
    assertEquals(InMemorySpiceDB.cursorAfter(3), onlyFull.get(1).afterResultCursor());

    var firstTwo = resources(db, "document", ALICE, LookupOptions.builder().limit(2).build());
    assertEquals(2, firstTwo.size());
    var rest =
        resources(
            db,
            "document",
            ALICE,
            LookupOptions.builder().cursor(firstTwo.get(1).afterResultCursor()).build());
    assertEquals(
        List.of("c", "d"), rest.stream().map(LookupResult.LookupResource::resourceId).toList());

    assertThrows(
        InvalidArgumentException.class,
        () ->
            db.lookupResources(
                Consistency.full(),
                "document",
                "viewer",
                ALICE,
                LookupOptions.builder().subjectRelation("member").build()));
  }

  @Test
  void lookupSubjectsReturnsGrantedSubjectsIncludingWildcard() {
    var db = new InMemorySpiceDB();
    write(
        db,
        "document:a#viewer@user:alice",
        "document:a#viewer@team:eng#member",
        "document:b#viewer@user:*",
        "document:b#editor@user:bob");
    assertEquals(List.of("alice"), subjectIds(db, DOC, "user"));
    var b = new ObjectRef("document", "b");
    // The wildcard grants every user the store mentions, and is itself a result.
    assertEquals(List.of("alice", "*", "bob"), subjectIds(db, b, "user"));
    assertEquals(List.of(), subjectIds(db, DOC, "team"), "the userset is not a direct team");
    try (var s =
        db.lookupSubjects(
            Consistency.full(),
            DOC,
            "viewer",
            "team",
            LookupOptions.builder().subjectRelation("member").build())) {
      assertEquals(List.of("eng"), s.map(r -> r.subject().subjectId()).toList());
    }
    try (var s =
        db.lookupSubjects(
            Consistency.full(), b, "viewer", "user", LookupOptions.builder().limit(1).build())) {
      assertEquals(1, s.count());
    }
    assertThrows(
        InvalidArgumentException.class,
        () ->
            db.lookupSubjects(
                Consistency.full(),
                b,
                "viewer",
                "user",
                LookupOptions.builder()
                    .permissionshipFilter(LookupPermissionshipFilter.HAS_PERMISSION_ONLY)
                    .build()));
  }

  @Test
  void lookupsKeepRecordingAndFaultHooks() {
    var db = new InMemorySpiceDB();
    write(db, "document:a#viewer@user:alice", "document:b#viewer@user:alice");

    db.failNext(
        ClientOperation.LOOKUP_RESOURCES,
        InMemorySpiceDB.serverError(Status.Code.UNAVAILABLE, "down"));
    var failing = db.lookupResources(Consistency.full(), "document", "viewer", ALICE);
    assertEquals(1, db.callsOf(ClientOperation.LOOKUP_RESOURCES).size(), "recorded at the call");
    try (failing) {
      assertThrows(UnavailableException.class, failing::toList, "fails when consumed");
    }

    db.setFaultHook(
        f ->
            f.stage() == InMemorySpiceDB.FaultStage.ITEM && f.item() == 1
                ? InMemorySpiceDB.serverError(Status.Code.UNAVAILABLE, "mid-stream")
                : null);
    try (var s = db.lookupResources(Consistency.full(), "document", "viewer", ALICE)) {
      var it = s.iterator();
      assertEquals("a", it.next().resourceId());
      assertThrows(UnavailableException.class, it::next);
    }
    db.setFaultHook(null);

    db.setCheckError(
        new ObjectRef("document", "b"),
        "viewer",
        ALICE,
        ErrorMapper.newError(Status.Code.NOT_FOUND, "gone"));
    try (var s = db.lookupResources(Consistency.full(), "document", "viewer", ALICE)) {
      var it = s.iterator();
      assertEquals("a", it.next().resourceId());
      assertThrows(NotFoundException.class, it::hasNext, "a registered error fails the stream");
    }
  }

  @Test
  void filterOnlyDeleteIsOneAllOrNothingRequest() {
    var db = new InMemorySpiceDB();
    var txn = new Transaction();
    for (int i = 0; i <= InMemorySpiceDB.DEFAULT_DELETE_LIMIT; i++)
      txn.touch(Relationship.fromTuple("document:d" + i + "#viewer@user:u"));
    db.write(txn);
    write(db, "folder:f#viewer@user:u");

    assertThrows(
        InvalidArgumentException.class, () -> db.deleteRelationships(Filter.of("document")));
    assertEquals(InMemorySpiceDB.DEFAULT_DELETE_LIMIT + 2, db.relationships().size());

    var result = db.deleteRelationships(Filter.of("folder"));
    assertTrue(result.completed());
    assertEquals(1, result.deletedCount());

    var call = db.callsOf(ClientOperation.DELETE_RELATIONSHIPS).get(1);
    assertEquals(List.of(Filter.of("folder")), call.arguments());
    assertEquals(PartialDeletion.DISALLOWED, call.options(DeleteOptions.class).partialDeletion());
  }

  @Test
  void resolverSeesTheCheckAsSent() {
    var db = new InMemorySpiceDB();
    var seen = new java.util.ArrayList<PermissionCheck>();
    db.setCheckResolver(
        c -> {
          seen.add(c);
          return null;
        });
    var request =
        new PermissionCheck(DOC, "view", ALICE, Consistency.minLatency())
            .withContext(Map.of("k", "v"));
    db.check(request);
    assertEquals(List.of(request), seen);

    // The call's default context is merged in; the check's own keys win.
    seen.clear();
    db.check(
        request.withContext(Map.of("k", "item")),
        CheckOptions.builder().context(Map.of("k", "call", "d", "default")).build());
    assertEquals(Map.of("k", "item", "d", "default"), seen.get(0).context());

    // Lookups pass their context option to the resolver.
    write(db, "document:a#viewer@user:alice");
    seen.clear();
    try (var s =
        db.lookupResources(
            Consistency.full(),
            "document",
            "viewer",
            ALICE,
            LookupOptions.builder().context(Map.of("ip", "10.0.0.1")).build())) {
      s.toList();
    }
    assertEquals(Map.of("ip", "10.0.0.1"), seen.get(0).context());
  }

  @Test
  void resolverResultCarriesMissingContextIntoLookups() {
    var db = new InMemorySpiceDB();
    write(db, "document:a#viewer@user:alice", "document:b#viewer@user:alice");
    db.setCheckResolver(
        c ->
            c.resource().objectId().equals("a")
                ? new CheckResult(
                    Permissionship.CONDITIONAL_PERMISSION, List.of("ip"), null, null, null)
                : null);
    var check = db.check(new PermissionCheck(DOC, "viewer", ALICE, Consistency.full()));
    assertEquals(Permissionship.CONDITIONAL_PERMISSION, check.permissionship());
    assertEquals(List.of("ip"), check.missingContext());
    var found = resources(db, "document", ALICE, LookupOptions.builder().build());
    assertEquals(List.of("ip"), found.get(0).partialCaveat().missingRequiredContext());
    assertEquals(Permissionship.HAS_PERMISSION, found.get(1).permissionship());
  }

  @Test
  void lookupCursorCountsGrantedResults() {
    var db = new InMemorySpiceDB();
    write(
        db,
        "document:a#viewer@user:alice",
        "document:x#viewer@user:bob",
        "document:c#viewer@user:alice");
    var found = resources(db, "document", ALICE, LookupOptions.builder().build());
    assertEquals(InMemorySpiceDB.cursorAfter(1), found.get(0).afterResultCursor());
    assertEquals(InMemorySpiceDB.cursorAfter(2), found.get(1).afterResultCursor());
    var rest =
        resources(
            db,
            "document",
            ALICE,
            LookupOptions.builder().cursor(InMemorySpiceDB.cursorAfter(1)).build());
    assertEquals("c", rest.get(0).resourceId());
  }

  /** A real server never sends an unset permissionship, so the double never produces one. */
  @Test
  void neverProducesAnUnsetPermissionship() {
    var db = new InMemorySpiceDB();
    assertThrows(
        LocalValidationException.class,
        () -> db.setCheckResult(DOC, "view", ALICE, Permissionship.UNSPECIFIED));
    assertThrows(LocalValidationException.class, () -> db.setCheckResult(DOC, "view", ALICE, null));
    write(db, "document:a#viewer@user:alice");
    db.setCheckResolver(c -> result(Permissionship.UNSPECIFIED));
    var check = new PermissionCheck(DOC, "viewer", ALICE, Consistency.full());
    assertThrows(IllegalStateException.class, () -> db.check(check));
    assertThrows(IllegalStateException.class, () -> db.checkBulk(List.of(check)));
    assertThrows(
        IllegalStateException.class,
        () -> resources(db, "document", ALICE, LookupOptions.builder().build()));
    assertThrows(
        IllegalStateException.class,
        () -> {
          try (var s = db.lookupSubjects(Consistency.full(), DOC, "viewer", "user")) {
            s.toList();
          }
        });
  }

  private static CheckResult result(Permissionship p) {
    return new CheckResult(p, List.of(), null, null, null);
  }
}
