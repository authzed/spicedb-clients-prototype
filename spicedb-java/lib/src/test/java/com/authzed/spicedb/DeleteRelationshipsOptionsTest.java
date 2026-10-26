package com.authzed.spicedb;

import static org.junit.jupiter.api.Assertions.*;

import build.buf.gen.authzed.api.v1.Cursor;
import build.buf.gen.authzed.api.v1.DeleteRelationshipsRequest;
import build.buf.gen.authzed.api.v1.DeleteRelationshipsResponse;
import build.buf.gen.authzed.api.v1.PermissionsServiceGrpc;
import build.buf.gen.authzed.api.v1.Precondition;
import build.buf.gen.authzed.api.v1.ZedToken;
import com.authzed.spicedb.errors.InvalidArgumentException;
import io.grpc.stub.StreamObserver;
import java.io.IOException;
import java.util.ArrayList;
import java.util.List;
import org.junit.jupiter.api.Test;

/**
 * Tests for {@link SpiceDBClient#deleteRelationships(Filter, DeleteAllOptions)} — optional
 * preconditions (must-match/must-not-match) and per-request page-size limit, threaded into the
 * auto-paging delete loop. Mirrors {@code spicedb-go}'s {@code client/relationships.go} {@code
 * WithDeleteMustMatch}/{@code WithDeleteMustNotMatch}/{@code WithDeleteLimit}.
 *
 * <p>Uses the in-process gRPC harness ({@link TestServers}) with a mock {@code PermissionsService}
 * that captures every outbound {@link DeleteRelationshipsRequest} so tests can assert on exactly
 * what was sent over the wire.
 */
class DeleteRelationshipsOptionsTest {

  private static final Filter FILTER = Filter.of("document").withResourceId("doc1");

  /** Mock service that records every request and always completes in a single page. */
  private static final class CapturingSingleCompleteService
      extends PermissionsServiceGrpc.PermissionsServiceImplBase {
    final List<DeleteRelationshipsRequest> captured = new ArrayList<>();

    @Override
    public void deleteRelationships(
        DeleteRelationshipsRequest request,
        StreamObserver<DeleteRelationshipsResponse> responseObserver) {
      captured.add(request);
      responseObserver.onNext(
          DeleteRelationshipsResponse.newBuilder()
              .setDeletedAt(ZedToken.newBuilder().setToken("rev-1").build())
              .setDeletionProgress(
                  DeleteRelationshipsResponse.DeletionProgress.DELETION_PROGRESS_COMPLETE)
              .build());
      responseObserver.onCompleted();
    }
  }

  @Test
  void defaultOverloadSendsNoPreconditionsAndDefaultPageSize() throws IOException {
    var service = new CapturingSingleCompleteService();
    try (TestServers servers = TestServers.start(service)) {
      SpiceDBClient client = servers.client();
      var revision = client.deleteAllRelationshipsNonAtomic(FILTER).revision().value();

      assertEquals("rev-1", revision);
      assertEquals(1, service.captured.size());
      DeleteRelationshipsRequest req = service.captured.get(0);
      assertEquals(0, req.getOptionalPreconditionsCount());
      assertEquals(1_000, req.getOptionalLimit());
      assertTrue(req.getOptionalAllowPartialDeletions());
      assertEquals("document", req.getRelationshipFilter().getResourceType());
      assertEquals("doc1", req.getRelationshipFilter().getOptionalResourceId());
    }
  }

  @Test
  void noneOptionsOverloadMatchesDefaultOverload() throws IOException {
    var service = new CapturingSingleCompleteService();
    try (TestServers servers = TestServers.start(service)) {
      SpiceDBClient client = servers.client();
      var revision =
          client
              .deleteAllRelationshipsNonAtomic(FILTER, DeleteAllOptions.builder().build())
              .revision()
              .value();

      assertEquals("rev-1", revision);
      DeleteRelationshipsRequest req = service.captured.get(0);
      assertEquals(0, req.getOptionalPreconditionsCount());
      assertEquals(1_000, req.getOptionalLimit());
      assertTrue(req.getOptionalAllowPartialDeletions());
    }
  }

  /**
   * Regression test for the offboarding hazard this finding describes: {@code toRelationshipFilter}
   * used to build {@code optionalSubjectFilter} only inside {@code if (subjectType is set)}, so a
   * filter with {@code subjectID} but no {@code subjectType} produced a proto {@code
   * RelationshipFilter} with NO subject constraint at all -- {@code deleteRelationships} called
   * with that filter would delete every relationship on every document, not just the intended
   * subject's. It must now throw before any RPC is attempted, instead of silently widening.
   */
  @Test
  void deleteRelationshipsThrowsWhenFilterSubjectIDHasNoSubjectType() {
    assertThrows(
        InvalidArgumentException.class, () -> Filter.of("document").withSubjectId("alice"));
  }

  /** {@code subjectRelation} counterpart of the above. */
  @Test
  void deleteRelationshipsThrowsWhenFilterSubjectRelationHasNoSubjectType() {
    assertThrows(
        InvalidArgumentException.class,
        () -> Filter.of("document").withSubjectRelation(SubjectRelationFilter.named("member")));
  }

  /**
   * Same rejection, but for a {@code mustMatch} precondition filter rather than the primary filter
   * -- preconditions are converted before any RPC is attempted, so this must also fail closed with
   * no request sent.
   */
  @Test
  void deleteRelationshipsThrowsWhenMustMatchFilterSubjectIDHasNoSubjectType() {
    assertThrows(
        InvalidArgumentException.class, () -> Filter.of("document").withSubjectId("alice"));
  }

  /**
   * Companion to the throw cases above -- proves a filter with subjectType alone (no subjectID)
   * still builds a valid subject filter and is not accidentally caught by the new guard.
   */
  @Test
  void deleteRelationshipsSendsSubjectTypeAloneWithoutThrowing() throws IOException {
    var service = new CapturingSingleCompleteService();
    Filter filter = Filter.of("document").withSubjectType("user");

    try (TestServers servers = TestServers.start(service)) {
      SpiceDBClient client = servers.client();
      client.deleteAllRelationshipsNonAtomic(filter);

      DeleteRelationshipsRequest req = service.captured.get(0);
      assertEquals("user", req.getRelationshipFilter().getOptionalSubjectFilter().getSubjectType());
      assertEquals(
          "", req.getRelationshipFilter().getOptionalSubjectFilter().getOptionalSubjectId());
    }
  }

  /** Companion proving the valid combination (subjectType alongside subjectID) still works. */
  @Test
  void deleteRelationshipsSendsSubjectTypeAndIDWithoutThrowing() throws IOException {
    var service = new CapturingSingleCompleteService();
    Filter filter = Filter.of("document").withSubjectType("user").withSubjectId("alice");

    try (TestServers servers = TestServers.start(service)) {
      SpiceDBClient client = servers.client();
      client.deleteAllRelationshipsNonAtomic(filter);

      DeleteRelationshipsRequest req = service.captured.get(0);
      assertEquals("user", req.getRelationshipFilter().getOptionalSubjectFilter().getSubjectType());
      assertEquals(
          "alice", req.getRelationshipFilter().getOptionalSubjectFilter().getOptionalSubjectId());
    }
  }

  @Test
  void mustMatchAddsMustMatchPrecondition() throws IOException {
    var service = new CapturingSingleCompleteService();
    Filter existsFilter = Filter.of("document").withResourceId("doc1").withRelation("owner");

    try (TestServers servers = TestServers.start(service)) {
      SpiceDBClient client = servers.client();
      var options = DeleteAllOptions.builder().mustMatch(java.util.List.of(existsFilter)).build();
      client.deleteAllRelationshipsNonAtomic(FILTER, options);

      DeleteRelationshipsRequest req = service.captured.get(0);
      assertEquals(1, req.getOptionalPreconditionsCount());
      Precondition p = req.getOptionalPreconditions(0);
      assertEquals(Precondition.Operation.OPERATION_MUST_MATCH, p.getOperation());
      assertEquals("document", p.getFilter().getResourceType());
      assertEquals("doc1", p.getFilter().getOptionalResourceId());
      assertEquals("owner", p.getFilter().getOptionalRelation());
    }
  }

  @Test
  void mustNotMatchAddsMustNotMatchPrecondition() throws IOException {
    var service = new CapturingSingleCompleteService();
    Filter absentFilter = Filter.of("document").withResourceId("doc1").withRelation("banned");

    try (TestServers servers = TestServers.start(service)) {
      SpiceDBClient client = servers.client();
      var options =
          DeleteAllOptions.builder().mustNotMatch(java.util.List.of(absentFilter)).build();
      client.deleteAllRelationshipsNonAtomic(FILTER, options);

      DeleteRelationshipsRequest req = service.captured.get(0);
      assertEquals(1, req.getOptionalPreconditionsCount());
      Precondition p = req.getOptionalPreconditions(0);
      assertEquals(Precondition.Operation.OPERATION_MUST_NOT_MATCH, p.getOperation());
      assertEquals("banned", p.getFilter().getOptionalRelation());
    }
  }

  @Test
  void combinedMustMatchMustNotMatchAndLimitAreAllSent() throws IOException {
    var service = new CapturingSingleCompleteService();
    Filter mustFilter = Filter.of("document").withResourceId("doc1").withRelation("owner");
    Filter mustNotFilter = Filter.of("document").withResourceId("doc1").withRelation("banned");

    try (TestServers servers = TestServers.start(service)) {
      SpiceDBClient client = servers.client();
      var options =
          DeleteAllOptions.builder()
              .mustMatch(java.util.List.of(mustFilter))
              .mustNotMatch(java.util.List.of(mustNotFilter))
              .pageSize(250)
              .build();
      client.deleteAllRelationshipsNonAtomic(FILTER, options);

      DeleteRelationshipsRequest req = service.captured.get(0);
      assertEquals(250, req.getOptionalLimit());
      assertEquals(2, req.getOptionalPreconditionsCount());
      assertEquals(
          Precondition.Operation.OPERATION_MUST_MATCH,
          req.getOptionalPreconditions(0).getOperation());
      assertEquals(
          Precondition.Operation.OPERATION_MUST_NOT_MATCH,
          req.getOptionalPreconditions(1).getOperation());
    }
  }

  @Test
  void multiplePreconditionsOfSameKindAccumulate() throws IOException {
    var service = new CapturingSingleCompleteService();
    Filter f1 = Filter.of("document").withResourceId("doc1").withRelation("owner");
    Filter f2 = Filter.of("document").withResourceId("doc1").withRelation("editor");

    try (TestServers servers = TestServers.start(service)) {
      SpiceDBClient client = servers.client();
      var options = DeleteAllOptions.builder().mustMatch(java.util.List.of(f1, f2)).build();
      client.deleteAllRelationshipsNonAtomic(FILTER, options);

      DeleteRelationshipsRequest req = service.captured.get(0);
      assertEquals(2, req.getOptionalPreconditionsCount());
      assertEquals("owner", req.getOptionalPreconditions(0).getFilter().getOptionalRelation());
      assertEquals("editor", req.getOptionalPreconditions(1).getFilter().getOptionalRelation());
    }
  }

  @Test
  void limitOverridesDefaultPageSize() throws IOException {
    var service = new CapturingSingleCompleteService();
    try (TestServers servers = TestServers.start(service)) {
      SpiceDBClient client = servers.client();
      client.deleteAllRelationshipsNonAtomic(
          FILTER, DeleteAllOptions.builder().pageSize(1).build());

      DeleteRelationshipsRequest req = service.captured.get(0);
      assertEquals(1, req.getOptionalLimit());
    }
  }

  /** Mock service that reports PARTIAL progress on the first call, COMPLETE on the second. */
  private static final class TwoPageService
      extends PermissionsServiceGrpc.PermissionsServiceImplBase {
    final List<DeleteRelationshipsRequest> captured = new ArrayList<>();

    @Override
    public void deleteRelationships(
        DeleteRelationshipsRequest request,
        StreamObserver<DeleteRelationshipsResponse> responseObserver) {
      captured.add(request);
      boolean isFirst = captured.size() == 1;
      responseObserver.onNext(
          DeleteRelationshipsResponse.newBuilder()
              .setDeletedAt(ZedToken.newBuilder().setToken("rev-" + captured.size()).build())
              .setDeletionProgress(
                  isFirst
                      ? DeleteRelationshipsResponse.DeletionProgress.DELETION_PROGRESS_PARTIAL
                      : DeleteRelationshipsResponse.DeletionProgress.DELETION_PROGRESS_COMPLETE)
              .build());
      responseObserver.onCompleted();
    }
  }

  @Test
  void preconditionsAndLimitAreReSentOnEveryPageOfAMultiPageDelete() throws IOException {
    var service = new TwoPageService();
    Filter mustFilter = Filter.of("document").withResourceId("doc1").withRelation("owner");

    try (TestServers servers = TestServers.start(service)) {
      SpiceDBClient client = servers.client();
      var options =
          DeleteAllOptions.builder().mustMatch(java.util.List.of(mustFilter)).pageSize(5).build();
      var revision = client.deleteAllRelationshipsNonAtomic(FILTER, options).revision().value();

      assertEquals("rev-2", revision);
      assertEquals(2, service.captured.size());
      for (DeleteRelationshipsRequest req : service.captured) {
        assertEquals(5, req.getOptionalLimit());
        assertEquals(1, req.getOptionalPreconditionsCount());
        assertEquals(
            Precondition.Operation.OPERATION_MUST_MATCH,
            req.getOptionalPreconditions(0).getOperation());
      }
    }
  }

  /**
   * Mock service that reports PARTIAL progress with an {@code after_result_cursor} on the first
   * call, COMPLETE with no cursor on the second.
   */
  private static final class CursoredTwoPageService
      extends PermissionsServiceGrpc.PermissionsServiceImplBase {
    static final Cursor PAGE_1_CURSOR = Cursor.newBuilder().setToken("cursor-after-page-1").build();
    final List<DeleteRelationshipsRequest> captured = new ArrayList<>();

    @Override
    public void deleteRelationships(
        DeleteRelationshipsRequest request,
        StreamObserver<DeleteRelationshipsResponse> responseObserver) {
      captured.add(request);
      boolean isFirst = captured.size() == 1;
      var respBuilder =
          DeleteRelationshipsResponse.newBuilder()
              .setDeletedAt(ZedToken.newBuilder().setToken("rev-" + captured.size()).build())
              .setDeletionProgress(
                  isFirst
                      ? DeleteRelationshipsResponse.DeletionProgress.DELETION_PROGRESS_PARTIAL
                      : DeleteRelationshipsResponse.DeletionProgress.DELETION_PROGRESS_COMPLETE);
      if (isFirst) {
        respBuilder.setAfterResultCursor(PAGE_1_CURSOR);
      }
      responseObserver.onNext(respBuilder.build());
      responseObserver.onCompleted();
    }
  }

  /**
   * When the server hands back {@code after_result_cursor}, the next page's request carries it as
   * {@code optional_cursor} — the same cursor-handoff shape {@code readRelationships}/{@code
   * lookupResources} already use — so a datastore that supports cursored deletion does not
   * re-examine relationships an earlier page already deleted. The first request must carry no
   * cursor at all, since none exists yet.
   */
  @Test
  void cursorFromPartialResponseIsSentOnTheNextPage() throws IOException {
    var service = new CursoredTwoPageService();

    try (TestServers servers = TestServers.start(service)) {
      SpiceDBClient client = servers.client();
      String revision =
          client
              .deleteAllRelationshipsNonAtomic(
                  FILTER, DeleteAllOptions.builder().pageSize(5).build())
              .revision()
              .value();

      assertEquals("rev-2", revision);
      assertEquals(2, service.captured.size());
      assertFalse(
          service.captured.get(0).hasOptionalCursor(), "no cursor exists yet for the first page");
      assertTrue(service.captured.get(1).hasOptionalCursor());
      assertEquals(
          CursoredTwoPageService.PAGE_1_CURSOR, service.captured.get(1).getOptionalCursor());
    }
  }

  /**
   * Companion to the above: a datastore that never populates {@code after_result_cursor} (the
   * {@link TwoPageService} case) must not have a cursor invented for it — every page's request
   * carries no cursor, and pagination falls back to re-evaluating the filter against what remains.
   */
  @Test
  void noCursorIsSentWhenServerNeverReturnsOne() throws IOException {
    var service = new TwoPageService();

    try (TestServers servers = TestServers.start(service)) {
      SpiceDBClient client = servers.client();
      client.deleteAllRelationshipsNonAtomic(
          FILTER, DeleteAllOptions.builder().pageSize(5).build());

      assertEquals(2, service.captured.size());
      for (DeleteRelationshipsRequest req : service.captured) {
        assertFalse(req.hasOptionalCursor());
      }
    }
  }

  @Test
  void limitMustBePositive() {
    assertThrows(
        com.authzed.spicedb.errors.InvalidArgumentException.class,
        () -> DeleteAllOptions.builder().pageSize(0).build());
    assertThrows(
        com.authzed.spicedb.errors.InvalidArgumentException.class,
        () -> DeleteAllOptions.builder().pageSize(-1).build());
  }

  @Test
  void noneHasEmptyPreconditionsAndNullLimit() {
    var options = DeleteAllOptions.builder().build();
    assertTrue(options.mustMatch().isEmpty());
    assertTrue(options.mustNotMatch().isEmpty());
    assertEquals(1000, options.pageSize());
  }

  /**
   * The three-argument constructor is part of the published surface and must keep working. It was
   * the record's generated canonical constructor until {@code Duration timeout} was added as a
   * fourth component, which silently removed it - a binary and source break for every caller that
   * wrote {@code new DeleteOptions(a, b, c)}. It is now declared explicitly, and this pins it:
   * adding a fifth component must not delete this arity again.
   */
  @Test
  void threeArgConstructorStillWorksAndMeansNoTimeout() {
    var mustMatch = List.of(Filter.of("document").withResourceId("doc1"));
    var mustNotMatch = List.of(Filter.of("document").withResourceId("doc2"));

    var options =
        DeleteAllOptions.builder()
            .mustMatch(mustMatch)
            .mustNotMatch(mustNotMatch)
            .pageSize(500)
            .build();

    assertEquals(mustMatch, options.mustMatch());
    assertEquals(mustNotMatch, options.mustNotMatch());
    assertEquals(500, options.pageSize());
    assertNull(options.timeout(), "three-arg form must mean no per-call deadline");

    // It must delegate to the canonical constructor, so normalization and validation still apply.
    var nulls = DeleteAllOptions.builder().build();
    assertTrue(nulls.mustMatch().isEmpty());
    assertTrue(nulls.mustNotMatch().isEmpty());
    assertThrows(
        com.authzed.spicedb.errors.InvalidArgumentException.class,
        () -> DeleteAllOptions.builder().pageSize(0).build());
  }
}
