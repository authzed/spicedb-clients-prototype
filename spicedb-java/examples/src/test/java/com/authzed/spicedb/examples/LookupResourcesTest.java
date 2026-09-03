package com.authzed.spicedb.examples;

import static com.authzed.spicedb.Consistency.*;
import static org.assertj.core.api.Assertions.*;

import build.buf.gen.authzed.api.v1.LookupPermissionship;
import build.buf.gen.authzed.api.v1.LookupResourcesRequest;
import build.buf.gen.authzed.api.v1.LookupResourcesResponse;
import build.buf.gen.authzed.api.v1.PermissionsServiceGrpc;
import com.authzed.spicedb.Filter;
import com.authzed.spicedb.LookupResult;
import com.authzed.spicedb.Relationship;
import com.authzed.spicedb.SpiceDBClient;
import com.authzed.spicedb.Transaction;
import io.grpc.Server;
import io.grpc.ServerBuilder;
import io.grpc.stub.StreamObserver;
import java.io.IOException;
import java.util.List;
import java.util.concurrent.atomic.AtomicBoolean;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

/**
 * Demonstrates finding resources a subject can access using {@link
 * com.authzed.spicedb.SpiceDBClient#lookupResources}.
 */
class LookupResourcesTest extends SpiceDBIntegrationTest {

  @BeforeEach
  void setUp() {
    client.deleteRelationships(Filter.of("document"));

    var txn = new Transaction();
    txn.touch(Relationship.of("document", "firstdoc", "viewer", "user", "alice"));
    txn.touch(Relationship.of("document", "seconddoc", "editor", "user", "alice"));
    txn.touch(Relationship.of("document", "thirddoc", "owner", "user", "bob"));
    client.write(txn);
  }

  @Test
  void alice_can_view_two_documents() {
    List<LookupResult.LookupResource> results;
    try (var stream = client.lookupResources(full(), "document", "view", "user", "alice")) {
      results = stream.toList();
    }

    // Every result carries a permissionship — callers must check it before treating a
    // CONDITIONAL_PERMISSION result as a full grant (see wildcard/caveat-aware examples).
    assertThat(results)
        .allSatisfy(
            r ->
                assertThat(r.permissionship())
                    .isEqualTo(LookupResult.Permissionship.HAS_PERMISSION));
    List<String> resourceIDs =
        results.stream().map(LookupResult.LookupResource::resourceId).toList();
    assertThat(resourceIDs).containsExactlyInAnyOrder("firstdoc", "seconddoc");
  }

  @Test
  void alice_can_edit_only_seconddoc() {
    List<String> resourceIDs;
    try (var stream = client.lookupResources(full(), "document", "edit", "user", "alice")) {
      resourceIDs = stream.map(LookupResult.LookupResource::resourceId).toList();
    }

    assertThat(resourceIDs).containsExactly("seconddoc");
  }

  @Test
  void bob_can_delete_thirddoc() {
    List<String> resourceIDs;
    try (var stream = client.lookupResources(full(), "document", "delete", "user", "bob")) {
      resourceIDs = stream.map(LookupResult.LookupResource::resourceId).toList();
    }

    assertThat(resourceIDs).containsExactly("thirddoc");
  }

  /**
   * {@code withDebug} maps the proto's {@code LookupResourcesRequest.with_debug} field (new
   * upstream): when {@code true}, it asks the server to attach debug information to an error should
   * one occur -- as of this writing SpiceDB only populates it for a maximum-recursion-depth error,
   * and that detail rides the same {@code StatusRuntimeException} cause this client already
   * preserves on every mapped exception. Provoking a real depth-exceeded failure needs a deeply
   * recursive schema this example doesn't otherwise need, so this proves the flag reaches the wire
   * against a stand-in {@code PermissionsService} instead, mirroring {@code spicedb-go}'s {@code
   * WithLookupResourcesDebug} example.
   */
  @Test
  void withDebugReachesTheWireRequest() throws IOException, InterruptedException {
    var gotWithDebug = new AtomicBoolean();
    Server standIn =
        ServerBuilder.forPort(0)
            .addService(
                new PermissionsServiceGrpc.PermissionsServiceImplBase() {
                  @Override
                  public void lookupResources(
                      LookupResourcesRequest request,
                      StreamObserver<LookupResourcesResponse> responseObserver) {
                    gotWithDebug.set(request.getWithDebug());
                    responseObserver.onNext(
                        LookupResourcesResponse.newBuilder()
                            .setResourceObjectId("doc1")
                            .setPermissionship(
                                LookupPermissionship.LOOKUP_PERMISSIONSHIP_HAS_PERMISSION)
                            .build());
                    responseObserver.onCompleted();
                  }
                })
            .build()
            .start();

    try (SpiceDBClient debugClient =
        SpiceDBClient.createPlaintext("127.0.0.1:" + standIn.getPort(), "some-token")) {
      try (var stream = debugClient.lookupResources(full(), "document", "view", "user", "alice")) {
        stream.toList();
      }
      assertThat(gotWithDebug)
          .as("WithDebug must be false when the withDebug overload is not used")
          .isFalse();

      try (var stream =
          debugClient.lookupResources(full(), "document", "view", "user", "alice", true)) {
        stream.toList();
      }
      assertThat(gotWithDebug)
          .as("the withDebug overload must set with_debug on the wire request")
          .isTrue();
    } finally {
      standIn.shutdownNow().awaitTermination();
    }
  }
}
