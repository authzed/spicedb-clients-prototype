package com.authzed.spicedb;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

import com.authzed.api.v1.CheckBulkPermissionsPair;
import com.authzed.api.v1.CheckBulkPermissionsRequest;
import com.authzed.api.v1.CheckBulkPermissionsResponse;
import com.authzed.api.v1.CheckBulkPermissionsResponseItem;
import com.authzed.api.v1.CheckPermissionResponse;
import com.authzed.api.v1.Cursor;
import com.authzed.api.v1.DeleteRelationshipsRequest;
import com.authzed.api.v1.DeleteRelationshipsResponse;
import com.authzed.api.v1.ExperimentalRegisterRelationshipCounterRequest;
import com.authzed.api.v1.ExperimentalRegisterRelationshipCounterResponse;
import com.authzed.api.v1.ExperimentalServiceGrpc;
import com.authzed.api.v1.ImportBulkRelationshipsRequest;
import com.authzed.api.v1.ImportBulkRelationshipsResponse;
import com.authzed.api.v1.LookupPermissionship;
import com.authzed.api.v1.LookupResourcesRequest;
import com.authzed.api.v1.LookupResourcesResponse;
import com.authzed.api.v1.LookupSubjectsRequest;
import com.authzed.api.v1.LookupSubjectsResponse;
import com.authzed.api.v1.PartialCaveatInfo;
import com.authzed.api.v1.PermissionsServiceGrpc;
import com.authzed.api.v1.ReadRelationshipsRequest;
import com.authzed.api.v1.ReadRelationshipsResponse;
import com.authzed.api.v1.ResolvedSubject;
import com.authzed.api.v1.SchemaServiceGrpc;
import com.authzed.api.v1.WatchRequest;
import com.authzed.api.v1.WatchResponse;
import com.authzed.api.v1.WatchServiceGrpc;
import com.authzed.api.v1.WriteRelationshipsRequest;
import com.authzed.api.v1.WriteRelationshipsResponse;
import com.authzed.api.v1.WriteSchemaRequest;
import com.authzed.api.v1.WriteSchemaResponse;
import com.authzed.spicedb.errors.FailedPreconditionException;
import com.authzed.spicedb.errors.InvalidArgumentException;
import com.authzed.spicedb.errors.SpiceDBException;
import com.authzed.spicedb.errors.UnavailableException;
import com.authzed.spicedb.protoconv.ProtoConv;
import io.grpc.Status;
import io.grpc.stub.StreamObserver;
import java.time.Duration;
import java.util.ArrayList;
import java.util.List;
import java.util.Map;
import java.util.concurrent.atomic.AtomicInteger;
import org.junit.jupiter.api.Test;

/**
 * Per-call retry overrides, empty writes, single-request paging, wrapper error status and
 * wire-fidelity protocol conversions.
 */
class PerCallBehaviorTest {

  private static final RetryPolicy RETRY_ONCE =
      RetryPolicy.builder().maxRetries(1).initialBackoff(Duration.ofMillis(1)).build();

  private static Relationship rel(String tuple) {
    return Relationship.fromTuple(tuple);
  }

  /** Fails the first write with UNAVAILABLE, then succeeds; counts every RPC it receives. */
  private static final class FlakyServer extends PermissionsServiceGrpc.PermissionsServiceImplBase {
    final AtomicInteger writes = new AtomicInteger();
    final AtomicInteger other = new AtomicInteger();
    final List<WriteRelationshipsRequest> written = new ArrayList<>();
    final List<LookupResourcesRequest> lookups = new ArrayList<>();
    final List<ReadRelationshipsRequest> reads = new ArrayList<>();
    final boolean failFirst;

    FlakyServer(boolean failFirst) {
      this.failFirst = failFirst;
    }

    @Override
    public void writeRelationships(
        WriteRelationshipsRequest request, StreamObserver<WriteRelationshipsResponse> out) {
      written.add(request);
      if (writes.incrementAndGet() == 1 && failFirst) {
        out.onError(Status.UNAVAILABLE.withDescription("transient").asRuntimeException());
        return;
      }
      out.onNext(
          WriteRelationshipsResponse.newBuilder()
              .setWrittenAt(com.authzed.api.v1.ZedToken.newBuilder().setToken("w"))
              .build());
      out.onCompleted();
    }

    @Override
    public void deleteRelationships(
        DeleteRelationshipsRequest request, StreamObserver<DeleteRelationshipsResponse> out) {
      other.incrementAndGet();
      out.onError(Status.UNAVAILABLE.asRuntimeException());
    }

    @Override
    public StreamObserver<ImportBulkRelationshipsRequest> importBulkRelationships(
        StreamObserver<ImportBulkRelationshipsResponse> out) {
      other.incrementAndGet();
      out.onError(Status.UNAVAILABLE.asRuntimeException());
      return new StreamObserver<>() {
        public void onNext(ImportBulkRelationshipsRequest value) {}

        public void onError(Throwable t) {}

        public void onCompleted() {}
      };
    }

    @Override
    public void lookupResources(
        LookupResourcesRequest request, StreamObserver<LookupResourcesResponse> out) {
      lookups.add(request);
      for (int i = 0; i < 3; i++)
        out.onNext(
            LookupResourcesResponse.newBuilder()
                .setResourceObjectId("r" + i)
                .setPermissionship(LookupPermissionship.LOOKUP_PERMISSIONSHIP_HAS_PERMISSION)
                .setAfterResultCursor(Cursor.newBuilder().setToken("c" + i))
                .build());
      out.onCompleted();
    }

    @Override
    public void readRelationships(
        ReadRelationshipsRequest request, StreamObserver<ReadRelationshipsResponse> out) {
      reads.add(request);
      for (int i = 0; i < 3; i++)
        out.onNext(
            ReadRelationshipsResponse.newBuilder()
                .setRelationship(ProtoConv.toProto(rel("document:d" + i + "#viewer@user:u")))
                .setAfterResultCursor(Cursor.newBuilder().setToken("c" + i))
                .build());
      out.onCompleted();
    }
  }

  @Test
  void mutationsRetryOnlyWhenIdempotentAndOptedIn() throws Exception {
    var touch = new Transaction();
    touch.touch(rel("document:a#viewer@user:alice"));

    // Never retried by default.
    var srv = new FlakyServer(true);
    try (var s = TestServers.start(srv)) {
      assertThrows(UnavailableException.class, () -> s.client().write(touch));
      assertEquals(1, srv.writes.get());
    }

    // A touch-only write may opt in for one call.
    srv = new FlakyServer(true);
    try (var s = TestServers.start(srv)) {
      var result = s.client().write(touch, WriteOptions.builder().retryPolicy(RETRY_ONCE).build());
      assertEquals("w", result.revision().value());
      assertEquals(2, srv.writes.get());
    }

    // Non-idempotent mutations refuse the opt-in before any request.
    srv = new FlakyServer(true);
    var counters =
        new ExperimentalServiceGrpc.ExperimentalServiceImplBase() {
          final AtomicInteger calls = new AtomicInteger();

          @Override
          public void experimentalRegisterRelationshipCounter(
              ExperimentalRegisterRelationshipCounterRequest request,
              StreamObserver<ExperimentalRegisterRelationshipCounterResponse> out) {
            calls.incrementAndGet();
            out.onError(Status.UNAVAILABLE.asRuntimeException());
          }
        };
    try (var s = TestServers.start(srv, counters)) {
      var client = s.client();
      var create = new Transaction();
      create.create(rel("document:a#viewer@user:alice"));
      var guarded = new Transaction();
      guarded.mustMatch(Filter.of("document"));
      guarded.touch(rel("document:a#viewer@user:alice"));
      var retry = WriteOptions.builder().retryPolicy(RETRY_ONCE).build();
      for (var txn : List.of(create, guarded))
        assertThrows(InvalidArgumentException.class, () -> client.write(txn, retry));
      assertEquals(0, srv.writes.get());
      assertThrows(
          InvalidArgumentException.class,
          () ->
              client.deleteRelationships(
                  Filter.of("document"),
                  1,
                  DeleteOptions.builder().retryPolicy(RETRY_ONCE).build()));
      assertThrows(
          InvalidArgumentException.class,
          () ->
              client.deleteAllRelationshipsNonAtomic(
                  Filter.of("document"),
                  DeleteAllOptions.builder().retryPolicy(RETRY_ONCE).build()));
      assertThrows(
          InvalidArgumentException.class,
          () ->
              client.importRelationships(
                  List.of(rel("document:a#viewer@user:alice")),
                  ImportOptions.builder().retryPolicy(RETRY_ONCE).build()));
      assertThrows(
          InvalidArgumentException.class,
          () ->
              client.experimentalRegisterRelationshipCounter(
                  "c",
                  Filter.of("document"),
                  CallOptions.builder().retryPolicy(RETRY_ONCE).build()));
      assertEquals(0, srv.other.get());
      assertEquals(0, counters.calls.get());
      // A zero-retry policy is not an opt-in and is accepted.
      assertThrows(
          UnavailableException.class,
          () ->
              client.deleteRelationships(
                  Filter.of("document"),
                  1,
                  DeleteOptions.builder()
                      .retryPolicy(RetryPolicy.builder().maxRetries(0).build())
                      .build()));
      assertEquals(1, srv.other.get());
    }
  }

  @Test
  void schemaWritesMayOptIntoRetries() throws Exception {
    var attempts = new AtomicInteger();
    var schema =
        new SchemaServiceGrpc.SchemaServiceImplBase() {
          @Override
          public void writeSchema(
              WriteSchemaRequest request, StreamObserver<WriteSchemaResponse> out) {
            if (attempts.incrementAndGet() == 1) {
              out.onError(Status.UNAVAILABLE.asRuntimeException());
              return;
            }
            out.onNext(
                WriteSchemaResponse.newBuilder()
                    .setWrittenAt(com.authzed.api.v1.ZedToken.newBuilder().setToken("s"))
                    .build());
            out.onCompleted();
          }
        };
    try (var s = TestServers.start(schema)) {
      assertThrows(UnavailableException.class, () -> s.client().writeSchema("definition user {}"));
      attempts.set(0);
      var token =
          s.client()
              .writeSchema(
                  "definition user {}", CallOptions.builder().retryPolicy(RETRY_ONCE).build());
      assertEquals("s", token.value());
      assertEquals(2, attempts.get());
    }
  }

  @Test
  void oneStreamCanBeExemptedWhileTheClientRetriesOthers() throws Exception {
    var watchCalls = new AtomicInteger();
    var watch =
        new WatchServiceGrpc.WatchServiceImplBase() {
          @Override
          public void watch(WatchRequest request, StreamObserver<WatchResponse> out) {
            watchCalls.incrementAndGet();
            out.onError(Status.UNAVAILABLE.asRuntimeException());
          }
        };
    String name = io.grpc.inprocess.InProcessServerBuilder.generateName();
    var server =
        io.grpc.inprocess.InProcessServerBuilder.forName(name)
            .directExecutor()
            .addService(watch)
            .build()
            .start();
    var channel = io.grpc.inprocess.InProcessChannelBuilder.forName(name).directExecutor().build();
    var retrying = RetryPolicy.builder().maxRetries(3).initialBackoff(Duration.ofMillis(1)).build();
    try (var client =
        SpiceDBClient.fromChannel(
            channel,
            ClientOptions.builder().retryPolicy(retrying).retries(Retries.ENABLED).build())) {
      try (var events = client.updates(List.of(), null)) {
        assertThrows(UnavailableException.class, () -> events.iterator().hasNext());
      }
      assertEquals(4, watchCalls.get(), "the client's policy retries establishment");
      watchCalls.set(0);
      try (var events =
          client.updates(
              List.of(),
              null,
              WatchOptions.builder()
                  .retryPolicy(RetryPolicy.builder().maxRetries(0).build())
                  .build())) {
        assertThrows(UnavailableException.class, () -> events.iterator().hasNext());
      }
      assertEquals(1, watchCalls.get(), "the exempted watch opens once");
    } finally {
      channel.shutdownNow();
      server.shutdownNow();
    }
  }

  @Test
  void emptyWritesAreSentToTheServer() throws Exception {
    var srv = new FlakyServer(false);
    try (var s = TestServers.start(srv)) {
      var result = s.client().write(new Transaction());
      assertEquals("w", result.revision().value());
      assertEquals(1, srv.writes.get());
      assertEquals(0, srv.written.get(0).getUpdatesCount());
    }
  }

  @Test
  void singleRequestPagingSendsExactlyOneRequest() throws Exception {
    var srv = new FlakyServer(false);
    try (var s = TestServers.start(srv)) {
      var subject = SubjectRef.direct(new ObjectRef("user", "alice"));
      List<String> cursors;
      try (var results =
          s.client()
              .lookupResources(
                  Consistency.full(),
                  "document",
                  "view",
                  subject,
                  LookupOptions.builder().pageSize(3).paging(Paging.SINGLE_REQUEST).build())) {
        cursors = results.map(LookupResult.LookupResource::afterResultCursor).toList();
      }
      assertEquals(List.of("c0", "c1", "c2"), cursors);
      assertEquals(1, srv.lookups.size(), "exactly one request");
      assertEquals(0, srv.lookups.get(0).getOptionalLimit(), "no limit means unbounded");

      // Automatic paging asks for a second page after a full one.
      try (var results =
          s.client()
              .lookupResources(
                  Consistency.full(),
                  "document",
                  "view",
                  subject,
                  LookupOptions.builder().pageSize(3).limit(4).build())) {
        assertEquals(4, results.count());
      }
      assertEquals(3, srv.lookups.size());

      try (var results =
          s.client()
              .readRelationships(
                  Consistency.full(),
                  Filter.of("document"),
                  ReadOptions.builder().limit(2).paging(Paging.SINGLE_REQUEST).build())) {
        assertEquals(2, results.count());
      }
      assertEquals(1, srv.reads.size());
      assertEquals(2, srv.reads.get(0).getOptionalLimit(), "the total limit is the request limit");

      assertThrows(
          InvalidArgumentException.class,
          () ->
              s.client()
                  .lookupSubjects(
                      Consistency.full(),
                      new ObjectRef("document", "d"),
                      "view",
                      "user",
                      LookupOptions.builder().paging(Paging.SINGLE_REQUEST).build()));
      assertThrows(
          InvalidArgumentException.class, () -> ReadOptions.builder().paging(null).build());
    }
  }

  private static PermissionCheck check(String id) {
    return new PermissionCheck(
        new ObjectRef("document", id),
        "view",
        SubjectRef.direct(new ObjectRef("user", "alice")),
        Consistency.full());
  }

  /** Answers bulk checks like SpiceDB: "broken" items fail, others are conditional. */
  private static final class BulkServer extends PermissionsServiceGrpc.PermissionsServiceImplBase {
    @Override
    public void checkBulkPermissions(
        CheckBulkPermissionsRequest request, StreamObserver<CheckBulkPermissionsResponse> out) {
      var resp =
          CheckBulkPermissionsResponse.newBuilder()
              .setCheckedAt(com.authzed.api.v1.ZedToken.newBuilder().setToken("bulk-rev"));
      for (var item : request.getItemsList()) {
        var pair = CheckBulkPermissionsPair.newBuilder().setRequest(item);
        if (item.getResource().getObjectId().equals("broken"))
          pair.setError(
              com.google.rpc.Status.newBuilder()
                  .setCode(Status.Code.FAILED_PRECONDITION.value())
                  .setMessage("permission view not found"));
        else
          pair.setItem(
              CheckBulkPermissionsResponseItem.newBuilder()
                  .setPermissionship(
                      CheckPermissionResponse.Permissionship.PERMISSIONSHIP_CONDITIONAL_PERMISSION)
                  .setPartialCaveatInfo(
                      PartialCaveatInfo.newBuilder().addMissingRequiredContext("cidr")));
        resp.addPairs(pair);
      }
      out.onNext(resp.build());
      out.onCompleted();
    }
  }

  @Test
  void bulkItemErrorsReportTheServerMessageOnce() throws Exception {
    try (var s = TestServers.start(new BulkServer())) {
      var e =
          assertThrows(
              FailedPreconditionException.class,
              () -> s.client().checkBulk(List.of(check("a"), check("broken"))));
      assertEquals("check item 1: permission view not found", e.getMessage());
      assertEquals("permission view not found", e.serverMessage());
      var status = Status.fromThrowable(e);
      assertEquals(Status.Code.FAILED_PRECONDITION, status.getCode());
      assertEquals("permission view not found", status.getDescription());
      assertEquals(
          status.getDescription(), e.toStatusRuntimeException().getStatus().getDescription());

      var returned =
          s.client()
              .checkBulk(
                  List.of(check("broken")),
                  CheckOptions.builder().bulkItemErrors(BulkItemErrors.RETURNED).build());
      assertEquals(
          "permission view not found",
          Status.fromThrowable(returned.get(0).error()).getDescription());
    }
  }

  @Test
  void deleteProgressErrorsReportTheServerMessageOnce() throws Exception {
    var srv = new FlakyServer(false);
    try (var s = TestServers.start(srv)) {
      var e =
          assertThrows(
              SpiceDBException.class,
              () -> s.client().deleteAllRelationshipsNonAtomic(Filter.of("document")));
      assertTrue(e.deleteProgress() != null);
      assertEquals(Status.Code.UNAVAILABLE, Status.fromThrowable(e).getCode());
    }
  }

  @Test
  void checkBulkRoundTripsTheServersResponse() throws Exception {
    try (var s = TestServers.start(new BulkServer())) {
      var requests =
          List.of(
              check("a"),
              check("broken"),
              new PermissionCheck(
                  new ObjectRef("document", "c"),
                  "view",
                  SubjectRef.direct(new ObjectRef("user", "alice")),
                  Consistency.full(),
                  Map.of("cidr", "10.0.0.0/8")));
      var options =
          CheckOptions.builder()
              .bulkItemErrors(BulkItemErrors.RETURNED)
              .context(Map.of("region", "us"))
              .build();
      var results = s.client().checkBulk(requests, options);
      var got = ProtoConv.checkBulkToProto(requests, results, options.context());

      var want =
          CheckBulkPermissionsResponse.newBuilder()
              .setCheckedAt(com.authzed.api.v1.ZedToken.newBuilder().setToken("bulk-rev"));
      for (var r : requests) {
        var pair =
            CheckBulkPermissionsPair.newBuilder()
                .setRequest(ProtoConv.toProto(r, options.context()));
        if (r.resource().objectId().equals("broken"))
          pair.setError(
              com.google.rpc.Status.newBuilder()
                  .setCode(Status.Code.FAILED_PRECONDITION.value())
                  .setMessage("permission view not found"));
        else
          pair.setItem(
              CheckBulkPermissionsResponseItem.newBuilder()
                  .setPermissionship(
                      CheckPermissionResponse.Permissionship.PERMISSIONSHIP_CONDITIONAL_PERMISSION)
                  .setPartialCaveatInfo(
                      PartialCaveatInfo.newBuilder().addMissingRequiredContext("cidr")));
        want.addPairs(pair);
      }
      assertEquals(want.build(), got);
      assertThrows(
          InvalidArgumentException.class,
          () -> ProtoConv.checkBulkToProto(requests.subList(0, 1), results));
    }
  }

  @Test
  @SuppressWarnings("deprecation")
  void lookupSubjectsRoundTripsTheServersResponse() throws Exception {
    // As SpiceDB sends it: the deprecated top-level fields mirror the current ones.
    var wire =
        LookupSubjectsResponse.newBuilder()
            .setLookedUpAt(com.authzed.api.v1.ZedToken.newBuilder().setToken("rev3"))
            .setSubject(
                ResolvedSubject.newBuilder()
                    .setSubjectObjectId("*")
                    .setPermissionship(LookupPermissionship.LOOKUP_PERMISSIONSHIP_HAS_PERMISSION))
            .setSubjectObjectId("*")
            .setPermissionship(LookupPermissionship.LOOKUP_PERMISSIONSHIP_HAS_PERMISSION)
            .addExcludedSubjects(
                ResolvedSubject.newBuilder()
                    .setSubjectObjectId("bob")
                    .setPermissionship(
                        LookupPermissionship.LOOKUP_PERMISSIONSHIP_CONDITIONAL_PERMISSION)
                    .setPartialCaveatInfo(
                        PartialCaveatInfo.newBuilder().addMissingRequiredContext("k")))
            .addExcludedSubjectIds("bob")
            .build();
    var service =
        new PermissionsServiceGrpc.PermissionsServiceImplBase() {
          @Override
          public void lookupSubjects(
              LookupSubjectsRequest request, StreamObserver<LookupSubjectsResponse> out) {
            out.onNext(wire);
            out.onCompleted();
          }
        };
    try (var s = TestServers.start(service)) {
      List<LookupResult.LookupSubject> results;
      try (var stream =
          s.client()
              .lookupSubjects(Consistency.full(), new ObjectRef("document", "d"), "view", "user")) {
        results = stream.toList();
      }
      assertEquals(wire, ProtoConv.toProto(results.get(0)));
    }
  }
}
