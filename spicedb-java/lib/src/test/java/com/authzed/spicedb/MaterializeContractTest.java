package com.authzed.spicedb;

import static org.junit.jupiter.api.Assertions.*;

import build.buf.gen.authzed.api.materialize.v0.*;
import io.grpc.stub.StreamObserver;
import java.util.*;
import org.junit.jupiter.api.Test;

class MaterializeContractTest {
  @Test
  void coversEveryNondeprecatedMaterializeRpc() throws Exception {
    var token = build.buf.gen.authzed.api.v1.ZedToken.newBuilder().setToken("materialized").build();
    var change =
        PermissionSetChange.newBuilder()
            .setAtRevision(token)
            .setOperation(PermissionSetChange.SetOperation.SET_OPERATION_ADDED)
            .setParentSet(
                SetReference.newBuilder()
                    .setObjectType("document")
                    .setObjectId("one")
                    .setPermissionOrRelation("view"))
            .setChildMember(MemberReference.newBuilder().setObjectType("user").setObjectId("alice"))
            .build();
    var cursor =
        Cursor.newBuilder()
            .setToken(token)
            .setStartingIndex(7)
            .setStartingKey("key")
            .setCompletedMembers(true)
            .setLimit(5)
            .setCursor("opaque")
            .build();
    var requests = new ArrayList<LookupPermissionSetsRequest>();
    try (var server =
        TestServers.start(
            new RoaringLookupResourcesServiceGrpc.RoaringLookupResourcesServiceImplBase() {
              public void experimentalRoaringLookupResources(
                  ExperimentalRoaringLookupResourcesRequest r,
                  StreamObserver<ExperimentalRoaringLookupResourcesResponse> out) {
                assertTrue(r.getConsistency().getFullyConsistent());
                assertEquals("member", r.getSubject().getOptionalRelation());
                out.onNext(
                    ExperimentalRoaringLookupResourcesResponse.newBuilder()
                        .setBitmap(com.google.protobuf.ByteString.copyFrom(new byte[] {1, 2}))
                        .setCardinality(9)
                        .setAtRevision(token)
                        .build());
                out.onCompleted();
              }
            },
            new RelationshipsServiceGrpc.RelationshipsServiceImplBase() {
              public void experimentalCountRelationshipsByFilter(
                  ExperimentalCountRelationshipsByFilterRequest r,
                  StreamObserver<ExperimentalCountRelationshipsByFilterResponse> out) {
                assertEquals("document", r.getRelationshipFilter().getResourceType());
                out.onNext(
                    ExperimentalCountRelationshipsByFilterResponse.newBuilder()
                        .setRelationshipCount(12)
                        .setReadAt(token)
                        .build());
                out.onCompleted();
              }
            },
            new WatchPermissionSetsServiceGrpc.WatchPermissionSetsServiceImplBase() {
              public void watchPermissionSets(
                  WatchPermissionSetsRequest r, StreamObserver<WatchPermissionSetsResponse> out) {
                assertEquals(token, r.getOptionalStartingAfter());
                out.onNext(WatchPermissionSetsResponse.newBuilder().setChange(change).build());
                out.onNext(
                    WatchPermissionSetsResponse.newBuilder().setCompletedRevision(token).build());
                out.onNext(WatchPermissionSetsResponse.getDefaultInstance());
                out.onCompleted();
              }

              public void lookupPermissionSets(
                  LookupPermissionSetsRequest r, StreamObserver<LookupPermissionSetsResponse> out) {
                requests.add(r);
                out.onNext(
                    LookupPermissionSetsResponse.newBuilder()
                        .setChange(change)
                        .setCursor(cursor)
                        .build());
                out.onCompleted();
              }

              public void downloadPermissionSets(
                  DownloadPermissionSetsRequest r,
                  StreamObserver<DownloadPermissionSetsResponse> out) {
                assertEquals(token, r.getOptionalAtRevision());
                out.onNext(
                    DownloadPermissionSetsResponse.newBuilder()
                        .setAtRevision(token)
                        .setTimestamp(com.google.protobuf.Timestamp.newBuilder().setSeconds(123))
                        .addFiles(
                            build.buf.gen.authzed.api.materialize.v0.File.newBuilder()
                                .setName("set")
                                .setUrl("https://example.invalid/set"))
                        .build());
                out.onCompleted();
              }
            })) {
      var client = server.client();
      var revision = new ZedToken("materialized");
      var bitmap =
          client.experimentalRoaringLookupResources(
              Consistency.full(),
              "document",
              "view",
              SubjectRef.named(new ObjectRef("group", "team"), "member"));
      assertEquals(9, bitmap.cardinality());
      var copy = bitmap.bitmap();
      copy[0] = 99;
      assertArrayEquals(new byte[] {1, 2}, bitmap.bitmap());
      assertEquals(revision, bitmap.atRevision());
      assertEquals(
          12,
          client.experimentalCountRelationshipsByFilter(Filter.of("document")).relationshipCount());
      try (var stream = client.experimentalWatchPermissionSets(revision)) {
        var events = stream.toList();
        assertInstanceOf(Materialize.Change.class, events.get(0));
        assertInstanceOf(Materialize.CompletedRevision.class, events.get(1));
        assertInstanceOf(Materialize.UnknownEvent.class, events.get(2));
      }
      String resume;
      try (var stream = client.experimentalLookupPermissionSets(revision)) {
        resume = stream.findFirst().orElseThrow().resumeCursor();
      }
      try (var stream =
          client.experimentalLookupPermissionSets(
              revision, PermissionSetOptions.builder().resumeCursor(resume).build())) {
        assertEquals(
            Materialize.SetOperation.ADDED, stream.findFirst().orElseThrow().change().operation());
      }
      assertEquals(cursor, requests.get(1).getOptionalStartingAfterCursor());
      var manifest = client.experimentalDownloadPermissionSets(revision);
      assertEquals(java.time.Instant.ofEpochSecond(123), manifest.timestamp());
      assertEquals("https://example.invalid/set", manifest.files().get(0).url());
      assertThrows(
          com.authzed.spicedb.errors.InvalidArgumentException.class,
          () ->
              client.experimentalRoaringLookupResources(
                  Consistency.full(),
                  "document",
                  "view",
                  SubjectRef.direct(new ObjectRef("user", "alice")),
                  LookupOptions.builder().debug(com.authzed.spicedb.LookupDebug.ENABLED).build()));
    }
  }
}
