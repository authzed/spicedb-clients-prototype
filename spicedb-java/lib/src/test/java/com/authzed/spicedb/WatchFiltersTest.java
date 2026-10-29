package com.authzed.spicedb;

import static org.junit.jupiter.api.Assertions.*;

import com.authzed.api.v1.WatchRequest;
import com.authzed.api.v1.WatchResponse;
import com.authzed.api.v1.WatchServiceGrpc;
import io.grpc.stub.StreamObserver;
import java.util.ArrayList;
import java.util.List;
import org.junit.jupiter.api.Test;

class WatchFiltersTest {
  @Test
  void filtersSnapshotAndSerializeEveryConstraintAndRejectConflictingObjectTypes()
      throws Exception {
    var requests = new ArrayList<WatchRequest>();
    var service =
        new WatchServiceGrpc.WatchServiceImplBase() {
          public void watch(WatchRequest request, StreamObserver<WatchResponse> out) {
            requests.add(request);
            out.onCompleted();
          }
        };
    var filters =
        new ArrayList<>(
            List.of(
                Filter.of("document")
                    .withResourceId("one")
                    .withRelation("viewer")
                    .withSubjectType("user")
                    .withSubjectId("alice")
                    .withSubjectRelation(SubjectRelationFilter.direct()),
                Filter.of("folder")
                    .withResourceIdPrefix("public-")
                    .withRelation("reader")
                    .withSubjectType("team")
                    .withSubjectId("eng")
                    .withSubjectRelation(SubjectRelationFilter.named("member"))));
    var builder = WatchOptions.builder().relationshipFilters(filters);
    filters.clear();
    var options = builder.build();
    assertEquals(2, options.relationshipFilters().size());
    assertThrows(UnsupportedOperationException.class, () -> options.relationshipFilters().clear());
    try (var servers = TestServers.start(service)) {
      assertThrows(
          com.authzed.spicedb.errors.InvalidArgumentException.class,
          () -> servers.client().updates(List.of("document"), null, options));
      assertTrue(requests.isEmpty());
      try (var stream = servers.client().updates(List.of(), new ZedToken("start"), options)) {
        assertEquals(0, stream.count());
      }
      var request = requests.get(0);
      assertEquals("start", request.getOptionalStartCursor().getToken());
      assertEquals(0, request.getOptionalObjectTypesCount());
      assertEquals(2, request.getOptionalRelationshipFiltersCount());
      var direct = request.getOptionalRelationshipFilters(0);
      assertEquals("document", direct.getResourceType());
      assertEquals("one", direct.getOptionalResourceId());
      assertEquals("viewer", direct.getOptionalRelation());
      assertEquals("user", direct.getOptionalSubjectFilter().getSubjectType());
      assertEquals("alice", direct.getOptionalSubjectFilter().getOptionalSubjectId());
      assertTrue(direct.getOptionalSubjectFilter().hasOptionalRelation());
      assertEquals("", direct.getOptionalSubjectFilter().getOptionalRelation().getRelation());
      var named = request.getOptionalRelationshipFilters(1);
      assertEquals("folder", named.getResourceType());
      assertEquals("public-", named.getOptionalResourceIdPrefix());
      assertEquals("reader", named.getOptionalRelation());
      assertEquals("team", named.getOptionalSubjectFilter().getSubjectType());
      assertEquals("eng", named.getOptionalSubjectFilter().getOptionalSubjectId());
      assertEquals("member", named.getOptionalSubjectFilter().getOptionalRelation().getRelation());
      try (var stream =
          servers
              .client()
              .updates(
                  List.of(), null, WatchOptions.builder().relationshipFilters(List.of()).build())) {
        assertEquals(0, stream.count());
      }
      assertEquals(0, requests.get(1).getOptionalRelationshipFiltersCount());
      assertEquals(0, requests.get(1).getOptionalObjectTypesCount());
      try (var stream = servers.client().updates(List.of("document"), null)) {
        assertEquals(0, stream.count());
      }
      assertEquals(List.of("document"), requests.get(2).getOptionalObjectTypesList());
    }
  }
}
