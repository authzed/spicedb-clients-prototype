package com.authzed.spicedb;

import static org.junit.jupiter.api.Assertions.*;

import build.buf.gen.authzed.api.v1.CheckBulkPermissionsPair;
import build.buf.gen.authzed.api.v1.CheckBulkPermissionsRequest;
import build.buf.gen.authzed.api.v1.CheckBulkPermissionsRequestItem;
import build.buf.gen.authzed.api.v1.CheckBulkPermissionsResponse;
import build.buf.gen.authzed.api.v1.CheckBulkPermissionsResponseItem;
import build.buf.gen.authzed.api.v1.CheckPermissionResponse;
import build.buf.gen.authzed.api.v1.PartialCaveatInfo;
import build.buf.gen.authzed.api.v1.PermissionsServiceGrpc;
import build.buf.gen.authzed.api.v1.ZedToken;
import com.authzed.spicedb.errors.SpiceDBException;
import io.grpc.stub.StreamObserver;
import java.io.IOException;
import java.util.ArrayList;
import java.util.List;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.params.ParameterizedTest;
import org.junit.jupiter.params.provider.ValueSource;

/**
 * Bulk-check chunking.
 *
 * <p>SpiceDB rejects a {@code CheckBulkPermissions} request carrying more items than {@code
 * maxBulkCheckCount} — 10,000, a hard-coded const in {@code internal/services/v1/bulkcheck.go} with
 * no flag to raise or lower it — with {@code ERROR_REASON_TOO_MANY_CHECKS_IN_REQUEST}. Nothing in
 * the proto enforces this ({@code CheckBulkPermissionsRequest.items} carries only a per-item {@code
 * required} rule, not a collection-size rule), so the client is what has to split large inputs.
 */
class CheckChunkingTest {

  /** Mirrors the client's own {@code DEFAULT_CHECK_BATCH_SIZE}, which is private. */
  private static final int CHECK_BATCH_SIZE = 1_000;

  private static final int TOTAL = CHECK_BATCH_SIZE * 2 + 7;

  /**
   * Answers every item, echoing the item's resource ID back through {@code
   * missing_required_context} so a caller can prove which request item each result came from — and
   * therefore that concatenating chunk responses preserved input order. Records the item count of
   * every request it received.
   */
  private static final class EchoService extends PermissionsServiceGrpc.PermissionsServiceImplBase {

    final List<Integer> requestSizes = new ArrayList<>();

    /** When >= 0, the request at that index returns one fewer pair than it was asked for. */
    private final int shortAtRequest;

    /**
     * When >= 0, the pair at that ABSOLUTE index — counted across every request, the way the caller
     * counts — carries a per-item error instead of an item.
     */
    private final int errAtAbsolute;

    EchoService(int shortAtRequest) {
      this(shortAtRequest, -1);
    }

    EchoService(int shortAtRequest, int errAtAbsolute) {
      this.shortAtRequest = shortAtRequest;
      this.errAtAbsolute = errAtAbsolute;
    }

    @Override
    public synchronized void checkBulkPermissions(
        CheckBulkPermissionsRequest request,
        StreamObserver<CheckBulkPermissionsResponse> responseObserver) {
      int index = requestSizes.size();
      int base = requestSizes.stream().mapToInt(Integer::intValue).sum();
      requestSizes.add(request.getItemsCount());

      List<CheckBulkPermissionsRequestItem> items = request.getItemsList();
      if (shortAtRequest == index && !items.isEmpty()) {
        items = items.subList(0, items.size() - 1);
      }

      var response =
          CheckBulkPermissionsResponse.newBuilder()
              .setCheckedAt(ZedToken.newBuilder().setToken("tok").build());
      for (int i = 0; i < items.size(); i++) {
        CheckBulkPermissionsRequestItem item = items.get(i);
        if (errAtAbsolute == base + i) {
          response.addPairs(
              CheckBulkPermissionsPair.newBuilder()
                  .setError(
                      build.buf.gen.google.rpc.Status.newBuilder()
                          .setCode(io.grpc.Status.Code.PERMISSION_DENIED.value())
                          .setMessage("nope")
                          .build())
                  .build());
          continue;
        }
        response.addPairs(
            CheckBulkPermissionsPair.newBuilder()
                .setItem(
                    CheckBulkPermissionsResponseItem.newBuilder()
                        .setPermissionship(
                            CheckPermissionResponse.Permissionship.PERMISSIONSHIP_HAS_PERMISSION)
                        .setPartialCaveatInfo(
                            PartialCaveatInfo.newBuilder()
                                .addMissingRequiredContext(item.getResource().getObjectId())))
                .build());
      }
      responseObserver.onNext(response.build());
      responseObserver.onCompleted();
    }
  }

  /** {@code n} relationships whose resource IDs are their zero-padded index. */
  private static Relationship[] numberedRels(int n) {
    var rels = new Relationship[n];
    for (int i = 0; i < n; i++) {
      rels[i] = Relationship.of("document", String.format("%05d", i), "view", "user", "alice");
    }
    return rels;
  }

  @Test
  void splitsAnOversizedInputIntoChunks() throws IOException {
    var service = new EchoService(-1);
    try (TestServers servers = TestServers.start(service)) {
      List<CheckResult> results =
          servers.client().checkPermissions(Consistency.full(), "view", numberedRels(TOTAL));

      assertEquals(TOTAL, results.size());
      assertEquals(
          List.of(CHECK_BATCH_SIZE, CHECK_BATCH_SIZE, 7),
          service.requestSizes,
          "expected three requests sized by the check batch size, not one unbounded request");
    }
  }

  @Test
  void chunkedResultsStayInInputOrder() throws IOException {
    // The echo carries each item's own resource ID, so a reordering — or a chunk's results landing
    // under the wrong offset — is visible on every one of the 2,007 results, not just at the seams.
    var service = new EchoService(-1);
    try (TestServers servers = TestServers.start(service)) {
      List<CheckResult> results =
          servers.client().checkPermissions(Consistency.full(), "view", numberedRels(TOTAL));

      assertEquals(TOTAL, results.size());
      for (int i = 0; i < TOTAL; i++) {
        assertEquals(
            String.format("%05d", i),
            results.get(i).missingContext().get(0),
            "result " + i + " must carry the answer for request item " + i);
      }
    }
  }

  @ParameterizedTest
  @ValueSource(ints = {1, 999, CHECK_BATCH_SIZE})
  void underTheChunkSizeSendsExactlyOneRequest(int n) throws IOException {
    // The common case must not regress into a loop with per-chunk overhead.
    var service = new EchoService(-1);
    try (TestServers servers = TestServers.start(service)) {
      List<CheckResult> results =
          servers.client().checkPermissions(Consistency.full(), "view", numberedRels(n));

      assertEquals(n, results.size());
      assertEquals(List.of(n), service.requestSizes);
    }
  }

  @Test
  void emptyInputSendsNoRequest() throws IOException {
    // Zero relationships costs zero round trips — not one request carrying an empty item list —
    // and returns an empty list rather than throwing.
    var service = new EchoService(-1);
    try (TestServers servers = TestServers.start(service)) {
      List<CheckResult> results =
          servers.client().checkPermissions(Consistency.full(), "view", new Relationship[0]);

      assertTrue(results.isEmpty());
      assertTrue(service.requestSizes.isEmpty(), "an empty input must not reach the wire at all");
    }
  }

  @Test
  void checkAllOnEmptyInputIsFalseAndSendsNoRequest() throws IOException {
    // Chunking must not resurrect the vacuous-true bug: an aggregate over zero checks is false,
    // and it costs no request.
    var service = new EchoService(-1);
    try (TestServers servers = TestServers.start(service)) {
      assertFalse(servers.client().checkAll(Consistency.full(), "view", new Relationship[0]));
      assertTrue(service.requestSizes.isEmpty());
    }
  }

  @Test
  void lengthGuardFiresOnALaterChunk() throws IOException {
    // The pair-count guard is evaluated per chunk, not once against the caller's total: the first
    // chunk answers in full, the second returns 999 pairs for 1,000 items. Without a per-chunk
    // guard the shortfall would silently desync every result from the second chunk onward.
    var service = new EchoService(1);
    try (TestServers servers = TestServers.start(service)) {
      SpiceDBException ex =
          assertThrows(
              SpiceDBException.class,
              () ->
                  servers
                      .client()
                      .checkPermissions(Consistency.full(), "view", numberedRels(TOTAL)));

      assertTrue(ex.getMessage().contains("999 pair(s)"), ex.getMessage());
      assertTrue(ex.getMessage().contains("1000 request item(s)"), ex.getMessage());
      assertEquals(
          List.of(CHECK_BATCH_SIZE, CHECK_BATCH_SIZE),
          service.requestSizes,
          "two requests went out before the guard fired — the failure was detected on the second"
              + " chunk, not on the whole input up front");
    }
  }

  @Test
  void perItemErrorReportsTheCallersAbsoluteIndex() throws IOException {
    // Chunking made every "check item N" message chunk-relative: a failure at relationship 1003
    // read as "check item 3", so a caller who logs or parses it acts on relationship 3 — one
    // resource's answer attributed to another, the same failure family the pair-count guard exists
    // to prevent, relocated into the diagnostic. spicedb-java/DESIGN.md pins this contract.
    int failing = CHECK_BATCH_SIZE + 3;
    var service = new EchoService(-1, failing);
    try (TestServers servers = TestServers.start(service)) {
      SpiceDBException ex =
          assertThrows(
              SpiceDBException.class,
              () ->
                  servers
                      .client()
                      .checkPermissions(
                          Consistency.full(), "view", numberedRels(CHECK_BATCH_SIZE * 2)));

      assertTrue(
          ex.getMessage().contains("check item " + failing + ":"),
          "must name the caller's index ("
              + failing
              + "), not the chunk-relative 3: "
              + ex.getMessage());
      assertFalse(ex.getMessage().contains("check item 3:"), ex.getMessage());
    }
  }
}
