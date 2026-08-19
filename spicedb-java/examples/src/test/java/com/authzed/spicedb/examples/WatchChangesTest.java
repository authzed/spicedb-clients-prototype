package com.authzed.spicedb.examples;

import static org.assertj.core.api.Assertions.*;

import com.authzed.spicedb.Relationship;
import com.authzed.spicedb.SpiceDBClient;
import com.authzed.spicedb.Transaction;
import com.authzed.spicedb.errors.CancelledException;
import java.util.ArrayList;
import java.util.List;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.ExecutionException;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.Future;
import java.util.concurrent.TimeUnit;
import java.util.stream.Stream;
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

    // The update must be the one that was written, not merely "an update": asserting only the
    // resource type would pass on a stream that delivered the seed write above, or any other
    // document relationship an earlier example left behind.
    assertThat(event.updates()).isNotEmpty();
    SpiceDBClient.Update update = event.updates().get(0);
    // TOUCH is a write, so it can only be the mapping for an explicit OPERATION_TOUCH -- never a
    // default an unrecognized operation falls into.
    assertThat(update.operation())
        .isIn(SpiceDBClient.UpdateOperation.CREATE, SpiceDBClient.UpdateOperation.TOUCH);
    assertThat(update.relationship().resourceType()).isEqualTo("document");
    assertThat(update.relationship().resourceID()).isEqualTo("watchdoc");
    assertThat(update.relationship().resourceRelation()).isEqualTo("viewer");
    assertThat(update.relationship().subjectType()).isEqualTo("user");
    assertThat(update.relationship().subjectID()).isEqualTo("alice");
  }

  @Test
  void abandoning_the_stream_releases_it() throws Exception {
    // A caller that walks away mid-stream must not be left waiting. The consumer here is parked on
    // a quiet watch stream -- nothing is being written, so nothing will ever arrive -- and closing
    // the stream has to end it.
    //
    // Unlike the same test in the C# client, where `await foreach` disposes the iterator and the
    // release is a language guarantee no assertion can fail on, the release here is this client's
    // own work: updates() registers an onClose handler that cancels the gRPC context. A stream
    // that dropped that handler leaves this consumer parked forever and fails the bound below.
    // See root DESIGN.md, "RULE: Abandoning a stream must release it".
    Stream<SpiceDBClient.WatchEvent> stream = client.updates(List.of("document"), null);

    ExecutorService executor = Executors.newSingleThreadExecutor();
    try {
      Future<?> consumer =
          executor.submit(
              () -> {
                // Consume forever; only the close below ends this.
                stream.forEach(event -> {});
              });

      // Give the stream a moment to actually open before abandoning it.
      Thread.sleep(200);
      stream.close();

      assertThatThrownBy(() -> consumer.get(10, TimeUnit.SECONDS))
          .as("the watch consumer was still running 10s after the stream was closed")
          .isInstanceOf(ExecutionException.class)
          .cause()
          // The native error type, not the raw gRPC one: an abandoned stream surfaces as
          // CancelledException, so this also pins the error mapping on the streaming path.
          .isInstanceOf(CancelledException.class);
    } finally {
      executor.shutdownNow();
    }
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
