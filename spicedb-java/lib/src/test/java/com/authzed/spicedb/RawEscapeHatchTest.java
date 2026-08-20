package com.authzed.spicedb;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

import build.buf.gen.authzed.api.v1.CheckPermissionRequest;
import build.buf.gen.authzed.api.v1.CheckPermissionResponse;
import build.buf.gen.authzed.api.v1.Consistency;
import build.buf.gen.authzed.api.v1.ObjectReference;
import build.buf.gen.authzed.api.v1.PermissionsServiceGrpc;
import build.buf.gen.authzed.api.v1.SubjectReference;
import build.buf.gen.authzed.api.v1.ZedToken;
import com.authzed.spicedb.errors.InvalidArgumentException;
import io.grpc.Metadata;
import io.grpc.Server;
import io.grpc.ServerCall;
import io.grpc.ServerCallHandler;
import io.grpc.ServerInterceptor;
import io.grpc.inprocess.InProcessChannelBuilder;
import io.grpc.inprocess.InProcessServerBuilder;
import io.grpc.stub.StreamObserver;
import java.io.IOException;
import java.util.List;
import java.util.concurrent.CopyOnWriteArrayList;
import org.junit.jupiter.api.Test;

/**
 * The escape hatch, {@link SpiceDBClient#rawChannel()}, exists so a request the idiomatic surface
 * cannot express has a workaround short of forking the client. Asserting the accessor returns
 * something non-null would prove none of that. What matters is whether a caller can build a
 * generated stub on it and get an answer out of a real server, with this client's bearer token
 * attached -- so these tests run a real (in-process) gRPC server and assert the {@code
 * authorization} metadata the server actually received.
 *
 * <p>The RPC driven here is {@code CheckPermission}, the single-check call the idiomatic client
 * never makes ({@link SpiceDBClient#checkPermission} routes every check through {@code
 * CheckBulkPermissions}), so the gap is genuine rather than contrived.
 */
class RawEscapeHatchTest {

  private static final Metadata.Key<String> AUTHORIZATION_KEY =
      Metadata.Key.of("authorization", Metadata.ASCII_STRING_MARSHALLER);

  /**
   * A real in-process server serving both the single-check and bulk-check RPCs, recording the
   * {@code authorization} metadata of every call in arrival order via a server-level interceptor.
   */
  private static final class RecordingServer implements AutoCloseable {
    final Server server;
    final String name;
    final List<String> capturedAuth = new CopyOnWriteArrayList<>();

    RecordingServer() throws IOException {
      this.name = InProcessServerBuilder.generateName();
      ServerInterceptor interceptor =
          new ServerInterceptor() {
            @Override
            public <ReqT, RespT> ServerCall.Listener<ReqT> interceptCall(
                ServerCall<ReqT, RespT> call,
                Metadata headers,
                ServerCallHandler<ReqT, RespT> next) {
              capturedAuth.add(headers.get(AUTHORIZATION_KEY));
              return next.startCall(call, headers);
            }
          };
      this.server =
          InProcessServerBuilder.forName(name)
              .directExecutor()
              .addService(
                  new PermissionsServiceGrpc.PermissionsServiceImplBase() {
                    @Override
                    public void checkPermission(
                        CheckPermissionRequest request,
                        StreamObserver<CheckPermissionResponse> observer) {
                      observer.onNext(
                          CheckPermissionResponse.newBuilder()
                              .setPermissionship(
                                  CheckPermissionResponse.Permissionship
                                      .PERMISSIONSHIP_HAS_PERMISSION)
                              .setCheckedAt(ZedToken.newBuilder().setToken("rev-raw").build())
                              .build());
                      observer.onCompleted();
                    }

                    @Override
                    public void checkBulkPermissions(
                        build.buf.gen.authzed.api.v1.CheckBulkPermissionsRequest request,
                        StreamObserver<build.buf.gen.authzed.api.v1.CheckBulkPermissionsResponse>
                            observer) {
                      var response =
                          build.buf.gen.authzed.api.v1.CheckBulkPermissionsResponse.newBuilder();
                      for (int i = 0; i < request.getItemsCount(); i++) {
                        response.addPairs(
                            build.buf.gen.authzed.api.v1.CheckBulkPermissionsPair.newBuilder()
                                .setItem(
                                    build.buf.gen.authzed.api.v1.CheckBulkPermissionsResponseItem
                                        .newBuilder()
                                        .setPermissionship(
                                            CheckPermissionResponse.Permissionship
                                                .PERMISSIONSHIP_HAS_PERMISSION)
                                        .build())
                                .build());
                      }
                      observer.onNext(response.build());
                      observer.onCompleted();
                    }
                  })
              .intercept(interceptor)
              .build()
              .start();
    }

    InProcessChannelBuilder channelBuilder() {
      return InProcessChannelBuilder.forName(name).directExecutor();
    }

    @Override
    public void close() {
      server.shutdownNow();
    }
  }

  private static CheckPermissionRequest request() {
    return CheckPermissionRequest.newBuilder()
        .setConsistency(Consistency.newBuilder().setFullyConsistent(true).build())
        .setResource(
            ObjectReference.newBuilder().setObjectType("document").setObjectId("readme").build())
        .setPermission("view")
        .setSubject(
            SubjectReference.newBuilder()
                .setObject(
                    ObjectReference.newBuilder().setObjectType("user").setObjectId("jimmy").build())
                .build())
        .build();
  }

  private static SpiceDBClient clientFor(RecordingServer server) {
    return SpiceDBClient.create(
        "localhost:50051",
        "test-token",
        SpiceDBClient.DEFAULT_TIMEOUT,
        server.channelBuilder(),
        SpiceDBClient.withInsecure());
  }

  @Test
  void rawChannelDrivesARealStubAgainstARealServer() throws Exception {
    try (RecordingServer server = new RecordingServer()) {
      try (SpiceDBClient client = clientFor(server)) {
        CheckPermissionResponse response =
            PermissionsServiceGrpc.newBlockingStub(client.rawChannel()).checkPermission(request());

        assertEquals(
            CheckPermissionResponse.Permissionship.PERMISSIONSHIP_HAS_PERMISSION,
            response.getPermissionship());
        assertEquals("rev-raw", response.getCheckedAt().getToken());
      }

      // The bearer token rides the channel this client hands out, so a raw call is
      // authenticated exactly as an idiomatic one is -- nothing extra to attach.
      assertEquals(List.of("Bearer test-token"), server.capturedAuth);
    }
  }

  @Test
  void rawChannelSharesTheConnectionTheIdiomaticMethodsUse() throws Exception {
    try (RecordingServer server = new RecordingServer()) {
      try (SpiceDBClient client = clientFor(server)) {
        CheckResult result =
            client.checkPermission(
                com.authzed.spicedb.Consistency.full(),
                "view",
                Relationship.of("document", "readme", "view", "user", "jimmy"));
        assertTrue(result.hasPermission());

        PermissionsServiceGrpc.newBlockingStub(client.rawChannel()).checkPermission(request());
      }

      // One idiomatic call (via CheckBulkPermissions) and one raw call (via the single-check
      // RPC), both authenticated, both on this client's own connection.
      assertEquals(List.of("Bearer test-token", "Bearer test-token"), server.capturedAuth);
    }
  }

  /**
   * The hatch must never grow into a way to build a connection. Root DESIGN.md, "RULE: Credentials
   * over insecure transport require an explicit opt-in", is enforced in {@code create}, on the
   * single path that builds a channel. Handing back an already-built channel cannot bypass that;
   * accepting an endpoint, key, or transport setting would.
   */
  @Test
  void rawChannelIsAnAccessorNotASecondConstructionPath() throws Exception {
    assertEquals(
        0,
        SpiceDBClient.class.getMethod("rawChannel").getParameterCount(),
        "rawChannel must take no arguments");

    // And the guard still refuses what it always did.
    InvalidArgumentException ex =
        assertThrows(
            InvalidArgumentException.class,
            () ->
                SpiceDBClient.create(
                    "evil.example.com:1234", "test-token", SpiceDBClient.withInsecure()));
    assertTrue(ex.getMessage().contains("allowInsecureRemoteCredentials"), ex.getMessage());
  }
}
