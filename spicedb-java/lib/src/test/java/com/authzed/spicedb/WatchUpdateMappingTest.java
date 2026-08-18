package com.authzed.spicedb;

import static org.junit.jupiter.api.Assertions.*;

import build.buf.gen.authzed.api.v1.RelationshipUpdate;
import build.buf.gen.authzed.api.v1.WatchRequest;
import build.buf.gen.authzed.api.v1.WatchResponse;
import build.buf.gen.authzed.api.v1.WatchServiceGrpc;
import io.grpc.stub.StreamObserver;
import java.io.IOException;
import java.util.Iterator;
import java.util.List;
import java.util.stream.Stream;
import org.junit.jupiter.api.Test;

/**
 * Proves that {@link SpiceDBClient#updateFromProto} maps an unrecognized watch {@code
 * RelationshipUpdate.Operation} — including {@code OPERATION_UNSPECIFIED} and any future wire
 * value this client does not know about — to {@link SpiceDBClient.UpdateOperation#UNSPECIFIED},
 * never to {@link SpiceDBClient.UpdateOperation#TOUCH}.
 *
 * <p>Root {@code DESIGN.md}, "RULE: A conversion that cannot preserve meaning must fail", clause
 * 2: server-supplied values the client does not recognise MUST NOT raise, and MUST map to the
 * safe, non-permissive default — never a grant, and never a write. Mapping an unrecognized
 * operation to {@code TOUCH} would let a cache or index mirror consuming the watch stream upsert
 * a relationship that may in fact have been deleted.
 */
class WatchUpdateMappingTest {

  @Test
  void unspecifiedOperationMapsToUnspecifiedNotTouch() throws IOException {
    var service =
        new WatchServiceGrpc.WatchServiceImplBase() {
          @Override
          public void watch(WatchRequest request, StreamObserver<WatchResponse> responseObserver) {
            responseObserver.onNext(
                WatchResponse.newBuilder()
                    .addUpdates(
                        RelationshipUpdate.newBuilder()
                            .setOperation(RelationshipUpdate.Operation.OPERATION_UNSPECIFIED)
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
      try (Stream<SpiceDBClient.Update> stream = client.updates(List.of("document"), null)) {
        Iterator<SpiceDBClient.Update> iterator = stream.iterator();
        assertTrue(iterator.hasNext());
        SpiceDBClient.Update update = iterator.next();

        assertEquals(SpiceDBClient.UpdateOperation.UNSPECIFIED, update.operation());
        assertNotEquals(SpiceDBClient.UpdateOperation.TOUCH, update.operation());
      }
    }
  }
}
