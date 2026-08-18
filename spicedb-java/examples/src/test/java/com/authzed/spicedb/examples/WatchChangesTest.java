package com.authzed.spicedb.examples;

import static org.assertj.core.api.Assertions.*;

import com.authzed.spicedb.Relationship;
import com.authzed.spicedb.SpiceDBClient;
import com.authzed.spicedb.Transaction;
import java.util.ArrayList;
import java.util.List;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.TimeUnit;
import org.junit.jupiter.api.Test;

/**
 * Demonstrates watching for relationship changes using {@link SpiceDBClient#updates}.
 *
 * <p>The watch API streams updates starting from a given revision. This test writes a relationship
 * and then watches for the change starting from the revision just before the write.
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

    // Watch from the start revision and collect the first event carrying updates
    CompletableFuture<SpiceDBClient.WatchEvent> future =
        CompletableFuture.supplyAsync(
            () -> {
              try (var stream = client.updates(List.of("document"), startRevision)) {
                return stream.filter(e -> !e.updates().isEmpty()).findFirst().orElseThrow();
              }
            });

    SpiceDBClient.WatchEvent event = future.get(10, TimeUnit.SECONDS);

    // event.changesThrough() is a resume point: keep it and pass it as startRevision on a later
    // updates() call to pick back up after a dropped stream, instead of reprocessing everything
    // since the original startRevision or silently losing changes by restarting from head.
    assertThat(event.changesThrough()).isNotEmpty();

    assertThat(event.updates()).isNotEmpty();
    SpiceDBClient.Update update = event.updates().get(0);
    assertThat(update.operation())
        .isIn(SpiceDBClient.UpdateOperation.CREATE, SpiceDBClient.UpdateOperation.TOUCH);
    assertThat(update.relationship().resourceType()).isEqualTo("document");
  }

  @Test
  void watches_with_checkpoints() throws Exception {
    // updates(objectTypes, startRevision, includeCheckpoints) asks the server for periodic
    // checkpoint events in addition to relationship updates -- recommended if this SpiceDB
    // instance is running behind a proxy that aborts idle connections, since a checkpoint keeps
    // the stream alive even when nothing has changed. A checkpoint carries no updates, so a
    // consumer must check WatchEvent#isCheckpoint to tell "nothing changed, here is a fresh
    // resume point" from "here are changes".
    var setup = new Transaction();
    setup.touch(Relationship.of("document", "watchdoc-cp", "viewer", "user", "setup"));
    client.write(setup);

    CompletableFuture<List<SpiceDBClient.WatchEvent>> future =
        CompletableFuture.supplyAsync(
            () -> {
              List<SpiceDBClient.WatchEvent> seen = new ArrayList<>();
              try (var stream = client.updates(List.of("document"), null, true)) {
                var iterator = stream.iterator();
                boolean seenCheckpoint = false;
                boolean seenUpdate = false;
                while (iterator.hasNext() && !(seenCheckpoint && seenUpdate)) {
                  SpiceDBClient.WatchEvent event = iterator.next();
                  seen.add(event);
                  if (event.isCheckpoint()) seenCheckpoint = true;
                  if (!event.updates().isEmpty()) seenUpdate = true;
                }
              }
              return seen;
            });

    // Trigger an update once the watch is established.
    Thread.sleep(100);
    var txn = new Transaction();
    txn.touch(Relationship.of("document", "watchdoc-cp", "viewer", "user", "bob"));
    client.write(txn);

    List<SpiceDBClient.WatchEvent> events = future.get(10, TimeUnit.SECONDS);

    assertThat(events).anyMatch(SpiceDBClient.WatchEvent::isCheckpoint);
    assertThat(events).anyMatch(e -> !e.updates().isEmpty());
    for (SpiceDBClient.WatchEvent event : events) {
      if (event.isCheckpoint()) {
        assertThat(event.updates()).isEmpty();
      }
    }
  }
}
