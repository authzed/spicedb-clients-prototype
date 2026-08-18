package com.authzed.spicedb;

import static org.junit.jupiter.api.Assertions.*;

import build.buf.gen.authzed.api.v1.RelationshipUpdate;
import build.buf.gen.authzed.api.v1.WatchKind;
import build.buf.gen.authzed.api.v1.WatchRequest;
import build.buf.gen.authzed.api.v1.WatchResponse;
import build.buf.gen.authzed.api.v1.WatchServiceGrpc;
import build.buf.gen.authzed.api.v1.ZedToken;
import io.grpc.stub.StreamObserver;
import java.io.IOException;
import java.util.Iterator;
import java.util.List;
import java.util.concurrent.atomic.AtomicReference;
import java.util.stream.Stream;
import org.junit.jupiter.api.Test;

/**
 * A watch stream that dies cannot be correctly resumed unless the client surfaces {@code
 * changes_through} (proto: "This token can be used in a subsequent WatchRequest to resume
 * watching from this point"), and cannot survive an idle-timeout proxy unless the client can
 * request {@code WATCH_KIND_INCLUDE_CHECKPOINTS}. These tests exercise both.
 */
class WatchResumabilityTest {

  @Test
  void watchEventExposesUsableResumeToken() throws IOException {
    var service =
        new WatchServiceGrpc.WatchServiceImplBase() {
          @Override
          public void watch(WatchRequest request, StreamObserver<WatchResponse> responseObserver) {
            responseObserver.onNext(
                WatchResponse.newBuilder()
                    .setChangesThrough(ZedToken.newBuilder().setToken("resume-me").build())
                    .build());
            responseObserver.onCompleted();
          }
        };

    try (TestServers servers = TestServers.start(service)) {
      SpiceDBClient client = servers.client();
      try (Stream<SpiceDBClient.WatchEvent> stream = client.updates(List.of("document"), null)) {
        List<SpiceDBClient.WatchEvent> events = stream.toList();
        assertEquals(1, events.size());
        assertEquals("resume-me", events.get(0).changesThrough());
      }
    }
  }

  @Test
  void updatesWithoutIncludeCheckpointsRequestsNoUpdateKinds() throws IOException {
    var seenRequest = new AtomicReference<WatchRequest>();
    var service =
        new WatchServiceGrpc.WatchServiceImplBase() {
          @Override
          public void watch(WatchRequest request, StreamObserver<WatchResponse> responseObserver) {
            seenRequest.set(request);
            responseObserver.onCompleted();
          }
        };

    try (TestServers servers = TestServers.start(service)) {
      SpiceDBClient client = servers.client();
      try (Stream<SpiceDBClient.WatchEvent> stream = client.updates(List.of("document"), null)) {
        stream.toList();
      }
    }

    assertNotNull(seenRequest.get());
    assertEquals(0, seenRequest.get().getOptionalUpdateKindsCount());
  }

  @Test
  void includeCheckpointsReachesTheWire() throws IOException {
    var seenRequest = new AtomicReference<WatchRequest>();
    var service =
        new WatchServiceGrpc.WatchServiceImplBase() {
          @Override
          public void watch(WatchRequest request, StreamObserver<WatchResponse> responseObserver) {
            seenRequest.set(request);
            responseObserver.onCompleted();
          }
        };

    try (TestServers servers = TestServers.start(service)) {
      SpiceDBClient client = servers.client();
      try (Stream<SpiceDBClient.WatchEvent> stream =
          client.updates(List.of("document"), null, true)) {
        stream.toList();
      }
    }

    assertNotNull(seenRequest.get());
    List<WatchKind> kinds = seenRequest.get().getOptionalUpdateKindsList();
    assertTrue(kinds.contains(WatchKind.WATCH_KIND_INCLUDE_CHECKPOINTS));
    // Requesting checkpoints must not silently drop relationship updates -- optionalUpdateKinds
    // is empty-means-default, so a non-empty list is the exact set requested.
    assertTrue(kinds.contains(WatchKind.WATCH_KIND_INCLUDE_RELATIONSHIP_UPDATES));
  }

  @Test
  void checkpointEventIsDistinguishableFromUpdateEvent() throws IOException {
    var service =
        new WatchServiceGrpc.WatchServiceImplBase() {
          @Override
          public void watch(WatchRequest request, StreamObserver<WatchResponse> responseObserver) {
            responseObserver.onNext(
                WatchResponse.newBuilder()
                    .setChangesThrough(ZedToken.newBuilder().setToken("checkpoint-rev").build())
                    .setIsCheckpoint(true)
                    .build());
            responseObserver.onNext(
                WatchResponse.newBuilder()
                    .setChangesThrough(ZedToken.newBuilder().setToken("update-rev").build())
                    .addUpdates(
                        RelationshipUpdate.newBuilder()
                            .setOperation(RelationshipUpdate.Operation.OPERATION_TOUCH)
                            .setRelationship(
                                SpiceDBClient.toProtoRelationship(
                                    Relationship.of("document", "doc1", "viewer", "user", "alice")))
                            .build())
                    .build());
            responseObserver.onCompleted();
          }
        };

    try (TestServers servers = TestServers.start(service)) {
      SpiceDBClient client = servers.client();
      try (Stream<SpiceDBClient.WatchEvent> stream =
          client.updates(List.of("document"), null, true)) {
        Iterator<SpiceDBClient.WatchEvent> iterator = stream.iterator();

        assertTrue(iterator.hasNext());
        SpiceDBClient.WatchEvent checkpoint = iterator.next();
        assertTrue(checkpoint.isCheckpoint());
        assertTrue(checkpoint.updates().isEmpty());
        assertEquals("checkpoint-rev", checkpoint.changesThrough());

        assertTrue(iterator.hasNext());
        SpiceDBClient.WatchEvent update = iterator.next();
        assertFalse(update.isCheckpoint());
        assertEquals(1, update.updates().size());
        assertEquals("update-rev", update.changesThrough());

        assertFalse(iterator.hasNext());
      }
    }
  }
}
