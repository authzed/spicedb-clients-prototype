package com.authzed.spicedb;

import static org.junit.jupiter.api.Assertions.*;

import build.buf.gen.authzed.api.v1.ExportBulkRelationshipsRequest;
import build.buf.gen.authzed.api.v1.ExportBulkRelationshipsResponse;
import build.buf.gen.authzed.api.v1.PermissionsServiceGrpc;
import io.grpc.stub.StreamObserver;
import java.io.IOException;
import java.time.Duration;
import java.util.Iterator;
import java.util.stream.Stream;
import org.junit.jupiter.api.Test;

/**
 * Proves that {@link SpiceDBClient#exportRelationships} yields its first item before the underlying
 * server stream completes, instead of buffering the whole export into memory first.
 *
 * <p>{@code ExportBulkRelationships}' {@code optional_limit} bounds the number of relationships in
 * a single response MESSAGE, unlike every other paginated RPC's {@code optional_limit}, which
 * bounds the WHOLE stream. A single {@code ExportBulkRelationships} call keeps streaming further
 * response messages until the entire dataset has been sent -- it does not end after one "page". The
 * mock service below exploits exactly this: it sends ONE response message and then leaves the
 * stream open (never calls {@code onCompleted()}), simulating an export that has far more data
 * still to come. Before this fix, {@code exportRelationships}'s internal {@code fetchNextPage()}
 * looped on {@code serverStream.hasNext()} until the server closed the stream -- against this mock
 * service, that loop would block forever waiting for a completion that never arrives, so even the
 * FIRST relationship would never reach the caller. The fix pulls exactly one response message per
 * internal refill, so the first item is available immediately regardless of whether the server has
 * finished.
 */
class ExportRelationshipsStreamingTest {

  private static Relationship aRelationship(String id) {
    return Relationship.of("document", id, "viewer", "user", "alice");
  }

  @Test
  void yieldsFirstItemBeforeTheServerStreamCompletes() throws IOException {
    var service =
        new PermissionsServiceGrpc.PermissionsServiceImplBase() {
          @Override
          public void exportBulkRelationships(
              ExportBulkRelationshipsRequest request,
              StreamObserver<ExportBulkRelationshipsResponse> responseObserver) {
            // Sends exactly one relationship, then deliberately never calls
            // onCompleted() (or onError()) -- the stream is left open, as a
            // real export with more data still in flight would be. A
            // buffer-the-whole-stream-first implementation can never
            // produce this item, since "the whole stream" never finishes.
            responseObserver.onNext(
                ExportBulkRelationshipsResponse.newBuilder()
                    .addRelationships(SpiceDBClient.toProtoRelationship(aRelationship("doc1")))
                    .build());
          }
        };

    try (TestServers servers = TestServers.start(service)) {
      SpiceDBClient client = servers.client();
      try (Stream<Relationship> stream =
          client.exportRelationships(Consistency.full(), Filter.of("document"))) {
        Iterator<Relationship> iterator = stream.iterator();

        // assertTimeoutPreemptively runs this on a separate thread and
        // aborts (failing the test) if it doesn't complete in time, rather
        // than hanging the whole test run the way a plain blocking call
        // would if the bug were still present.
        Relationship first =
            assertTimeoutPreemptively(
                Duration.ofSeconds(5),
                () -> {
                  assertTrue(iterator.hasNext(), "expected a first item to be available");
                  return iterator.next();
                },
                "the first item must be yielded without waiting for the (never-completing) "
                    + "server stream to finish -- if this times out, exportRelationships is "
                    + "buffering the whole stream before yielding anything");

        assertEquals("doc1", first.resourceID());
      }
    }
  }
}
