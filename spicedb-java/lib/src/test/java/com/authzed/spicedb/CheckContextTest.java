package com.authzed.spicedb;

import static org.junit.jupiter.api.Assertions.*;

import build.buf.gen.authzed.api.v1.CheckBulkPermissionsPair;
import build.buf.gen.authzed.api.v1.CheckBulkPermissionsRequest;
import build.buf.gen.authzed.api.v1.CheckBulkPermissionsResponse;
import build.buf.gen.authzed.api.v1.CheckBulkPermissionsResponseItem;
import build.buf.gen.authzed.api.v1.CheckPermissionResponse;
import build.buf.gen.authzed.api.v1.PermissionsServiceGrpc;
import build.buf.gen.authzed.api.v1.ZedToken;
import com.google.protobuf.Struct;
import com.google.protobuf.Value;
import io.grpc.stub.StreamObserver;
import java.io.IOException;
import java.util.ArrayList;
import java.util.List;
import java.util.Map;
import org.junit.jupiter.api.Test;

/**
 * Proves the caveat CHECK-time context contract (spec D3b) against the ACTUAL BUILT REQUEST:
 *
 * <ul>
 *   <li>C1 — call-level context alone reaches every item in a bulk request
 *   <li>C2 — per-item context ({@link Relationship#withCheckContext}) alone reaches only that item
 *   <li>C3 — the merge rule: item wins per-key, call-level keys absent from the item survive
 *   <li>C4 — neither supplied means no {@code context} field is set on the wire (null, not an empty
 *       {@link Struct})
 * </ul>
 *
 * Uses the reusable in-process gRPC harness ({@link TestServers}), same pattern as {@link
 * CheckResultsTest}, but with a service implementation that CAPTURES the request it received so
 * assertions can inspect the built {@code CheckBulkPermissionsRequestItem.context} by value.
 */
class CheckContextTest {

  // ---------------------------------------------------------------------
  // C1 — call-level context alone reaches every item.
  // ---------------------------------------------------------------------

  @Test
  void c1_callLevelContextReachesEveryItem() throws IOException {
    var captured = new ArrayList<CheckBulkPermissionsRequest>();
    try (TestServers servers = TestServers.start(capturingService(captured))) {
      SpiceDBClient client = servers.client();
      Map<String, Object> callLevel = Map.of("now", 42);

      client.checkPermissions(
          Consistency.full(),
          "view",
          callLevel,
          Relationship.of("document", "doc1", "view", "user", "alice"),
          Relationship.of("document", "doc2", "view", "user", "bob"));

      assertEquals(1, captured.size());
      CheckBulkPermissionsRequest request = captured.get(0);
      assertEquals(2, request.getItemsCount());
      assertEquals(expectedStruct(Map.of("now", 42)), request.getItems(0).getContext());
      assertEquals(expectedStruct(Map.of("now", 42)), request.getItems(1).getContext());
    }
  }

  // ---------------------------------------------------------------------
  // C2 — per-item context alone reaches only that item.
  // ---------------------------------------------------------------------

  @Test
  void c2_perItemContextReachesOnlyThatItem() throws IOException {
    var captured = new ArrayList<CheckBulkPermissionsRequest>();
    try (TestServers servers = TestServers.start(capturingService(captured))) {
      SpiceDBClient client = servers.client();

      Relationship withContext =
          Relationship.of("document", "doc1", "view", "user", "alice")
              .withCheckContext(Map.of("region", "eu"));
      Relationship withoutContext = Relationship.of("document", "doc2", "view", "user", "bob");

      client.checkPermissions(Consistency.full(), "view", withContext, withoutContext);

      assertEquals(1, captured.size());
      CheckBulkPermissionsRequest request = captured.get(0);
      assertEquals(2, request.getItemsCount());
      assertEquals(expectedStruct(Map.of("region", "eu")), request.getItems(0).getContext());
      assertFalse(
          request.getItems(1).hasContext(),
          "item without any context must have no context field set");
    }
  }

  // ---------------------------------------------------------------------
  // C3 — the merge rule: {...callLevel, ...item}. Asserts BOTH items — a
  // single-item assertion (just the overriding item) would also pass under
  // wholesale-replacement semantics, so it would not pin the per-key merge.
  // ---------------------------------------------------------------------

  @Test
  void c3_mergeRuleItemWinsPerKeyCallLevelRetainedForAbsentKeys() throws IOException {
    var captured = new ArrayList<CheckBulkPermissionsRequest>();
    try (TestServers servers = TestServers.start(capturingService(captured))) {
      SpiceDBClient client = servers.client();

      Map<String, Object> callLevel = Map.of("now", 42, "region", "us");
      Relationship item0 =
          Relationship.of("document", "doc1", "view", "user", "alice")
              .withCheckContext(Map.of("region", "eu"));
      Relationship item1 = Relationship.of("document", "doc2", "view", "user", "bob");

      client.checkPermissions(Consistency.full(), "view", callLevel, item0, item1);

      assertEquals(1, captured.size());
      CheckBulkPermissionsRequest request = captured.get(0);
      assertEquals(2, request.getItemsCount());

      assertEquals(
          expectedStruct(Map.of("now", 42, "region", "eu")),
          request.getItems(0).getContext(),
          "item 0 overrides 'region' but must retain the call-level 'now'");
      assertEquals(
          expectedStruct(Map.of("now", 42, "region", "us")),
          request.getItems(1).getContext(),
          "item 1 supplied no per-item context, so it must retain the call-level default"
              + " unchanged");
    }
  }

  // ---------------------------------------------------------------------
  // C4 — neither supplied means no context field on the wire (null, not an
  // empty Struct).
  // ---------------------------------------------------------------------

  @Test
  void c4_neitherSuppliedMeansNoContextFieldOnWire() throws IOException {
    var captured = new ArrayList<CheckBulkPermissionsRequest>();
    try (TestServers servers = TestServers.start(capturingService(captured))) {
      SpiceDBClient client = servers.client();

      client.checkPermissions(
          Consistency.full(), "view", Relationship.of("document", "doc1", "view", "user", "alice"));

      assertEquals(1, captured.size());
      CheckBulkPermissionsRequest request = captured.get(0);
      assertFalse(request.getItems(0).hasContext());
    }
  }

  @Test
  void c4_explicitlyEmptyMapsAlsoMeanNoContextFieldOnWire() throws IOException {
    // An explicitly-supplied empty map is functionally "neither supplied" -- must not become an
    // empty (but present) Struct on the wire.
    var captured = new ArrayList<CheckBulkPermissionsRequest>();
    try (TestServers servers = TestServers.start(capturingService(captured))) {
      SpiceDBClient client = servers.client();

      client.checkPermissions(
          Consistency.full(),
          "view",
          Map.of(),
          Relationship.of("document", "doc1", "view", "user", "alice").withCheckContext(Map.of()));

      CheckBulkPermissionsRequest request = captured.get(0);
      assertFalse(request.getItems(0).hasContext());
    }
  }

  // ---------------------------------------------------------------------
  // checkPermission (singular) and checkAny/checkAll must accept context too
  // (the brief: "checkAny/checkAll need the same shape -- they are
  // aggregates over the same request and must evaluate caveats too").
  // ---------------------------------------------------------------------

  @Test
  void checkPermissionSingularAcceptsContext() throws IOException {
    var captured = new ArrayList<CheckBulkPermissionsRequest>();
    try (TestServers servers = TestServers.start(capturingService(captured))) {
      SpiceDBClient client = servers.client();

      client.checkPermission(
          Consistency.full(),
          "view",
          Relationship.of("document", "doc1", "view", "user", "alice"),
          Map.of("now", 42));

      CheckBulkPermissionsRequest request = captured.get(0);
      assertEquals(expectedStruct(Map.of("now", 42)), request.getItems(0).getContext());
    }
  }

  @Test
  void checkAnyAcceptsContext() throws IOException {
    var captured = new ArrayList<CheckBulkPermissionsRequest>();
    try (TestServers servers = TestServers.start(capturingService(captured))) {
      SpiceDBClient client = servers.client();

      boolean any =
          client.checkAny(
              Consistency.full(),
              "view",
              Map.of("now", 42),
              Relationship.of("document", "doc1", "view", "user", "alice"));

      assertTrue(any);
      CheckBulkPermissionsRequest request = captured.get(0);
      assertEquals(expectedStruct(Map.of("now", 42)), request.getItems(0).getContext());
    }
  }

  @Test
  void checkAllAcceptsContext() throws IOException {
    var captured = new ArrayList<CheckBulkPermissionsRequest>();
    try (TestServers servers = TestServers.start(capturingService(captured))) {
      SpiceDBClient client = servers.client();

      boolean all =
          client.checkAll(
              Consistency.full(),
              "view",
              Map.of("now", 42),
              Relationship.of("document", "doc1", "view", "user", "alice"));

      assertTrue(all);
      CheckBulkPermissionsRequest request = captured.get(0);
      assertEquals(expectedStruct(Map.of("now", 42)), request.getItems(0).getContext());
    }
  }

  // ---------------------------------------------------------------------
  // Helpers
  // ---------------------------------------------------------------------

  private static Struct expectedStruct(Map<String, Object> values) {
    var builder = Struct.newBuilder();
    for (var entry : values.entrySet()) {
      Object v = entry.getValue();
      Value protoValue;
      if (v instanceof Number n) {
        protoValue = Value.newBuilder().setNumberValue(n.doubleValue()).build();
      } else if (v instanceof String s) {
        protoValue = Value.newBuilder().setStringValue(s).build();
      } else if (v instanceof Boolean b) {
        protoValue = Value.newBuilder().setBoolValue(b).build();
      } else {
        throw new IllegalArgumentException("unsupported test value type: " + v);
      }
      builder.putFields(entry.getKey(), protoValue);
    }
    return builder.build();
  }

  /** A fake PermissionsService that records every request it receives and grants everything. */
  private static PermissionsServiceGrpc.PermissionsServiceImplBase capturingService(
      List<CheckBulkPermissionsRequest> captured) {
    return new PermissionsServiceGrpc.PermissionsServiceImplBase() {
      @Override
      public void checkBulkPermissions(
          CheckBulkPermissionsRequest request,
          StreamObserver<CheckBulkPermissionsResponse> responseObserver) {
        captured.add(request);
        var respBuilder =
            CheckBulkPermissionsResponse.newBuilder()
                .setCheckedAt(ZedToken.newBuilder().setToken("rev").build());
        for (int i = 0; i < request.getItemsCount(); i++) {
          respBuilder.addPairs(
              CheckBulkPermissionsPair.newBuilder()
                  .setItem(
                      CheckBulkPermissionsResponseItem.newBuilder()
                          .setPermissionship(
                              CheckPermissionResponse.Permissionship.PERMISSIONSHIP_HAS_PERMISSION)
                          .build())
                  .build());
        }
        responseObserver.onNext(respBuilder.build());
        responseObserver.onCompleted();
      }
    };
  }
}
