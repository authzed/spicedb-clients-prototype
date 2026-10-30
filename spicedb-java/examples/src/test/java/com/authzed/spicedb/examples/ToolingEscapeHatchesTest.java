package com.authzed.spicedb.examples;

import static org.assertj.core.api.Assertions.*;

import com.authzed.spicedb.*;
import com.authzed.spicedb.errors.AlreadyExistsException;
import com.authzed.spicedb.errors.ErrorMapper;
import com.authzed.spicedb.errors.SpiceDBException;
import io.grpc.ManagedChannelBuilder;
import io.grpc.Metadata;
import io.grpc.Status;
import io.grpc.stub.MetadataUtils;
import java.time.Duration;
import java.util.ArrayList;
import java.util.List;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

/**
 * The tooling-grade surface a schema-agnostic tool (a CLI, a backup job, an MCP server) needs
 * without the raw protocol escape hatch: paging cursors, a caller-owned channel, configurable
 * retries and deadlines, response metadata, import conflicts, all-or-nothing deletes, delete
 * progress, watch schema changes, full tuple syntax and error status interop.
 */
class ToolingEscapeHatchesTest extends SpiceDBIntegrationTest {

  private static final String TOOLING_SCHEMA =
      "use expiration\n\n"
          + SCHEMA
          + """


        caveat only_on(day int) {
            day == 1
        }

        definition tooling_doc {
            relation viewer: user | user with only_on | user with expiration
            permission view = viewer
        }""";

  private static final Filter TOOLING = Filter.of("tooling_doc");

  @BeforeEach
  void setUp() {
    client.writeSchema(TOOLING_SCHEMA);
    client.deleteAllRelationshipsNonAtomic(TOOLING);
  }

  @AfterEach
  void tearDown() {
    // Later examples rewrite the shared schema, which drops tooling_doc: clear it first.
    client.deleteAllRelationshipsNonAtomic(TOOLING);
  }

  private ZedToken writeDocs(int n) {
    var txn = new Transaction();
    for (int i = 0; i < n; i++)
      txn.touch(Relationship.fromTuple("tooling_doc:doc" + i + "#viewer@user:alice"));
    return client.write(txn).revision();
  }

  @Test
  void paged_reads_cross_pages_and_resume_from_a_cursor() {
    var revision = writeDocs(5);
    // atLeast(...) is exactly what SpiceDB hashes into each cursor: every page must repeat it.
    var consistency = Consistency.atLeast(revision);
    var twoPerPage = ReadOptions.builder().pageSize(2);

    List<ReadRelationship> all;
    try (var s = client.readRelationships(consistency, TOOLING, twoPerPage.build())) {
      all = s.toList();
    }
    assertThat(all).hasSize(5);
    assertThat(all).allSatisfy(r -> assertThat(r.afterResultCursor()).isNotEmpty());
    assertThat(all).allSatisfy(r -> assertThat(r.readAt()).isNotNull());

    // A total limit that is not a multiple of the page size is enforced client-side.
    try (var s = client.readRelationships(consistency, TOOLING, twoPerPage.limit(3).build())) {
      assertThat(s.count()).isEqualTo(3);
    }

    // Resume after the second item, repeating the request and page size.
    try (var s =
        client.readRelationships(
            consistency,
            TOOLING,
            ReadOptions.builder().pageSize(2).cursor(all.get(1).afterResultCursor()).build())) {
      assertThat(s.map(r -> r.relationship().resourceId()).toList())
          .containsExactlyElementsOf(
              all.subList(2, 5).stream().map(r -> r.relationship().resourceId()).toList());
    }

    // Resource lookups page the same way.
    try (var s =
        client.lookupResources(
            consistency,
            "tooling_doc",
            "view",
            SubjectRef.direct(new ObjectRef("user", "alice")),
            LookupOptions.builder().pageSize(2).build())) {
      var found = s.toList();
      assertThat(found).hasSize(5);
      assertThat(found).allSatisfy(r -> assertThat(r.afterResultCursor()).isNotEmpty());
    }
  }

  @Test
  void tuples_round_trip_caveats_and_expiration_through_the_server() {
    var caveated = Relationship.fromTuple("tooling_doc:t1#viewer@user:bob[only_on:{\"day\":1}]");
    var expiring =
        Relationship.fromTuple("tooling_doc:t2#viewer@user:carol[expiration:2099-01-02T03:04:05Z]");
    var txn = new Transaction();
    txn.touch(caveated);
    txn.touch(expiring);
    assertThat(txn.size()).isEqualTo(2);
    client.write(txn);

    List<String> read;
    try (var s = client.readRelationships(Consistency.full(), TOOLING)) {
      read = s.map(r -> r.relationship().toString()).sorted().toList();
    }
    assertThat(read).containsExactly(caveated.toString(), expiring.toString());
    assertThat(caveated.toString())
        .isEqualTo("tooling_doc:t1#viewer@user:bob[only_on:{\"day\":1}]");
  }

  @Test
  void import_reports_the_servers_conflict_not_end_of_stream() {
    writeDocs(1);
    var duplicate = List.of(Relationship.fromTuple("tooling_doc:doc0#viewer@user:alice"));
    var e =
        catchThrowableOfType(
            AlreadyExistsException.class,
            () ->
                client.importRelationships(
                    duplicate, ImportOptions.builder().batchSize(1).build()));
    assertThat(e).isNotNull();
    assertThat(Status.fromThrowable(e).getCode()).isEqualTo(Status.Code.ALREADY_EXISTS);
  }

  @Test
  void all_or_nothing_delete_and_delete_progress() {
    writeDocs(5);
    var allOrNothing = DeleteOptions.builder().partialDeletion(PartialDeletion.DISALLOWED).build();
    var refused =
        catchThrowableOfType(
            SpiceDBException.class, () -> client.deleteRelationships(TOOLING, 2, allOrNothing));
    assertThat(refused).isNotNull();
    // The test double (testing.InMemorySpiceDB) mirrors this code.
    assertThat(refused.code()).as(refused.getMessage()).isEqualTo(Status.Code.INVALID_ARGUMENT);
    try (var s = client.readRelationships(Consistency.full(), TOOLING)) {
      assertThat(s.count()).as("nothing was deleted").isEqualTo(5);
    }
    // With no limit, the server default applies and everything is deleted at once.
    assertThat(client.deleteRelationships(TOOLING, allOrNothing).deletedCount()).isEqualTo(5);

    // The one-argument form is the raw filter-only DeleteRelationships request: the same
    // all-or-nothing delete with the server's default limit.
    writeDocs(3);
    var filterOnly = client.deleteRelationships(TOOLING);
    assertThat(filterOnly.deletedCount()).isEqualTo(3);
    assertThat(filterOnly.completed()).isTrue();

    writeDocs(5);
    var progress = new ArrayList<SpiceDBClient.DeleteResult>();
    var result =
        client.deleteAllRelationshipsNonAtomic(
            TOOLING, DeleteAllOptions.builder().pageSize(2).onProgress(progress::add).build());
    assertThat(result.deletedCount()).isEqualTo(5);
    assertThat(progress).hasSizeGreaterThanOrEqualTo(3);
    assertThat(progress.get(progress.size() - 1).deletedCount()).isEqualTo(5);
  }

  @Test
  void keepalive_alongside_client_options() {
    // Channel settings ClientOptions does not cover (keepalive here) go in a channel-builder
    // customizer, on the same constructor that takes ClientOptions.
    try (var tuned =
        SpiceDBClient.createPlaintext(
            ENDPOINT,
            TOKEN,
            ClientOptions.builder().defaultTimeout(Duration.ofSeconds(10)).build(),
            b ->
                b.keepAliveTime(60, java.util.concurrent.TimeUnit.SECONDS)
                    .keepAliveTimeout(10, java.util.concurrent.TimeUnit.SECONDS)
                    .keepAliveWithoutCalls(true))) {
      assertThat(tuned.readSchema().schema()).contains("tooling_doc");
    }
  }

  @Test
  void caller_owned_channel_retry_and_deadline_options_and_response_metadata() {
    var auth = new Metadata();
    auth.put(Metadata.Key.of("authorization", Metadata.ASCII_STRING_MARSHALLER), "Bearer " + TOKEN);
    var channel =
        ManagedChannelBuilder.forTarget(ENDPOINT)
            .usePlaintext()
            .intercept(MetadataUtils.newAttachHeadersInterceptor(auth))
            .build();
    try {
      var options =
          ClientOptions.builder()
              .defaultDeadline(DefaultDeadline.DISABLED)
              .retryPolicy(RetryPolicy.builder().maxRetries(1).build())
              .streamRetries(StreamRetries.DISABLED)
              .build();
      try (var tool = SpiceDBClient.fromChannel(channel, options)) {
        var md = new ResponseMetadata();
        var schema =
            tool.readSchema(
                CallOptions.builder()
                    .transport(TransportOptions.builder().responseMetadata(md).build())
                    .build());
        assertThat(schema.schema()).contains("tooling_doc");
        assertThat(md.headers()).isNotNull();
        assertThat(md.trailers()).isNotNull();
      }
      assertThat(channel.isShutdown()).as("close leaves a supplied channel open").isFalse();
    } finally {
      channel.shutdownNow();
    }
  }

  @Test
  void watch_reports_schema_changes() {
    var start = writeDocs(1);
    client.writeSchema(TOOLING_SCHEMA + "\n\n// schema revision for the watch example\n");
    try (var events =
        client.updates(
            List.of(),
            start,
            WatchOptions.builder()
                .schemaChanges(WatchSchemaChanges.INCLUDED)
                .timeout(Duration.ofSeconds(10))
                .build())) {
      assertThat(events.anyMatch(SpiceDBClient.WatchEvent::schemaUpdated)).isTrue();
    }
  }

  @Test
  void errors_interoperate_with_grpc_status() {
    var bogus =
        new PermissionCheck(
            new ObjectRef("no_such_type", "x"),
            "view",
            SubjectRef.direct(new ObjectRef("user", "alice")),
            Consistency.full());
    var e = catchThrowableOfType(SpiceDBException.class, () -> client.check(bogus));
    assertThat(e).isNotNull();
    assertThat(Status.fromThrowable(e).getCode()).isEqualTo(e.code());
    assertThat(ErrorMapper.codeOf(e)).isEqualTo(Status.Code.FAILED_PRECONDITION);
    assertThat(e.serverMessage()).isEqualTo(Status.fromThrowable(e).getDescription());

    // Per-item errors can be returned next to the other items' decisions.
    var ok =
        new PermissionCheck(
            new ObjectRef("tooling_doc", "x"),
            "view",
            SubjectRef.direct(new ObjectRef("user", "alice")),
            Consistency.full());
    var results =
        client.checkBulk(
            List.of(ok, bogus),
            CheckOptions.builder().bulkItemErrors(BulkItemErrors.RETURNED).build());
    assertThat(results.get(0).error()).isNull();
    assertThat(results.get(1).error()).isNotNull();
    assertThat(results.get(1).hasPermission()).isFalse();
  }
}
