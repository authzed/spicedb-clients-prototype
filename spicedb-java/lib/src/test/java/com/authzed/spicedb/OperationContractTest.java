package com.authzed.spicedb;

import static org.junit.jupiter.api.Assertions.*;

import com.authzed.api.v1.*;
import com.authzed.spicedb.errors.*;
import io.grpc.stub.*;
import java.time.Duration;
import java.util.*;
import java.util.concurrent.*;
import java.util.concurrent.atomic.*;
import org.junit.jupiter.api.Test;

class OperationContractTest {
  @org.junit.jupiter.params.ParameterizedTest
  @org.junit.jupiter.params.provider.NullAndEmptySource
  @org.junit.jupiter.params.provider.ValueSource(strings = {"next-page"})
  void nonAtomicDeletionForwardsOnlyNonemptyResponseCursors(String cursor) throws Exception {
    var requests = new ArrayList<DeleteRelationshipsRequest>();
    try (var server =
        TestServers.start(
            new PermissionsServiceGrpc.PermissionsServiceImplBase() {
              public void deleteRelationships(
                  DeleteRelationshipsRequest request,
                  StreamObserver<DeleteRelationshipsResponse> out) {
                requests.add(request);
                var response =
                    DeleteRelationshipsResponse.newBuilder()
                        .setRelationshipsDeletedCount(1)
                        .setDeletedAt(com.authzed.api.v1.ZedToken.newBuilder().setToken("revision"))
                        .setDeletionProgress(
                            requests.size() == 1
                                ? DeleteRelationshipsResponse.DeletionProgress
                                    .DELETION_PROGRESS_PARTIAL
                                : DeleteRelationshipsResponse.DeletionProgress
                                    .DELETION_PROGRESS_COMPLETE);
                if (cursor != null)
                  response.setAfterResultCursor(Cursor.newBuilder().setToken(cursor));
                out.onNext(response.build());
                out.onCompleted();
              }
            })) {
      var result =
          server
              .client()
              .deleteAllRelationshipsNonAtomic(
                  Filter.of("document"), DeleteAllOptions.builder().resumeCursor("").build());
      assertEquals(2, requests.size());
      assertFalse(requests.get(0).hasOptionalCursor());
      assertEquals(cursor != null && !cursor.isEmpty(), requests.get(1).hasOptionalCursor());
      if (cursor != null && !cursor.isEmpty())
        assertEquals(cursor, requests.get(1).getOptionalCursor().getToken());
      assertEquals(cursor, result.resumeCursor());
      assertEquals(2, result.deletedCount());
      assertEquals(new ZedToken("revision"), result.revision());
    }
  }

  @Test
  void deletionApiUsesExplicitNonAtomicNameAndRevision() throws Exception {
    assertNotNull(SpiceDBClient.class.getMethod("deleteAllRelationshipsNonAtomic", Filter.class));
    assertThrows(
        NoSuchMethodException.class,
        () -> SpiceDBClient.class.getMethod("deleteAllRelationships", Filter.class));
    assertThrows(
        NoSuchMethodException.class, () -> SpiceDBClient.DeleteResult.class.getMethod("deletedAt"));
    assertNotNull(SpiceDBClient.DeleteResult.class.getMethod("revision"));
  }

  @Test
  void boundedDeleteSendsOneRpcAndPreservesExactSubject() throws Exception {
    var captured = new ArrayList<DeleteRelationshipsRequest>();
    try (var server =
        TestServers.start(
            new PermissionsServiceGrpc.PermissionsServiceImplBase() {
              public void deleteRelationships(
                  DeleteRelationshipsRequest r, StreamObserver<DeleteRelationshipsResponse> out) {
                captured.add(r);
                out.onNext(
                    DeleteRelationshipsResponse.newBuilder()
                        .setDeletionProgress(
                            DeleteRelationshipsResponse.DeletionProgress.DELETION_PROGRESS_PARTIAL)
                        .setRelationshipsDeletedCount(2)
                        .setDeletedAt(com.authzed.api.v1.ZedToken.newBuilder().setToken("opaque"))
                        .setAfterResultCursor(Cursor.newBuilder().setToken("next"))
                        .build());
                out.onCompleted();
              }
            })) {
      var tuple =
          Relationship.of(
              new ObjectRef("document", "one"),
              "viewer",
              SubjectRef.direct(new ObjectRef("user", "alice")));
      var result = server.client().deleteRelationships(tuple.toFilter(), 2);
      assertEquals(1, captured.size());
      assertEquals(2, captured.get(0).getOptionalLimit());
      assertTrue(captured.get(0).getOptionalAllowPartialDeletions());
      assertTrue(
          captured.get(0).getRelationshipFilter().getOptionalSubjectFilter().hasOptionalRelation());
      assertEquals(
          "",
          captured
              .get(0)
              .getRelationshipFilter()
              .getOptionalSubjectFilter()
              .getOptionalRelation()
              .getRelation());
      assertFalse(result.completed());
      assertEquals(2, result.deletedCount());
      assertEquals(new ZedToken("opaque"), result.revision());
      assertEquals("next", result.resumeCursor());
    }
  }

  @Test
  void deleteAllFailureRetainsTypedProgressAndDoesNotReplay() throws Exception {
    var calls = new AtomicInteger();
    try (var server =
        TestServers.start(
            new PermissionsServiceGrpc.PermissionsServiceImplBase() {
              public void deleteRelationships(
                  DeleteRelationshipsRequest r, StreamObserver<DeleteRelationshipsResponse> out) {
                if (calls.incrementAndGet() == 1) {
                  out.onNext(
                      DeleteRelationshipsResponse.newBuilder()
                          .setDeletionProgress(
                              DeleteRelationshipsResponse.DeletionProgress
                                  .DELETION_PROGRESS_PARTIAL)
                          .setRelationshipsDeletedCount(3)
                          .setDeletedAt(com.authzed.api.v1.ZedToken.newBuilder().setToken("ack"))
                          .setAfterResultCursor(Cursor.newBuilder().setToken("resume"))
                          .build());
                  out.onCompleted();
                } else {
                  assertEquals("resume", r.getOptionalCursor().getToken());
                  out.onError(io.grpc.Status.UNAVAILABLE.asRuntimeException());
                }
              }
            })) {
      var error =
          assertThrows(
              UnavailableException.class,
              () -> server.client().deleteAllRelationshipsNonAtomic(Filter.of("document")));
      assertEquals(2, calls.get());
      assertEquals(
          new DeleteProgress(3, new ZedToken("ack"), "resume", 1, true), error.deleteProgress());
      assertNotNull(error.getCause());
    }
  }

  @Test
  void bulkChunksSpendOneDeadline() throws Exception {
    var deadlines = new ArrayList<Long>();
    try (var server =
        TestServers.start(
            new PermissionsServiceGrpc.PermissionsServiceImplBase() {
              public void checkBulkPermissions(
                  CheckBulkPermissionsRequest r, StreamObserver<CheckBulkPermissionsResponse> out) {
                deadlines.add(
                    io.grpc.Context.current().getDeadline().timeRemaining(TimeUnit.MILLISECONDS));
                try {
                  Thread.sleep(45);
                } catch (InterruptedException e) {
                  Thread.currentThread().interrupt();
                }
                var b = CheckBulkPermissionsResponse.newBuilder();
                for (var item : r.getItemsList())
                  b.addPairs(
                      CheckBulkPermissionsPair.newBuilder()
                          .setRequest(item)
                          .setItem(
                              CheckBulkPermissionsResponseItem.newBuilder()
                                  .setPermissionship(
                                      CheckPermissionResponse.Permissionship
                                          .PERMISSIONSHIP_NO_PERMISSION)));
                out.onNext(b.build());
                out.onCompleted();
              }
            })) {
      var request =
          new PermissionCheck(
              new ObjectRef("document", "one"),
              "view",
              SubjectRef.direct(new ObjectRef("user", "alice")),
              Consistency.full());
      server
          .client()
          .checkBulk(
              Collections.nCopies(1001, request),
              CheckOptions.builder().timeout(Duration.ofSeconds(2)).build());
      assertEquals(2, deadlines.size());
      assertTrue(deadlines.get(1) < deadlines.get(0) - 20, deadlines.toString());
    }
  }

  @Test
  void interruptionCancelsBlockedCheckAndPreservesInterruptFlag() throws Exception {
    var started = new CountDownLatch(1);
    var cancelled = new CountDownLatch(1);
    var failure = new AtomicReference<Throwable>();
    var preserved = new AtomicBoolean();
    try (var server =
        TestServers.start(
            new PermissionsServiceGrpc.PermissionsServiceImplBase() {
              public void checkPermission(
                  CheckPermissionRequest r, StreamObserver<CheckPermissionResponse> out) {
                ((ServerCallStreamObserver<CheckPermissionResponse>) out)
                    .setOnCancelHandler(cancelled::countDown);
                started.countDown();
              }
            })) {
      var thread =
          new Thread(
              () -> {
                try {
                  server
                      .client()
                      .check(
                          new PermissionCheck(
                              new ObjectRef("document", "one"),
                              "view",
                              SubjectRef.direct(new ObjectRef("user", "alice")),
                              Consistency.full()));
                } catch (Throwable e) {
                  failure.set(e);
                  preserved.set(Thread.currentThread().isInterrupted());
                }
              });
      thread.start();
      assertTrue(started.await(2, TimeUnit.SECONDS));
      thread.interrupt();
      thread.join(2000);
      assertFalse(thread.isAlive());
      assertInstanceOf(CancellationException.class, failure.get());
      assertTrue(preserved.get());
      assertTrue(cancelled.await(2, TimeUnit.SECONDS));
    }
  }

  @Test
  void producerFailureClosesSourceAndCancelsUpload() throws Exception {
    var cancelled = new CountDownLatch(1);
    var closed = new AtomicBoolean();
    class Source implements Iterator<Relationship>, AutoCloseable {
      int count;

      public boolean hasNext() {
        if (count == 1) throw new IllegalStateException("producer failed");
        return true;
      }

      public Relationship next() {
        count++;
        return Relationship.of(
            new ObjectRef("document", "one"),
            "viewer",
            SubjectRef.direct(new ObjectRef("user", "alice")));
      }

      public void close() {
        closed.set(true);
      }
    }
    try (var server =
        TestServers.start(
            new PermissionsServiceGrpc.PermissionsServiceImplBase() {
              public StreamObserver<ImportBulkRelationshipsRequest> importBulkRelationships(
                  StreamObserver<ImportBulkRelationshipsResponse> out) {
                ((ServerCallStreamObserver<ImportBulkRelationshipsResponse>) out)
                    .setOnCancelHandler(cancelled::countDown);
                return new StreamObserver<>() {
                  public void onNext(ImportBulkRelationshipsRequest r) {}

                  public void onError(Throwable e) {}

                  public void onCompleted() {}
                };
              }
            })) {
      assertThrows(
          IllegalStateException.class,
          () ->
              server
                  .client()
                  .importRelationships(
                      () -> new Source(), ImportOptions.builder().batchSize(1).build()));
      assertTrue(closed.get());
      assertTrue(cancelled.await(2, TimeUnit.SECONDS));
    }
  }

  @Test
  void importPreservesRichFailureDetails() throws Exception {
    var detail =
        com.google.rpc.ErrorInfo.newBuilder()
            .setReason("IMPORT_DENIED")
            .setDomain("test")
            .putMetadata("source", "upload")
            .build();
    var error =
        io.grpc.protobuf.StatusProto.toStatusRuntimeException(
            com.google.rpc.Status.newBuilder()
                .setCode(io.grpc.Status.Code.PERMISSION_DENIED.value())
                .setMessage("denied")
                .addDetails(com.google.protobuf.Any.pack(detail))
                .build());
    try (var server =
        TestServers.start(
            new PermissionsServiceGrpc.PermissionsServiceImplBase() {
              public StreamObserver<ImportBulkRelationshipsRequest> importBulkRelationships(
                  StreamObserver<ImportBulkRelationshipsResponse> out) {
                out.onError(error);
                return new StreamObserver<>() {
                  public void onNext(ImportBulkRelationshipsRequest r) {}

                  public void onError(Throwable e) {}

                  public void onCompleted() {}
                };
              }
            })) {
      var failure =
          assertThrows(
              PermissionDeniedException.class,
              () -> server.client().importRelationships(List.of()));
      assertEquals("IMPORT_DENIED", failure.getReason());
      assertEquals("upload", failure.getReasonMetadata().get("source"));
      assertNotNull(failure.getCause());
    }
  }

  @Test
  void bulkRejectsMissingReorderedAndDuplicatedEchoes() throws Exception {
    for (int mode = 0; mode < 3; mode++) {
      final int variant = mode;
      try (var server =
          TestServers.start(
              new PermissionsServiceGrpc.PermissionsServiceImplBase() {
                public void checkBulkPermissions(
                    CheckBulkPermissionsRequest r,
                    StreamObserver<CheckBulkPermissionsResponse> out) {
                  var b = CheckBulkPermissionsResponse.newBuilder();
                  for (int i = 0; i < r.getItemsCount(); i++) {
                    var pair =
                        CheckBulkPermissionsPair.newBuilder()
                            .setItem(
                                CheckBulkPermissionsResponseItem.newBuilder()
                                    .setPermissionship(
                                        CheckPermissionResponse.Permissionship
                                            .PERMISSIONSHIP_HAS_PERMISSION));
                    if (variant > 0)
                      pair.setRequest(r.getItems(variant == 1 ? r.getItemsCount() - 1 - i : 0));
                    b.addPairs(pair);
                  }
                  out.onNext(b.build());
                  out.onCompleted();
                }
              })) {
        var subject = SubjectRef.direct(new ObjectRef("user", "alice"));
        var requests =
            List.of(
                new PermissionCheck(
                    new ObjectRef("document", "one"), "view", subject, Consistency.full()),
                new PermissionCheck(
                    new ObjectRef("document", "two"), "view", subject, Consistency.full()));
        var error = assertThrows(SpiceDBException.class, () -> server.client().checkBulk(requests));
        assertTrue(error.getMessage().contains("echoed request"));
      }
    }
  }
}
