package com.authzed.spicedb;

import static org.junit.jupiter.api.Assertions.*;

import build.buf.gen.authzed.api.v1.DeleteRelationshipsRequest;
import build.buf.gen.authzed.api.v1.DeleteRelationshipsResponse;
import build.buf.gen.authzed.api.v1.PermissionsServiceGrpc;
import build.buf.gen.authzed.api.v1.Precondition;
import build.buf.gen.authzed.api.v1.ZedToken;
import io.grpc.stub.StreamObserver;
import java.io.IOException;
import java.util.ArrayList;
import java.util.List;
import org.junit.jupiter.api.Test;

/**
 * Tests for {@link SpiceDBClient#deleteRelationships(Filter, SpiceDBClient.DeleteOptions)} —
 * optional preconditions (must-match/must-not-match) and per-request page-size limit, threaded
 * into the auto-paging delete loop. Mirrors {@code spicedb-go}'s {@code client/relationships.go}
 * {@code WithDeleteMustMatch}/{@code WithDeleteMustNotMatch}/{@code WithDeleteLimit}.
 *
 * <p>Uses the in-process gRPC harness ({@link TestServers}) with a mock {@code
 * PermissionsService} that captures every outbound {@link DeleteRelationshipsRequest} so tests can
 * assert on exactly what was sent over the wire.
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
      assertEquals(10_000, req.getOptionalLimit());
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
      assertEquals(10_000, req.getOptionalLimit());
      assertTrue(req.getOptionalAllowPartialDeletions());
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
}
