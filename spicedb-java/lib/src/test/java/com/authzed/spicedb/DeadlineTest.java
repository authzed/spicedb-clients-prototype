package com.authzed.spicedb;

import static org.junit.jupiter.api.Assertions.*;

import build.buf.gen.authzed.api.v1.CheckBulkPermissionsResponse;
import build.buf.gen.authzed.api.v1.ObjectReference;
import build.buf.gen.authzed.api.v1.PermissionsServiceGrpc;
import build.buf.gen.authzed.api.v1.ReadRelationshipsRequest;
import build.buf.gen.authzed.api.v1.ReadRelationshipsResponse;
import build.buf.gen.authzed.api.v1.Relationship;
import build.buf.gen.authzed.api.v1.SubjectReference;
import com.authzed.spicedb.errors.DeadlineExceededException;
import io.grpc.Context;
import io.grpc.ManagedChannel;
import io.grpc.Server;
import io.grpc.inprocess.InProcessChannelBuilder;
import io.grpc.inprocess.InProcessServerBuilder;
import io.grpc.stub.StreamObserver;
import java.time.Duration;
import java.util.List;
import java.util.concurrent.Callable;
import java.util.concurrent.ExecutionException;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.Future;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.TimeoutException;
import org.junit.jupiter.api.Test;

/**
 * Call-deadline enforcement. Root DESIGN.md, "RULE: A unary call must have a deadline".
 *
 * <p>Runs a real in-process gRPC server (grpc-java's in-process transport, same harness style as
 * {@link TestServers}) whose handlers deliberately stall, so these tests exercise real {@code
 * withDeadlineAfter} enforcement end to end -- a canned/immediate mock response can't prove a
 * deadline is actually enforced, since grpc's deadline machinery lives below the handler.
 *
 * <p>Every stalling handler self-bounds its stall to {@link #STALL_MS} rather than blocking
 * forever: long enough to dwarf the tiny per-test deadlines below (proving enforcement, not
 * luck). Each call is additionally run on a background thread and joined with {@link
 * Future#get(long, TimeUnit)}, which fails the test (instead of hanging it, and CI along with it)
 * if a regression reintroduces an unbounded call.
 */
class DeadlineTest {

  private static final long STALL_MS = 2000;
  private static final long WATCHDOG_SECONDS = 10;

  @Test
  void defaultTimeoutIsThirtySeconds() {
    assertEquals(Duration.ofSeconds(30), SpiceDBClient.DEFAULT_TIMEOUT);
  }

  @Test
  void unaryCallAgainstStubThatNeverRespondsTimesOutWithDeadlineExceeded() throws Exception {
    var service =
        new PermissionsServiceGrpc.PermissionsServiceImplBase() {
          @Override
          public void checkBulkPermissions(
              build.buf.gen.authzed.api.v1.CheckBulkPermissionsRequest request,
              StreamObserver<CheckBulkPermissionsResponse> responseObserver) {
            waitOutStallOrCancellation(STALL_MS);
            responseObserver.onNext(CheckBulkPermissionsResponse.getDefaultInstance());
            responseObserver.onCompleted();
          }
        };

    try (InProcessHarness harness = InProcessHarness.start(service, Duration.ofMillis(200))) {
      SpiceDBClient client = harness.client();
      var rel = com.authzed.spicedb.Relationship.of("document", "doc1", "view", "user", "alice");

      long start = System.nanoTime();
      Throwable thrown =
          runWithWatchdog(() -> client.checkPermission(Consistency.full(), "view", rel));
      long elapsedMs = (System.nanoTime() - start) / 1_000_000;

      assertInstanceOf(DeadlineExceededException.class, thrown, "got: " + thrown);
      assertTrue(
          elapsedMs < STALL_MS,
          "the call must fail at the ~200ms client default, not wait out the server's "
              + STALL_MS
              + "ms stall (elapsed="
              + elapsedMs
              + "ms)");
    }
  }

  @Test
  void perCallTimeoutOverridesMuchLargerClientDefault() throws Exception {
    var service =
        new PermissionsServiceGrpc.PermissionsServiceImplBase() {
          @Override
          public void checkBulkPermissions(
              build.buf.gen.authzed.api.v1.CheckBulkPermissionsRequest request,
              StreamObserver<CheckBulkPermissionsResponse> responseObserver) {
            waitOutStallOrCancellation(STALL_MS);
            responseObserver.onNext(CheckBulkPermissionsResponse.getDefaultInstance());
            responseObserver.onCompleted();
          }
        };

    // Client default is larger than the server's stall -- if the per-call override did not
    // take effect, this call would not fail quickly.
    try (InProcessHarness harness =
        InProcessHarness.start(service, Duration.ofMillis(STALL_MS * 10))) {
      SpiceDBClient client = harness.client();
      var rel = com.authzed.spicedb.Relationship.of("document", "doc1", "view", "user", "alice");

      long start = System.nanoTime();
      Throwable thrown =
          runWithWatchdog(
              () ->
                  client.checkPermission(
                      Consistency.full(), "view", rel, Duration.ofMillis(200)));
      long elapsedMs = (System.nanoTime() - start) / 1_000_000;

      assertInstanceOf(DeadlineExceededException.class, thrown, "got: " + thrown);
      assertTrue(
          elapsedMs < STALL_MS,
          "the per-call timeout=200ms must override the large client default (elapsed="
              + elapsedMs
              + "ms)");
    }
  }

  @Test
  void streamingCallDoesNotInheritTheUnaryDefault() throws Exception {
    var service =
        new PermissionsServiceGrpc.PermissionsServiceImplBase() {
          @Override
          public void readRelationships(
              ReadRelationshipsRequest request,
              StreamObserver<ReadRelationshipsResponse> responseObserver) {
            waitOutStallOrCancellation(STALL_MS);
            responseObserver.onNext(
                ReadRelationshipsResponse.newBuilder()
                    .setRelationship(
                        Relationship.newBuilder()
                            .setResource(
                                ObjectReference.newBuilder()
                                    .setObjectType("document")
                                    .setObjectId("a")
                                    .build())
                            .setRelation("viewer")
                            .setSubject(
                                SubjectReference.newBuilder()
                                    .setObject(
                                        ObjectReference.newBuilder()
                                            .setObjectType("user")
                                            .setObjectId("jimmy")
                                            .build())
                                    .build())
                            .build())
                    .build());
            responseObserver.onCompleted();
          }
        };

    // defaultTimeout is far smaller than the server's stall. If readRelationships inherited it,
    // this would throw DeadlineExceededException instead of yielding the item.
    try (InProcessHarness harness = InProcessHarness.start(service, Duration.ofMillis(100))) {
      SpiceDBClient client = harness.client();

      long start = System.nanoTime();
      List<com.authzed.spicedb.Relationship> items =
          runOrThrow(
              () -> {
                try (var stream =
                    client.readRelationships(Consistency.full(), Filter.of("document"))) {
                  return stream.toList();
                }
              });
      long elapsedMs = (System.nanoTime() - start) / 1_000_000;

      assertEquals(1, items.size());
      assertEquals("a", items.get(0).resourceID());
      assertTrue(
          elapsedMs >= STALL_MS,
          "the stream must outlive the tiny unary default -- it should have waited out the "
              + "server's "
              + STALL_MS
              + "ms stall (elapsed="
              + elapsedMs
              + "ms)");
    }
  }

  /**
   * Simulates a wedged server: blocks for up to {@code ms}, but polls {@link Context#isCancelled}
   * every 10ms and returns early once the client gives up (deadline expiry propagates a
   * cancellation to the server-side {@link Context}). Without this, the handler would sleep the
   * full {@code ms} regardless of the client's much shorter deadline, and grpc's channel/server
   * shutdown (used in {@link InProcessHarness#close}) would block for several extra seconds
   * waiting for this handler thread to notice and finish -- these tests don't need to pay that
   * cost to prove the CLIENT enforces its deadline.
   */
  private static void waitOutStallOrCancellation(long ms) {
    Context context = Context.current();
    long deadlineNanos = System.nanoTime() + ms * 1_000_000L;
    while (System.nanoTime() < deadlineNanos) {
      if (context.isCancelled()) return;
      try {
        Thread.sleep(10);
      } catch (InterruptedException e) {
        Thread.currentThread().interrupt();
        return;
      }
    }
  }

  /**
   * Runs {@code call} on a background thread and waits up to {@link #WATCHDOG_SECONDS}, failing
   * the test (instead of hanging it, and CI along with it) if a regression reintroduces an
   * unbounded call. Returns the exception the call threw -- callers use this when they expect a
   * failure.
   */
  private static <T> Throwable runWithWatchdog(Callable<T> call) throws Exception {
    ExecutorService executor = Executors.newSingleThreadExecutor();
    try {
      Future<T> future = executor.submit(call);
      try {
        T result = future.get(WATCHDOG_SECONDS, TimeUnit.SECONDS);
        fail("expected the call to throw, but it returned: " + result);
        return null; // unreachable
      } catch (ExecutionException e) {
        return e.getCause();
      } catch (TimeoutException e) {
        fail(
            "call did not return within "
                + WATCHDOG_SECONDS
                + "s -- deadline enforcement regressed");
        return null; // unreachable
      }
    } finally {
      executor.shutdownNow();
    }
  }

  /** As {@link #runWithWatchdog}, for a call expected to SUCCEED -- returns its result. */
  private static <T> T runOrThrow(Callable<T> call) throws Exception {
    ExecutorService executor = Executors.newSingleThreadExecutor();
    try {
      Future<T> future = executor.submit(call);
      try {
        return future.get(WATCHDOG_SECONDS, TimeUnit.SECONDS);
      } catch (ExecutionException e) {
        if (e.getCause() instanceof Exception ex) throw ex;
        throw e;
      } catch (TimeoutException e) {
        fail(
            "call did not return within "
                + WATCHDOG_SECONDS
                + "s -- deadline enforcement regressed");
        return null; // unreachable
      }
    } finally {
      executor.shutdownNow();
    }
  }

  /**
   * Minimal in-process gRPC harness with an explicit {@code defaultTimeout}, distinct from {@link
   * TestServers} (which always uses {@link SpiceDBClient#DEFAULT_TIMEOUT}) so these tests can
   * exercise tiny/large timeouts without changing shared test infra used elsewhere.
   */
  private static final class InProcessHarness implements AutoCloseable {
    private final Server server;
    private final ManagedChannel channel;
    private final SpiceDBClient client;

    private InProcessHarness(Server server, ManagedChannel channel, SpiceDBClient client) {
      this.server = server;
      this.channel = channel;
      this.client = client;
    }

    static InProcessHarness start(io.grpc.BindableService service, Duration defaultTimeout)
        throws java.io.IOException {
      String name = InProcessServerBuilder.generateName();
      Server server = InProcessServerBuilder.forName(name).addService(service).build().start();
      ManagedChannel channel = InProcessChannelBuilder.forName(name).build();
      SpiceDBClient client = SpiceDBClient.forChannel(channel, defaultTimeout);
      return new InProcessHarness(server, channel, client);
    }

    SpiceDBClient client() {
      return client;
    }

    /**
     * Shuts the channel and server down forcibly (not gracefully): a deadline-exceeded call
     * leaves the underlying in-process stream in a state where {@code SpiceDBClient#close()}'s
     * graceful {@code channel.awaitTermination(5, SECONDS)} can block for the full 5s even though
     * the RPC itself has long since finished from the client's point of view -- these short-lived,
     * single-test channels don't need graceful drain, only prompt reclamation.
     */
    @Override
    public void close() {
      channel.shutdownNow();
      server.shutdownNow();
      try {
        channel.awaitTermination(500, TimeUnit.MILLISECONDS);
        server.awaitTermination(500, TimeUnit.MILLISECONDS);
      } catch (InterruptedException e) {
        Thread.currentThread().interrupt();
      }
    }
  }
}
