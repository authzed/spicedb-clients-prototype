package com.authzed.spicedb;

import static org.junit.jupiter.api.Assertions.*;

import com.authzed.api.v1.*;
import io.grpc.stub.StreamObserver;
import java.util.*;
import org.junit.jupiter.api.Test;

class TracingContractTest {
  private PermissionCheck request() {
    return new PermissionCheck(
        new ObjectRef("document", "one"),
        "view",
        SubjectRef.direct(new ObjectRef("user", "alice")),
        Consistency.full());
  }

  private com.authzed.api.v1.DebugInformation trace() {
    var child =
        CheckDebugTrace.newBuilder()
            .setResource(ObjectReference.newBuilder().setObjectType("document").setObjectId("one"))
            .setSubject(
                SubjectReference.newBuilder()
                    .setObject(
                        ObjectReference.newBuilder().setObjectType("user").setObjectId("alice")))
            .setPermission("viewer")
            .setPermissionType(CheckDebugTrace.PermissionType.PERMISSION_TYPE_RELATION)
            .setResult(CheckDebugTrace.Permissionship.PERMISSIONSHIP_HAS_PERMISSION)
            .setWasCachedResult(false)
            .setTraceOperationId("child")
            .setSource("test")
            .setDuration(com.google.protobuf.Duration.newBuilder().setNanos(19));
    var root =
        child
            .clone()
            .setPermission("view")
            .setPermissionType(CheckDebugTrace.PermissionType.PERMISSION_TYPE_PERMISSION)
            .setSubProblems(CheckDebugTrace.SubProblems.newBuilder().addTraces(child))
            .setOptionalExpiresAt(com.google.protobuf.Timestamp.newBuilder().setSeconds(123))
            .setCaveatEvaluationInfo(
                CaveatEvalInfo.newBuilder()
                    .setExpression("x == 1")
                    .setCaveatName("test_caveat")
                    .setResult(CaveatEvalInfo.Result.RESULT_MISSING_SOME_CONTEXT)
                    .setContext(
                        com.google.protobuf.Struct.newBuilder()
                            .putFields(
                                "nullable",
                                com.google.protobuf.Value.newBuilder()
                                    .setNullValue(com.google.protobuf.NullValue.NULL_VALUE)
                                    .build()))
                    .setPartialCaveatInfo(
                        PartialCaveatInfo.newBuilder().addMissingRequiredContext("x")));
    return com.authzed.api.v1.DebugInformation.newBuilder()
        .setSchemaUsed("definition user {}")
        .setCheck(root)
        .build();
  }

  @Test
  void singularWireAndRecursiveTrace() throws Exception {
    var captured = new ArrayList<CheckPermissionRequest>();
    try (var server =
        TestServers.start(
            new PermissionsServiceGrpc.PermissionsServiceImplBase() {
              @Override
              public void checkPermission(
                  CheckPermissionRequest r, StreamObserver<CheckPermissionResponse> out) {
                captured.add(r);
                var b =
                    CheckPermissionResponse.newBuilder()
                        .setPermissionship(
                            CheckPermissionResponse.Permissionship.PERMISSIONSHIP_HAS_PERMISSION)
                        .setCheckedAt(
                            com.authzed.api.v1.ZedToken.newBuilder().setToken("revision"));
                if (r.getWithTracing())
                  b.setDebugTrace(trace())
                      .setOptionalExpiresAt(
                          com.google.protobuf.Timestamp.newBuilder().setSeconds(456).setNanos(789));
                out.onNext(b.build());
                out.onCompleted();
              }
            })) {
      var result =
          server
              .client()
              .check(
                  request(),
                  CheckOptions.builder()
                      .tracing(com.authzed.spicedb.CheckTracing.ENABLED)
                      .context(Map.of("region", "us"))
                      .build());
      assertTrue(captured.get(0).getWithTracing());
      assertEquals("us", captured.get(0).getContext().getFieldsOrThrow("region").getStringValue());
      assertTrue(result.hasPermission());
      assertEquals(java.time.Instant.ofEpochSecond(456, 789), result.expiration());
      assertEquals(new ZedToken("revision"), result.checkedAt());
      assertEquals("definition user {}", result.debugTrace().schemaUsed());
      var root = result.debugTrace().check();
      assertEquals(java.time.Instant.ofEpochSecond(123), root.expiration());
      assertTrue(root.caveatEvaluation().context().containsKey("nullable"));
      assertNull(root.caveatEvaluation().context().get("nullable"));
      assertEquals(List.of("x"), root.caveatEvaluation().missingContext());
      var child = ((DebugInformation.Subproblems) root.resolution()).traces().get(0);
      assertEquals(new DebugInformation.Cached(false), child.resolution());
      assertEquals(java.time.Duration.ofNanos(19), child.duration());
      assertEquals("child", child.traceOperationId());
      assertEquals("test", child.source());
      assertNull(server.client().check(request()).debugTrace());
      assertNull(server.client().check(request()).expiration());
      assertFalse(captured.get(1).getWithTracing());
    }
  }

  @Test
  void bulkItemTraceAndMixedConsistency() throws Exception {
    var calls = new java.util.concurrent.atomic.AtomicInteger();
    try (var server =
        TestServers.start(
            new PermissionsServiceGrpc.PermissionsServiceImplBase() {
              @Override
              public void checkBulkPermissions(
                  CheckBulkPermissionsRequest r, StreamObserver<CheckBulkPermissionsResponse> out) {
                calls.incrementAndGet();
                assertTrue(r.getWithTracing());
                var b = CheckBulkPermissionsResponse.newBuilder();
                for (int i = 0; i < r.getItemsCount(); i++) {
                  var item =
                      CheckBulkPermissionsResponseItem.newBuilder()
                          .setPermissionship(
                              CheckPermissionResponse.Permissionship.PERMISSIONSHIP_HAS_PERMISSION);
                  if (i == 0) item.setDebugTrace(trace());
                  b.addPairs(
                      CheckBulkPermissionsPair.newBuilder()
                          .setRequest(r.getItems(i))
                          .setItem(item));
                }
                out.onNext(b.build());
                out.onCompleted();
              }
            })) {
      var results =
          server
              .client()
              .checkBulk(
                  List.of(request(), request()),
                  CheckOptions.builder().tracing(com.authzed.spicedb.CheckTracing.ENABLED).build());
      assertNotNull(results.get(0).debugTrace());
      assertNull(results.get(1).debugTrace());
      assertNull(results.get(0).expiration());
      var other =
          new PermissionCheck(
              request().resource(), "view", request().subject(), Consistency.minLatency());
      assertThrows(
          com.authzed.spicedb.errors.InvalidArgumentException.class,
          () -> server.client().checkBulk(List.of(request(), other)));
      assertEquals(1, calls.get());
    }
  }
}
