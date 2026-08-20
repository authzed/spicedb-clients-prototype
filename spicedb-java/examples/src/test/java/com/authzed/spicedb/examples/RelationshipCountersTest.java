package com.authzed.spicedb.examples;

import static org.assertj.core.api.Assertions.*;

import com.authzed.spicedb.Filter;
import com.authzed.spicedb.Relationship;
import com.authzed.spicedb.SpiceDBClient;
import com.authzed.spicedb.Transaction;
import com.authzed.spicedb.errors.FailedPreconditionException;
import java.time.Duration;
import java.time.Instant;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

/**
 * Demonstrates experimental relationship counters: registering, reading, and unregistering
 * counters.
 *
 * <p>Note: these APIs are experimental and may change without notice.
 *
 * <p>This example used to sleep three seconds and then wrap every assertion in {@code if
 * (!result.stillCalculating())}, which asserts nothing at all on a slow run -- and nothing on ANY
 * run if the still-calculating mapping is inverted, the likeliest bug on that exact field. It now
 * polls to a terminal state with a timeout that fails rather than skips, and asserts an exact
 * count.
 */
class RelationshipCountersTest extends SpiceDBIntegrationTest {

  private static final String COUNTER_NAME = "java_example_document_viewers";

  /**
   * Bounds how long the counter may stay "still calculating". Expiry is a failure, deliberately,
   * and not a way out of asserting.
   */
  private static final Duration COUNTER_TIMEOUT = Duration.ofSeconds(30);

  private static final Duration COUNTER_POLL_INTERVAL = Duration.ofMillis(100);

  @BeforeEach
  void setUp() {
    client.deleteRelationships(Filter.of("document"));

    var txn = new Transaction();
    txn.touch(Relationship.of("document", "countdoc1", "viewer", "user", "alice"));
    txn.touch(Relationship.of("document", "countdoc2", "viewer", "user", "bob"));
    txn.touch(Relationship.of("document", "countdoc3", "viewer", "user", "charlie"));
    // An `editor` the counter's filter must NOT count. Without a relationship the filter has to
    // exclude, a counter that ignored the relation filter entirely would still report the
    // expected number.
    txn.touch(Relationship.of("document", "countdoc1", "editor", "user", "dave"));
    client.write(txn);

    // Clean up any existing counter from a prior run. Only "not registered" is tolerated -- an
    // unreachable server or a bad token must still fail the example.
    try {
      client.experimentalUnregisterRelationshipCounter(COUNTER_NAME);
    } catch (FailedPreconditionException e) {
      // Not registered yet: nothing to remove.
    }
  }

  @AfterEach
  void tearDown() {
    try {
      client.experimentalUnregisterRelationshipCounter(COUNTER_NAME);
    } catch (FailedPreconditionException e) {
      // Already unregistered by the test itself: nothing to remove.
    }
  }

  @Test
  void register_and_read_counter() {
    Filter filter = Filter.of("document").withRelation("viewer");

    client.experimentalRegisterRelationshipCounter(COUNTER_NAME, filter);

    SpiceDBClient.CountResult result = settledCounter();

    // Exactly the three viewer relationships written in setUp, and not the editor. A count of
    // zero -- registration silently no-op'ing, or the value never being read off the response --
    // fails here, and so does a count of four, which is what ignoring the relation filter would
    // produce.
    assertThat(result.relationshipCount()).isEqualTo(3);
    assertThat(result.revision()).isNotEmpty();
  }

  @Test
  void unregister_counter() {
    Filter filter = Filter.of("document").withRelation("viewer");

    client.experimentalRegisterRelationshipCounter(COUNTER_NAME, filter);
    client.experimentalUnregisterRelationshipCounter(COUNTER_NAME);

    // Unregistering has to actually remove it: reading a counter that is not registered is an
    // error, so a no-op unregister -- which "does not throw" is equally happy with -- would leave
    // this call succeeding.
    assertThatThrownBy(() -> client.experimentalCountRelationships(COUNTER_NAME))
        .isInstanceOf(FailedPreconditionException.class);
  }

  /**
   * Polls the named counter until it settles, failing the test if it never does within {@link
   * #COUNTER_TIMEOUT}.
   */
  private SpiceDBClient.CountResult settledCounter() {
    Instant deadline = Instant.now().plus(COUNTER_TIMEOUT);
    while (true) {
      SpiceDBClient.CountResult result = client.experimentalCountRelationships(COUNTER_NAME);
      if (!result.stillCalculating()) {
        return result;
      }
      assertThat(Instant.now())
          .as("counter %s never settled within %s", COUNTER_NAME, COUNTER_TIMEOUT)
          .isBefore(deadline);
      try {
        Thread.sleep(COUNTER_POLL_INTERVAL.toMillis());
      } catch (InterruptedException e) {
        Thread.currentThread().interrupt();
        throw new AssertionError("interrupted while polling the counter", e);
      }
    }
  }
}
