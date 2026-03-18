package com.authzed.spicedb.examples;

import com.authzed.spicedb.SpiceDBClient;

import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.BeforeEach;

/**
 * Base class for integration tests that share a single SpiceDB schema.
 *
 * <p>All tests use the same standard {@code user} and {@code document} types,
 * matching the Go examples. Each test writes the same schema in {@code @BeforeEach}
 * (idempotent), so tests can run sequentially without conflicts.
 */
abstract class SpiceDBIntegrationTest {

    /**
     * The standard schema used by all integration tests, matching the Go examples.
     */
    protected static final String SCHEMA = """
        definition user {}

        definition document {
            relation viewer: user
            relation editor: user
            relation owner: user
            permission view = viewer + editor + owner
            permission edit = editor + owner
            permission delete = owner
        }""";

    protected SpiceDBClient client;

    @BeforeEach
    void baseSetUp() {
        client = SpiceDBClient.createPlaintext("localhost:50051", "somerandomkeyhere");
        client.writeSchema(SCHEMA);
    }

    @AfterEach
    void baseTearDown() {
        if (client != null) {
            client.close();
        }
    }
}
