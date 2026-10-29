package com.authzed.spicedb;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertNotNull;
import static org.junit.jupiter.api.Assertions.assertNull;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

import com.authzed.api.v1.CheckBulkPermissionsPair;
import com.authzed.api.v1.CheckBulkPermissionsRequest;
import com.authzed.api.v1.CheckBulkPermissionsResponse;
import com.authzed.api.v1.CheckBulkPermissionsResponseItem;
import com.authzed.api.v1.Cursor;
import com.authzed.api.v1.DeleteRelationshipsRequest;
import com.authzed.api.v1.DeleteRelationshipsResponse;
import com.authzed.api.v1.ExportBulkRelationshipsRequest;
import com.authzed.api.v1.ExportBulkRelationshipsResponse;
import com.authzed.api.v1.ImportBulkRelationshipsRequest;
import com.authzed.api.v1.ImportBulkRelationshipsResponse;
import com.authzed.api.v1.LookupResourcesRequest;
import com.authzed.api.v1.LookupResourcesResponse;
import com.authzed.api.v1.PermissionsServiceGrpc;
import com.authzed.api.v1.ReadRelationshipsRequest;
import com.authzed.api.v1.ReadRelationshipsResponse;
import com.authzed.api.v1.ReadSchemaRequest;
import com.authzed.api.v1.ReadSchemaResponse;
import com.authzed.api.v1.SchemaServiceGrpc;
import com.authzed.api.v1.WatchKind;
import com.authzed.api.v1.WatchRequest;
import com.authzed.api.v1.WatchResponse;
import com.authzed.api.v1.WatchServiceGrpc;
import com.authzed.api.v1.WriteRelationshipsRequest;
import com.authzed.api.v1.WriteRelationshipsResponse;
import com.authzed.spicedb.errors.AlreadyExistsException;
import com.authzed.spicedb.errors.ResourceExhaustedException;
import com.authzed.spicedb.errors.UnavailableException;
import io.grpc.BindableService;
import io.grpc.Context;
import io.grpc.ForwardingServerCall;
import io.grpc.ManagedChannel;
import io.grpc.Metadata;
import io.grpc.Server;
import io.grpc.ServerCall;
import io.grpc.ServerCallHandler;
import io.grpc.ServerInterceptor;
import io.grpc.ServerInterceptors;
import io.grpc.Status;
import io.grpc.inprocess.InProcessChannelBuilder;
import io.grpc.inprocess.InProcessServerBuilder;
import io.grpc.stub.StreamObserver;
import java.time.Duration;
import java.util.ArrayList;
import java.util.Collections;
import java.util.List;
import java.util.Set;
import java.util.concurrent.atomic.AtomicInteger;
import java.util.concurrent.atomic.AtomicReference;
import org.junit.jupiter.api.Test;

/** Tooling options against an in-process server. */
class ToolingOptionsTest {

  /** In-process server plus a channel the test owns. */
  private static final class Harness implements AutoCloseable {
    final Server server;
    final ManagedChannel channel;

    Harness(BindableService... services) throws Exception {
      String name = InProcessServerBuilder.generateName();
      var b = InProcessServerBuilder.forName(name).directExecutor();
      for (var s : services) b.addService(s);
      server = b.build().start();
      channel = InProcessChannelBuilder.forName(name).directExecutor().build();
    }

    Harness(io.grpc.ServerServiceDefinition service) throws Exception {
      String name = InProcessServerBuilder.generateName();
      server = InProcessServerBuilder.forName(name).directExecutor().addService(service).build();
      server.start();
      channel = InProcessChannelBuilder.forName(name).directExecutor().build();
    }

    SpiceDBClient client(ClientOptions options) {
      return SpiceDBClient.fromChannel(channel, options);
    }

    @Override
    public void close() {
      channel.shutdownNow();
      server.shutdownNow();
    }
  }

  private static ClientOptions fastRetries(RetryPolicy.Builder policy) {
    return ClientOptions.builder()
        .retryPolicy(
            policy.initialBackoff(Duration.ofMillis(1)).maxBackoff(Duration.ofMillis(2)).build())
        .build();
  }

  private static com.authzed.api.v1.Relationship protoRel(String tuple) {
    return SpiceDBClient.toProtoRelationship(Relationship.fromTuple(tuple));
  }

  // ------------------------------------------------------------------ from channel

  @Test
  void fromChannelUsesCallerOwnedChannelAndNeverClosesIt() throws Exception {
    var service =
        new SchemaServiceGrpc.SchemaServiceImplBase() {
          @Override
          public void readSchema(ReadSchemaRequest req, StreamObserver<ReadSchemaResponse> resp) {
            resp.onNext(
                ReadSchemaResponse.newBuilder().setSchemaText("definition user {}").build());
            resp.onCompleted();
          }
        };
    try (var h = new Harness(service)) {
      var client = SpiceDBClient.fromChannel(h.channel);
      assertEquals("definition user {}", client.readSchema().schema());
      client.close();
      assertFalse(h.channel.isShutdown(), "close must not shut a supplied channel down");
      // A second client over the same channel still works.
      assertEquals(
          "definition user {}", SpiceDBClient.fromChannel(h.channel).readSchema().schema());
      // Transport and credential options do not apply to a supplied channel.
      assertThrows(
          com.authzed.spicedb.errors.InvalidArgumentException.class,
          () ->
              SpiceDBClient.fromChannel(
                  h.channel,
                  ClientOptions.builder()
                      .remotePlaintextCredentials(RemotePlaintextCredentials.ALLOW)
                      .build()));
    }
  }

  // ------------------------------------------------------------------ import

  @Test
  void importReportsTheServersAbortStatusAndHonorsBatchSize() throws Exception {
    var messages = new AtomicInteger();
    var abort = new java.util.concurrent.atomic.AtomicBoolean(true);
    var service =
        new PermissionsServiceGrpc.PermissionsServiceImplBase() {
          @Override
          public StreamObserver<ImportBulkRelationshipsRequest> importBulkRelationships(
              StreamObserver<ImportBulkRelationshipsResponse> resp) {
            return new StreamObserver<>() {
              long total;
              boolean failed;

              public void onNext(ImportBulkRelationshipsRequest r) {
                messages.incrementAndGet();
                total += r.getRelationshipsCount();
                if (abort.get() && !failed) {
                  failed = true;
                  resp.onError(
                      Status.ALREADY_EXISTS
                          .withDescription("duplicate relationship")
                          .asRuntimeException());
                }
              }

              public void onError(Throwable t) {}

              public void onCompleted() {
                if (failed) return;
                resp.onNext(
                    ImportBulkRelationshipsResponse.newBuilder().setNumLoaded(total).build());
                resp.onCompleted();
              }
            };
          }
        };
    try (var h = new Harness(service)) {
      var client = h.client(ClientOptions.builder().build());
      var rels = new ArrayList<Relationship>();
      for (int i = 0; i < 5; i++)
        rels.add(Relationship.fromTuple("document:d" + i + "#viewer@user:u"));
      var e =
          assertThrows(
              AlreadyExistsException.class,
              () -> client.importRelationships(rels, ImportOptions.builder().batchSize(2).build()));
      assertEquals("duplicate relationship", e.serverMessage());

      abort.set(false);
      messages.set(0);
      long loaded = client.importRelationships(rels, ImportOptions.builder().batchSize(2).build());
      assertEquals(5, loaded);
      assertEquals(3, messages.get(), "5 relationships at batch size 2 is 3 messages");
    }
  }

  // ------------------------------------------------------------------ paging

  @Test
  void readPagesRepeatConsistencyAndPageSizeAndExposeCursors() throws Exception {
    var requests = Collections.synchronizedList(new ArrayList<ReadRelationshipsRequest>());
    var service =
        new PermissionsServiceGrpc.PermissionsServiceImplBase() {
          @Override
          public void readRelationships(
              ReadRelationshipsRequest req, StreamObserver<ReadRelationshipsResponse> resp) {
            requests.add(req);
            int start =
                req.hasOptionalCursor() ? Integer.parseInt(req.getOptionalCursor().getToken()) : 0;
            for (int i = start; i < Math.min(start + req.getOptionalLimit(), 5); i++) {
              resp.onNext(
                  ReadRelationshipsResponse.newBuilder()
                      .setRelationship(protoRel("document:d" + i + "#viewer@user:u"))
                      .setAfterResultCursor(Cursor.newBuilder().setToken(String.valueOf(i + 1)))
                      .setReadAt(com.authzed.api.v1.ZedToken.newBuilder().setToken("rev" + start))
                      .build());
            }
            resp.onCompleted();
          }
        };
    try (var h = new Harness(service)) {
      var client = h.client(ClientOptions.builder().build());
      var consistency = Consistency.atLeast(new ZedToken("t1"));
      List<ReadRelationship> items;
      try (var s =
          client.readRelationships(
              consistency,
              Filter.of("document"),
              ReadOptions.builder().pageSize(2).limit(3).build())) {
        items = s.toList();
      }
      assertEquals(3, items.size());
      assertEquals("1", items.get(0).afterResultCursor());
      assertEquals("3", items.get(2).afterResultCursor());
      assertEquals("rev0", items.get(0).readAt().value());
      assertEquals("rev2", items.get(2).readAt().value());
      assertEquals(2, requests.size());
      for (var r : requests) {
        // SpiceDB hashes these into the cursor: every page must repeat them exactly.
        assertEquals(2, r.getOptionalLimit(), "page size must not shrink for the last page");
        assertEquals("t1", r.getConsistency().getAtLeastAsFresh().getToken());
      }

      requests.clear();
      try (var s =
          client.readRelationships(
              consistency,
              Filter.of("document"),
              ReadOptions.builder().pageSize(2).cursor("3").build())) {
        assertEquals(
            List.of("document:d3#viewer@user:u", "document:d4#viewer@user:u"),
            s.map(r -> r.relationship().toString()).toList());
      }
      assertEquals("3", requests.get(0).getOptionalCursor().getToken());
    }
  }

  @Test
  void lookupResourcesAndExportCarryCursorsAndResume() throws Exception {
    var lookups = Collections.synchronizedList(new ArrayList<LookupResourcesRequest>());
    var exports = Collections.synchronizedList(new ArrayList<ExportBulkRelationshipsRequest>());
    var service =
        new PermissionsServiceGrpc.PermissionsServiceImplBase() {
          @Override
          public void lookupResources(
              LookupResourcesRequest req, StreamObserver<LookupResourcesResponse> resp) {
            lookups.add(req);
            resp.onNext(
                LookupResourcesResponse.newBuilder()
                    .setResourceObjectId("d9")
                    .setAfterResultCursor(Cursor.newBuilder().setToken("c9"))
                    .build());
            resp.onCompleted();
          }

          @Override
          public void exportBulkRelationships(
              ExportBulkRelationshipsRequest req,
              StreamObserver<ExportBulkRelationshipsResponse> resp) {
            exports.add(req);
            resp.onNext(
                ExportBulkRelationshipsResponse.newBuilder()
                    .addRelationships(protoRel("document:d1#viewer@user:u"))
                    .setAfterResultCursor(Cursor.newBuilder().setToken("e1"))
                    .build());
            resp.onCompleted();
          }
        };
    try (var h = new Harness(service)) {
      var client = h.client(ClientOptions.builder().build());
      try (var s =
          client.lookupResources(
              Consistency.full(),
              "document",
              "view",
              SubjectRef.direct(new ObjectRef("user", "u")),
              LookupOptions.builder().cursor("c0").build())) {
        assertEquals("c9", s.toList().get(0).afterResultCursor());
      }
      assertEquals("c0", lookups.get(0).getOptionalCursor().getToken());
      try (var s =
          client.exportRelationships(
              Consistency.full(),
              Filter.of("document"),
              ExportOptions.builder().cursor("e0").build())) {
        assertEquals("e1", s.toList().get(0).afterResultCursor());
      }
      assertEquals("e0", exports.get(0).getOptionalCursor().getToken());
    }
  }

  // ------------------------------------------------------------------ retry policy

  /** A schema service that fails with {@code code} for the first {@code failures} calls. */
  private static SchemaServiceGrpc.SchemaServiceImplBase flakySchema(
      Status.Code code, int failures, AtomicInteger calls) {
    return new SchemaServiceGrpc.SchemaServiceImplBase() {
      @Override
      public void readSchema(ReadSchemaRequest req, StreamObserver<ReadSchemaResponse> resp) {
        if (calls.incrementAndGet() <= failures) {
          resp.onError(Status.fromCode(code).asRuntimeException());
          return;
        }
        resp.onNext(ReadSchemaResponse.newBuilder().setSchemaText("ok").build());
        resp.onCompleted();
      }
    };
  }

  @Test
  void retryPolicyIsConfigurableAndDisableable() throws Exception {
    var calls = new AtomicInteger();
    try (var h = new Harness(flakySchema(Status.Code.UNAVAILABLE, 100, calls))) {
      var attempts = new ArrayList<RetryPolicy.RetryAttempt>();
      var client =
          h.client(fastRetries(RetryPolicy.builder().maxRetries(1).onRetry(attempts::add)));
      assertThrows(UnavailableException.class, client::readSchema);
      assertEquals(2, calls.get(), "maxRetries(1) makes two attempts");
      assertEquals(1, attempts.size());
      assertEquals(Status.Code.UNAVAILABLE, attempts.get(0).code());

      calls.set(0);
      var disabled = h.client(ClientOptions.builder().retries(Retries.DISABLED).build());
      assertThrows(UnavailableException.class, disabled::readSchema);
      assertEquals(1, calls.get(), "Retries.DISABLED makes exactly one attempt");
    }

    // The default never retries RESOURCE_EXHAUSTED; a policy may opt in.
    calls.set(0);
    try (var h = new Harness(flakySchema(Status.Code.RESOURCE_EXHAUSTED, 1, calls))) {
      assertThrows(
          ResourceExhaustedException.class,
          () -> h.client(fastRetries(RetryPolicy.builder())).readSchema());
      assertEquals(1, calls.get());
      calls.set(0);
      var optIn =
          h.client(
              fastRetries(
                  RetryPolicy.builder()
                      .retryableCodes(
                          Set.of(Status.Code.RESOURCE_EXHAUSTED, Status.Code.UNAVAILABLE))));
      assertEquals("ok", optIn.readSchema().schema());
      assertEquals(2, calls.get());
    }
  }

  @Test
  void mutationsNeverRetryUnderAnyPolicy() throws Exception {
    var calls = new AtomicInteger();
    var service =
        new PermissionsServiceGrpc.PermissionsServiceImplBase() {
          @Override
          public void writeRelationships(
              WriteRelationshipsRequest req, StreamObserver<WriteRelationshipsResponse> resp) {
            calls.incrementAndGet();
            resp.onError(Status.UNAVAILABLE.asRuntimeException());
          }
        };
    try (var h = new Harness(service)) {
      var client =
          h.client(
              fastRetries(
                  RetryPolicy.builder()
                      .maxRetries(5)
                      .retryableCodes(Set.of(Status.Code.UNAVAILABLE, Status.Code.ABORTED))));
      var txn = new Transaction();
      txn.touch(Relationship.fromTuple("document:d#viewer@user:u"));
      assertThrows(UnavailableException.class, () -> client.write(txn));
      assertEquals(1, calls.get());
    }
  }

  @Test
  void streamEstablishmentRetryHasItsOwnSwitch() throws Exception {
    var calls = new AtomicInteger();
    var service =
        new PermissionsServiceGrpc.PermissionsServiceImplBase() {
          @Override
          public void readRelationships(
              ReadRelationshipsRequest req, StreamObserver<ReadRelationshipsResponse> resp) {
            calls.incrementAndGet();
            resp.onError(Status.UNAVAILABLE.asRuntimeException());
          }
        };
    try (var h = new Harness(service)) {
      var retrying = h.client(fastRetries(RetryPolicy.builder()));
      try (var s = retrying.readRelationships(Consistency.full(), Filter.of("document"))) {
        assertThrows(UnavailableException.class, () -> s.findFirst());
      }
      assertEquals(4, calls.get(), "default: three retries of stream establishment");

      calls.set(0);
      var noStreamRetry =
          h.client(ClientOptions.builder().streamRetries(StreamRetries.DISABLED).build());
      try (var s = noStreamRetry.readRelationships(Consistency.full(), Filter.of("document"))) {
        assertThrows(UnavailableException.class, () -> s.findFirst());
      }
      assertEquals(1, calls.get());
    }
  }

  // ------------------------------------------------------------------ default deadline

  @Test
  void defaultDeadlineCanBeDisabled() throws Exception {
    var deadlines = new ArrayList<io.grpc.Deadline>();
    var service =
        new SchemaServiceGrpc.SchemaServiceImplBase() {
          @Override
          public void readSchema(ReadSchemaRequest req, StreamObserver<ReadSchemaResponse> resp) {
            deadlines.add(Context.current().getDeadline());
            resp.onNext(ReadSchemaResponse.getDefaultInstance());
            resp.onCompleted();
          }
        };
    try (var h = new Harness(service)) {
      h.client(ClientOptions.builder().build()).readSchema();
      h.client(ClientOptions.builder().defaultDeadline(DefaultDeadline.DISABLED).build())
          .readSchema();
      h.client(ClientOptions.builder().defaultDeadline(DefaultDeadline.DISABLED).build())
          .readSchema(CallOptions.builder().timeout(Duration.ofSeconds(5)).build());
      assertNotNull(deadlines.get(0), "default 30s budget");
      assertNull(deadlines.get(1), "no default deadline and no caller deadline: unbounded");
      assertNotNull(deadlines.get(2), "an explicit operation timeout still applies");
    }
  }

  // ------------------------------------------------------------------ bulk item errors

  @Test
  void bulkItemErrorsCanBeReturnedPerItem() throws Exception {
    var service =
        new PermissionsServiceGrpc.PermissionsServiceImplBase() {
          @Override
          public void checkBulkPermissions(
              CheckBulkPermissionsRequest req, StreamObserver<CheckBulkPermissionsResponse> resp) {
            resp.onNext(
                CheckBulkPermissionsResponse.newBuilder()
                    .addPairs(
                        CheckBulkPermissionsPair.newBuilder()
                            .setRequest(req.getItems(0))
                            .setItem(
                                CheckBulkPermissionsResponseItem.newBuilder()
                                    .setPermissionship(
                                        com.authzed.api.v1.CheckPermissionResponse.Permissionship
                                            .PERMISSIONSHIP_HAS_PERMISSION)))
                    .addPairs(
                        CheckBulkPermissionsPair.newBuilder()
                            .setRequest(req.getItems(1))
                            .setError(
                                com.google.rpc.Status.newBuilder()
                                    .setCode(Status.Code.INVALID_ARGUMENT.value())
                                    .setMessage("bad item")))
                    .build());
            resp.onCompleted();
          }
        };
    try (var h = new Harness(service)) {
      var client = h.client(ClientOptions.builder().build());
      var checks =
          List.of(
              new PermissionCheck(
                  new ObjectRef("document", "a"),
                  "view",
                  SubjectRef.direct(new ObjectRef("user", "u")),
                  Consistency.full()),
              new PermissionCheck(
                  new ObjectRef("document", "b"),
                  "view",
                  SubjectRef.direct(new ObjectRef("user", "u")),
                  Consistency.full()));
      assertThrows(
          com.authzed.spicedb.errors.InvalidArgumentException.class,
          () -> client.checkBulk(checks));
      var results =
          client.checkBulk(
              checks, CheckOptions.builder().bulkItemErrors(BulkItemErrors.RETURNED).build());
      assertTrue(results.get(0).hasPermission());
      assertNull(results.get(0).error());
      assertFalse(results.get(1).hasPermission(), "an errored item never grants");
      assertEquals(Permissionship.UNSPECIFIED, results.get(1).permissionship());
      assertEquals("bad item", results.get(1).error().serverMessage());
      assertTrue(results.get(1).error().getMessage().contains("check item 1"));
    }
  }

  // ------------------------------------------------------------------ transport options

  @Test
  void transportOptionsAdjustCallsAndCaptureMetadata() throws Exception {
    var headerKey = Metadata.Key.of("io.spicedb.version", Metadata.ASCII_STRING_MARSHALLER);
    var trailerKey = Metadata.Key.of("x-trailer", Metadata.ASCII_STRING_MARSHALLER);
    ServerInterceptor addMetadata =
        new ServerInterceptor() {
          @Override
          public <Q, R> ServerCall.Listener<Q> interceptCall(
              ServerCall<Q, R> call, Metadata headers, ServerCallHandler<Q, R> next) {
            return next.startCall(
                new ForwardingServerCall.SimpleForwardingServerCall<Q, R>(call) {
                  @Override
                  public void sendHeaders(Metadata h) {
                    h.put(headerKey, "v1.99.0");
                    super.sendHeaders(h);
                  }

                  @Override
                  public void close(Status status, Metadata trailers) {
                    trailers.put(trailerKey, "t");
                    super.close(status, trailers);
                  }
                },
                headers);
          }
        };
    var calls = new AtomicInteger();
    var service = flakySchema(Status.Code.UNAVAILABLE, 0, calls);
    try (var h = new Harness(ServerInterceptors.intercept(service, addMetadata))) {
      var md = new ResponseMetadata();
      var hookCalls = new AtomicInteger();
      var seen = new AtomicReference<String>();
      var key = io.grpc.CallOptions.Key.create("test-key");
      h.client(ClientOptions.builder().build())
          .readSchema(
              CallOptions.builder()
                  .transport(
                      TransportOptions.builder()
                          .callOptions(
                              o -> {
                                hookCalls.incrementAndGet();
                                seen.set(String.valueOf(o.getDeadline() != null));
                                return o.withOption(key, "v").withWaitForReady();
                              })
                          .responseMetadata(md)
                          .build())
                  .build());
      assertEquals(1, hookCalls.get());
      assertEquals("true", seen.get(), "the hook sees the client's deadline already applied");
      assertEquals("v1.99.0", md.headers().get(headerKey));
      assertEquals("t", md.trailers().get(trailerKey));
    }
  }

  // ------------------------------------------------------------------ watch schema changes

  @Test
  void watchRequestsAndReportsSchemaChanges() throws Exception {
    var request = new AtomicReference<WatchRequest>();
    var service =
        new WatchServiceGrpc.WatchServiceImplBase() {
          @Override
          public void watch(WatchRequest req, StreamObserver<WatchResponse> resp) {
            request.set(req);
            resp.onNext(WatchResponse.newBuilder().setSchemaUpdated(true).build());
            resp.onCompleted();
          }
        };
    try (var h = new Harness(service)) {
      var client = h.client(ClientOptions.builder().build());
      try (var s =
          client.updates(
              List.of(),
              null,
              WatchOptions.builder()
                  .content(WatchContent.RELATIONSHIPS_AND_CHECKPOINTS)
                  .schemaChanges(WatchSchemaChanges.INCLUDED)
                  .build())) {
        assertTrue(s.findFirst().orElseThrow().schemaUpdated());
      }
      assertEquals(
          List.of(
              WatchKind.WATCH_KIND_INCLUDE_RELATIONSHIP_UPDATES,
              WatchKind.WATCH_KIND_INCLUDE_CHECKPOINTS,
              WatchKind.WATCH_KIND_INCLUDE_SCHEMA_UPDATES),
          request.get().getOptionalUpdateKindsList());
    }
  }

  // ------------------------------------------------------------------ deletion modes

  @Test
  void allOrNothingDeleteAndDeleteAllProgress() throws Exception {
    var requests = Collections.synchronizedList(new ArrayList<DeleteRelationshipsRequest>());
    var service =
        new PermissionsServiceGrpc.PermissionsServiceImplBase() {
          @Override
          public void deleteRelationships(
              DeleteRelationshipsRequest req, StreamObserver<DeleteRelationshipsResponse> resp) {
            requests.add(req);
            boolean last = requests.size() >= 3 || !req.getOptionalAllowPartialDeletions();
            var b =
                DeleteRelationshipsResponse.newBuilder()
                    .setRelationshipsDeletedCount(2)
                    .setDeletedAt(
                        com.authzed.api.v1.ZedToken.newBuilder().setToken("r" + requests.size()))
                    .setDeletionProgress(
                        last
                            ? DeleteRelationshipsResponse.DeletionProgress
                                .DELETION_PROGRESS_COMPLETE
                            : DeleteRelationshipsResponse.DeletionProgress
                                .DELETION_PROGRESS_PARTIAL);
            if (!last) b.setAfterResultCursor(Cursor.newBuilder().setToken("c" + requests.size()));
            resp.onNext(b.build());
            resp.onCompleted();
          }
        };
    try (var h = new Harness(service)) {
      var client = h.client(ClientOptions.builder().build());
      var allOrNothing =
          DeleteOptions.builder().partialDeletion(PartialDeletion.DISALLOWED).build();
      client.deleteRelationships(Filter.of("document"), allOrNothing);
      assertFalse(requests.get(0).getOptionalAllowPartialDeletions());
      assertEquals(0, requests.get(0).getOptionalLimit(), "server default limit");
      assertThrows(
          com.authzed.spicedb.errors.InvalidArgumentException.class,
          () -> client.deleteRelationships(Filter.of("document"), DeleteOptions.builder().build()));

      requests.clear();
      var progress = new ArrayList<SpiceDBClient.DeleteResult>();
      var result =
          client.deleteAllRelationshipsNonAtomic(
              Filter.of("document"),
              DeleteAllOptions.builder().pageSize(2).onProgress(progress::add).build());
      assertEquals(6, result.deletedCount());
      assertEquals(3, progress.size());
      assertEquals(2, progress.get(0).deletedCount());
      assertEquals(4, progress.get(1).deletedCount());
      assertEquals("c1", progress.get(0).resumeCursor());
      assertEquals("r2", progress.get(1).revision().value());
      assertTrue(progress.get(2).completed());
    }
  }
}
