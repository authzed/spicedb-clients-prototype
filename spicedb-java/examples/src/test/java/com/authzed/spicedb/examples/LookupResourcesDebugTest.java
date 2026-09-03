package com.authzed.spicedb.examples;

import static org.assertj.core.api.Assertions.*;

import build.buf.gen.authzed.api.v1.DebugInformation;
import build.buf.gen.authzed.api.v1.LookupResourcesRequest;
import build.buf.gen.authzed.api.v1.LookupResourcesResponse;
import build.buf.gen.authzed.api.v1.PermissionsServiceGrpc;
import com.authzed.spicedb.Consistency;
import com.authzed.spicedb.SpiceDBClient;
import com.authzed.spicedb.errors.ResourceExhaustedException;
import com.google.protobuf.Any;
import io.grpc.Server;
import io.grpc.ServerBuilder;
import io.grpc.protobuf.StatusProto;
import io.grpc.stub.StreamObserver;
import java.io.IOException;
import java.util.stream.Stream;
import org.junit.jupiter.api.Test;

/**
 * Demonstrates {@code lookupResources}' {@code withDebug} overload, which sets the new {@code
 * LookupResourcesRequest.with_debug} field. As of this client's proto version, SpiceDB populates
 * debug information only for a {@code MaxDepthExceeded} failure, attaching a {@code
 * DebugInformation} detail to the failed call's error -- there is no successful-response payload to
 * attach it to, since the call errored.
 *
 * <p>That payload gets no dedicated client-native field. Root DESIGN.md's "RULE: Error mapping must
 * not lose the server's detail" is already satisfied generically, because {@link
 * com.authzed.spicedb.errors.ErrorMapper#toSpiceDBException} preserves the underlying {@link
 * io.grpc.StatusRuntimeException} as every mapped exception's cause. This example proves two
 * things: that {@code withDebug} controls whether the server bothers attaching the detail at all,
 * and the intended access path for reading it once attached -- {@link
 * StatusProto#fromThrowable(Throwable)} on the exception's cause, unpacking a {@code
 * DebugInformation} from its details list.
 *
 * <p><b>Why this example stands up its own server.</b> A real SpiceDB cannot be made to hit {@code
 * MaxDepthExceeded} on demand without standing up dozens of chained schema definitions, so this
 * stands up a minimal stand-in that returns the failure this example exists to recover from -- the
 * same way {@link ErrorMappingTest} and {@link RetryPolicyTest} do for codes the real integration
 * SpiceDB does not produce deterministically.
 */
class LookupResourcesDebugTest {

  /**
   * Always fails {@code LookupResources} with the code a real {@code MaxDepthExceeded} produces,
   * attaching a {@code DebugInformation} detail ONLY when the request opted in via {@code
   * with_debug} -- exactly how a real SpiceDB behaves, so a caller who didn't ask for debug info
   * doesn't pay for computing it.
   */
  private static Server standIn() throws IOException {
    return ServerBuilder.forPort(0)
        .addService(
            new PermissionsServiceGrpc.PermissionsServiceImplBase() {
              @Override
              public void lookupResources(
                  LookupResourcesRequest request,
                  StreamObserver<LookupResourcesResponse> responseObserver) {
                var status =
                    com.google.rpc.Status.newBuilder()
                        .setCode(io.grpc.Status.Code.RESOURCE_EXHAUSTED.value())
                        .setMessage("max recursion depth exceeded");
                if (request.getWithDebug()) {
                  status.addDetails(
                      Any.pack(
                          DebugInformation.newBuilder()
                              .setSchemaUsed("definition user {}")
                              .build()));
                }
                responseObserver.onError(StatusProto.toStatusRuntimeException(status.build()));
              }
            })
        .build()
        .start();
  }

  @Test
  void withoutWithDebugNoDetailIsAttached() throws Exception {
    Server server = standIn();
    try (SpiceDBClient client =
        SpiceDBClient.createPlaintext("127.0.0.1:" + server.getPort(), "some-token")) {
      try (Stream<?> ignored =
          client.lookupResources(Consistency.full(), "document", "view", "user", "alice")) {
        assertThatThrownBy(ignored::findFirst)
            .isInstanceOf(ResourceExhaustedException.class)
            .satisfies(
                e -> {
                  com.google.rpc.Status status = StatusProto.fromThrowable(e.getCause());
                  assertThat(status).isNotNull();
                  boolean hasDebugInfo =
                      status.getDetailsList().stream().anyMatch(d -> d.is(DebugInformation.class));
                  assertThat(hasDebugInfo)
                      .as("did not set withDebug, but a DebugInformation detail came back anyway")
                      .isFalse();
                });
      }
    } finally {
      server.shutdownNow().awaitTermination();
    }
  }

  @Test
  void withDebugTheServerAttachesADebugInformationDetail() throws Exception {
    Server server = standIn();
    try (SpiceDBClient client =
        SpiceDBClient.createPlaintext("127.0.0.1:" + server.getPort(), "some-token")) {
      try (Stream<?> ignored =
          client.lookupResources(
              Consistency.full(), "document", "view", "user", "alice", /* withDebug= */ true)) {
        assertThatThrownBy(ignored::findFirst)
            .isInstanceOf(ResourceExhaustedException.class)
            .satisfies(
                e -> {
                  com.google.rpc.Status status = StatusProto.fromThrowable(e.getCause());
                  assertThat(status)
                      .as(
                          "the underlying gRPC status must remain reachable through the exception's"
                              + " cause")
                      .isNotNull();
                  DebugInformation info =
                      status.getDetailsList().stream()
                          .filter(d -> d.is(DebugInformation.class))
                          .findFirst()
                          .map(
                              d -> {
                                try {
                                  return d.unpack(DebugInformation.class);
                                } catch (com.google.protobuf.InvalidProtocolBufferException ex) {
                                  throw new RuntimeException(ex);
                                }
                              })
                          .orElse(null);
                  assertThat(info)
                      .as(
                          "withDebug should have caused the server to attach a DebugInformation"
                              + " detail, but none was found on the mapped exception's underlying"
                              + " status")
                      .isNotNull();
                  assertThat(info.getSchemaUsed()).isEqualTo("definition user {}");
                });
      }
    } finally {
      server.shutdownNow().awaitTermination();
    }
  }
}
