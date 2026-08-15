package com.authzed.spicedb;

import static org.junit.jupiter.api.Assertions.*;

import build.buf.gen.authzed.api.v1.ExportBulkRelationshipsRequest;
import build.buf.gen.authzed.api.v1.ExportBulkRelationshipsResponse;
import build.buf.gen.authzed.api.v1.LookupResourcesRequest;
import build.buf.gen.authzed.api.v1.LookupResourcesResponse;
import build.buf.gen.authzed.api.v1.LookupSubjectsRequest;
import build.buf.gen.authzed.api.v1.LookupSubjectsResponse;
import build.buf.gen.authzed.api.v1.PermissionsServiceGrpc;
import build.buf.gen.authzed.api.v1.ReadRelationshipsRequest;
import build.buf.gen.authzed.api.v1.ReadRelationshipsResponse;
import build.buf.gen.authzed.api.v1.RelationshipUpdate;
import build.buf.gen.authzed.api.v1.WatchRequest;
import build.buf.gen.authzed.api.v1.WatchResponse;
import build.buf.gen.authzed.api.v1.WatchServiceGrpc;
import com.authzed.spicedb.errors.NotFoundException;
import io.grpc.Status;
import io.grpc.stub.StreamObserver;
import java.io.IOException;
import java.util.Iterator;
import java.util.List;
import java.util.stream.Stream;
import org.junit.jupiter.api.Test;

/**
 * Proves that all 5 streaming methods on {@link SpiceDBClient} map mid-stream gRPC errors to native
 * {@link com.authzed.spicedb.errors.SpiceDBException} subtypes, rather than leaking a raw {@link
 * io.grpc.StatusRuntimeException} to the consumer of the returned {@link Stream}.
 *
 * <p>Uses the reusable in-process gRPC harness ({@link TestServers}) to drive real gRPC framing
 * (not just unit-level mocks), so the assertions exercise the actual blocking-stub iteration path.
 */
class StreamingErrorMappingTest {

  // ---------------------------------------------------------------------
  // readRelationships (backed by paginatedRelationshipStream)
  // ---------------------------------------------------------------------

  @Test
  void readRelationshipsMapsImmediateStreamError() throws IOException {
    var service =
        new PermissionsServiceGrpc.PermissionsServiceImplBase() {
          @Override
          public void readRelationships(
              ReadRelationshipsRequest request,
              StreamObserver<ReadRelationshipsResponse> responseObserver) {
            responseObserver.onError(
                Status.NOT_FOUND.withDescription("no such relationships").asRuntimeException());
          }
        };

    try (TestServers servers = TestServers.start(service)) {
      SpiceDBClient client = servers.client();
      try (Stream<Relationship> stream =
          client.readRelationships(Consistency.full(), Filter.of("document"))) {
        assertThrows(NotFoundException.class, () -> stream.forEach(r -> {}));
      }
    }
  }

  // ---------------------------------------------------------------------
  // lookupResources
  // ---------------------------------------------------------------------

  @Test
  void lookupResourcesMapsImmediateStreamError() throws IOException {
    var service =
        new PermissionsServiceGrpc.PermissionsServiceImplBase() {
          @Override
          public void lookupResources(
              LookupResourcesRequest request,
              StreamObserver<LookupResourcesResponse> responseObserver) {
            responseObserver.onError(Status.NOT_FOUND.asRuntimeException());
          }
        };

    try (TestServers servers = TestServers.start(service)) {
      SpiceDBClient client = servers.client();
      try (Stream<LookupResult.LookupResource> stream =
          client.lookupResources(Consistency.full(), "document", "view", "user", "alice")) {
        assertThrows(NotFoundException.class, () -> stream.forEach(r -> {}));
      }
    }
  }

  // ---------------------------------------------------------------------
  // lookupSubjects
  // ---------------------------------------------------------------------

  @Test
  void lookupSubjectsMapsImmediateStreamError() throws IOException {
    var service =
        new PermissionsServiceGrpc.PermissionsServiceImplBase() {
          @Override
          public void lookupSubjects(
              LookupSubjectsRequest request,
              StreamObserver<LookupSubjectsResponse> responseObserver) {
            responseObserver.onError(Status.NOT_FOUND.asRuntimeException());
          }
        };

    try (TestServers servers = TestServers.start(service)) {
      SpiceDBClient client = servers.client();
      // lookupSubjects streams all results eagerly (no cursor pagination — see DESIGN.md), so the
      // mapped error surfaces from the call itself rather than from later stream iteration.
      assertThrows(
          NotFoundException.class,
          () -> client.lookupSubjects(Consistency.full(), "document", "doc1", "view", "user"));
    }
  }

  // ---------------------------------------------------------------------
  // exportRelationships
  // ---------------------------------------------------------------------

  @Test
  void exportRelationshipsMapsImmediateStreamError() throws IOException {
    var service =
        new PermissionsServiceGrpc.PermissionsServiceImplBase() {
          @Override
          public void exportBulkRelationships(
              ExportBulkRelationshipsRequest request,
              StreamObserver<ExportBulkRelationshipsResponse> responseObserver) {
            responseObserver.onError(Status.NOT_FOUND.asRuntimeException());
          }
        };

    try (TestServers servers = TestServers.start(service)) {
      SpiceDBClient client = servers.client();
      try (Stream<Relationship> stream =
          client.exportRelationships(Consistency.full(), Filter.of("document"))) {
        assertThrows(NotFoundException.class, () -> stream.forEach(r -> {}));
      }
    }
  }

  // ---------------------------------------------------------------------
  // updates (watch) — error surfaces AFTER a successful item, proving the
  // mapping applies mid-stream and not just on the very first poll.
  // ---------------------------------------------------------------------

  @Test
  void updatesMapsStreamErrorAfterSuccessfulItems() throws IOException {
    var service =
        new WatchServiceGrpc.WatchServiceImplBase() {
          @Override
          public void watch(WatchRequest request, StreamObserver<WatchResponse> responseObserver) {
            responseObserver.onNext(
                WatchResponse.newBuilder()
                    .addUpdates(
                        RelationshipUpdate.newBuilder()
                            .setOperation(RelationshipUpdate.Operation.OPERATION_TOUCH)
                            .setRelationship(
                                SpiceDBClient.toProtoRelationship(
                                    Relationship.of("document", "doc1", "viewer", "user", "alice")))
                            .build())
                    .build());
            responseObserver.onError(
                Status.NOT_FOUND.withDescription("watch failed").asRuntimeException());
          }
        };

    try (TestServers servers = TestServers.start(service)) {
      SpiceDBClient client = servers.client();
      try (Stream<SpiceDBClient.Update> stream = client.updates(List.of("document"), null)) {
        Iterator<SpiceDBClient.Update> iterator = stream.iterator();

        assertTrue(iterator.hasNext());
        SpiceDBClient.Update first = iterator.next();
        assertEquals("doc1", first.relationship().resourceID());

        assertThrows(NotFoundException.class, iterator::hasNext);
      }
    }
  }
}
