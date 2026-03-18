package com.authzed.spicedb.examples;

import com.authzed.spicedb.Relationship;
import com.authzed.spicedb.SpiceDBClient;
import com.authzed.spicedb.Transaction;

import org.junit.jupiter.api.Test;

import java.util.List;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.TimeUnit;

import static org.assertj.core.api.Assertions.*;

/**
 * Demonstrates watching for relationship changes using
 * {@link SpiceDBClient#updates}.
 *
 * <p>The watch API streams updates starting from a given revision.
 * This test writes a relationship and then watches for the change
 * starting from the revision just before the write.
 */
class WatchChangesTest extends SpiceDBIntegrationTest {

    @Test
    void watches_for_relationship_changes() throws Exception {
        // Get a starting revision by writing initial data
        var setup = new Transaction();
        setup.touch(Relationship.of("document", "watchdoc", "viewer", "user", "setup"));
        String startRevision = client.write(setup);

        // Write a new relationship after the start revision
        var txn = new Transaction();
        txn.touch(Relationship.of("document", "watchdoc", "viewer", "user", "alice"));
        client.write(txn);

        // Watch from the start revision and collect the first update
        CompletableFuture<List<SpiceDBClient.Update>> future = CompletableFuture.supplyAsync(() -> {
            try (var stream = client.updates(List.of("document"), startRevision)) {
                return stream.limit(1).toList();
            }
        });

        List<SpiceDBClient.Update> updates = future.get(10, TimeUnit.SECONDS);

        assertThat(updates).isNotEmpty();
        SpiceDBClient.Update update = updates.get(0);
        assertThat(update.operation()).isIn(
            SpiceDBClient.UpdateOperation.CREATE,
            SpiceDBClient.UpdateOperation.TOUCH);
        assertThat(update.relationship().resourceType()).isEqualTo("document");
    }
}
