package com.authzed.spicedb;

import static org.junit.jupiter.api.Assertions.*;

import build.buf.gen.authzed.api.v1.CheckBulkPermissionsPair;
import build.buf.gen.authzed.api.v1.CheckBulkPermissionsRequest;
import build.buf.gen.authzed.api.v1.CheckBulkPermissionsResponse;
import build.buf.gen.authzed.api.v1.CheckBulkPermissionsResponseItem;
import build.buf.gen.authzed.api.v1.CheckPermissionResponse;
import build.buf.gen.authzed.api.v1.PartialCaveatInfo;
import build.buf.gen.authzed.api.v1.PermissionsServiceGrpc;
import build.buf.gen.authzed.api.v1.ZedToken;
import com.authzed.spicedb.errors.PermissionDeniedException;
import io.grpc.Status;
import io.grpc.stub.StreamObserver;
import java.io.IOException;
import java.util.List;
import java.util.stream.Stream;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.params.ParameterizedTest;
import org.junit.jupiter.params.provider.Arguments;
import org.junit.jupiter.params.provider.MethodSource;

/**
 * Proves that {@link SpiceDBClient#checkPermission}/{@link SpiceDBClient#checkPermissions} surface
 * the server's full three-valued (four-valued including {@code UNSPECIFIED}) {@code
 * CheckPermissionResponse.permissionship}, the missing caveat context, and {@code checked_at} —
 * instead of collapsing the response to a bare {@code boolean} — and that a per-item error from
 * {@code CheckBulkPermissions} surfaces as the correct typed {@link
 * com.authzed.spicedb.errors.SpiceDBException} subtype with the item index preserved in the
 * message, rather than the untyped base exception.
 *
 * <p>Uses the reusable in-process gRPC harness ({@link TestServers}), same pattern as {@link
 * LookupResultsTest}.
 */
class CheckResultsTest {

  // ---------------------------------------------------------------------
  // T1 — hasPermission() is true ONLY for HAS_PERMISSION, parametrized over
  // all four Permissionship values (RULE: "Only an unconditional grant is
  // true", clauses 1 and 2 — a single equality comparison, never a
  // disjunction).
  // ---------------------------------------------------------------------

  private static Stream<Arguments> permissionshipCases() {
    return Stream.of(
        Arguments.of(
            CheckPermissionResponse.Permissionship.PERMISSIONSHIP_HAS_PERMISSION,
            LookupResult.Permissionship.HAS_PERMISSION,
            true),
        Arguments.of(
            CheckPermissionResponse.Permissionship.PERMISSIONSHIP_NO_PERMISSION,
            LookupResult.Permissionship.NO_PERMISSION,
            false),
        Arguments.of(
            CheckPermissionResponse.Permissionship.PERMISSIONSHIP_CONDITIONAL_PERMISSION,
            LookupResult.Permissionship.CONDITIONAL_PERMISSION,
            false),
        Arguments.of(
            CheckPermissionResponse.Permissionship.PERMISSIONSHIP_UNSPECIFIED,
            LookupResult.Permissionship.UNSPECIFIED,
            false));
  }

  @ParameterizedTest
  @MethodSource("permissionshipCases")
  void hasPermissionTrueOnlyForHasPermission(
      CheckPermissionResponse.Permissionship protoPermissionship,
      LookupResult.Permissionship expectedPermissionship,
      boolean expectedHasPermission)
      throws IOException {
    var service = bulkCheckReturning(protoPermissionship, null, "rev-t1");

    try (TestServers servers = TestServers.start(service)) {
      SpiceDBClient client = servers.client();
      CheckResult result =
          client.checkPermission(
              Consistency.full(),
              "view",
              Relationship.of("document", "doc1", "view", "user", "alice"));

      assertEquals(expectedPermissionship, result.permissionship());
      assertEquals(expectedHasPermission, result.hasPermission());
    }
  }

  // ---------------------------------------------------------------------
  // T2 — missingContext carries the server's missing_required_context
  // CONTENTS, asserted by value (not merely non-empty).
  // ---------------------------------------------------------------------

  @Test
  void missingContextCarriesServerValues() throws IOException {
    var service =
        bulkCheckReturning(
            CheckPermissionResponse.Permissionship.PERMISSIONSHIP_CONDITIONAL_PERMISSION,
            List.of("region", "now"),
            "rev-t2");

    try (TestServers servers = TestServers.start(service)) {
      SpiceDBClient client = servers.client();
      CheckResult result =
          client.checkPermission(
              Consistency.full(),
              "view",
              Relationship.of("document", "doc1", "view", "user", "alice"));

      assertEquals(List.of("region", "now"), result.missingContext());
      assertFalse(result.hasPermission());
    }
  }

  @Test
  void missingContextIsEmptyWhenNotConditional() throws IOException {
    var service =
        bulkCheckReturning(
            CheckPermissionResponse.Permissionship.PERMISSIONSHIP_HAS_PERMISSION, null, "rev-t2b");

    try (TestServers servers = TestServers.start(service)) {
      SpiceDBClient client = servers.client();
      CheckResult result =
          client.checkPermission(
              Consistency.full(),
              "view",
              Relationship.of("document", "doc1", "view", "user", "alice"));

      assertEquals(List.of(), result.missingContext());
    }
  }

  // ---------------------------------------------------------------------
  // T3 — checkedAt is populated from the response, and (bulk-specific
  // finding) the single response-level checked_at is propagated to every
  // item, since CheckBulkPermissionsResponseItem carries no per-item token.
  // ---------------------------------------------------------------------

  @Test
  void checkedAtPopulatedFromResponse() throws IOException {
    var service =
        bulkCheckReturning(
            CheckPermissionResponse.Permissionship.PERMISSIONSHIP_HAS_PERMISSION,
            null,
            "rev-checked-at-123");

    try (TestServers servers = TestServers.start(service)) {
      SpiceDBClient client = servers.client();
      CheckResult result =
          client.checkPermission(
              Consistency.full(),
              "view",
              Relationship.of("document", "doc1", "view", "user", "alice"));

      assertEquals("rev-checked-at-123", result.checkedAt());
    }
  }

  @Test
  void checkedAtPropagatesFromResponseToEveryBulkItem() throws IOException {
    var service =
        new PermissionsServiceGrpc.PermissionsServiceImplBase() {
          @Override
          public void checkBulkPermissions(
              CheckBulkPermissionsRequest request,
              StreamObserver<CheckBulkPermissionsResponse> responseObserver) {
            responseObserver.onNext(
                CheckBulkPermissionsResponse.newBuilder()
                    .setCheckedAt(ZedToken.newBuilder().setToken("rev-shared").build())
                    .addPairs(
                        CheckBulkPermissionsPair.newBuilder()
                            .setItem(
                                CheckBulkPermissionsResponseItem.newBuilder()
                                    .setPermissionship(
                                        CheckPermissionResponse.Permissionship
                                            .PERMISSIONSHIP_HAS_PERMISSION)
                                    .build())
                            .build())
                    .addPairs(
                        CheckBulkPermissionsPair.newBuilder()
                            .setItem(
                                CheckBulkPermissionsResponseItem.newBuilder()
                                    .setPermissionship(
                                        CheckPermissionResponse.Permissionship
                                            .PERMISSIONSHIP_NO_PERMISSION)
                                    .build())
                            .build())
                    .build());
            responseObserver.onCompleted();
          }
        };

    try (TestServers servers = TestServers.start(service)) {
      SpiceDBClient client = servers.client();
      List<CheckResult> results =
          client.checkPermissions(
              Consistency.full(),
              "view",
              Relationship.of("document", "doc1", "view", "user", "alice"),
              Relationship.of("document", "doc2", "view", "user", "bob"));

      assertEquals(2, results.size());
      assertEquals("rev-shared", results.get(0).checkedAt());
      assertEquals("rev-shared", results.get(1).checkedAt());
    }
  }

  // ---------------------------------------------------------------------
  // Per-item error defect: a CheckBulkPermissionsPair error must surface as
  // the SPECIFIC typed exception (via ErrorMapper), not the untyped base
  // SpiceDBException, and the item's index must survive in the message.
  // ---------------------------------------------------------------------

  @Test
  void perItemErrorMapsToTypedExceptionWithIndexPreserved() throws IOException {
    var service =
        new PermissionsServiceGrpc.PermissionsServiceImplBase() {
          @Override
          public void checkBulkPermissions(
              CheckBulkPermissionsRequest request,
              StreamObserver<CheckBulkPermissionsResponse> responseObserver) {
            responseObserver.onNext(
                CheckBulkPermissionsResponse.newBuilder()
                    .setCheckedAt(ZedToken.newBuilder().setToken("rev-err").build())
                    .addPairs(
                        CheckBulkPermissionsPair.newBuilder()
                            .setItem(
                                CheckBulkPermissionsResponseItem.newBuilder()
                                    .setPermissionship(
                                        CheckPermissionResponse.Permissionship
                                            .PERMISSIONSHIP_HAS_PERMISSION)
                                    .build())
                            .build())
                    .addPairs(
                        CheckBulkPermissionsPair.newBuilder()
                            .setError(
                                build.buf.gen.google.rpc.Status.newBuilder()
                                    .setCode(Status.Code.PERMISSION_DENIED.value())
                                    .setMessage("schema mismatch on doc2")
                                    .build())
                            .build())
                    .build());
            responseObserver.onCompleted();
          }
        };

    try (TestServers servers = TestServers.start(service)) {
      SpiceDBClient client = servers.client();

      PermissionDeniedException ex =
          assertThrows(
              PermissionDeniedException.class,
              () ->
                  client.checkPermissions(
                      Consistency.full(),
                      "view",
                      Relationship.of("document", "doc1", "view", "user", "alice"),
                      Relationship.of("document", "doc2", "view", "user", "bob")));

      // The typed exception subtype (PermissionDeniedException, not the base SpiceDBException)
      // proves the per-item error code is routed through ErrorMapper.
      assertTrue(
          ex.getMessage().contains("check item 1"),
          "expected message to preserve the item index, got: " + ex.getMessage());
      assertTrue(
          ex.getMessage().contains("schema mismatch on doc2"),
          "expected message to carry the server's per-item error text, got: " + ex.getMessage());
    }
  }

  // ---------------------------------------------------------------------
  // Aggregates — checkAny/checkAll count ONLY HAS_PERMISSION (RULE clause
  // 3): a CONDITIONAL_PERMISSION result must never contribute to a true.
  // ---------------------------------------------------------------------

  @Test
  void checkAnyDoesNotCountConditionalAsGranted() throws IOException {
    var service =
        new PermissionsServiceGrpc.PermissionsServiceImplBase() {
          @Override
          public void checkBulkPermissions(
              CheckBulkPermissionsRequest request,
              StreamObserver<CheckBulkPermissionsResponse> responseObserver) {
            responseObserver.onNext(
                CheckBulkPermissionsResponse.newBuilder()
                    .setCheckedAt(ZedToken.newBuilder().setToken("rev-any").build())
                    .addPairs(
                        CheckBulkPermissionsPair.newBuilder()
                            .setItem(
                                CheckBulkPermissionsResponseItem.newBuilder()
                                    .setPermissionship(
                                        CheckPermissionResponse.Permissionship
                                            .PERMISSIONSHIP_CONDITIONAL_PERMISSION)
                                    .build())
                            .build())
                    .addPairs(
                        CheckBulkPermissionsPair.newBuilder()
                            .setItem(
                                CheckBulkPermissionsResponseItem.newBuilder()
                                    .setPermissionship(
                                        CheckPermissionResponse.Permissionship
                                            .PERMISSIONSHIP_NO_PERMISSION)
                                    .build())
                            .build())
                    .build());
            responseObserver.onCompleted();
          }
        };

    try (TestServers servers = TestServers.start(service)) {
      SpiceDBClient client = servers.client();
      boolean anyGranted =
          client.checkAny(
              Consistency.full(),
              "view",
              Relationship.of("document", "doc1", "view", "user", "alice"),
              Relationship.of("document", "doc2", "view", "user", "bob"));

      assertFalse(anyGranted, "a CONDITIONAL_PERMISSION result must not count as a grant");
    }
  }

  // ---------------------------------------------------------------------
  // checkAll must not be vacuously true on zero relationships: Java's
  // for-loop aggregate (like every language's all/every primitive) never
  // executes its body on an empty array and falls through to `return
  // true`, turning checkAll into a fail-open gate whenever a caller passes
  // a derived relationship array that happens to be empty (a filter that
  // matched nothing, an upstream returning an empty array). RULE: "An
  // aggregate over zero checks is not a grant." checkPermissions already
  // returns List.of() for zero relationships without calling the server, so
  // this never reaches checkBulkPermissions either — the service below
  // fails the test if that assumption ever changes.
  // ---------------------------------------------------------------------

  @Test
  void checkAllReturnsFalseForZeroRelationships() throws IOException {
    var service =
        new PermissionsServiceGrpc.PermissionsServiceImplBase() {
          @Override
          public void checkBulkPermissions(
              CheckBulkPermissionsRequest request,
              StreamObserver<CheckBulkPermissionsResponse> responseObserver) {
            throw new AssertionError(
                "checkAll with zero relationships must not consult the server");
          }
        };

    try (TestServers servers = TestServers.start(service)) {
      SpiceDBClient client = servers.client();
      boolean allGranted = client.checkAll(Consistency.full(), "view");

      assertFalse(
          allGranted, "checkAll must return false, not vacuously true, for zero relationships");
    }
  }

  // ---------------------------------------------------------------------
  // HARD REQUIREMENT: a response with fewer (or more) pairs than request
  // items must fail loudly with a typed error naming both counts, not
  // silently return a misaligned List<CheckResult>. The proto guarantees
  // pairs are returned in request order but says nothing about count, so a
  // short response would otherwise silently desync results[i] from
  // relationships[i] for every item after the gap.
  // ---------------------------------------------------------------------

  @Test
  void checkPermissionsThrowsWhenResponseHasFewerPairsThanRequestItems() throws IOException {
    var service =
        new PermissionsServiceGrpc.PermissionsServiceImplBase() {
          @Override
          public void checkBulkPermissions(
              CheckBulkPermissionsRequest request,
              StreamObserver<CheckBulkPermissionsResponse> responseObserver) {
            // Two relationships requested, only one pair returned.
            responseObserver.onNext(
                CheckBulkPermissionsResponse.newBuilder()
                    .setCheckedAt(ZedToken.newBuilder().setToken("rev-short").build())
                    .addPairs(
                        CheckBulkPermissionsPair.newBuilder()
                            .setItem(
                                CheckBulkPermissionsResponseItem.newBuilder()
                                    .setPermissionship(
                                        CheckPermissionResponse.Permissionship
                                            .PERMISSIONSHIP_HAS_PERMISSION)
                                    .build())
                            .build())
                    .build());
            responseObserver.onCompleted();
          }
        };

    try (TestServers servers = TestServers.start(service)) {
      SpiceDBClient client = servers.client();

      com.authzed.spicedb.errors.SpiceDBException ex =
          assertThrows(
              com.authzed.spicedb.errors.SpiceDBException.class,
              () ->
                  client.checkPermissions(
                      Consistency.full(),
                      "view",
                      Relationship.of("document", "doc1", "view", "user", "alice"),
                      Relationship.of("document", "doc2", "view", "user", "bob")));

      assertTrue(
          ex.getMessage().contains("1") && ex.getMessage().contains("2"),
          "expected message to name both the pair count (1) and item count (2), got: "
              + ex.getMessage());
    }
  }

  @Test
  void checkPermissionsThrowsOnMalformedPairInsteadOfShrinkingResults() throws IOException {
    var service =
        new PermissionsServiceGrpc.PermissionsServiceImplBase() {
          @Override
          public void checkBulkPermissions(
              CheckBulkPermissionsRequest request,
              StreamObserver<CheckBulkPermissionsResponse> responseObserver) {
            // Neither `item` nor `error` set on the pair's `response` oneof — the proto schema
            // guarantees a well-behaved server never sends this, but nothing on the wire
            // prevents it.
            responseObserver.onNext(
                CheckBulkPermissionsResponse.newBuilder()
                    .setCheckedAt(ZedToken.newBuilder().setToken("rev-malformed").build())
                    .addPairs(CheckBulkPermissionsPair.newBuilder().build())
                    .build());
            responseObserver.onCompleted();
          }
        };

    try (TestServers servers = TestServers.start(service)) {
      SpiceDBClient client = servers.client();

      com.authzed.spicedb.errors.SpiceDBException ex =
          assertThrows(
              com.authzed.spicedb.errors.SpiceDBException.class,
              () ->
                  client.checkPermissions(
                      Consistency.full(),
                      "view",
                      Relationship.of("document", "doc1", "view", "user", "alice")));

      assertTrue(
          ex.getMessage().contains("check item 0"),
          "expected message to preserve the item index, got: " + ex.getMessage());
    }
  }

  // ---------------------------------------------------------------------
  // Helper
  // ---------------------------------------------------------------------

  private static PermissionsServiceGrpc.PermissionsServiceImplBase bulkCheckReturning(
      CheckPermissionResponse.Permissionship permissionship,
      List<String> missingRequiredContext,
      String checkedAtToken) {
    return new PermissionsServiceGrpc.PermissionsServiceImplBase() {
      @Override
      public void checkBulkPermissions(
          CheckBulkPermissionsRequest request,
          StreamObserver<CheckBulkPermissionsResponse> responseObserver) {
        var itemBuilder =
            CheckBulkPermissionsResponseItem.newBuilder().setPermissionship(permissionship);
        if (missingRequiredContext != null) {
          itemBuilder.setPartialCaveatInfo(
              PartialCaveatInfo.newBuilder().addAllMissingRequiredContext(missingRequiredContext));
        }
        responseObserver.onNext(
            CheckBulkPermissionsResponse.newBuilder()
                .setCheckedAt(ZedToken.newBuilder().setToken(checkedAtToken).build())
                .addPairs(CheckBulkPermissionsPair.newBuilder().setItem(itemBuilder).build())
                .build());
        responseObserver.onCompleted();
      }
    };
  }
}
