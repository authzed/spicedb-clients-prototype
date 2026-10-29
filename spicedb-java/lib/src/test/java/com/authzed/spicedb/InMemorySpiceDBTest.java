package com.authzed.spicedb;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertNotNull;
import static org.junit.jupiter.api.Assertions.assertNull;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

import com.authzed.spicedb.errors.AlreadyExistsException;
import com.authzed.spicedb.errors.ErrorMapper;
import com.authzed.spicedb.errors.FailedPreconditionException;
import com.authzed.spicedb.errors.InvalidArgumentException;
import com.authzed.spicedb.errors.NotFoundException;
import com.authzed.spicedb.errors.SpiceDBException;
import com.authzed.spicedb.errors.UnavailableException;
import com.authzed.spicedb.testing.InMemorySpiceDB;
import io.grpc.Metadata;
import io.grpc.Status;
import java.lang.reflect.Method;
import java.time.Clock;
import java.time.Instant;
import java.time.ZoneOffset;
import java.util.Arrays;
import java.util.List;
import org.junit.jupiter.api.Test;

/** Test double: same surface as the client, server write/read/delete semantics. */
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
                            "checkBulk",
                            "importRelationships",
                            "exportRelationships",
                            "readSchema",
                            "writeSchema",
                            "reflectSchema")
                        .contains(m.getName()))
            .count();
    assertEquals(PermissionsApi.class.getMethods().length, clientOps);
  }

  private static String signature(Method m) {
    return m.getName()
        + Arrays.stream(m.getGenericParameterTypes())
            .map(java.lang.reflect.Type::getTypeName)
            .toList()
        + " -> "
        + m.getGenericReturnType().getTypeName();
  }

  @Test
  void fullInterfaceMatchesTheClientsMethodSet() {
    // SpiceDBApi lists exactly the client's public instance methods, so the double, which
    // implements it, covers the whole client.
    var clientMethods =
        Arrays.stream(SpiceDBClient.class.getMethods())
            .filter(m -> !java.lang.reflect.Modifier.isStatic(m.getModifiers()))
            .filter(m -> m.getDeclaringClass() != Object.class)
            .map(InMemorySpiceDBTest::signature)
            .collect(java.util.stream.Collectors.toCollection(java.util.TreeSet::new));
    var interfaceMethods =
        Arrays.stream(SpiceDBApi.class.getMethods())
            .map(InMemorySpiceDBTest::signature)
            .collect(java.util.stream.Collectors.toCollection(java.util.TreeSet::new));
    // AutoCloseable.close() (throws Exception) is narrowed by SpiceDBApi.close(); both appear.
    interfaceMethods.remove("close[] -> void");
    interfaceMethods.add("close[] -> void");
    assertEquals(clientMethods, interfaceMethods);
    assertTrue(SpiceDBApi.class.isAssignableFrom(SpiceDBClient.class));
    assertTrue(SpiceDBApi.class.isAssignableFrom(InMemorySpiceDB.class));
  }

  /** Code written against the full interface accepts the double. */
  private static long countAll(SpiceDBApi api) {
    try (api;
        var s = api.readRelationships(Consistency.full(), Filter.of("document"))) {
      return s.count();
    }
  }

  @Test
  void operationsWithoutInMemorySemanticsAreRecordedAndUnimplemented() {
    var db = new InMemorySpiceDB();
    var doc = new ObjectRef("document", "a");
    var alice = SubjectRef.direct(new ObjectRef("user", "alice"));

    var e =
        assertThrows(
            SpiceDBException.class, () -> db.expandPermissionTree(Consistency.full(), doc, "view"));
    assertEquals(Status.Code.UNIMPLEMENTED, e.code());
    assertEquals(Status.Code.UNIMPLEMENTED, Status.fromThrowable(e).getCode());
    assertEquals("not implemented by InMemorySpiceDB", e.serverMessage());
    var expand = db.callsOf(ClientOperation.EXPAND_PERMISSION_TREE).get(0);
    assertEquals(List.of(Consistency.full(), doc, "view"), expand.arguments());

    // Streams fail where the real client reports a failed call: when consumed.
    var watch = db.updates(List.of("document"), null);
    assertEquals(1, db.callsOf(ClientOperation.WATCH).size());
    try (var s = watch) {
      assertEquals(
          Status.Code.UNIMPLEMENTED, assertThrows(SpiceDBException.class, s::toList).code());
    }

    // The fault hook runs first, so tests can script these operations' failures.
    db.failNext(
        ClientOperation.LOOKUP_SUBJECTS,
        InMemorySpiceDB.serverError(Status.Code.UNAVAILABLE, "down"));
    try (var s = db.lookupSubjects(Consistency.full(), doc, "view", "user")) {
      assertThrows(UnavailableException.class, s::toList);
    }
    db.failNext(
        ClientOperation.COUNT_RELATIONSHIPS,
        InMemorySpiceDB.serverError(Status.Code.NOT_FOUND, "no counter"));
    assertThrows(NotFoundException.class, () -> db.experimentalCountRelationships("c"));

    // A retry opt-in on a never-idempotent operation is refused, as the client refuses it.
    var retry =
        RetryPolicy.builder().maxRetries(1).initialBackoff(java.time.Duration.ofMillis(1)).build();
    assertThrows(
        InvalidArgumentException.class,
        () ->
            db.experimentalRegisterRelationshipCounter(
                "c", Filter.of("document"), CallOptions.builder().retryPolicy(retry).build()));

    assertNull(db.rawChannel());
    db.close();
  }

  @Test
  void deleteAllCheckAnyAndCheckAllWorkInMemory() {
    var db = new InMemorySpiceDB();
    db.importRelationships(users(4));
    var folder = new Transaction();
    folder.touch(rel("folder:f#viewer@user:u0"));
    db.write(folder);
    var guarded = DeleteAllOptions.builder().mustMatch(List.of(Filter.of("missing"))).build();
    assertThrows(
        FailedPreconditionException.class,
        () -> db.deleteAllRelationshipsNonAtomic(Filter.of("document"), guarded));
    assertEquals(4, countViewers(db), "a failed precondition deletes nothing");

    var progress = new java.util.ArrayList<SpiceDBClient.DeleteResult>();
    var result =
        db.deleteAllRelationshipsNonAtomic(
            Filter.of("document"), DeleteAllOptions.builder().onProgress(progress::add).build());
    assertEquals(4, result.deletedCount());
    assertTrue(result.completed());
    assertEquals(List.of(result), progress);
    assertEquals(1, db.relationships().size(), "other types are untouched");
    assertEquals(0, countAll(db));
    assertEquals(2, db.callsOf(ClientOperation.DELETE_ALL_RELATIONSHIPS_NON_ATOMIC).size());

    var doc = new ObjectRef("document", "a");
    var alice = SubjectRef.direct(new ObjectRef("user", "alice"));
    db.setCheckResult(doc, "view", alice, Permissionship.HAS_PERMISSION);
    var yes = new PermissionCheck(doc, "view", alice, Consistency.full());
    var no = new PermissionCheck(doc, "edit", alice, Consistency.full());
    assertTrue(db.checkAny(List.of(no, yes)));
    assertFalse(db.checkAll(List.of(no, yes)));
    assertTrue(db.checkAll(List.of(yes)));
    assertFalse(db.checkAll(List.of()));
    assertEquals(4, db.callsOf(ClientOperation.CHECK_BULK).size(), "recorded as bulk checks");
  }

  @Test
  void schemaReadsCanOmitTheRevision() {
    var db = new InMemorySpiceDB();
    db.writeSchema("definition user {}");
    assertNotNull(db.readSchema().revision());
    db.setReadSchemaRevision(InMemorySpiceDB.SchemaRevision.OMITTED);
    var read = db.readSchema();
    assertEquals("definition user {}", read.schema());
    assertNull(read.revision());
    db.setReadSchemaRevision(InMemorySpiceDB.SchemaRevision.INCLUDED);
    assertNotNull(db.readSchema().revision());
  }

  @Test
  void importBatchFaultsSeeTheBatchContents() {
    var db = new InMemorySpiceDB();
    var batches = new java.util.ArrayList<List<Relationship>>();
    db.setFaultHook(
        f -> {
          if (f.stage() == InMemorySpiceDB.FaultStage.IMPORT_BATCH) {
            assertEquals(batches.size(), f.item());
            batches.add(f.batch());
          } else assertTrue(f.batch().isEmpty());
          return null;
        });
    var input = users(7);
    db.importRelationships(input, ImportOptions.builder().batchSize(3).build());
    assertEquals(List.of(input.subList(0, 3), input.subList(3, 6), input.subList(6, 7)), batches);
  }

  @Test
  void cursorAfterMatchesTheCursorOnTheNthItem() {
    var db = new InMemorySpiceDB();
    db.importRelationships(users(5));
    List<ReadRelationship> all;
    try (var s = db.readRelationships(Consistency.full(), Filter.of("document"))) {
      all = s.toList();
    }
    for (int n = 1; n <= all.size(); n++)
      assertEquals(InMemorySpiceDB.cursorAfter(n), all.get(n - 1).afterResultCursor());
    try (var s =
        db.exportRelationships(
            Consistency.full(),
            null,
            ExportOptions.builder().cursor(InMemorySpiceDB.cursorAfter(3)).build())) {
      var rest = s.toList();
      assertEquals(2, rest.size());
      assertEquals(all.get(3).relationship(), rest.get(0).relationship());
      assertEquals(InMemorySpiceDB.cursorAfter(4), rest.get(0).afterResultCursor());
    }
    assertEquals("inmemory-cursor-0", InMemorySpiceDB.cursorAfter(0));
    assertThrows(InvalidArgumentException.class, () -> InMemorySpiceDB.cursorAfter(-1));
  }

  @Test
  void errorsMatchTheRealClientsShape() {
    var db = new InMemorySpiceDB();
    var guarded = new Transaction();
    guarded.mustMatch(Filter.of("org").withRelation("owner"));
    var e = assertThrows(FailedPreconditionException.class, () -> db.write(guarded));
    assertTrue(e.getMessage().startsWith("unable to satisfy write precondition `"));
    assertEquals(e.getMessage(), e.serverMessage(), "server errors keep SpiceDB's message");

    // Local validation looks like the client's: a LocalValidationException whose verbatim
    // message is its own.
    var local =
        assertThrows(
            com.authzed.spicedb.errors.LocalValidationException.class,
            () -> db.deleteRelationships(Filter.of("d"), 0));
    assertEquals(local.getMessage(), local.serverMessage());

    // A per-call retry opt-in on a non-idempotent mutation is refused, as the client does.
    var retry =
        RetryPolicy.builder().maxRetries(1).initialBackoff(java.time.Duration.ofMillis(1)).build();
    var create = new Transaction();
    create.create(rel("document:a#viewer@user:alice"));
    assertThrows(
        InvalidArgumentException.class,
        () -> db.write(create, WriteOptions.builder().retryPolicy(retry).build()));
    var touch = new Transaction();
    touch.touch(rel("document:a#viewer@user:alice"));
    db.write(touch, WriteOptions.builder().retryPolicy(retry).build());

    // An empty write checks preconditions and returns the current revision.
    var before = db.write(new Transaction()).revision();
    assertEquals(before, db.write(new Transaction()).revision());
    var emptyGuarded = new Transaction();
    emptyGuarded.mustNotMatch(Filter.of("document"));
    assertThrows(FailedPreconditionException.class, () -> db.write(emptyGuarded));
  }

  @Test
  void importsExportsAndSchemaOperations() {
    var db = new InMemorySpiceDB();
    var tuples =
        List.of(
            "document:a#viewer@user:x",
            "document:b#viewer@user:x",
            "document:c#viewer@user:x",
            "folder:f#viewer@user:x");
    assertEquals(4, db.importRelationships(tuples.stream().map(Relationship::fromTuple).toList()));
    assertThrows(
        AlreadyExistsException.class,
        () ->
            db.importRelationships(
                List.of(rel("document:z#viewer@user:x"), rel("document:a#viewer@user:x"))));
    assertEquals(4, db.relationships().size(), "a failed import adds nothing");

    List<ExportedRelationship> first;
    try (var s =
        db.exportRelationships(
            Consistency.full(), Filter.of("document"), ExportOptions.builder().limit(2).build())) {
      first = s.toList();
    }
    assertEquals(2, first.size());
    try (var s =
        db.exportRelationships(
            Consistency.full(),
            Filter.of("document"),
            ExportOptions.builder().cursor(first.get(1).afterResultCursor()).build())) {
      assertEquals(List.of("document:c#viewer@user:x"), s.map(Object::toString).toList());
    }
    try (var s = db.exportRelationships(Consistency.full(), null)) {
      assertEquals(4, s.count());
    }

    assertThrows(NotFoundException.class, db::readSchema);
    db.writeSchema("definition user {}");
    assertEquals("definition user {}", db.readSchema().schema());
    assertThrows(NotFoundException.class, () -> db.reflectSchema(Consistency.minLatency()));
    db.setReflectSchemaResult(
        new SpiceDBClient.ReflectSchemaResult(
            List.of(new SpiceDBClient.SchemaDefinition("user", "", List.of(), List.of())),
            List.of(),
            null));
    var reflected = db.reflectSchema(Consistency.minLatency());
    assertEquals("user", reflected.definitions().get(0).name());
    assertEquals(db.readSchema().revision(), reflected.revision());
  }

  @Test
  void responseMetadataIsReportedToTheCapture() {
    var db = new InMemorySpiceDB();
    var key = Metadata.Key.of("io.spicedb.respmeta.version", Metadata.ASCII_STRING_MARSHALLER);
    var headers = new Metadata();
    headers.put(key, "v1.99");
    db.setResponseMetadata(headers, new Metadata());
    var capture = new ResponseMetadata();
    db.writeSchema(
        "definition user {}",
        CallOptions.builder()
            .transport(TransportOptions.builder().responseMetadata(capture).build())
            .build());
    assertEquals("v1.99", capture.headers().get(key));
    assertTrue(capture.trailers() != null);
  }

  private static List<Relationship> users(int n) {
    var out = new java.util.ArrayList<Relationship>();
    for (int i = 0; i < n; i++) out.add(rel("document:d" + i + "#viewer@user:u" + i));
    return out;
  }

  @Test
  void anImportBatchFaultAppliesNothing() {
    var db = new InMemorySpiceDB();
    db.setFaultHook(
        f ->
            f.stage() == InMemorySpiceDB.FaultStage.IMPORT_BATCH && f.item() == 2
                ? InMemorySpiceDB.serverError(Status.Code.ALREADY_EXISTS, "duplicate")
                : null);
    var e =
        assertThrows(
            AlreadyExistsException.class,
            () -> db.importRelationships(users(7), ImportOptions.builder().batchSize(3).build()));
    assertEquals("duplicate", e.serverMessage());
    assertTrue(db.relationships().isEmpty(), "a failed import applies nothing");

    // The commit point fails after every batch was sent, still applying nothing.
    db.setFaultHook(
        f ->
            f.stage() == InMemorySpiceDB.FaultStage.IMPORT_COMMIT
                ? InMemorySpiceDB.serverError(Status.Code.UNAVAILABLE, "gone")
                : null);
    assertThrows(UnavailableException.class, () -> db.importRelationships(users(2)));
    assertTrue(db.relationships().isEmpty());
    db.setFaultHook(null);
    assertEquals(2, db.importRelationships(users(2)));
  }

  @Test
  void anExportFailsAfterKItems() {
    var db = new InMemorySpiceDB();
    db.importRelationships(users(5));
    db.setFaultHook(
        f ->
            f.stage() == InMemorySpiceDB.FaultStage.ITEM && f.item() == 3
                ? InMemorySpiceDB.serverError(Status.Code.UNAVAILABLE, "stream broke")
                : null);
    var seen = new java.util.ArrayList<ExportedRelationship>();
    try (var s = db.exportRelationships(Consistency.full(), null)) {
      var it = s.iterator();
      assertThrows(
          UnavailableException.class,
          () -> {
            while (it.hasNext()) seen.add(it.next());
          });
    }
    assertEquals(3, seen.size(), "exactly the items before the fault are yielded");

    // Reads use the same point.
    try (var s = db.readRelationships(Consistency.full(), Filter.of("document"))) {
      assertThrows(UnavailableException.class, s::toList);
    }
  }

  @Test
  void failNextAffectsExactlyOneCall() {
    var db = new InMemorySpiceDB();
    db.failNext(
        ClientOperation.WRITE, InMemorySpiceDB.serverError(Status.Code.UNAVAILABLE, "try again"));
    var txn = new Transaction();
    txn.touch(rel("document:a#viewer@user:alice"));
    assertEquals(0, countViewers(db), "other operations are unaffected");
    assertThrows(UnavailableException.class, () -> db.write(txn));
    assertEquals(0, countViewers(db), "a failed write applies nothing");
    db.write(txn);
    db.write(txn);
    assertEquals(1, countViewers(db));

    // Streaming operations fail where the real client would: when the stream is consumed.
    db.failNext(
        ClientOperation.READ_RELATIONSHIPS,
        InMemorySpiceDB.serverError(Status.Code.UNAVAILABLE, "try again"));
    try (var s = db.readRelationships(Consistency.full(), Filter.of("document"))) {
      assertThrows(UnavailableException.class, s::toList);
    }
    assertEquals(1, countViewers(db));
  }

  @Test
  void recordedCallsExposeArgumentsAndOptions() {
    var db = new InMemorySpiceDB();
    var txn = new Transaction();
    txn.touch(rel("document:a#viewer@user:alice"));
    var retry = RetryPolicy.builder().maxRetries(1).build();
    db.write(txn, WriteOptions.builder().retryPolicy(retry).build());
    db.write(txn);
    var check =
        PermissionCheck.builder()
            .resource(new ObjectRef("document", "a"))
            .permission("view")
            .subject(SubjectRef.direct(new ObjectRef("user", "alice")))
            .consistency(Consistency.full())
            .build();
    db.check(check, CheckOptions.builder().tracing(CheckTracing.ENABLED).build());
    try (var s =
        db.readRelationships(
            Consistency.minLatency(),
            Filter.of("document"),
            ReadOptions.builder().limit(5).build())) {
      s.toList();
    }

    var calls = db.calls();
    assertEquals(
        List.of(
            ClientOperation.WRITE,
            ClientOperation.WRITE,
            ClientOperation.CHECK,
            ClientOperation.READ_RELATIONSHIPS),
        calls.stream().map(InMemorySpiceDB.Call::operation).toList());
    var writes = db.callsOf(ClientOperation.WRITE);
    assertEquals(0, writes.get(0).index());
    assertEquals(1, writes.get(1).index());
    assertEquals(List.of(txn), writes.get(0).arguments());
    assertEquals(retry, writes.get(0).options(WriteOptions.class).retryPolicy());
    assertNull(writes.get(1).options(WriteOptions.class).retryPolicy(), "defaults are recorded");
    assertEquals(
        CheckTracing.ENABLED,
        db.callsOf(ClientOperation.CHECK).get(0).options(CheckOptions.class).tracing());
    var read = db.callsOf(ClientOperation.READ_RELATIONSHIPS).get(0);
    assertEquals(List.of(Consistency.minLatency(), Filter.of("document")), read.arguments());
    assertEquals(5, read.options(ReadOptions.class).limit());

    // The fault hook sees the call it interrupts.
    var seen = new java.util.ArrayList<InMemorySpiceDB.Fault>();
    db.setFaultHook(
        f -> {
          seen.add(f);
          return null;
        });
    db.writeSchema("definition user {}");
    assertEquals(1, seen.size());
    assertEquals(InMemorySpiceDB.FaultStage.CALL, seen.get(0).stage());
    assertEquals(ClientOperation.WRITE_SCHEMA, seen.get(0).call().operation());
    assertEquals(List.of("definition user {}"), seen.get(0).call().arguments());
  }
}
