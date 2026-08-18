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
  // C5 -- nested Map/List values in CHECK-TIME context must convert to a
  // proper proto Struct/ListValue, not get stringified. Scalars already
  // worked (see C1-C4), which is why no earlier test caught this.
  //
  // The write path no longer stringifies either: SpiceDBClient#toProtoValue
  // is now the single converter for BOTH surfaces -- check-time (via
  // toProtoStruct) and write-time (a relationship's stored caveatContext, via
  // toProtoRelationship). The tests below stay scoped to the check surface;
  // the write surface has its own coverage. Keeping them separate is
  // deliberate, so a regression on one surface can't be masked by the other.
  // ---------------------------------------------------------------------

  @Test
  void c5_nestedMapContextConvertsToProtoStruct() throws IOException {
    var captured = new ArrayList<CheckBulkPermissionsRequest>();
    try (TestServers servers = TestServers.start(capturingService(captured))) {
      SpiceDBClient client = servers.client();

      Map<String, Object> nested = Map.of("min", 10, "max", 20);
      Map<String, Object> callLevel = Map.of("range", nested);

      client.checkPermissions(
          Consistency.full(),
          "view",
          callLevel,
          Relationship.of("document", "doc1", "view", "user", "alice"));

      Struct context = captured.get(0).getItems(0).getContext();
      Value rangeValue = context.getFieldsOrThrow("range");

      assertEquals(
          Value.KindCase.STRUCT_VALUE,
          rangeValue.getKindCase(),
          "nested map must become a proto Struct, not a stringified value: " + rangeValue);
      Struct rangeStruct = rangeValue.getStructValue();
      assertEquals(10.0, rangeStruct.getFieldsOrThrow("min").getNumberValue());
      assertEquals(20.0, rangeStruct.getFieldsOrThrow("max").getNumberValue());
    }
  }

  @Test
  void c5_listContextConvertsToProtoListValue() throws IOException {
    var captured = new ArrayList<CheckBulkPermissionsRequest>();
    try (TestServers servers = TestServers.start(capturingService(captured))) {
      SpiceDBClient client = servers.client();

      Map<String, Object> callLevel = Map.of("tags", List.of("a", "b", 3));

      client.checkPermissions(
          Consistency.full(),
          "view",
          callLevel,
          Relationship.of("document", "doc1", "view", "user", "alice"));

      Struct context = captured.get(0).getItems(0).getContext();
      Value tagsValue = context.getFieldsOrThrow("tags");

      assertEquals(
          Value.KindCase.LIST_VALUE,
          tagsValue.getKindCase(),
          "list must become a proto ListValue, not a stringified value: " + tagsValue);
      List<Value> values = tagsValue.getListValue().getValuesList();
      assertEquals(3, values.size());
      assertEquals("a", values.get(0).getStringValue());
      assertEquals("b", values.get(1).getStringValue());
      assertEquals(3.0, values.get(2).getNumberValue());
    }
  }

  @Test
  void c5_deeplyNestedMapAndListConvertRecursively() throws IOException {
    var captured = new ArrayList<CheckBulkPermissionsRequest>();
    try (TestServers servers = TestServers.start(capturingService(captured))) {
      SpiceDBClient client = servers.client();

      Map<String, Object> inner = Map.of("ids", List.of(1, 2));
      Map<String, Object> outer = Map.of("filter", inner);
      Map<String, Object> callLevel = Map.of("query", outer);

      client.checkPermissions(
          Consistency.full(),
          "view",
          callLevel,
          Relationship.of("document", "doc1", "view", "user", "alice"));

      Struct context = captured.get(0).getItems(0).getContext();
      Struct query = context.getFieldsOrThrow("query").getStructValue();
      Struct filter = query.getFieldsOrThrow("filter").getStructValue();
      List<Value> ids = filter.getFieldsOrThrow("ids").getListValue().getValuesList();
      assertEquals(2, ids.size());
      assertEquals(1.0, ids.get(0).getNumberValue());
      assertEquals(2.0, ids.get(1).getNumberValue());
    }
  }

  // ---------------------------------------------------------------------
  // An unrepresentable check-time caveat context value must raise, not
  // silently stringify (root DESIGN.md "RULE: A conversion that cannot
  // preserve meaning must fail", clause 1). Shared converter with the write
  // path -- SpiceDBClientTest covers toProtoRelationship.
  // ---------------------------------------------------------------------

  private static final class UnrepresentableValue {}

  @Test
  void unrepresentableCheckContextValueThrows() throws IOException {
    var captured = new ArrayList<CheckBulkPermissionsRequest>();
    try (TestServers servers = TestServers.start(capturingService(captured))) {
      SpiceDBClient client = servers.client();

      var thrown =
          assertThrows(
              com.authzed.spicedb.errors.InvalidArgumentException.class,
              () ->
                  client.checkPermissions(
                      Consistency.full(),
                      "view",
                      Map.of("bad_key", new UnrepresentableValue()),
                      Relationship.of("document", "doc1", "view", "user", "alice")));

      assertTrue(thrown.getMessage().contains("bad_key"), thrown.getMessage());
      assertTrue(
          thrown.getMessage().contains(UnrepresentableValue.class.getName()),
          thrown.getMessage());
      assertTrue(captured.isEmpty(), "no request should have been sent to the server");
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
