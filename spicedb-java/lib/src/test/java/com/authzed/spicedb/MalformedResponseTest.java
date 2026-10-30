package com.authzed.spicedb;

import static org.junit.jupiter.api.Assertions.*;

import com.authzed.api.v1.CheckBulkPermissionsPair;
import com.authzed.api.v1.CheckBulkPermissionsRequest;
import com.authzed.api.v1.CheckBulkPermissionsResponse;
import com.authzed.api.v1.CheckBulkPermissionsResponseItem;
import com.authzed.api.v1.CheckPermissionRequest;
import com.authzed.api.v1.CheckPermissionResponse;
import com.authzed.api.v1.ExperimentalCountRelationshipsRequest;
import com.authzed.api.v1.ExperimentalCountRelationshipsResponse;
import com.authzed.api.v1.ExperimentalServiceGrpc;
import com.authzed.api.v1.ExportBulkRelationshipsRequest;
import com.authzed.api.v1.ExportBulkRelationshipsResponse;
import com.authzed.api.v1.LookupPermissionship;
import com.authzed.api.v1.LookupResourcesRequest;
import com.authzed.api.v1.LookupResourcesResponse;
import com.authzed.api.v1.LookupSubjectsRequest;
import com.authzed.api.v1.LookupSubjectsResponse;
import com.authzed.api.v1.ObjectReference;
import com.authzed.api.v1.PermissionsServiceGrpc;
import com.authzed.api.v1.ReadRelationshipsRequest;
import com.authzed.api.v1.ReadRelationshipsResponse;
import com.authzed.api.v1.RelationshipUpdate;
import com.authzed.api.v1.SubjectReference;
import com.authzed.api.v1.WatchRequest;
import com.authzed.api.v1.WatchResponse;
import com.authzed.api.v1.WatchServiceGrpc;
import com.authzed.spicedb.errors.ErrorMapper;
import com.authzed.spicedb.protoconv.ProtoConv;
import io.grpc.Status;
import io.grpc.stub.StreamObserver;
import java.util.List;
import java.util.concurrent.atomic.AtomicInteger;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.function.Executable;

/**
 * Root DESIGN.md, "Expose native errors": a response the client cannot interpret fails with {@code
 * INTERNAL} (never retried by default), and is never skipped or filled with empty values.
 */
class MalformedResponseTest {

  private static final SubjectRef JIMMY = SubjectRef.direct(new ObjectRef("user", "jimmy"));

  private static void assertInternal(Executable call) {
    assertInternal(call, "");
  }

  private static void assertInternal(Executable call, String detail) {
    var thrown = assertThrows(RuntimeException.class, call);
    assertEquals(Status.Code.INTERNAL, ErrorMapper.codeOf(thrown), "got: " + thrown);
    assertTrue(
        thrown.getMessage().contains("client detected malformed response from server: " + detail),
        thrown.getMessage());
  }

  private static com.authzed.api.v1.Relationship.Builder relationship() {
    return com.authzed.api.v1.Relationship.newBuilder()
        .setResource(ObjectReference.newBuilder().setObjectType("document").setObjectId("a"))
        .setRelation("viewer")
        .setSubject(
            SubjectReference.newBuilder()
                .setObject(ObjectReference.newBuilder().setObjectType("user").setObjectId("j")));
  }

  @Test
  void readResponseWithoutARelationshipFailsAsInternalWithoutRetrying() throws Exception {
    var calls = new AtomicInteger();
    var service =
        new PermissionsServiceGrpc.PermissionsServiceImplBase() {
          @Override
          public void readRelationships(
              ReadRelationshipsRequest r, StreamObserver<ReadRelationshipsResponse> out) {
            calls.incrementAndGet();
            out.onNext(ReadRelationshipsResponse.getDefaultInstance());
            out.onCompleted();
          }
        };
    try (var servers = TestServers.start(service)) {
      assertInternal(
          () ->
              servers
                  .client()
                  .readRelationships(Consistency.full(), Filter.of("document"))
                  .toList());
      assertEquals(1, calls.get(), "INTERNAL is not retried by default");
    }
  }

  @Test
  void relationshipMissingASubjectOrResourceFailsAsInternal() throws Exception {
    var service =
        new PermissionsServiceGrpc.PermissionsServiceImplBase() {
          @Override
          public void exportBulkRelationships(
              ExportBulkRelationshipsRequest r,
              StreamObserver<ExportBulkRelationshipsResponse> out) {
            out.onNext(
                ExportBulkRelationshipsResponse.newBuilder()
                    .addRelationships(relationship().clearResource())
                    .build());
            out.onCompleted();
          }

          @Override
          public void readRelationships(
              ReadRelationshipsRequest r, StreamObserver<ReadRelationshipsResponse> out) {
            out.onNext(
                ReadRelationshipsResponse.newBuilder()
                    .setRelationship(relationship().clearSubject())
                    .build());
            out.onCompleted();
          }
        };
    try (var servers = TestServers.start(service)) {
      assertInternal(
          () ->
              servers
                  .client()
                  .exportRelationships(Consistency.full(), Filter.of("document"))
                  .toList());
      assertInternal(
          () ->
              servers
                  .client()
                  .readRelationships(Consistency.full(), Filter.of("document"))
                  .toList());
    }
  }

  @Test
  void lookupsWithoutAnIdFailAsInternal() throws Exception {
    var service =
        new PermissionsServiceGrpc.PermissionsServiceImplBase() {
          @Override
          public void lookupResources(
              LookupResourcesRequest r, StreamObserver<LookupResourcesResponse> out) {
            out.onNext(LookupResourcesResponse.getDefaultInstance());
            out.onCompleted();
          }

          @Override
          public void lookupSubjects(
              LookupSubjectsRequest r, StreamObserver<LookupSubjectsResponse> out) {
            out.onNext(LookupSubjectsResponse.getDefaultInstance());
            out.onCompleted();
          }
        };
    try (var servers = TestServers.start(service)) {
      assertInternal(
          () ->
              servers
                  .client()
                  .lookupResources(Consistency.full(), "document", "view", JIMMY)
                  .toList());
      assertInternal(
          () ->
              servers
                  .client()
                  .lookupSubjects(
                      Consistency.full(), new ObjectRef("document", "a"), "view", "user")
                  .toList());
    }
  }

  @Test
  void watchUpdateWithoutARelationshipFailsAsInternal() throws Exception {
    var service =
        new WatchServiceGrpc.WatchServiceImplBase() {
          @Override
          public void watch(WatchRequest r, StreamObserver<WatchResponse> out) {
            out.onNext(
                WatchResponse.newBuilder()
                    .addUpdates(
                        RelationshipUpdate.newBuilder()
                            .setOperation(RelationshipUpdate.Operation.OPERATION_TOUCH))
                    .build());
            out.onCompleted();
          }
        };
    try (var servers = TestServers.start(service)) {
      assertInternal(() -> servers.client().updates(List.of("document"), null).toList());
    }
  }

  @Test
  void bulkCheckPairWithNeitherItemNorErrorFailsAsInternal() throws Exception {
    var service =
        new PermissionsServiceGrpc.PermissionsServiceImplBase() {
          @Override
          public void checkBulkPermissions(
              CheckBulkPermissionsRequest r, StreamObserver<CheckBulkPermissionsResponse> out) {
            out.onNext(
                CheckBulkPermissionsResponse.newBuilder()
                    .addPairs(CheckBulkPermissionsPair.newBuilder().setRequest(r.getItems(0)))
                    .build());
            out.onCompleted();
          }
        };
    try (var servers = TestServers.start(service)) {
      var rel = Relationship.of(new ObjectRef("document", "a"), "view", JIMMY);
      assertInternal(
          () ->
              servers
                  .client()
                  .checkBulk(List.of(TestRequests.check(rel, "view", Consistency.full()))));
    }
  }

  @Test
  void counterResponseWithoutAValueFailsAsInternal() throws Exception {
    var service =
        new ExperimentalServiceGrpc.ExperimentalServiceImplBase() {
          @Override
          public void experimentalCountRelationships(
              ExperimentalCountRelationshipsRequest r,
              StreamObserver<ExperimentalCountRelationshipsResponse> out) {
            out.onNext(ExperimentalCountRelationshipsResponse.getDefaultInstance());
            out.onCompleted();
          }
        };
    try (var servers = TestServers.start(service)) {
      assertInternal(() -> servers.client().experimentalCountRelationships("c"));
    }
  }

  @Test
  void protoConvRejectsMissingObjectsAsInternal() {
    assertInternal(() -> ProtoConv.fromProto(ObjectReference.getDefaultInstance()));
    assertInternal(() -> ProtoConv.fromProto(SubjectReference.getDefaultInstance()));
    assertInternal(() -> ProtoConv.fromProto(relationship().setRelation("").build()));
  }

  /**
   * The server always sets a check's or looked-up resource's permissionship, so an unset one is a
   * malformed response the client detects, never an UNSPECIFIED result the caller must handle. The
   * message calls it unset: UNSPECIFIED is the safe value a newer permissionship maps to.
   */
  @Test
  void unsetPermissionshipFailsAsInternal() throws Exception {
    var service =
        new PermissionsServiceGrpc.PermissionsServiceImplBase() {
          @Override
          public void checkPermission(
              CheckPermissionRequest r, StreamObserver<CheckPermissionResponse> out) {
            out.onNext(CheckPermissionResponse.getDefaultInstance());
            out.onCompleted();
          }

          @Override
          public void checkBulkPermissions(
              CheckBulkPermissionsRequest r, StreamObserver<CheckBulkPermissionsResponse> out) {
            out.onNext(
                CheckBulkPermissionsResponse.newBuilder()
                    .addPairs(
                        CheckBulkPermissionsPair.newBuilder()
                            .setRequest(r.getItems(0))
                            .setItem(CheckBulkPermissionsResponseItem.getDefaultInstance()))
                    .build());
            out.onCompleted();
          }

          @Override
          public void lookupResources(
              LookupResourcesRequest r, StreamObserver<LookupResourcesResponse> out) {
            out.onNext(LookupResourcesResponse.newBuilder().setResourceObjectId("a").build());
            out.onCompleted();
          }

          @Override
          public void lookupSubjects(
              LookupSubjectsRequest r, StreamObserver<LookupSubjectsResponse> out) {
            out.onNext(
                LookupSubjectsResponse.newBuilder()
                    .setSubject(
                        com.authzed.api.v1.ResolvedSubject.newBuilder().setSubjectObjectId("j"))
                    .build());
            out.onCompleted();
          }
        };
    try (var servers = TestServers.start(service)) {
      var rel = Relationship.of(new ObjectRef("document", "a"), "view", JIMMY);
      var check = TestRequests.check(rel, "view", Consistency.full());
      assertInternal(
          () -> servers.client().check(check), "check response: permissionship is unset");
      assertInternal(
          () -> servers.client().checkBulk(List.of(check)),
          "check item 0: permissionship is unset");
      // A malformed item is not a per-item error: returned item errors still fail the call.
      assertInternal(
          () ->
              servers
                  .client()
                  .checkBulk(
                      List.of(check),
                      CheckOptions.builder().bulkItemErrors(BulkItemErrors.RETURNED).build()));
      assertInternal(
          () ->
              servers
                  .client()
                  .lookupResources(Consistency.full(), "document", "view", JIMMY)
                  .toList(),
          "looked-up resource: permissionship is unset");
      // The HAS_PERMISSION-only filter does not hide it as "not granted".
      assertInternal(
          () ->
              servers
                  .client()
                  .lookupResources(
                      Consistency.full(),
                      "document",
                      "view",
                      JIMMY,
                      LookupOptions.builder()
                          .permissionshipFilter(LookupPermissionshipFilter.HAS_PERMISSION_ONLY)
                          .build())
                  .toList());
      assertInternal(
          () ->
              servers
                  .client()
                  .lookupSubjects(
                      Consistency.full(), new ObjectRef("document", "a"), "view", "user")
                  .toList(),
          "looked-up subject: permissionship is unset");
    }
    assertInternal(
        () ->
            ProtoConv.fromProto(
                LookupResourcesResponse.newBuilder()
                    .setResourceObjectId("a")
                    .setPermissionship(LookupPermissionship.LOOKUP_PERMISSIONSHIP_UNSPECIFIED)
                    .build()));
  }

  /**
   * Only the unset (zero) permissionship is malformed. A value newer than this client maps to
   * UNSPECIFIED, which never grants, and does not fail the call, so upgrading SpiceDB cannot turn
   * into a client outage.
   */
  @Test
  void newerPermissionshipMapsToUnspecifiedWithoutFailing() throws Exception {
    int newer = 99;
    var service =
        new PermissionsServiceGrpc.PermissionsServiceImplBase() {
          @Override
          public void checkPermission(
              CheckPermissionRequest r, StreamObserver<CheckPermissionResponse> out) {
            out.onNext(CheckPermissionResponse.newBuilder().setPermissionshipValue(newer).build());
            out.onCompleted();
          }

          @Override
          public void checkBulkPermissions(
              CheckBulkPermissionsRequest r, StreamObserver<CheckBulkPermissionsResponse> out) {
            out.onNext(
                CheckBulkPermissionsResponse.newBuilder()
                    .addPairs(
                        CheckBulkPermissionsPair.newBuilder()
                            .setRequest(r.getItems(0))
                            .setItem(
                                CheckBulkPermissionsResponseItem.newBuilder()
                                    .setPermissionshipValue(newer)))
                    .build());
            out.onCompleted();
          }

          @Override
          public void lookupResources(
              LookupResourcesRequest r, StreamObserver<LookupResourcesResponse> out) {
            out.onNext(
                LookupResourcesResponse.newBuilder()
                    .setResourceObjectId("a")
                    .setPermissionshipValue(newer)
                    .build());
            out.onCompleted();
          }

          @Override
          public void lookupSubjects(
              LookupSubjectsRequest r, StreamObserver<LookupSubjectsResponse> out) {
            out.onNext(
                LookupSubjectsResponse.newBuilder()
                    .setSubject(
                        com.authzed.api.v1.ResolvedSubject.newBuilder()
                            .setSubjectObjectId("j")
                            .setPermissionshipValue(newer))
                    .build());
            out.onCompleted();
          }
        };
    try (var servers = TestServers.start(service)) {
      var rel = Relationship.of(new ObjectRef("document", "a"), "view", JIMMY);
      var check = TestRequests.check(rel, "view", Consistency.full());
      var single = servers.client().check(check);
      assertEquals(Permissionship.UNSPECIFIED, single.permissionship());
      assertFalse(single.hasPermission());
      var bulk = servers.client().checkBulk(List.of(check));
      assertEquals(Permissionship.UNSPECIFIED, bulk.get(0).permissionship());
      assertFalse(bulk.get(0).hasPermission());
      var resources =
          servers.client().lookupResources(Consistency.full(), "document", "view", JIMMY).toList();
      assertEquals(Permissionship.UNSPECIFIED, resources.get(0).permissionship());
      // The HAS_PERMISSION-only filter excludes it rather than failing.
      assertEquals(
          List.of(),
          servers
              .client()
              .lookupResources(
                  Consistency.full(),
                  "document",
                  "view",
                  JIMMY,
                  LookupOptions.builder()
                      .permissionshipFilter(LookupPermissionshipFilter.HAS_PERMISSION_ONLY)
                      .build())
              .toList());
      var subjects =
          servers
              .client()
              .lookupSubjects(Consistency.full(), new ObjectRef("document", "a"), "view", "user")
              .toList();
      assertEquals(Permissionship.UNSPECIFIED, subjects.get(0).subject().permissionship());
    }
  }
}
