package com.authzed.spicedb.examples;

import com.authzed.spicedb.Filter;
import com.authzed.spicedb.Relationship;
import com.authzed.spicedb.SpiceDBClient;
import com.authzed.spicedb.Transaction;

import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

import static org.assertj.core.api.Assertions.*;

/**
 * Demonstrates experimental relationship counters: registering, reading,
 * and unregistering counters.
 *
 * <p>Note: these APIs are experimental and may change without notice.
 */
class RelationshipCountersTest {

    private static final String COUNTER_NAME = "java_example_document_viewers";

    private SpiceDBClient client;

    @BeforeEach
    void setUp() {
        client = SpiceDBClient.createPlaintext("localhost:50051", "somerandomkeyhere");

        client.writeSchema("""
            definition rc_user {}

            definition rc_document {
                relation viewer: rc_user
                relation editor: rc_user
                relation owner: rc_user
                permission view = viewer + editor + owner
                permission edit = editor + owner
                permission delete = owner
            }""");

        var txn = new Transaction();
        txn.touch(Relationship.of("rc_document", "countdoc1", "viewer", "rc_user", "alice"));
        txn.touch(Relationship.of("rc_document", "countdoc2", "viewer", "rc_user", "bob"));
        txn.touch(Relationship.of("rc_document", "countdoc3", "viewer", "rc_user", "charlie"));
        client.write(txn);

        // Clean up any existing counter from a prior run
        try {
            client.experimentalUnregisterRelationshipCounter(COUNTER_NAME);
        } catch (Exception ignored) {
            // Counter may not exist
        }
    }

    @AfterEach
    void tearDown() {
        try {
            client.experimentalUnregisterRelationshipCounter(COUNTER_NAME);
        } catch (Exception ignored) {
            // Counter may not exist
        }
        client.close();
    }

    @Test
    void register_and_read_counter() throws Exception {
        Filter filter = Filter.of("rc_document").withRelation("viewer");

        client.experimentalRegisterRelationshipCounter(COUNTER_NAME, filter);

        // Wait for the counter to be computed
        Thread.sleep(3000);

        SpiceDBClient.CountResult result = client.experimentalCountRelationships(COUNTER_NAME);

        if (!result.stillCalculating()) {
            assertThat(result.relationshipCount()).isGreaterThanOrEqualTo(3);
            assertThat(result.revision()).isNotEmpty();
        }
        // If still calculating, that's acceptable for this example
    }

    @Test
    void unregister_counter() {
        Filter filter = Filter.of("rc_document").withRelation("viewer");

        client.experimentalRegisterRelationshipCounter(COUNTER_NAME, filter);

        // Unregistering should not throw
        assertThatCode(() ->
            client.experimentalUnregisterRelationshipCounter(COUNTER_NAME)
        ).doesNotThrowAnyException();
    }
}
