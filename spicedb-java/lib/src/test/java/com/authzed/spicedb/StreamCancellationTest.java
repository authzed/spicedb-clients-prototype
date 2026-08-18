package com.authzed.spicedb;

import static org.junit.jupiter.api.Assertions.*;

import build.buf.gen.authzed.api.v1.PermissionsServiceGrpc;
import build.buf.gen.authzed.api.v1.ReadRelationshipsRequest;
import build.buf.gen.authzed.api.v1.ReadRelationshipsResponse;
import build.buf.gen.authzed.api.v1.RelationshipUpdate;
import build.buf.gen.authzed.api.v1.WatchRequest;
import build.buf.gen.authzed.api.v1.WatchResponse;
import build.buf.gen.authzed.api.v1.WatchServiceGrpc;
import io.grpc.stub.ServerCallStreamObserver;
import io.grpc.stub.StreamObserver;
import java.io.IOException;
import java.util.Iterator;
import java.util.List;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.TimeUnit;
import java.util.stream.Stream;
import org.junit.jupiter.api.Test;

/**
 * Proves that closing a lazy streaming {@link SpiceDBClient} {@link Stream} cancels the underlying
 * gRPC server-streaming call, rather than leaving it open server-side.
 *
 * <p>Each mock service holds its RPC open indefinitely (sends one item, never calls {@code
 * onCompleted}/{@code onError}) and registers {@link ServerCallStreamObserver#setOnCancelHandler}
 * so the test can observe server-side cancellation triggered by {@code stream.close()}.
 */
class StreamCancellationTest {

  // ---------------------------------------------------------------------
  // updates (watch) — a single item is pulled per hasNext()/next() call (no
  // eager whole-page buffering), so we can deterministically prove
  // cancellation on a single thread: consume one item while the RPC is
  // still open, close(), and assert the server observed a cancel.
  // ---------------------------------------------------------------------

  @Test
  void closingUpdatesStreamCancelsUnderlyingCall() throws IOException, InterruptedException {
    var cancelLatch = new CountDownLatch(1);
    var service =
        new WatchServiceGrpc.WatchServiceImplBase() {
          @Override
          public void watch(WatchRequest request, StreamObserver<WatchResponse> responseObserver) {
            var scso = (ServerCallStreamObserver<WatchResponse>) responseObserver;
            scso.setOnCancelHandler(cancelLatch::countDown);
            scso.onNext(
                WatchResponse.newBuilder()
                    .addUpdates(
                        RelationshipUpdate.newBuilder()
                            .setOperation(RelationshipUpdate.Operation.OPERATION_TOUCH)
                            .setRelationship(
                                SpiceDBClient.toProtoRelationship(
                                    Relationship.of("document", "doc1", "viewer", "user", "alice")))
                            .build())
                    .build());
            // Never call onCompleted/onError — hold the stream open so we can prove close()
            // cancels it rather than the test merely observing natural completion.
          }
        };

    try (TestServers servers = TestServers.start(service)) {
      SpiceDBClient client = servers.client();
      try (Stream<SpiceDBClient.WatchEvent> stream = client.updates(List.of("document"), null)) {
        Iterator<SpiceDBClient.WatchEvent> iterator = stream.iterator();
        assertTrue(iterator.hasNext());
        SpiceDBClient.WatchEvent first = iterator.next();
        assertEquals("doc1", first.updates().get(0).relationship().resourceID());

        // Stream is still open here (server never completed it). Closing must cancel the call.
      }

      assertTrue(
          cancelLatch.await(2, TimeUnit.SECONDS),
          "server did not observe cancellation after stream.close()");
    }
  }

  // ---------------------------------------------------------------------
  // readRelationships (backed by paginatedRelationshipStream) — each page is
  // drained eagerly and synchronously inside fetchNextPage(), so from the
  // consumer's point of view the RPC is only "in flight" while a page fetch
  // is blocked waiting for more items/completion. We hold the first page
  // open (one item sent, never completed) and close() from another thread
  // while the fetch is blocked, proving the per-page attach/detach wiring
  // also propagates cancellation (not just the single-call case above).
  // ---------------------------------------------------------------------

  @Test
  void closingReadRelationshipsStreamCancelsInFlightPageFetch()
      throws IOException, InterruptedException {
    var cancelLatch = new CountDownLatch(1);
    var firstItemSent = new CountDownLatch(1);
    var service =
        new PermissionsServiceGrpc.PermissionsServiceImplBase() {
          @Override
          public void readRelationships(
              ReadRelationshipsRequest request,
              StreamObserver<ReadRelationshipsResponse> responseObserver) {
            var scso = (ServerCallStreamObserver<ReadRelationshipsResponse>) responseObserver;
            scso.setOnCancelHandler(cancelLatch::countDown);
            scso.onNext(
                ReadRelationshipsResponse.newBuilder()
                    .setRelationship(
                        SpiceDBClient.toProtoRelationship(
                            Relationship.of("document", "doc1", "viewer", "user", "alice")))
                    .build());
            firstItemSent.countDown();
            // Never call onCompleted — the page never "finishes", so the client's page-drain
            // loop stays blocked waiting for a 2nd item/end-of-stream, exactly as it would for a
            // slow/large page in production.
          }
        };

    try (TestServers servers = TestServers.start(service)) {
      SpiceDBClient client = servers.client();
      Stream<Relationship> stream =
          client.readRelationships(Consistency.full(), Filter.of("document"));
      Iterator<Relationship> iterator = stream.iterator();

      Thread fetchThread =
          new Thread(
              () -> {
                try {
                  iterator.hasNext();
                } catch (RuntimeException expected) {
                  // Expected once the main thread cancels via stream.close() below.
                }
              });
      fetchThread.start();

      assertTrue(firstItemSent.await(2, TimeUnit.SECONDS), "server never sent the first item");
      // Give the client's blocking iterator a moment to reach its 2nd, blocking hasNext() call.
      // (Not required for correctness — cancellation is delivered to the call regardless of
      // exactly when it's polled — but it makes the test's intent unambiguous.)
      Thread.sleep(200);

      stream.close();

      assertTrue(
          cancelLatch.await(2, TimeUnit.SECONDS),
          "server did not observe cancellation after stream.close()");
      fetchThread.join(2000);
      assertFalse(fetchThread.isAlive(), "fetch thread should have unblocked after cancellation");
    }
  }
}
