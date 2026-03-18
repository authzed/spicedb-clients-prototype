package com.authzed.spicedb.examples;

import com.authzed.spicedb.Relationship;
import com.authzed.spicedb.SpiceDBClient;
import com.authzed.spicedb.Transaction;

import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

import java.util.List;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.TimeUnit;

import static com.authzed.spicedb.Consistency.*;
import static org.assertj.core.api.Assertions.*;

/**
 * Demonstrates watching for relationship changes using
 * {@link SpiceDBClient#updates}.
 *
 * <p>The watch API streams updates starting from a given revision.
 * This test writes a relationship and then watches for the change
 * starting from the revision just before the write.
 */
class WatchChangesTest {

    private SpiceDBClient client;

    @BeforeEach
    void setUp() {
        client = SpiceDBClient.createPlaintext("localhost:50051", "somerandomkeyhere");

        client.writeSchema("""
            definition wc_user {}

            definition wc_document {
                relation viewer: wc_user
                relation editor: wc_user
                relation owner: wc_user
                permission view = viewer + editor + owner
                permission edit = editor + owner
                permission delete = owner
            }""");
    }

    @AfterEach
    void tearDown() {
        client.close();
    }

    @Test
    void watches_for_relationship_changes() throws Exception {
        // Get a starting revision by writing initial data
        var setup = new Transaction();
        setup.touch(Relationship.of("wc_document", "watchdoc", "viewer", "wc_user", "setup"));
        String startRevision = client.write(setup);

        // Write a new relationship after the start revision
        var txn = new Transaction();
        txn.touch(Relationship.of("wc_document", "watchdoc", "viewer", "wc_user", "alice"));
        client.write(txn);

        // Watch from the start revision and collect the first update
        CompletableFuture<List<SpiceDBClient.Update>> future = CompletableFuture.supplyAsync(() -> {
            try (var stream = client.updates(List.of("wc_document"), startRevision)) {
                return stream.limit(1).toList();
            }
        });

        List<SpiceDBClient.Update> updates = future.get(10, TimeUnit.SECONDS);

        assertThat(updates).isNotEmpty();
        SpiceDBClient.Update update = updates.get(0);
        assertThat(update.operation()).isIn(
            SpiceDBClient.UpdateOperation.CREATE,
            SpiceDBClient.UpdateOperation.TOUCH);
        assertThat(update.relationship().resourceType()).isEqualTo("wc_document");
    }
}
