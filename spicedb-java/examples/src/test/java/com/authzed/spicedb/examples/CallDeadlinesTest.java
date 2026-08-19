package com.authzed.spicedb.examples;

import static com.authzed.spicedb.Consistency.*;
import static org.assertj.core.api.Assertions.*;

import com.authzed.spicedb.CheckResult;
import com.authzed.spicedb.Filter;
import com.authzed.spicedb.Relationship;
import com.authzed.spicedb.SpiceDBClient;
import com.authzed.spicedb.Transaction;
import java.time.Duration;
import java.util.ArrayList;
import java.util.List;
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
            "localhost:50051", "somerandomkeyhere", Duration.ofSeconds(5));
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
    client = SpiceDBClient.createPlaintext("localhost:50051", "somerandomkeyhere");
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
    client = SpiceDBClient.createPlaintext("localhost:50051", "somerandomkeyhere");
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
}
