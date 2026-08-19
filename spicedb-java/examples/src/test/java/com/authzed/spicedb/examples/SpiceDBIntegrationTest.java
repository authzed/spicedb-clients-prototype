package com.authzed.spicedb.examples;

import com.authzed.spicedb.Filter;
import com.authzed.spicedb.SpiceDBClient;
import com.authzed.spicedb.errors.FailedPreconditionException;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.BeforeEach;

/**
 * Base class for integration tests that share a single SpiceDB schema.
 *
 * <p>All tests use the same standard {@code user} and {@code document} types, matching the Go
 * examples. Each test writes the same schema in {@code @BeforeEach} (idempotent), so tests can run
 * sequentially without conflicts.
 */
abstract class SpiceDBIntegrationTest {

  /** The standard schema used by all integration tests, matching the Go examples. */
  protected static final String SCHEMA =
      """
        definition user {}

        definition document {
            relation viewer: user
            relation editor: user
            relation owner: user
            permission view = viewer + editor + owner
            permission edit = editor + owner
            permission delete = owner
        }""";

  /**
   * The SpiceDB the examples talk to. {@code mage integrationTest} starts it and exports both
   * variables; the defaults are the endpoint and preshared key in {@code docker-compose.test.yml},
   * so an example run by hand against a container started the same way needs no environment at all.
   */
  protected static final String ENDPOINT = envOr("SPICEDB_ENDPOINT", "localhost:50051");

  /** The preshared key to authenticate with. See {@link #ENDPOINT}. */
  protected static final String TOKEN = envOr("SPICEDB_TOKEN", "somerandomkeyhere");

  static String envOr(String name, String fallback) {
    String value = System.getenv(name);
    return value == null || value.isEmpty() ? fallback : value;
  }

  /**
   * Deletes every {@code document} relationship, ignoring the case where no {@code document}
   * definition exists yet.
   *
   * <p>Every example runs against the same SpiceDB and writes a whole schema, and SpiceDB refuses a
   * {@code WriteSchema} that drops a relation while a relationship still exists under it. An
   * example whose schema is narrower than the shared one above therefore has to clear what an
   * earlier example left behind before it can write its own — and JUnit's class order is not
   * something to rely on for that not to happen.
   */
  static void clearDocumentRelationships(SpiceDBClient client) {
    try {
      client.deleteRelationships(Filter.of("document"));
    } catch (FailedPreconditionException e) {
      // No `document` definition in the live schema yet: nothing to clear.
      // SpiceDB reports that as FAILED_PRECONDITION
      // (ERROR_REASON_UNKNOWN_DEFINITION). Only that error is tolerated -- an
      // unreachable server or a bad token must still fail the example.
    }
  }

  protected SpiceDBClient client;

  @BeforeEach
  void baseSetUp() {
    client = SpiceDBClient.createPlaintext(ENDPOINT, TOKEN);
    client.writeSchema(SCHEMA);
  }

  @AfterEach
  void baseTearDown() {
    if (client != null) {
      client.close();
    }
  }
}
