package com.authzed.spicedb;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

import com.authzed.api.v1.Cursor;
import com.authzed.api.v1.DeleteRelationshipsRequest;
import com.authzed.api.v1.DeleteRelationshipsResponse;
import com.authzed.api.v1.LookupPermissionship;
import com.authzed.api.v1.LookupResourcesRequest;
import com.authzed.api.v1.LookupResourcesResponse;
import com.authzed.api.v1.PermissionsServiceGrpc;
import com.authzed.api.v1.ReadRelationshipsRequest;
import com.authzed.api.v1.ReadRelationshipsResponse;
import com.authzed.spicedb.errors.SpiceDBException;
import com.authzed.spicedb.protoconv.ProtoConv;
import io.grpc.Status;
import io.grpc.stub.StreamObserver;
import java.util.ArrayList;
import java.util.List;
import java.util.function.Function;
import org.junit.jupiter.api.Test;

/**
 * A paged operation whose server returns a full page without a new cursor fails with INTERNAL
 * instead of re-requesting the same page forever.
 */
class PagingCursorAdvanceTest {

  /** Requests answered before the server fails, so a missing guard fails instead of hanging. */
  private static final int CAP = 5;

  /**
   * Answers every read and lookup with a full page of two items whose cursor comes from {@code
   * cursorFor} (given the request's cursor; {@code null} means none), and every delete with a
   * partial page.
   */
  private static final class StuckServer extends PermissionsServiceGrpc.PermissionsServiceImplBase {
    final List<String> sent = new ArrayList<>();
    final Function<String, String> cursorFor;
    final long deletedPerPage;

    StuckServer(Function<String, String> cursorFor, long deletedPerPage) {
      this.cursorFor = cursorFor;
      this.deletedPerPage = deletedPerPage;
    }

    private boolean capped(Cursor cursor, StreamObserver<?> out) {
      sent.add(cursor.getToken());
      if (sent.size() <= CAP) return false;
      out.onError(
          Status.FAILED_PRECONDITION.withDescription("request cap reached").asRuntimeException());
      return true;
    }

    private Cursor next(Cursor request) {
      var token = cursorFor.apply(request.getToken());
      return token == null ? null : Cursor.newBuilder().setToken(token).build();
    }

    @Override
    public void readRelationships(
        ReadRelationshipsRequest request, StreamObserver<ReadRelationshipsResponse> out) {
      if (capped(request.getOptionalCursor(), out)) return;
      var next = next(request.getOptionalCursor());
      for (int i = 0; i < 2; i++) {
        var b =
            ReadRelationshipsResponse.newBuilder()
                .setRelationship(
                    ProtoConv.toProto(Relationship.fromTuple("document:d" + i + "#viewer@user:u")));
        if (next != null) b.setAfterResultCursor(next);
        out.onNext(b.build());
      }
      out.onCompleted();
    }

    @Override
    public void lookupResources(
        LookupResourcesRequest request, StreamObserver<LookupResourcesResponse> out) {
      if (capped(request.getOptionalCursor(), out)) return;
      var next = next(request.getOptionalCursor());
      for (int i = 0; i < 2; i++) {
        var b =
            LookupResourcesResponse.newBuilder()
                .setResourceObjectId("r" + i)
                .setPermissionship(LookupPermissionship.LOOKUP_PERMISSIONSHIP_HAS_PERMISSION);
        if (next != null) b.setAfterResultCursor(next);
        out.onNext(b.build());
      }
      out.onCompleted();
    }

    @Override
    public void deleteRelationships(
        DeleteRelationshipsRequest request, StreamObserver<DeleteRelationshipsResponse> out) {
      if (capped(request.getOptionalCursor(), out)) return;
      var next = next(request.getOptionalCursor());
      var b =
          DeleteRelationshipsResponse.newBuilder()
              .setDeletedAt(com.authzed.api.v1.ZedToken.newBuilder().setToken("t"))
              .setRelationshipsDeletedCount(deletedPerPage)
              .setDeletionProgress(
                  DeleteRelationshipsResponse.DeletionProgress.DELETION_PROGRESS_PARTIAL);
      if (next != null) b.setAfterResultCursor(next);
      out.onNext(b.build());
      out.onCompleted();
    }
  }

  private static SpiceDBException readAll(SpiceDBClient client) {
    return assertThrows(
        SpiceDBException.class,
        () -> {
          try (var s =
              client.readRelationships(
                  Consistency.full(),
                  Filter.of("document"),
                  ReadOptions.builder().pageSize(2).build())) {
            s.forEach(r -> {});
          }
        });
  }

  private static SpiceDBException lookupAll(SpiceDBClient client) {
    return assertThrows(
        SpiceDBException.class,
        () -> {
          try (var s =
              client.lookupResources(
                  Consistency.full(),
                  "document",
                  "view",
                  SubjectRef.direct(new ObjectRef("user", "alice")),
                  LookupOptions.builder().pageSize(2).build())) {
            s.forEach(r -> {});
          }
        });
  }

  private static SpiceDBException deleteAll(SpiceDBClient client) {
    return assertThrows(
        SpiceDBException.class,
        () -> client.deleteAllRelationshipsNonAtomic(Filter.of("document")));
  }

  private static void assertNotAdvanced(SpiceDBException e, String what) {
    assertEquals(Status.Code.INTERNAL, e.code(), e.getMessage());
    assertTrue(e.getMessage().contains(what + " page did not advance cursor"), e.getMessage());
  }

  @Test
  void fullPageWithoutCursorFails() throws Exception {
    var srv = new StuckServer(sent -> null, 0);
    try (var s = TestServers.start(srv)) {
      assertNotAdvanced(readAll(s.client()), "read");
      assertEquals(List.of(""), srv.sent);
      srv.sent.clear();
      assertNotAdvanced(lookupAll(s.client()), "lookup");
      assertEquals(List.of(""), srv.sent);
    }
  }

  @Test
  void fullPageRepeatingTheSentCursorFails() throws Exception {
    // The first page advances to "c"; every later page answers "c" again.
    var srv = new StuckServer(sent -> "c", 0);
    try (var s = TestServers.start(srv)) {
      assertNotAdvanced(readAll(s.client()), "read");
      assertEquals(List.of("", "c"), srv.sent);
      srv.sent.clear();
      assertNotAdvanced(lookupAll(s.client()), "lookup");
      assertEquals(List.of("", "c"), srv.sent);
    }
  }

  @Test
  void advancingCursorsKeepPaging() throws Exception {
    var srv = new StuckServer(sent -> sent + "x", 0);
    try (var s = TestServers.start(srv)) {
      // Only the request cap stops this server; the guard never fires.
      var e = readAll(s.client());
      assertEquals(Status.Code.FAILED_PRECONDITION, e.code());
      assertEquals(CAP + 1, srv.sent.size());
    }
  }

  @Test
  void partialDeleteRepeatingTheSentCursorFails() throws Exception {
    var srv = new StuckServer(sent -> "c", 1);
    try (var s = TestServers.start(srv)) {
      var e = deleteAll(s.client());
      assertNotAdvanced(e, "delete");
      assertEquals(List.of("", "c"), srv.sent);
      var progress = e.deleteProgress();
      assertEquals(2, progress.acknowledgedCount());
      assertEquals(2, progress.completedPages());
      assertEquals("c", progress.lastCursor());
      assertFalse(progress.outcomeUnknown());
    }
  }

  @Test
  void partialDeleteWithoutCursorKeepsGoing() throws Exception {
    // Without a cursor the next page re-evaluates the filter against what remains.
    var srv = new StuckServer(sent -> null, 1);
    try (var s = TestServers.start(srv)) {
      var e = deleteAll(s.client());
      assertEquals(Status.Code.FAILED_PRECONDITION, e.code());
      assertEquals(CAP + 1, srv.sent.size());
    }
  }
}
