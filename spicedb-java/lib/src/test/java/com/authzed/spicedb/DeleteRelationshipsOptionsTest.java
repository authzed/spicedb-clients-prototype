package com.authzed.spicedb;

import static org.junit.jupiter.api.Assertions.*;

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
 * Tests for {@link SpiceDBClient#deleteRelationships(Filter, SpiceDBClient.DeleteOptions)} —
 * optional preconditions (must-match/must-not-match) and per-request page-size limit, threaded into
 * the auto-paging delete loop. Mirrors {@code spicedb-go}'s {@code client/relationships.go} {@code
 * WithDeleteMustMatch}/{@code WithDeleteMustNotMatch}/{@code WithDeleteLimit}.
 *
 * <p>Uses the in-process gRPC harness ({@link TestServers}) with a mock {@code PermissionsService}
 * that captures every outbound {@link DeleteRelationshipsRequest} so tests can assert on exactly
 * what was sent over the wire.
 */
class DeleteRelationshipsOptionsTest {

  private static final Filter FILTER = Filter.of("document").withResourceID("doc1");

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
      String revision = client.deleteRelationships(FILTER);

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
      String revision = client.deleteRelationships(FILTER, SpiceDBClient.DeleteOptions.none());

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
  void deleteRelationshipsThrowsWhenFilterSubjectIDHasNoSubjectType() throws IOException {
    var service = new CapturingSingleCompleteService();
    Filter badFilter = Filter.of("document").withSubjectID("alice");

    try (TestServers servers = TestServers.start(service)) {
      SpiceDBClient client = servers.client();
      InvalidArgumentException e =
          assertThrows(InvalidArgumentException.class, () -> client.deleteRelationships(badFilter));
      assertTrue(e.getMessage().contains("subjectID"));
      assertTrue(e.getMessage().contains("subjectType"));
      assertEquals(0, service.captured.size(), "no request should reach the server");
    }
  }

  /** {@code subjectRelation} counterpart of the above. */
  @Test
  void deleteRelationshipsThrowsWhenFilterSubjectRelationHasNoSubjectType() throws IOException {
    var service = new CapturingSingleCompleteService();
    Filter badFilter = Filter.of("document").withSubjectRelation("member");

    try (TestServers servers = TestServers.start(service)) {
      SpiceDBClient client = servers.client();
      InvalidArgumentException e =
          assertThrows(InvalidArgumentException.class, () -> client.deleteRelationships(badFilter));
      assertTrue(e.getMessage().contains("subjectRelation"));
      assertTrue(e.getMessage().contains("subjectType"));
      assertEquals(0, service.captured.size(), "no request should reach the server");
    }
  }

  /**
   * Same rejection, but for a {@code mustMatch} precondition filter rather than the primary filter
   * -- preconditions are converted before any RPC is attempted, so this must also fail closed with
   * no request sent.
   */
  @Test
  void deleteRelationshipsThrowsWhenMustMatchFilterSubjectIDHasNoSubjectType() throws IOException {
    var service = new CapturingSingleCompleteService();
    Filter badGuard = Filter.of("document").withSubjectID("alice");

    try (TestServers servers = TestServers.start(service)) {
      SpiceDBClient client = servers.client();
      var options = SpiceDBClient.DeleteOptions.none().withMustMatch(badGuard);
      assertThrows(
          InvalidArgumentException.class, () -> client.deleteRelationships(FILTER, options));
      assertEquals(0, service.captured.size(), "no request should reach the server");
    }
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
      client.deleteRelationships(filter);

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
    Filter filter = Filter.of("document").withSubjectType("user").withSubjectID("alice");

    try (TestServers servers = TestServers.start(service)) {
      SpiceDBClient client = servers.client();
      client.deleteRelationships(filter);

      DeleteRelationshipsRequest req = service.captured.get(0);
      assertEquals("user", req.getRelationshipFilter().getOptionalSubjectFilter().getSubjectType());
      assertEquals(
          "alice", req.getRelationshipFilter().getOptionalSubjectFilter().getOptionalSubjectId());
    }
  }

  @Test
  void mustMatchAddsMustMatchPrecondition() throws IOException {
    var service = new CapturingSingleCompleteService();
    Filter existsFilter = Filter.of("document").withResourceID("doc1").withRelation("owner");

    try (TestServers servers = TestServers.start(service)) {
      SpiceDBClient client = servers.client();
      var options = SpiceDBClient.DeleteOptions.none().withMustMatch(existsFilter);
      client.deleteRelationships(FILTER, options);

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
    Filter absentFilter = Filter.of("document").withResourceID("doc1").withRelation("banned");

    try (TestServers servers = TestServers.start(service)) {
      SpiceDBClient client = servers.client();
      var options = SpiceDBClient.DeleteOptions.none().withMustNotMatch(absentFilter);
      client.deleteRelationships(FILTER, options);

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
    Filter mustFilter = Filter.of("document").withResourceID("doc1").withRelation("owner");
    Filter mustNotFilter = Filter.of("document").withResourceID("doc1").withRelation("banned");

    try (TestServers servers = TestServers.start(service)) {
      SpiceDBClient client = servers.client();
      var options =
          SpiceDBClient.DeleteOptions.none()
              .withMustMatch(mustFilter)
              .withMustNotMatch(mustNotFilter)
              .withLimit(250);
      client.deleteRelationships(FILTER, options);

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
    Filter f1 = Filter.of("document").withResourceID("doc1").withRelation("owner");
    Filter f2 = Filter.of("document").withResourceID("doc1").withRelation("editor");

    try (TestServers servers = TestServers.start(service)) {
      SpiceDBClient client = servers.client();
      var options = SpiceDBClient.DeleteOptions.none().withMustMatch(f1).withMustMatch(f2);
      client.deleteRelationships(FILTER, options);

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
      client.deleteRelationships(FILTER, SpiceDBClient.DeleteOptions.none().withLimit(1));

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
    Filter mustFilter = Filter.of("document").withResourceID("doc1").withRelation("owner");

    try (TestServers servers = TestServers.start(service)) {
      SpiceDBClient client = servers.client();
      var options = SpiceDBClient.DeleteOptions.none().withMustMatch(mustFilter).withLimit(5);
      String revision = client.deleteRelationships(FILTER, options);

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

  @Test
  void limitMustBePositive() {
    assertThrows(
        IllegalArgumentException.class, () -> SpiceDBClient.DeleteOptions.none().withLimit(0));
    assertThrows(
        IllegalArgumentException.class, () -> SpiceDBClient.DeleteOptions.none().withLimit(-1));
  }

  @Test
  void noneHasEmptyPreconditionsAndNullLimit() {
    var options = SpiceDBClient.DeleteOptions.none();
    assertTrue(options.mustMatch().isEmpty());
    assertTrue(options.mustNotMatch().isEmpty());
    assertNull(options.limit());
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
    var mustMatch = List.of(Filter.of("document").withResourceID("doc1"));
    var mustNotMatch = List.of(Filter.of("document").withResourceID("doc2"));

    var options = new SpiceDBClient.DeleteOptions(mustMatch, mustNotMatch, 500);

    assertEquals(mustMatch, options.mustMatch());
    assertEquals(mustNotMatch, options.mustNotMatch());
    assertEquals(500, options.limit());
    assertNull(options.timeout(), "three-arg form must mean no per-call deadline");

    // It must delegate to the canonical constructor, so normalization and validation still apply.
    var nulls = new SpiceDBClient.DeleteOptions(null, null, null);
    assertTrue(nulls.mustMatch().isEmpty());
    assertTrue(nulls.mustNotMatch().isEmpty());
    assertThrows(
        IllegalArgumentException.class, () -> new SpiceDBClient.DeleteOptions(null, null, 0));
  }
}
