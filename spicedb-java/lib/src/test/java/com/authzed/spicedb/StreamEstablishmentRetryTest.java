package com.authzed.spicedb;

import static org.junit.jupiter.api.Assertions.*;

import build.buf.gen.authzed.api.v1.ExportBulkRelationshipsRequest;
import build.buf.gen.authzed.api.v1.ExportBulkRelationshipsResponse;
import build.buf.gen.authzed.api.v1.LookupPermissionship;
import build.buf.gen.authzed.api.v1.LookupResourcesRequest;
import build.buf.gen.authzed.api.v1.LookupResourcesResponse;
import build.buf.gen.authzed.api.v1.LookupSubjectsRequest;
import build.buf.gen.authzed.api.v1.LookupSubjectsResponse;
import build.buf.gen.authzed.api.v1.PermissionsServiceGrpc;
import build.buf.gen.authzed.api.v1.ReadRelationshipsRequest;
import build.buf.gen.authzed.api.v1.ReadRelationshipsResponse;
import build.buf.gen.authzed.api.v1.RelationshipUpdate;
import build.buf.gen.authzed.api.v1.ResolvedSubject;
import build.buf.gen.authzed.api.v1.WatchRequest;
import build.buf.gen.authzed.api.v1.WatchResponse;
import build.buf.gen.authzed.api.v1.WatchServiceGrpc;
import com.authzed.spicedb.errors.SpiceDBException;
import io.grpc.Status;
import io.grpc.stub.StreamObserver;
import java.io.IOException;
import java.util.Iterator;
import java.util.List;
import java.util.concurrent.atomic.AtomicInteger;
import java.util.stream.Stream;
import org.junit.jupiter.api.Test;

/**
 * Proves that the 5 streaming methods on {@link SpiceDBClient} ({@code readRelationships}, {@code
 * lookupResources}, {@code lookupSubjects}, {@code exportRelationships}, {@code updates}) make
 * stream/page ESTABLISHMENT effectively retryable on transient gRPC errors ({@code UNAVAILABLE},
 * {@code ABORTED}) — and, just as importantly, that a transient error
 * occurring AFTER an item has already been read from the current stream/page is NEVER retried
 * (which would risk replaying/duplicating that item for the caller).
 *
 * <p>Each method gets two tests:
 *
 * <ul>
 *   <li><b>retries establishment</b>: the mock service fails the FIRST establishment attempt with a
 *       transient {@code UNAVAILABLE}, then succeeds on the second. Asserts the client ultimately
 *       yields the expected item(s) AND that the server observed exactly 2 establishment attempts
 *       (proving the retry is EFFECTIVE, not merely tolerated).
 *   <li><b>does not retry after an item is read</b>: the mock service successfully delivers at
 *       least one item, THEN fails with a transient {@code UNAVAILABLE}. Asserts the error is
 *       mapped to {@link SpiceDBException} and rethrown, AND that the server observed exactly 1
 *       attempt (proving no replay-risking re-establishment was attempted).
 * </ul>
 *
 * <p>Before this fix, wrapping only the blocking-stub CALL in {@code withRetry} never actually
 * retried anything for grpc-java's blocking server-streaming: the RPC's outcome only surfaces on
 * the returned iterator's first {@code hasNext()}/{@code next()}, which happens outside the retry
 * loop. Without {@code SpiceDBClient#openStreamWithRetry} folding that priming {@code hasNext()}
 * into the retried unit of work, the first (RED) test in each pair would see {@code attempts == 1}
 * and the transient error escaping unmapped-and-unretried.
 */
class StreamEstablishmentRetryTest {

  private static Relationship aRelationship() {
    return Relationship.of("document", "doc1", "viewer", "user", "alice");
  }

  // ---------------------------------------------------------------------
  // readRelationships (backed by paginatedRelationshipStream)
  // ---------------------------------------------------------------------

  @Test
  void readRelationshipsRetriesEstablishmentOnTransientError() throws IOException {
    var attempts = new AtomicInteger(0);
    var service =
        new PermissionsServiceGrpc.PermissionsServiceImplBase() {
          @Override
          public void readRelationships(
              ReadRelationshipsRequest request,
              StreamObserver<ReadRelationshipsResponse> responseObserver) {
            if (attempts.incrementAndGet() == 1) {
              responseObserver.onError(
                  Status.UNAVAILABLE.withDescription("try again").asRuntimeException());
              return;
            }
            responseObserver.onNext(
                ReadRelationshipsResponse.newBuilder()
                    .setRelationship(SpiceDBClient.toProtoRelationship(aRelationship()))
                    .build());
            responseObserver.onCompleted();
          }
        };

    try (TestServers servers = TestServers.start(service)) {
      SpiceDBClient client = servers.client();
      List<Relationship> results;
      try (Stream<Relationship> stream =
          client.readRelationships(Consistency.full(), Filter.of("document"))) {
        results = stream.toList();
      }

      assertEquals(1, results.size());
      assertEquals("doc1", results.get(0).resourceID());
      assertEquals(2, attempts.get(), "establishment retry should have made a 2nd attempt");
    }
  }

  @Test
  void readRelationshipsDoesNotRetryTransientErrorAfterItemRead() throws IOException {
    var attempts = new AtomicInteger(0);
    var service =
        new PermissionsServiceGrpc.PermissionsServiceImplBase() {
          @Override
          public void readRelationships(
              ReadRelationshipsRequest request,
              StreamObserver<ReadRelationshipsResponse> responseObserver) {
            attempts.incrementAndGet();
            responseObserver.onNext(
                ReadRelationshipsResponse.newBuilder()
                    .setRelationship(SpiceDBClient.toProtoRelationship(aRelationship()))
                    .build());
            responseObserver.onError(
                Status.UNAVAILABLE.withDescription("mid-page failure").asRuntimeException());
          }
        };

    try (TestServers servers = TestServers.start(service)) {
      SpiceDBClient client = servers.client();
      try (Stream<Relationship> stream =
          client.readRelationships(Consistency.full(), Filter.of("document"))) {
        assertThrows(SpiceDBException.class, () -> stream.toList());
      }
      assertEquals(1, attempts.get(), "a transient error after an item was read must not retry");
    }
  }

  // ---------------------------------------------------------------------
  // lookupResources
  // ---------------------------------------------------------------------

  @Test
  void lookupResourcesRetriesEstablishmentOnTransientError() throws IOException {
    var attempts = new AtomicInteger(0);
    var service =
        new PermissionsServiceGrpc.PermissionsServiceImplBase() {
          @Override
          public void lookupResources(
              LookupResourcesRequest request,
              StreamObserver<LookupResourcesResponse> responseObserver) {
            if (attempts.incrementAndGet() == 1) {
              responseObserver.onError(
                  Status.UNAVAILABLE.withDescription("try again").asRuntimeException());
              return;
            }
            responseObserver.onNext(
                LookupResourcesResponse.newBuilder()
                    .setResourceObjectId("doc1")
                    .setPermissionship(LookupPermissionship.LOOKUP_PERMISSIONSHIP_HAS_PERMISSION)
                    .build());
            responseObserver.onCompleted();
          }
        };

    try (TestServers servers = TestServers.start(service)) {
      SpiceDBClient client = servers.client();
      List<LookupResult.LookupResource> results;
      try (Stream<LookupResult.LookupResource> stream =
          client.lookupResources(Consistency.full(), "document", "view", "user", "alice")) {
        results = stream.toList();
      }

      assertEquals(1, results.size());
      assertEquals("doc1", results.get(0).resourceId());
      assertEquals(2, attempts.get(), "establishment retry should have made a 2nd attempt");
    }
  }

  @Test
  void lookupResourcesDoesNotRetryTransientErrorAfterItemRead() throws IOException {
    var attempts = new AtomicInteger(0);
    var service =
        new PermissionsServiceGrpc.PermissionsServiceImplBase() {
          @Override
          public void lookupResources(
              LookupResourcesRequest request,
              StreamObserver<LookupResourcesResponse> responseObserver) {
            attempts.incrementAndGet();
            responseObserver.onNext(
                LookupResourcesResponse.newBuilder()
                    .setResourceObjectId("doc1")
                    .setPermissionship(LookupPermissionship.LOOKUP_PERMISSIONSHIP_HAS_PERMISSION)
                    .build());
            responseObserver.onError(
                Status.UNAVAILABLE.withDescription("mid-page failure").asRuntimeException());
          }
        };

    try (TestServers servers = TestServers.start(service)) {
      SpiceDBClient client = servers.client();
      try (Stream<LookupResult.LookupResource> stream =
          client.lookupResources(Consistency.full(), "document", "view", "user", "alice")) {
        assertThrows(SpiceDBException.class, () -> stream.toList());
      }
      assertEquals(1, attempts.get(), "a transient error after an item was read must not retry");
    }
  }

  // ---------------------------------------------------------------------
  // lookupSubjects — single eager call, no cursor pagination
  // ---------------------------------------------------------------------

  @Test
  void lookupSubjectsRetriesEstablishmentOnTransientError() throws IOException {
    var attempts = new AtomicInteger(0);
    var service =
        new PermissionsServiceGrpc.PermissionsServiceImplBase() {
          @Override
          public void lookupSubjects(
              LookupSubjectsRequest request,
              StreamObserver<LookupSubjectsResponse> responseObserver) {
            if (attempts.incrementAndGet() == 1) {
              responseObserver.onError(
                  Status.UNAVAILABLE.withDescription("try again").asRuntimeException());
              return;
            }
            responseObserver.onNext(
                LookupSubjectsResponse.newBuilder()
                    .setSubject(
                        ResolvedSubject.newBuilder()
                            .setSubjectObjectId("alice")
                            .setPermissionship(
                                LookupPermissionship.LOOKUP_PERMISSIONSHIP_HAS_PERMISSION)
                            .build())
                    .build());
            responseObserver.onCompleted();
          }
        };

    try (TestServers servers = TestServers.start(service)) {
      SpiceDBClient client = servers.client();
      List<LookupResult.LookupSubject> results;
      try (Stream<LookupResult.LookupSubject> stream =
          client.lookupSubjects(Consistency.full(), "document", "doc1", "view", "user")) {
        results = stream.toList();
      }

      assertEquals(1, results.size());
      assertEquals("alice", results.get(0).subject().subjectId());
      assertEquals(2, attempts.get(), "establishment retry should have made a 2nd attempt");
    }
  }

  @Test
  void lookupSubjectsDoesNotRetryTransientErrorAfterItemRead() throws IOException {
    var attempts = new AtomicInteger(0);
    var service =
        new PermissionsServiceGrpc.PermissionsServiceImplBase() {
          @Override
          public void lookupSubjects(
              LookupSubjectsRequest request,
              StreamObserver<LookupSubjectsResponse> responseObserver) {
            attempts.incrementAndGet();
            responseObserver.onNext(
                LookupSubjectsResponse.newBuilder()
                    .setSubject(
                        ResolvedSubject.newBuilder()
                            .setSubjectObjectId("alice")
                            .setPermissionship(
                                LookupPermissionship.LOOKUP_PERMISSIONSHIP_HAS_PERMISSION)
                            .build())
                    .build());
            responseObserver.onError(
                Status.UNAVAILABLE.withDescription("mid-stream failure").asRuntimeException());
          }
        };

    try (TestServers servers = TestServers.start(service)) {
      SpiceDBClient client = servers.client();
      assertThrows(
          SpiceDBException.class,
          () -> client.lookupSubjects(Consistency.full(), "document", "doc1", "view", "user"));
      assertEquals(1, attempts.get(), "a transient error after an item was read must not retry");
    }
  }

  // ---------------------------------------------------------------------
  // exportRelationships
  // ---------------------------------------------------------------------

  @Test
  void exportRelationshipsRetriesEstablishmentOnTransientError() throws IOException {
    var attempts = new AtomicInteger(0);
    var service =
        new PermissionsServiceGrpc.PermissionsServiceImplBase() {
          @Override
          public void exportBulkRelationships(
              ExportBulkRelationshipsRequest request,
              StreamObserver<ExportBulkRelationshipsResponse> responseObserver) {
            if (attempts.incrementAndGet() == 1) {
              responseObserver.onError(
                  Status.UNAVAILABLE.withDescription("try again").asRuntimeException());
              return;
            }
            responseObserver.onNext(
                ExportBulkRelationshipsResponse.newBuilder()
                    .addRelationships(SpiceDBClient.toProtoRelationship(aRelationship()))
                    .build());
            responseObserver.onCompleted();
          }
        };

    try (TestServers servers = TestServers.start(service)) {
      SpiceDBClient client = servers.client();
      List<Relationship> results;
      try (Stream<Relationship> stream =
          client.exportRelationships(Consistency.full(), Filter.of("document"))) {
        results = stream.toList();
      }

      assertEquals(1, results.size());
      assertEquals("doc1", results.get(0).resourceID());
      assertEquals(2, attempts.get(), "establishment retry should have made a 2nd attempt");
    }
  }

  @Test
  void exportRelationshipsDoesNotRetryTransientErrorAfterItemRead() throws IOException {
    var attempts = new AtomicInteger(0);
    var service =
        new PermissionsServiceGrpc.PermissionsServiceImplBase() {
          @Override
          public void exportBulkRelationships(
              ExportBulkRelationshipsRequest request,
              StreamObserver<ExportBulkRelationshipsResponse> responseObserver) {
            attempts.incrementAndGet();
            responseObserver.onNext(
                ExportBulkRelationshipsResponse.newBuilder()
                    .addRelationships(SpiceDBClient.toProtoRelationship(aRelationship()))
                    .build());
            responseObserver.onError(
                Status.UNAVAILABLE.withDescription("mid-page failure").asRuntimeException());
          }
        };

    try (TestServers servers = TestServers.start(service)) {
      SpiceDBClient client = servers.client();
      try (Stream<Relationship> stream =
          client.exportRelationships(Consistency.full(), Filter.of("document"))) {
        assertThrows(SpiceDBException.class, () -> stream.toList());
      }
      assertEquals(1, attempts.get(), "a transient error after an item was read must not retry");
    }
  }

  // ---------------------------------------------------------------------
  // updates (watch) — establishment-only retry, never mid-watch. Unlike the
  // 4 methods above (which buffer a whole page/response internally before
  // exposing anything to the caller), updates() exposes one WatchResponse's
  // worth of updates at a time via a lazily-driven iterator, so a
  // successfully-read item IS visible to the caller before a later transient
  // error — proving the guard is "no retry once establishment succeeded",
  // matching the brief's "establishment only, never mid-watch".
  // ---------------------------------------------------------------------

  @Test
  void updatesRetriesEstablishmentOnTransientError() throws IOException {
    var attempts = new AtomicInteger(0);
    var service =
        new WatchServiceGrpc.WatchServiceImplBase() {
          @Override
          public void watch(WatchRequest request, StreamObserver<WatchResponse> responseObserver) {
            if (attempts.incrementAndGet() == 1) {
              responseObserver.onError(
                  Status.UNAVAILABLE.withDescription("try again").asRuntimeException());
              return;
            }
            responseObserver.onNext(
                WatchResponse.newBuilder()
                    .addUpdates(
                        RelationshipUpdate.newBuilder()
                            .setOperation(RelationshipUpdate.Operation.OPERATION_TOUCH)
                            .setRelationship(SpiceDBClient.toProtoRelationship(aRelationship()))
                            .build())
                    .build());
            responseObserver.onCompleted();
          }
        };

    try (TestServers servers = TestServers.start(service)) {
      SpiceDBClient client = servers.client();
      List<SpiceDBClient.WatchEvent> results;
      try (Stream<SpiceDBClient.WatchEvent> stream = client.updates(List.of("document"), null)) {
        results = stream.toList();
      }

      assertEquals(1, results.size());
      assertEquals(1, results.get(0).updates().size());
      assertEquals("doc1", results.get(0).updates().get(0).relationship().resourceID());
      assertEquals(2, attempts.get(), "establishment retry should have made a 2nd attempt");
    }
  }

  @Test
  void updatesDoesNotRetryTransientErrorAfterUpdateYielded() throws IOException {
    var attempts = new AtomicInteger(0);
    var service =
        new WatchServiceGrpc.WatchServiceImplBase() {
          @Override
          public void watch(WatchRequest request, StreamObserver<WatchResponse> responseObserver) {
            attempts.incrementAndGet();
            responseObserver.onNext(
                WatchResponse.newBuilder()
                    .addUpdates(
                        RelationshipUpdate.newBuilder()
                            .setOperation(RelationshipUpdate.Operation.OPERATION_TOUCH)
                            .setRelationship(SpiceDBClient.toProtoRelationship(aRelationship()))
                            .build())
                    .build());
            responseObserver.onError(
                Status.UNAVAILABLE.withDescription("mid-watch failure").asRuntimeException());
          }
        };

    try (TestServers servers = TestServers.start(service)) {
      SpiceDBClient client = servers.client();
      try (Stream<SpiceDBClient.WatchEvent> stream = client.updates(List.of("document"), null)) {
        Iterator<SpiceDBClient.WatchEvent> iterator = stream.iterator();

        assertTrue(iterator.hasNext());
        SpiceDBClient.WatchEvent first = iterator.next();
        assertEquals("doc1", first.updates().get(0).relationship().resourceID());

        assertThrows(SpiceDBException.class, iterator::hasNext);
      }
      assertEquals(
          1, attempts.get(), "a transient error after an update was yielded must not retry");
    }
  }
}
