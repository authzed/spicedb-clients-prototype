package com.authzed.spicedb;

import static org.junit.jupiter.api.Assertions.*;

import com.authzed.api.v1.Cursor;
import com.authzed.api.v1.LookupResourcesBatch;
import com.authzed.api.v1.LookupResourcesRequest;
import com.authzed.api.v1.LookupResourcesResponse;
import com.authzed.api.v1.LookupResourcesResult;
import com.authzed.api.v1.PartialCaveatInfo;
import com.authzed.api.v1.PermissionsServiceGrpc;
import com.authzed.api.v1.ZedToken;
import com.authzed.spicedb.errors.ErrorMapper;
import com.authzed.spicedb.errors.LocalValidationException;
import io.grpc.Status;
import io.grpc.stub.StreamObserver;
import java.util.ArrayList;
import java.util.Arrays;
import java.util.List;
import org.junit.jupiter.api.Test;

/**
 * {@link SpiceDBClient#lookupResources} requests batched responses by default and yields one
 * resource at a time from either wire form, with a cursor only where resuming is exact.
 */
class LookupBatchingTest {
  private static final SubjectRef ALICE = SubjectRef.direct(new ObjectRef("user", "alice"));
  private static final int HAS = 1;
  private static final int CONDITIONAL = 2;

  /**
   * Serves {@code doc0..docN} with the given wire permissionship values, honoring the request's
   * limit, cursor (the decimal count of results already sent) and batch size as SpiceDB does: a
   * batch is flushed when full and when the page ends.
   */
  private static final class BatchingServer
      extends PermissionsServiceGrpc.PermissionsServiceImplBase {
    final int[] values;
    final boolean batches;
    final List<LookupResourcesRequest> requests = new ArrayList<>();

    BatchingServer(boolean batches, int... values) {
      this.batches = batches;
      this.values = values;
    }

    static BatchingServer granting(int count) {
      var values = new int[count];
      Arrays.fill(values, HAS);
      return new BatchingServer(true, values);
    }

    @Override
    public void lookupResources(
        LookupResourcesRequest request, StreamObserver<LookupResourcesResponse> out) {
      requests.add(request);
      int start =
          request.hasOptionalCursor()
              ? Integer.parseInt(request.getOptionalCursor().getToken())
              : 0;
      int end =
          request.getOptionalLimit() == 0
              ? values.length
              : Math.min(values.length, start + request.getOptionalLimit());
      int size = batches ? request.getOptionalResponseBatchSize() : 0;
      var batch = LookupResourcesBatch.newBuilder();
      for (int i = start; i < end; i++) {
        if (size == 0) {
          out.onNext(
              LookupResourcesResponse.newBuilder()
                  .setResourceObjectId("doc" + i)
                  .setPermissionshipValue(values[i])
                  .setAfterResultCursor(cursor(i + 1))
                  .setLookedUpAt(ZedToken.newBuilder().setToken("revision"))
                  .build());
          continue;
        }
        var result =
            LookupResourcesResult.newBuilder()
                .setResourceObjectId("doc" + i)
                .setPermissionshipValue(values[i]);
        if (values[i] == CONDITIONAL)
          result.setPartialCaveatInfo(
              PartialCaveatInfo.newBuilder().addMissingRequiredContext("ip"));
        batch.addResults(result);
        if (batch.getResultsCount() == size || i == end - 1) {
          out.onNext(batched(batch.setAfterCursor(cursor(i + 1))));
          batch = LookupResourcesBatch.newBuilder();
        }
      }
      out.onCompleted();
    }

    List<Integer> batchSizes() {
      return requests.stream().map(LookupResourcesRequest::getOptionalResponseBatchSize).toList();
    }

    List<String> cursors() {
      return requests.stream().map(r -> r.getOptionalCursor().getToken()).toList();
    }
  }

  private static Cursor.Builder cursor(int after) {
    return Cursor.newBuilder().setToken("" + after);
  }

  private static LookupResourcesResponse batched(LookupResourcesBatch.Builder batch) {
    return LookupResourcesResponse.newBuilder()
        .setBatched(batch)
        .setLookedUpAt(ZedToken.newBuilder().setToken("revision"))
        .build();
  }

  private static List<LookupResult.LookupResource> lookup(
      PermissionsServiceGrpc.PermissionsServiceImplBase server, LookupOptions options)
      throws Exception {
    try (var servers = TestServers.start(server);
        var results =
            servers
                .client()
                .lookupResources(Consistency.full(), "document", "view", ALICE, options)) {
      return results.toList();
    }
  }

  private static List<String> ids(List<LookupResult.LookupResource> results) {
    return results.stream().map(LookupResult.LookupResource::resourceId).toList();
  }

  private static List<String> cursors(List<LookupResult.LookupResource> results) {
    return results.stream().map(LookupResult.LookupResource::afterResultCursor).toList();
  }

  private static int requestedBatchSize(LookupOptions options) throws Exception {
    var server = BatchingServer.granting(1);
    lookup(server, options);
    return server.batchSizes().get(0);
  }

  @Test
  void requestCarriesTheBatchSize() throws Exception {
    assertEquals(512, requestedBatchSize(LookupOptions.builder().build()));
    assertEquals(50, requestedBatchSize(LookupOptions.builder().pageSize(50).build()));
    assertEquals(1000, requestedBatchSize(LookupOptions.builder().pageSize(2000).build()));
    // An explicit size is sent as given, never clamped to the page size or the server's maximum.
    assertEquals(
        5000,
        requestedBatchSize(LookupOptions.builder().pageSize(3).responseBatchSize(5000).build()));
    assertEquals(
        0, requestedBatchSize(LookupOptions.builder().batching(LookupBatching.DISABLED).build()));
    var single = LookupOptions.builder().paging(Paging.SINGLE_REQUEST);
    assertEquals(1000, requestedBatchSize(single.build()));
    assertEquals(5, requestedBatchSize(single.limit(5).build()));
    assertEquals(1000, requestedBatchSize(single.limit(5000).build()));
  }

  @Test
  void optionsValidateAndReportTheBatchSize() {
    assertThrows(
        LocalValidationException.class, () -> LookupOptions.builder().responseBatchSize(0).build());
    assertThrows(
        LocalValidationException.class,
        () -> LookupOptions.builder().responseBatchSize(-1).build());
    assertThrows(
        LocalValidationException.class, () -> LookupOptions.builder().batching(null).build());
    // Disabled wins over an explicit size, which is still validated and then ignored.
    var sizedButDisabled =
        LookupOptions.builder().responseBatchSize(5).batching(LookupBatching.DISABLED).build();
    assertNull(sizedButDisabled.responseBatchSize());
    assertEquals(0, sizedButDisabled.responseBatchSizeForPage(0));
    assertThrows(
        LocalValidationException.class,
        () ->
            LookupOptions.builder().responseBatchSize(0).batching(LookupBatching.DISABLED).build());

    assertEquals(512, LookupOptions.builder().build().responseBatchSize());
    assertEquals(1000, LookupOptions.builder().pageSize(4096).build().responseBatchSize());
    assertEquals(7, LookupOptions.builder().responseBatchSize(7).build().responseBatchSize());
    assertEquals(
        20,
        LookupOptions.builder()
            .paging(Paging.SINGLE_REQUEST)
            .limit(20)
            .build()
            .responseBatchSize());
    var disabled = LookupOptions.builder().batching(LookupBatching.DISABLED).build();
    assertNull(disabled.responseBatchSize());
    assertEquals(LookupBatching.DISABLED, disabled.toBuilder().build().batching());
    // toBuilder keeps an explicit size explicit and a default one following the page size.
    assertEquals(
        7,
        LookupOptions.builder().responseBatchSize(7).build().toBuilder()
            .pageSize(3)
            .build()
            .responseBatchSize());
    assertEquals(
        3, LookupOptions.builder().build().toBuilder().pageSize(3).build().responseBatchSize());
  }

  @Test
  void batchingOptionsAreLookupResourcesOnly() throws Exception {
    try (var servers = TestServers.start(BatchingServer.granting(1))) {
      var doc = new ObjectRef("document", "a");
      for (var options :
          List.of(
              LookupOptions.builder().responseBatchSize(10).build(),
              LookupOptions.builder().batching(LookupBatching.DISABLED).build())) {
        assertThrows(
            LocalValidationException.class,
            () ->
                servers.client().lookupSubjects(Consistency.full(), doc, "view", "user", options));
        assertThrows(
            LocalValidationException.class,
            () ->
                servers
                    .client()
                    .experimentalRoaringLookupResources(
                        Consistency.full(), "document", "view", ALICE, options));
      }
    }
  }

  @Test
  void batchedStreamYieldsEveryResultWithCursorsOnlyAtBatchEnds() throws Exception {
    var server = new BatchingServer(true, HAS, CONDITIONAL, HAS, HAS, HAS);
    var results = lookup(server, LookupOptions.builder().responseBatchSize(2).build());
    assertEquals(List.of("doc0", "doc1", "doc2", "doc3", "doc4"), ids(results));
    assertEquals(Arrays.asList(null, "2", null, "4", "5"), cursors(results));
    assertEquals(Permissionship.HAS_PERMISSION, results.get(0).permissionship());
    assertNull(results.get(0).partialCaveat());
    assertEquals(Permissionship.CONDITIONAL_PERMISSION, results.get(1).permissionship());
    assertEquals(List.of("ip"), results.get(1).partialCaveat().missingRequiredContext());
    for (var result : results) assertEquals("revision", result.lookedUpAt().value());
    assertEquals(1, server.requests.size());
  }

  @Test
  void pagingCountsResultsNotMessages() throws Exception {
    // Each full page is one message; a message count would end the read after the first page.
    var server = BatchingServer.granting(7);
    var results = lookup(server, LookupOptions.builder().pageSize(3).build());
    assertEquals(7, results.size());
    assertEquals(List.of("", "3", "6"), server.cursors());
    assertEquals(List.of(3, 3, 3), server.batchSizes());
    assertEquals(Arrays.asList(null, null, "3", null, null, "6", "7"), cursors(results));

    // Two messages per full page, and a short page ends the read without another request.
    server = BatchingServer.granting(7);
    results = lookup(server, LookupOptions.builder().pageSize(4).responseBatchSize(2).build());
    assertEquals(List.of("doc0", "doc1", "doc2", "doc3", "doc4", "doc5", "doc6"), ids(results));
    assertEquals(List.of("", "4"), server.cursors());
  }

  @Test
  void singularResponsesToABatchedRequestStillCarryEveryCursor() throws Exception {
    var values = new int[5];
    Arrays.fill(values, HAS);
    var server = new BatchingServer(false, values);
    var results = lookup(server, LookupOptions.builder().pageSize(2).build());
    assertEquals(List.of("1", "2", "3", "4", "5"), cursors(results));
    assertEquals(List.of(2, 2, 2), server.batchSizes());
    assertEquals(List.of("", "2", "4"), server.cursors());
  }

  @Test
  void disabledBatchingCarriesEveryCursor() throws Exception {
    var server = BatchingServer.granting(3);
    var results = lookup(server, LookupOptions.builder().batching(LookupBatching.DISABLED).build());
    assertEquals(List.of("1", "2", "3"), cursors(results));
  }

  @Test
  void filterMovesTheCursorToTheLastKeptResultOfABatch() throws Exception {
    var server =
        new BatchingServer(
            true, HAS, CONDITIONAL, HAS, CONDITIONAL, CONDITIONAL, HAS, CONDITIONAL, 99);
    var grantedOnly =
        LookupOptions.builder()
            .pageSize(5)
            .permissionshipFilter(LookupPermissionshipFilter.HAS_PERMISSION_ONLY)
            .build();
    var results = lookup(server, grantedOnly);
    assertEquals(List.of("doc0", "doc2", "doc5"), ids(results));
    // doc2 resumes after its whole batch: everything between was dropped by the filter.
    assertEquals(Arrays.asList(null, "5", "8"), cursors(results));

    // A batch the filter drops entirely gives no item a cursor, yet paging moves past it.
    server = new BatchingServer(true, CONDITIONAL, CONDITIONAL, HAS);
    results = lookup(server, grantedOnly.toBuilder().pageSize(2).build());
    assertEquals(List.of("doc2"), ids(results));
    assertEquals(List.of("3"), cursors(results));
    assertEquals(List.of("", "2"), server.cursors());
  }

  @Test
  void aValueNewerThanTheClientMapsToUnspecified() throws Exception {
    var results = lookup(new BatchingServer(true, HAS, 99), LookupOptions.builder().build());
    assertEquals(
        List.of(Permissionship.HAS_PERMISSION, Permissionship.UNSPECIFIED),
        results.stream().map(LookupResult.LookupResource::permissionship).toList());
  }

  @Test
  void limitedCallEndsOnACursor() throws Exception {
    var server = BatchingServer.granting(12);
    var results = lookup(server, LookupOptions.builder().pageSize(5).limit(7).build());
    assertEquals(7, results.size());
    assertEquals("7", results.get(6).afterResultCursor());
    assertEquals(Arrays.asList(null, null, null, null, "5", null, "7"), cursors(results));
    // The last page's batches are sized to what is left; its limit is still the page size.
    assertEquals(List.of(5, 2), server.batchSizes());
    assertEquals(
        List.of(5, 5),
        server.requests.stream().map(LookupResourcesRequest::getOptionalLimit).toList());

    // The cursor resumes an unlimited call with a different batch size.
    server = BatchingServer.granting(12);
    var rest = lookup(server, LookupOptions.builder().pageSize(5).cursor("7").build());
    assertEquals(List.of("doc7", "doc8", "doc9", "doc10", "doc11"), ids(rest));

    // Unbatchable remainder: every item of that page carries its cursor.
    server = BatchingServer.granting(12);
    results =
        lookup(server, LookupOptions.builder().pageSize(5).responseBatchSize(3).limit(9).build());
    assertEquals(List.of(3, 0), server.batchSizes());
    assertEquals(Arrays.asList(null, null, "3", null, "5", "6", "7", "8", "9"), cursors(results));

    // With the grants-only filter a limited call is not batched at all.
    server = new BatchingServer(true, HAS, CONDITIONAL, HAS, HAS);
    results =
        lookup(
            server,
            LookupOptions.builder()
                .limit(2)
                .permissionshipFilter(LookupPermissionshipFilter.HAS_PERMISSION_ONLY)
                .build());
    assertEquals(List.of(0), server.batchSizes());
    assertEquals(List.of("1", "3"), cursors(results));
  }

  @Test
  void pageBatchSizeKeepsALimitOnABatchEnd() {
    var options = LookupOptions.builder().pageSize(10).limit(25).build();
    // A full page or more remains: the configured size.
    assertEquals(10, options.responseBatchSizeForPage(0));
    assertEquals(10, options.responseBatchSizeForPage(10));
    // Fewer remain than a batch holds: exactly the remainder.
    assertEquals(5, options.responseBatchSizeForPage(20));

    // The remainder is a multiple of the batch size: unchanged.
    options = LookupOptions.builder().pageSize(10).responseBatchSize(2).limit(14).build();
    assertEquals(2, options.responseBatchSizeForPage(10));
    // Neither: the page is not batched.
    options = LookupOptions.builder().pageSize(10).responseBatchSize(3).limit(14).build();
    assertEquals(3, options.responseBatchSizeForPage(0));
    assertEquals(0, options.responseBatchSizeForPage(10));

    assertEquals(
        0,
        LookupOptions.builder()
            .limit(25)
            .permissionshipFilter(LookupPermissionshipFilter.HAS_PERMISSION_ONLY)
            .build()
            .responseBatchSizeForPage(0));
    // Without a limit the filter does not matter.
    assertEquals(
        512,
        LookupOptions.builder()
            .permissionshipFilter(LookupPermissionshipFilter.HAS_PERMISSION_ONLY)
            .build()
            .responseBatchSizeForPage(0));
    // A single request ends at its limit, where the server flushes the last batch.
    assertEquals(
        3,
        LookupOptions.builder()
            .paging(Paging.SINGLE_REQUEST)
            .responseBatchSize(3)
            .limit(14)
            .build()
            .responseBatchSizeForPage(0));
    assertEquals(
        0,
        LookupOptions.builder()
            .batching(LookupBatching.DISABLED)
            .limit(25)
            .build()
            .responseBatchSizeForPage(20));
  }

  private static PermissionsServiceGrpc.PermissionsServiceImplBase serving(
      LookupResourcesResponse... responses) {
    return new PermissionsServiceGrpc.PermissionsServiceImplBase() {
      @Override
      public void lookupResources(
          LookupResourcesRequest request, StreamObserver<LookupResourcesResponse> out) {
        for (var response : responses) out.onNext(response);
        out.onCompleted();
      }
    };
  }

  private static void assertInternal(
      PermissionsServiceGrpc.PermissionsServiceImplBase server, String detail) {
    var thrown =
        assertThrows(RuntimeException.class, () -> lookup(server, LookupOptions.builder().build()));
    assertEquals(Status.Code.INTERNAL, ErrorMapper.codeOf(thrown), "got: " + thrown);
    assertTrue(
        thrown.getMessage().contains("client detected malformed response from server: " + detail),
        thrown.getMessage());
  }

  @Test
  void malformedBatchResultFailsAsInternal() {
    var good =
        LookupResourcesResult.newBuilder().setResourceObjectId("a").setPermissionshipValue(HAS);
    assertInternal(
        serving(
            batched(
                LookupResourcesBatch.newBuilder()
                    .addResults(good)
                    .addResults(LookupResourcesResult.newBuilder().setPermissionshipValue(HAS))
                    .setAfterCursor(cursor(2)))),
        "looked-up resource has no ID");
    assertInternal(
        serving(
            batched(
                LookupResourcesBatch.newBuilder()
                    .addResults(good)
                    .addResults(LookupResourcesResult.newBuilder().setResourceObjectId("b"))
                    .setAfterCursor(cursor(2)))),
        "looked-up resource: permissionship is unset");
  }

  @Test
  void emptyBatchYieldsNothing() throws Exception {
    var results =
        lookup(
            serving(
                batched(LookupResourcesBatch.newBuilder()),
                batched(
                    LookupResourcesBatch.newBuilder()
                        .addResults(
                            LookupResourcesResult.newBuilder()
                                .setResourceObjectId("a")
                                .setPermissionshipValue(HAS))
                        .setAfterCursor(cursor(1)))),
            LookupOptions.builder().build());
    assertEquals(List.of("a"), ids(results));
    assertEquals(List.of("1"), cursors(results));
    assertEquals(
        List.of(),
        lookup(
            serving(batched(LookupResourcesBatch.newBuilder())), LookupOptions.builder().build()));
  }
}
