package com.authzed.spicedb;

import static org.junit.jupiter.api.Assertions.*;

import com.authzed.api.materialize.v0.LookupPermissionSetsRequest;
import com.authzed.api.materialize.v0.LookupPermissionSetsResponse;
import com.authzed.api.materialize.v0.MemberReference;
import com.authzed.api.materialize.v0.PermissionSetChange;
import com.authzed.api.materialize.v0.SetReference;
import com.authzed.api.materialize.v0.WatchPermissionSetsServiceGrpc;
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
import com.authzed.api.v1.ResolvedSubject;
import com.authzed.api.v1.SubjectReference;
import io.grpc.BindableService;
import io.grpc.ManagedChannel;
import io.grpc.Server;
import io.grpc.inprocess.InProcessChannelBuilder;
import io.grpc.inprocess.InProcessServerBuilder;
import io.grpc.stub.StreamObserver;
import java.time.Duration;
import java.util.function.Function;
import java.util.stream.Stream;
import org.junit.jupiter.api.Test;

/**
 * Root DESIGN.md: the per-page default deadline bounds only waiting on the server, never the time
 * the caller spends between items. Each read below has a healthy fake that answers at once, a 200
 * ms client default, and a caller that spends 100 ms on each of 5 items: 500 ms in all, so the read
 * fails if processing time counted.
 */
class SlowConsumerDeadlineTest {

  private static final int ITEMS = 5;
  private static final Duration DEFAULT = Duration.ofMillis(200);

  private static <T> void consumeSlowly(
      BindableService service, Function<SpiceDBClient, Stream<T>> read) throws Exception {
    String name = InProcessServerBuilder.generateName();
    Server server = InProcessServerBuilder.forName(name).addService(service).build().start();
    ManagedChannel channel = InProcessChannelBuilder.forName(name).build();
    try {
      var client = SpiceDBClient.forChannel(channel, DEFAULT);
      int seen = 0;
      try (var stream = read.apply(client)) {
        var it = stream.iterator();
        while (it.hasNext()) {
          it.next();
          seen++;
          Thread.sleep(100);
        }
      }
      assertEquals(ITEMS, seen);
    } finally {
      channel.shutdownNow();
      server.shutdownNow();
    }
  }

  /**
   * Sends {@code items} as the client asks for them (gRPC flow control), as a server with more than
   * a flow-control window does, so the call stays open while the caller works through the items.
   */
  private static <M> void sendOnDemand(java.util.List<M> items, StreamObserver<M> out) {
    var call = (io.grpc.stub.ServerCallStreamObserver<M>) out;
    var next = new java.util.concurrent.atomic.AtomicInteger();
    call.setOnReadyHandler(
        () -> {
          while (call.isReady() && next.get() < items.size())
            call.onNext(items.get(next.getAndIncrement()));
          if (next.get() == items.size() && next.getAndIncrement() == items.size())
            call.onCompleted();
        });
  }

  private static com.authzed.api.v1.Relationship relationship(int i) {
    return com.authzed.api.v1.Relationship.newBuilder()
        .setResource(ObjectReference.newBuilder().setObjectType("document").setObjectId("d" + i))
        .setRelation("viewer")
        .setSubject(
            SubjectReference.newBuilder()
                .setObject(ObjectReference.newBuilder().setObjectType("user").setObjectId("u")))
        .build();
  }

  private static final PermissionsServiceGrpc.PermissionsServiceImplBase PERMISSIONS =
      new PermissionsServiceGrpc.PermissionsServiceImplBase() {
        @Override
        public void readRelationships(
            ReadRelationshipsRequest r, StreamObserver<ReadRelationshipsResponse> out) {
          var items = new java.util.ArrayList<ReadRelationshipsResponse>();
          for (int i = 0; i < ITEMS; i++)
            items.add(
                ReadRelationshipsResponse.newBuilder()
                    .setRelationship(relationship(i))
                    .setAfterResultCursor(com.authzed.api.v1.Cursor.newBuilder().setToken("c" + i))
                    .build());
          sendOnDemand(items, out);
        }

        @Override
        public void lookupResources(
            LookupResourcesRequest r, StreamObserver<LookupResourcesResponse> out) {
          var items = new java.util.ArrayList<LookupResourcesResponse>();
          for (int i = 0; i < ITEMS; i++)
            items.add(
                LookupResourcesResponse.newBuilder()
                    .setResourceObjectId("d" + i)
                    .setPermissionship(LookupPermissionship.LOOKUP_PERMISSIONSHIP_HAS_PERMISSION)
                    .setAfterResultCursor(com.authzed.api.v1.Cursor.newBuilder().setToken("c" + i))
                    .build());
          sendOnDemand(items, out);
        }

        @Override
        public void lookupSubjects(
            LookupSubjectsRequest r, StreamObserver<LookupSubjectsResponse> out) {
          var items = new java.util.ArrayList<LookupSubjectsResponse>();
          for (int i = 0; i < ITEMS; i++)
            items.add(
                LookupSubjectsResponse.newBuilder()
                    .setSubject(
                        ResolvedSubject.newBuilder()
                            .setSubjectObjectId("u" + i)
                            .setPermissionship(
                                LookupPermissionship.LOOKUP_PERMISSIONSHIP_HAS_PERMISSION))
                    .build());
          sendOnDemand(items, out);
        }

        @Override
        public void exportBulkRelationships(
            ExportBulkRelationshipsRequest r, StreamObserver<ExportBulkRelationshipsResponse> out) {
          var items = new java.util.ArrayList<ExportBulkRelationshipsResponse>();
          for (int i = 0; i < ITEMS; i++)
            items.add(
                ExportBulkRelationshipsResponse.newBuilder()
                    .addRelationships(relationship(i))
                    .setAfterResultCursor(com.authzed.api.v1.Cursor.newBuilder().setToken("c" + i))
                    .build());
          sendOnDemand(items, out);
        }
      };

  @Test
  void readRelationshipsDoesNotCountTheCallersTime() throws Exception {
    consumeSlowly(
        PERMISSIONS,
        c ->
            c.readRelationships(
                Consistency.full(),
                Filter.of("document"),
                ReadOptions.builder().pageSize(10).build()));
  }

  @Test
  void singleRequestReadDoesNotCountTheCallersTime() throws Exception {
    consumeSlowly(
        PERMISSIONS,
        c ->
            c.readRelationships(
                Consistency.full(),
                Filter.of("document"),
                ReadOptions.builder().paging(Paging.SINGLE_REQUEST).build()));
  }

  @Test
  void lookupResourcesDoesNotCountTheCallersTime() throws Exception {
    consumeSlowly(
        PERMISSIONS,
        c ->
            c.lookupResources(
                Consistency.full(),
                "document",
                "view",
                SubjectRef.direct(new ObjectRef("user", "u")),
                LookupOptions.builder().pageSize(10).build()));
  }

  @Test
  void lookupSubjectsDoesNotCountTheCallersTime() throws Exception {
    consumeSlowly(
        PERMISSIONS,
        c -> c.lookupSubjects(Consistency.full(), new ObjectRef("document", "d"), "view", "user"));
  }

  @Test
  void exportDoesNotCountTheCallersTime() throws Exception {
    consumeSlowly(
        PERMISSIONS, c -> c.exportRelationships(Consistency.full(), Filter.of("document")));
  }

  @Test
  void permissionSetLookupDoesNotCountTheCallersTime() throws Exception {
    var change =
        PermissionSetChange.newBuilder()
            .setOperation(PermissionSetChange.SetOperation.SET_OPERATION_ADDED)
            .setParentSet(
                SetReference.newBuilder()
                    .setObjectType("document")
                    .setObjectId("one")
                    .setPermissionOrRelation("view"))
            .setChildMember(MemberReference.newBuilder().setObjectType("user").setObjectId("a"))
            .build();
    consumeSlowly(
        new WatchPermissionSetsServiceGrpc.WatchPermissionSetsServiceImplBase() {
          @Override
          public void lookupPermissionSets(
              LookupPermissionSetsRequest r, StreamObserver<LookupPermissionSetsResponse> out) {
            var items = new java.util.ArrayList<LookupPermissionSetsResponse>();
            for (int i = 0; i < ITEMS; i++)
              items.add(LookupPermissionSetsResponse.newBuilder().setChange(change).build());
            sendOnDemand(items, out);
          }
        },
        c -> c.experimentalLookupPermissionSets(null));
  }
}
