package com.authzed.spicedb;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

import com.authzed.spicedb.errors.AlreadyExistsException;
import com.authzed.spicedb.errors.ErrorMapper;
import com.authzed.spicedb.errors.FailedPreconditionException;
import com.authzed.spicedb.errors.InvalidArgumentException;
import com.authzed.spicedb.errors.NotFoundException;
import com.authzed.spicedb.testing.InMemorySpiceDB;
import io.grpc.Status;
import java.lang.reflect.Method;
import java.time.Clock;
import java.time.Instant;
import java.time.ZoneOffset;
import java.util.Arrays;
import java.util.List;
import org.junit.jupiter.api.Test;

/** N2 test double: same surface as the client, server write/read/delete semantics. */
class InMemorySpiceDBTest {

  private static Relationship rel(String t) {
    return Relationship.fromTuple(t);
  }

  /** Code written against the shared interface works with either implementation. */
  private static int countViewers(PermissionsApi api) {
    try (var s =
        api.readRelationships(Consistency.full(), Filter.of("document").withRelation("viewer"))) {
      return (int) s.count();
    }
  }

  @Test
  void writesHonorPreconditionsCreateConflictsAndAtomicity() {
    var db = new InMemorySpiceDB();
    var txn = new Transaction();
    txn.create(rel("document:a#viewer@user:alice"));
    txn.touch(rel("document:b#viewer@team:eng#member"));
    assertTrue(db.write(txn).revision().value().startsWith("inmemory-"));
    assertEquals(2, countViewers(db));

    var dup = new Transaction();
    dup.touch(rel("document:c#viewer@user:carol"));
    dup.create(rel("document:a#viewer@user:alice"));
    assertThrows(AlreadyExistsException.class, () -> db.write(dup));
    assertEquals(2, countViewers(db), "a failed transaction applies nothing");

    var guarded = new Transaction();
    guarded.mustNotMatch(Filter.of("document").withResourceId("a"));
    guarded.delete(rel("document:a#viewer@user:alice"));
    var e = assertThrows(FailedPreconditionException.class, () -> db.write(guarded));
    assertEquals(Status.Code.FAILED_PRECONDITION, Status.fromThrowable(e).getCode());

    var twice = new Transaction();
    twice.touch(rel("document:z#viewer@user:z"));
    twice.delete(rel("document:z#viewer@user:z"));
    assertThrows(InvalidArgumentException.class, () -> db.write(twice));
  }

  @Test
  void readsAndDeletesUseServerFilterSemantics() {
    var now = Instant.parse("2030-01-01T00:00:00Z");
    var db = new InMemorySpiceDB(Clock.fixed(now, ZoneOffset.UTC));
    var txn = new Transaction();
    txn.touch(rel("document:a#viewer@user:alice"));
    txn.touch(rel("document:a#viewer@team:eng#member"));
    txn.touch(rel("document:b#viewer@user:bob").withExpiration(now.minusSeconds(1)));
    db.write(txn);
    assertEquals(2, countViewers(db), "expired relationships are invisible");

    try (var s =
        db.readRelationships(
            Consistency.full(),
            Filter.of("document")
                .withSubjectType("team")
                .withSubjectRelation(SubjectRelationFilter.direct()))) {
      assertEquals(0, s.count());
    }
    List<ReadRelationship> page;
    try (var s =
        db.readRelationships(
            Consistency.full(), Filter.of("document"), ReadOptions.builder().limit(1).build())) {
      page = s.toList();
    }
    try (var s =
        db.readRelationships(
            Consistency.full(),
            Filter.of("document"),
            ReadOptions.builder().cursor(page.get(0).afterResultCursor()).build())) {
      assertEquals("document:a#viewer@team:eng#member", s.toList().get(0).toString());
    }

    var allOrNothing = DeleteOptions.builder().partialDeletion(PartialDeletion.DISALLOWED).build();
    // SpiceDB rejects an over-limit all-or-nothing delete with INVALID_ARGUMENT (verified live in
    // the ToolingEscapeHatchesTest example).
    assertThrows(
        InvalidArgumentException.class,
        () -> db.deleteRelationships(Filter.of("document"), 1, allOrNothing));
    var partial = db.deleteRelationships(Filter.of("document"), 1);
    assertEquals(1, partial.deletedCount());
    assertFalse(partial.completed());
    assertTrue(db.deleteRelationships(Filter.of("document"), allOrNothing).completed());
    assertEquals(0, countViewers(db));
  }

  @Test
  void checksReturnRegisteredResultsAndErrors() {
    var db = new InMemorySpiceDB();
    var doc = new ObjectRef("document", "a");
    var alice = SubjectRef.direct(new ObjectRef("user", "alice"));
    var bob = SubjectRef.direct(new ObjectRef("user", "bob"));
    db.setCheckResult(doc, "view", alice, Permissionship.HAS_PERMISSION);
    db.setCheckError(doc, "view", bob, ErrorMapper.newError(Status.Code.NOT_FOUND, "missing"));
    var a = new PermissionCheck(doc, "view", alice, Consistency.full());
    var b = new PermissionCheck(doc, "view", bob, Consistency.full());
    var c = new PermissionCheck(doc, "edit", alice, Consistency.full());
    assertTrue(db.check(a).hasPermission());
    assertEquals(Permissionship.NO_PERMISSION, db.check(c).permissionship());
    assertThrows(NotFoundException.class, () -> db.check(b));
    assertThrows(NotFoundException.class, () -> db.checkBulk(List.of(a, b)));
    var results =
        db.checkBulk(
            List.of(a, b, c),
            CheckOptions.builder().bulkItemErrors(BulkItemErrors.RETURNED).build());
    assertTrue(results.get(0).hasPermission());
    assertEquals("missing", results.get(1).error().serverMessage());
    assertFalse(results.get(2).hasPermission());
  }

  @Test
  void surfaceMatchesTheClient() throws Exception {
    // Every PermissionsApi method exists on both, with identical signatures (the interface is
    // the compile-time proof; this guards against the interface drifting from the client).
    for (Method m : PermissionsApi.class.getMethods()) {
      var onClient = SpiceDBClient.class.getMethod(m.getName(), m.getParameterTypes());
      var onDouble = InMemorySpiceDB.class.getMethod(m.getName(), m.getParameterTypes());
      assertEquals(onClient.getGenericReturnType(), onDouble.getGenericReturnType(), m.toString());
    }
    var clientOps =
        Arrays.stream(SpiceDBClient.class.getMethods())
            .filter(
                m ->
                    List.of(
                            "write",
                            "readRelationships",
                            "deleteRelationships",
                            "check",
                            "checkBulk")
                        .contains(m.getName()))
            .count();
    assertEquals(PermissionsApi.class.getMethods().length, clientOps);
  }
}
