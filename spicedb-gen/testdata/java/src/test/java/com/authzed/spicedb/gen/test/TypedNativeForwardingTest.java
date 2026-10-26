package com.authzed.spicedb.gen.test;

import static org.junit.jupiter.api.Assertions.*;

import build.buf.gen.authzed.api.v1.*;
import com.authzed.spicedb.CallOptions;
import com.authzed.spicedb.DeleteAllOptions;
import com.authzed.spicedb.DeleteOptions;
import com.authzed.spicedb.ExportOptions;
import com.authzed.spicedb.Filter;
import com.authzed.spicedb.ImportOptions;
import com.authzed.spicedb.ObjectRef;
import com.authzed.spicedb.SpiceDBClient;
import com.authzed.spicedb.SubjectRef;
import com.authzed.spicedb.WatchOptions;
import com.authzed.spicedb.errors.UnavailableException;
import io.grpc.stub.StreamObserver;
import io.grpc.stub.ServerCallStreamObserver;
import java.time.Duration;
import java.util.*;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.TimeUnit;
import org.junit.jupiter.api.Test;

class TypedNativeForwardingTest {
  private static final Duration TIMEOUT = Duration.ofSeconds(3);

  @Test
  void bulkChecksForwardNativeRequestsContextAndTracing() throws Exception {
    var requests = new ArrayList<CheckBulkPermissionsRequest>();
    var server = io.grpc.ServerBuilder.forPort(0).addService(new PermissionsServiceGrpc.PermissionsServiceImplBase() {
      public void checkBulkPermissions(CheckBulkPermissionsRequest request, StreamObserver<CheckBulkPermissionsResponse> out) {
        requests.add(request);
        var response = CheckBulkPermissionsResponse.newBuilder().setCheckedAt(ZedToken.newBuilder().setToken("checked"));
        for (var item : request.getItemsList()) response.addPairs(CheckBulkPermissionsPair.newBuilder().setRequest(item).setItem(CheckBulkPermissionsResponseItem.newBuilder().setPermissionship(CheckPermissionResponse.Permissionship.PERMISSIONSHIP_HAS_PERMISSION)));
        out.onNext(response.build());out.onCompleted();
      }
    }).build().start();
    try (var raw = SpiceDBClient.createPlaintext("localhost:" + server.getPort(), "test"); var client = new Permissions.TypedClient(raw)) {
      var checks = List.of(new com.authzed.spicedb.PermissionCheck(new ObjectRef("document", "one"), "view", SubjectRef.direct(new ObjectRef("user", "alice")), com.authzed.spicedb.Consistency.full()));
      var options = com.authzed.spicedb.CheckOptions.builder().context(Map.of("region", "us")).tracing(com.authzed.spicedb.CheckTracing.ENABLED).timeout(TIMEOUT).build();
      var results = client.checkBulk(checks, options); assertTrue(results.get(0).hasPermission()); assertEquals("checked", results.get(0).checkedAt().value());
      assertTrue(client.checkAny(checks, options));assertTrue(client.checkAll(checks, options));
      assertEquals(3, requests.size());assertTrue(requests.get(0).getWithTracing());assertEquals("us", requests.get(0).getItems(0).getContext().getFieldsOrThrow("region").getStringValue());
    } finally { server.shutdownNow();server.awaitTermination(); }
  }

  @Test
  void boundedAndNonAtomicDeletionForwardOptionsCursorsAndFailureProgress() throws Exception {
    var requests = new ArrayList<DeleteRelationshipsRequest>();
    var service = new PermissionsServiceGrpc.PermissionsServiceImplBase() {
      public void deleteRelationships(DeleteRelationshipsRequest request, StreamObserver<DeleteRelationshipsResponse> out) {
        requests.add(request);
        if (request.getOptionalCursor().getToken().equals("next")) { out.onError(io.grpc.Status.UNAVAILABLE.asRuntimeException()); return; }
        out.onNext(DeleteRelationshipsResponse.newBuilder().setRelationshipsDeletedCount(2)
            .setDeletedAt(ZedToken.newBuilder().setToken("revision"))
            .setAfterResultCursor(Cursor.newBuilder().setToken("next"))
            .setDeletionProgress(DeleteRelationshipsResponse.DeletionProgress.DELETION_PROGRESS_PARTIAL).build());
        out.onCompleted();
      }
    };
    var server = io.grpc.ServerBuilder.forPort(0).addService(service).build().start();
    try (var raw = SpiceDBClient.createPlaintext("localhost:" + server.getPort(), "test"); var client = new Permissions.TypedClient(raw)) {
      var filter = Filter.of("document").withResourceId("one");
      var bounded = client.deleteRelationships(filter, 2, DeleteOptions.builder().resumeCursor("seed").mustMatch(List.of(filter)).timeout(TIMEOUT).build());
      assertEquals("revision", bounded.revision().value()); assertFalse(bounded.completed()); assertEquals(1, requests.size());
      assertEquals("seed", requests.get(0).getOptionalCursor().getToken()); assertEquals(1, requests.get(0).getOptionalPreconditionsCount());
      requests.clear();
      var error = assertThrows(UnavailableException.class, () -> client.deleteAllRelationshipsNonAtomic(filter, DeleteAllOptions.builder().pageSize(2).resumeCursor("seed").mustMatch(List.of(filter)).timeout(TIMEOUT).build()));
      assertEquals(2, requests.size()); assertEquals("next", requests.get(1).getOptionalCursor().getToken());
      assertEquals("one", requests.get(1).getRelationshipFilter().getOptionalResourceId());
      assertEquals(2, requests.get(1).getOptionalLimit()); assertEquals(1, requests.get(1).getOptionalPreconditionsCount());
      assertEquals(2, error.deleteProgress().acknowledgedCount()); assertEquals("next", error.deleteProgress().lastCursor()); assertEquals(1, error.deleteProgress().completedPages()); assertTrue(error.deleteProgress().outcomeUnknown());
      assertSame(raw.rawChannel(), client.rawChannel());
    } finally { server.shutdownNow(); server.awaitTermination(); }
  }

  @Test
  void watchForwardsFiltersCursorAndCloseCancelsNativeStream() throws Exception {
    var requests = new ArrayList<WatchRequest>();
    var canceled = new CountDownLatch(1);
    var service = new WatchServiceGrpc.WatchServiceImplBase() {
      public void watch(WatchRequest request, StreamObserver<WatchResponse> out) {
        requests.add(request);
        ((ServerCallStreamObserver<WatchResponse>) out).setOnCancelHandler(canceled::countDown);
        out.onNext(WatchResponse.newBuilder().setIsCheckpoint(true).setChangesThrough(ZedToken.newBuilder().setToken("checkpoint")).build());
      }
    };
    var server = io.grpc.ServerBuilder.forPort(0).addService(service).build().start();
    try (var raw = SpiceDBClient.createPlaintext("localhost:" + server.getPort(), "test"); var client = new Permissions.TypedClient(raw)) {
      try (var stream = client.updates(List.of(), new com.authzed.spicedb.ZedToken("start"), WatchOptions.builder().relationshipFilters(List.of(Filter.of("document").withResourceId("one"))).timeout(TIMEOUT).build())) {
        assertEquals("checkpoint", stream.findFirst().orElseThrow().changesThrough().value());
      }
      assertTrue(canceled.await(3, TimeUnit.SECONDS));
      assertEquals("start", requests.get(0).getOptionalStartCursor().getToken());
      assertEquals("one", requests.get(0).getOptionalRelationshipFilters(0).getOptionalResourceId());
      assertEquals(0, requests.get(0).getOptionalObjectTypesCount());
    } finally { server.shutdownNow(); server.awaitTermination(); }
  }

  @Test
  void importExportAndExperimentalCapabilitiesUseNativeStreamsAndOptions() throws Exception {
    var imported = new ArrayList<ImportBulkRelationshipsRequest>();
    var exported = new ArrayList<ExportBulkRelationshipsRequest>();
    var materialized = new ArrayList<build.buf.gen.authzed.api.materialize.v0.ExperimentalCountRelationshipsByFilterRequest>();
    var wireRelationship = Relationship.newBuilder().setResource(ObjectReference.newBuilder().setObjectType("document").setObjectId("one")).setRelation("viewer").setSubject(SubjectReference.newBuilder().setObject(ObjectReference.newBuilder().setObjectType("user").setObjectId("alice"))).build();
    var service = new PermissionsServiceGrpc.PermissionsServiceImplBase() {
      public StreamObserver<ImportBulkRelationshipsRequest> importBulkRelationships(StreamObserver<ImportBulkRelationshipsResponse> out) {
        return new StreamObserver<>() {
          public void onNext(ImportBulkRelationshipsRequest request) { imported.add(request); }
          public void onError(Throwable error) { out.onError(error); }
          public void onCompleted() { out.onNext(ImportBulkRelationshipsResponse.newBuilder().setNumLoaded(2).build()); out.onCompleted(); }
        };
      }
      public void exportBulkRelationships(ExportBulkRelationshipsRequest request, StreamObserver<ExportBulkRelationshipsResponse> out) {
        exported.add(request); out.onNext(ExportBulkRelationshipsResponse.newBuilder().addRelationships(wireRelationship).build()); out.onCompleted();
      }
    };
    var materialize = new build.buf.gen.authzed.api.materialize.v0.RelationshipsServiceGrpc.RelationshipsServiceImplBase() {
      public void experimentalCountRelationshipsByFilter(build.buf.gen.authzed.api.materialize.v0.ExperimentalCountRelationshipsByFilterRequest request, StreamObserver<build.buf.gen.authzed.api.materialize.v0.ExperimentalCountRelationshipsByFilterResponse> out) {
        materialized.add(request); out.onNext(build.buf.gen.authzed.api.materialize.v0.ExperimentalCountRelationshipsByFilterResponse.newBuilder().setRelationshipCount(7).setReadAt(ZedToken.newBuilder().setToken("count-revision")).build()); out.onCompleted();
      }
    };
    var server = io.grpc.ServerBuilder.forPort(0).addService(service).addService(materialize).build().start();
    try (var raw = SpiceDBClient.createPlaintext("localhost:" + server.getPort(), "test"); var client = new Permissions.TypedClient(raw)) {
      var relationship = com.authzed.spicedb.Relationship.of(new ObjectRef("document", "one"), "viewer", SubjectRef.direct(new ObjectRef("user", "alice")));
      var iterations = new java.util.concurrent.atomic.AtomicInteger();
      Iterable<com.authzed.spicedb.Relationship> source = () -> { assertEquals(1, iterations.incrementAndGet()); return List.of(relationship, relationship).iterator(); };
      assertEquals(2, client.importRelationships(source, ImportOptions.builder().batchSize(1).timeout(TIMEOUT).build()));
      assertEquals(2, imported.size()); assertEquals(wireRelationship, imported.get(0).getRelationships(0));
      try (var stream = client.exportRelationships(com.authzed.spicedb.Consistency.full(), Filter.of("document"), ExportOptions.builder().pageSize(5).limit(1).timeout(TIMEOUT).build())) { assertEquals(List.of(relationship), stream.toList()); }
      assertEquals(5, exported.get(0).getOptionalLimit());
      var count = client.experimentalCountRelationshipsByFilter(Filter.of("document").withRelation("viewer"), CallOptions.builder().timeout(TIMEOUT).build());
      assertEquals(7, count.relationshipCount()); assertEquals("count-revision", count.revision().value()); assertEquals("viewer", materialized.get(0).getRelationshipFilter().getOptionalRelation());
    } finally { server.shutdownNow(); server.awaitTermination(); }
  }
}
