package com.authzed.spicedb.examples;

import static com.authzed.spicedb.Consistency.*;
import static org.assertj.core.api.Assertions.*;

import com.authzed.spicedb.CheckResult;
import com.authzed.spicedb.Filter;
import com.authzed.spicedb.Relationship;
import com.authzed.spicedb.SpiceDBClient;
import com.authzed.spicedb.Transaction;
import com.authzed.spicedb.errors.DeadlineExceededException;
import java.io.IOException;
import java.net.InetAddress;
import java.net.ServerSocket;
import java.time.Duration;
import java.util.ArrayList;
import java.util.List;
import java.util.concurrent.Callable;
import java.util.concurrent.ExecutionException;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.Future;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.TimeoutException;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.Test;

/**
 * Demonstrates the client-level {@code Duration defaultTimeout} construction parameter, a per-call
 * {@code timeout} override, and that bulk import ({@link SpiceDBClient#importRelationships}) is a
 * client-streaming call that is NOT bounded by the unary default -- see root DESIGN.md, "RULE: A
 * unary call must have a deadline".
 *
 * <p>Unlike the other examples in this package, this test constructs its own {@link SpiceDBClient}
 * (rather than using {@link SpiceDBIntegrationTest}'s shared fixture) so it can exercise the
 * documented {@code Duration defaultTimeout} overload directly, against a real SpiceDB.
 *
 * <p>The failure that rule exists to close is a <em>wedged</em> server: one that accepts the
 * connection and then never answers. Nothing looks wrong at the transport level, so an unbounded
 * call hangs forever rather than erroring. The tests against a real SpiceDB below pass identically
 * whether or not the timeout ever reaches the wire, so the last two stand up a socket that behaves
 * exactly that way and require the call to come back {@link DeadlineExceededException} on the
 * caller's schedule.
 */
class CallDeadlinesTest {

  private static final String SCHEMA =
      """
        definition user {}

        definition document {
            relation viewer: user
            permission view = viewer
        }""";

  private SpiceDBClient client;

  @AfterEach
  void tearDown() {
    if (client != null) {
      client.close();
    }
  }

  @Test
  void defaultTimeoutConstructionParamAppliesEndToEnd() {
    // Duration defaultTimeout is the documented, real construction path -- not a mock -- so a
    // signature drift here (e.g. the overload silently disappearing) would fail this example, not
    // just a unit test against a stalling stub.
    client =
        SpiceDBClient.createPlaintext(
            SpiceDBIntegrationTest.ENDPOINT, SpiceDBIntegrationTest.TOKEN, Duration.ofSeconds(5));
    // The schema here is narrower than the shared one in SpiceDBIntegrationTest, and every
    // example runs against the same SpiceDB. SpiceDB refuses a WriteSchema that drops a relation
    // while a relationship still exists under it, so clear before writing, not after: an earlier
    // example leaving document:report#editor@user:alice behind is enough to fail this outright.
    SpiceDBIntegrationTest.clearDocumentRelationships(client);
    client.writeSchema(SCHEMA);
    client.deleteRelationships(Filter.of("document"));

    var txn = new Transaction();
    txn.touch(Relationship.of("document", "readme", "viewer", "user", "alice"));
    client.write(txn);

    // Bound by the 5s default set above.
    CheckResult result =
        client.checkPermission(
            full(), "view", Relationship.of("document", "readme", "view", "user", "alice"));
    assertThat(result.hasPermission()).isTrue();
  }

  @Test
  void perCallTimeoutOverridesTheClientDefault() {
    client =
        SpiceDBClient.createPlaintext(
            SpiceDBIntegrationTest.ENDPOINT, SpiceDBIntegrationTest.TOKEN);
    // The schema here is narrower than the shared one in SpiceDBIntegrationTest, and every
    // example runs against the same SpiceDB. SpiceDB refuses a WriteSchema that drops a relation
    // while a relationship still exists under it, so clear before writing, not after: an earlier
    // example leaving document:report#editor@user:alice behind is enough to fail this outright.
    SpiceDBIntegrationTest.clearDocumentRelationships(client);
    client.writeSchema(SCHEMA);
    client.deleteRelationships(Filter.of("document"));

    var txn = new Transaction();
    txn.touch(Relationship.of("document", "readme", "viewer", "user", "alice"));
    // 2 seconds is generous for a real call against a local SpiceDB -- this exercises the real
    // timeout overload end-to-end, not testing how small a timeout can be.
    client.write(txn, Duration.ofSeconds(2));

    CheckResult result =
        client.checkPermission(
            full(),
            "view",
            Relationship.of("document", "readme", "view", "user", "alice"),
            Duration.ofSeconds(2));
    assertThat(result.hasPermission()).isTrue();
  }

  @Test
  void bulkImportIsNotBoundedByTheUnaryDefault() {
    // importRelationships (ImportBulkRelationships) is client-streaming: its duration scales
    // with the size of the caller's dataset, not with server latency, so it is explicitly
    // excluded from the unary default. Calling it with no timeout bound at all -- as below --
    // must still succeed; if a future change accidentally routed the unary default into this
    // call, a large enough import would start failing with DeadlineExceededException well before
    // it finished.
    client =
        SpiceDBClient.createPlaintext(
            SpiceDBIntegrationTest.ENDPOINT, SpiceDBIntegrationTest.TOKEN);
    // The schema here is narrower than the shared one in SpiceDBIntegrationTest, and every
    // example runs against the same SpiceDB. SpiceDB refuses a WriteSchema that drops a relation
    // while a relationship still exists under it, so clear before writing, not after: an earlier
    // example leaving document:report#editor@user:alice behind is enough to fail this outright.
    SpiceDBIntegrationTest.clearDocumentRelationships(client);
    client.writeSchema(SCHEMA);
    client.deleteRelationships(Filter.of("document"));

    List<Relationship> relationships = new ArrayList<>();
    for (int i = 0; i < 50; i++) {
      relationships.add(Relationship.of("document", "bulk-" + i, "viewer", "user", "alice"));
    }
    long numLoaded = client.importRelationships(relationships);
    assertThat(numLoaded).isEqualTo(50);

    // A caller-supplied timeout on the same client-streaming call must still be honored -- the
    // exclusion is from the *default*, not from the ability to bound the call at all.
    List<Relationship> moreRelationships = new ArrayList<>();
    for (int i = 0; i < 50; i++) {
      moreRelationships.add(Relationship.of("document", "bulk2-" + i, "viewer", "user", "alice"));
    }
    long numLoadedBounded = client.importRelationships(moreRelationships, Duration.ofSeconds(30));
    assertThat(numLoadedBounded).isEqualTo(50);
  }

  /**
   * The deadline handed to the calls against the wedged server. Short, because the point is to
   * watch it expire.
   */
  private static final Duration WEDGED_TIMEOUT = Duration.ofSeconds(2);

  /**
   * Wall-clock bound on a wedged call. If a call with a {@link #WEDGED_TIMEOUT} deadline has not
   * returned after this long, the deadline is not reaching the RPC -- and the test fails with that
   * message instead of hanging the CI job. It is comfortably below {@link
   * SpiceDBClient#DEFAULT_TIMEOUT}, so a per-call timeout that was accepted and dropped falls back
   * to the 30s client default and trips this rather than passing.
   */
  private static final Duration WEDGED_WATCHDOG = Duration.ofSeconds(17);

  @Test
  void defaultTimeoutExpiresAgainstAServerThatNeverAnswers() throws IOException {
    try (ServerSocket wedged = wedgedListener()) {
      client =
          SpiceDBClient.createPlaintext(
              endpointOf(wedged), SpiceDBIntegrationTest.TOKEN, WEDGED_TIMEOUT);

      expectDeadlineToFire(
          "defaultTimeout",
          () ->
              client.checkPermission(
                  full(), "view", Relationship.of("document", "readme", "view", "user", "alice")));
    }
  }

  @Test
  void perCallTimeoutExpiresAgainstAServerThatNeverAnswers() throws IOException {
    try (ServerSocket wedged = wedgedListener()) {
      // No defaultTimeout here, so only the per-call argument can bound this. The override is a
      // different code path, and one that accepted the argument and dropped it would still pass
      // every fast-local-call test above.
      client = SpiceDBClient.createPlaintext(endpointOf(wedged), SpiceDBIntegrationTest.TOKEN);

      expectDeadlineToFire(
          "per-call timeout",
          () ->
              client.checkPermission(
                  full(),
                  "view",
                  Relationship.of("document", "readme", "view", "user", "alice"),
                  WEDGED_TIMEOUT));
    }
  }

  /**
   * A socket that accepts TCP connections and never speaks gRPC. The kernel completes the handshake
   * for connections sitting in the backlog, so a client connects successfully and then waits
   * forever for the HTTP/2 server preface. That is what a wedged SpiceDB looks like from a client
   * -- an open, healthy-looking connection with no reply behind it -- and it is why "the connection
   * worked" is not a bound.
   */
  private static ServerSocket wedgedListener() throws IOException {
    return new ServerSocket(0, 50, InetAddress.getLoopbackAddress());
  }

  private static String endpointOf(ServerSocket listener) {
    return "127.0.0.1:" + listener.getLocalPort();
  }

  /** Runs the call under a watchdog and requires it to fail with DeadlineExceededException. */
  private static void expectDeadlineToFire(String what, Callable<CheckResult> call) {
    ExecutorService executor = Executors.newSingleThreadExecutor();
    try {
      Future<CheckResult> future = executor.submit(call);
      try {
        future.get(WEDGED_WATCHDOG.toMillis(), TimeUnit.MILLISECONDS);
        throw new AssertionError(
            "a call with a "
                + WEDGED_TIMEOUT
                + " "
                + what
                + " returned successfully against a server that never answers");
      } catch (TimeoutException e) {
        future.cancel(true);
        throw new AssertionError(
            "a call with a "
                + WEDGED_TIMEOUT
                + " "
                + what
                + " had not returned after "
                + WEDGED_WATCHDOG
                + " against a server that never answers: the deadline is not reaching the RPC");
      } catch (ExecutionException e) {
        // The specific exception matters. "Some exception" is also satisfied by an
        // UnavailableException from a refused connection, which says nothing at all about
        // deadlines.
        assertThat(e.getCause()).isInstanceOf(DeadlineExceededException.class);
      } catch (InterruptedException e) {
        Thread.currentThread().interrupt();
        throw new AssertionError("interrupted waiting for the wedged call", e);
      }
    } finally {
      executor.shutdownNow();
    }
  }
}
