package com.authzed.spicedb.examples;

import static org.assertj.core.api.Assertions.*;

import build.buf.gen.authzed.api.v1.CheckBulkPermissionsPair;
import build.buf.gen.authzed.api.v1.CheckBulkPermissionsRequest;
import build.buf.gen.authzed.api.v1.CheckBulkPermissionsResponse;
import build.buf.gen.authzed.api.v1.CheckBulkPermissionsResponseItem;
import build.buf.gen.authzed.api.v1.CheckPermissionResponse;
import build.buf.gen.authzed.api.v1.PermissionsServiceGrpc;
import build.buf.gen.authzed.api.v1.WriteRelationshipsRequest;
import build.buf.gen.authzed.api.v1.WriteRelationshipsResponse;
import com.authzed.spicedb.CheckResult;
import com.authzed.spicedb.Consistency;
import com.authzed.spicedb.Relationship;
import com.authzed.spicedb.SpiceDBClient;
import com.authzed.spicedb.Transaction;
import com.authzed.spicedb.errors.ResourceExhaustedException;
import com.authzed.spicedb.errors.UnavailableException;
import io.grpc.Server;
import io.grpc.ServerBuilder;
import io.grpc.Status;
import io.grpc.stub.StreamObserver;
import java.io.IOException;
import java.util.concurrent.atomic.AtomicInteger;
import org.junit.jupiter.api.Test;

/**
 * Demonstrates which calls this client retries on your behalf and which it deliberately does not --
 * see root DESIGN.md, "RULE: Automatic retry is for idempotent operations only".
 *
 * <p>The rule exists because a silently retried mutation produces a confident wrong answer. If a
 * {@code WriteRelationships} carrying {@code OPERATION_CREATE} commits and the response is lost,
 * the retry comes back {@code ALREADY_EXISTS} -- and the caller concludes a write failed that in
 * fact succeeded. Retrying reads is free; retrying mutations is only safe when the caller opted in
 * knowing that.
 *
 * <p>Attempts are counted <em>server-side</em>, which is the only way to tell a retry from its
 * absence: from the caller's side a transparently-retried success and a first-try success are
 * identical, and that is exactly the property that would rot unnoticed.
 *
 * <p>It stands up a stand-in SpiceDB because a real one cannot be asked to fail transiently on
 * demand.
 */
class RetryPolicyTest {

  private static final Relationship DOC =
      Relationship.of("document", "readme", "view", "user", "alice");

  /** Fails a configurable number of opening attempts per RPC and counts every one. */
  private static final class CountingService
      extends PermissionsServiceGrpc.PermissionsServiceImplBase {
    final AtomicInteger checkAttempts = new AtomicInteger();
    final AtomicInteger writeAttempts = new AtomicInteger();
    private final int checkFailures;
    private final Status checkStatus;

    CountingService(int checkFailures, Status checkStatus) {
      this.checkFailures = checkFailures;
      this.checkStatus = checkStatus;
    }

    @Override
    public void checkBulkPermissions(
        CheckBulkPermissionsRequest request,
        StreamObserver<CheckBulkPermissionsResponse> responseObserver) {
      if (checkAttempts.incrementAndGet() <= checkFailures) {
        responseObserver.onError(
            checkStatus.withDescription("transient, from the stand-in").asRuntimeException());
        return;
      }
      var builder = CheckBulkPermissionsResponse.newBuilder();
      for (int i = 0; i < request.getItemsCount(); i++) {
        builder.addPairs(
            CheckBulkPermissionsPair.newBuilder()
                .setItem(
                    CheckBulkPermissionsResponseItem.newBuilder()
                        .setPermissionship(
                            CheckPermissionResponse.Permissionship.PERMISSIONSHIP_HAS_PERMISSION)
                        .build())
                .build());
      }
      responseObserver.onNext(builder.build());
      responseObserver.onCompleted();
    }

    @Override
    public void writeRelationships(
        WriteRelationshipsRequest request,
        StreamObserver<WriteRelationshipsResponse> responseObserver) {
      writeAttempts.incrementAndGet();
      // Always fails, transiently. A retrying client would come back.
      responseObserver.onError(
          Status.UNAVAILABLE.withDescription("transient, from the stand-in").asRuntimeException());
    }
  }

  private static Server serve(CountingService service) throws IOException {
    return ServerBuilder.forPort(0).addService(service).build().start();
  }

  @Test
  void aReadIsRetriedTransparently() throws Exception {
    // Two UNAVAILABLE responses, then success. The caller sees one successful check and never
    // learns the first two attempts happened -- the entire value of retrying reads, and safe
    // precisely because a repeated read changes nothing.
    var service = new CountingService(2, Status.UNAVAILABLE);
    Server server = serve(service);
    try (SpiceDBClient client =
        SpiceDBClient.createPlaintext("127.0.0.1:" + server.getPort(), "t")) {
      CheckResult result = client.checkPermission(Consistency.full(), "view", DOC);
      assertThat(result.hasPermission()).isTrue();
      assertThat(service.checkAttempts.get())
          .as(
              "expected 2 failures plus 1 success = 3 attempts (0 or 1 means reads are not retried)")
          .isEqualTo(3);
    } finally {
      server.shutdownNow().awaitTermination();
    }
  }

  @Test
  void aMutationIsNotRetried() throws Exception {
    // The same transient code, on a write. The error reaches the caller on the first attempt, so
    // the caller -- who alone knows whether a replay is safe for the transaction they built --
    // decides what happens next.
    var service = new CountingService(0, Status.UNAVAILABLE);
    Server server = serve(service);
    try (SpiceDBClient client =
        SpiceDBClient.createPlaintext("127.0.0.1:" + server.getPort(), "t")) {
      var txn = new Transaction();
      txn.touch(Relationship.of("document", "readme", "viewer", "user", "alice"));
      assertThatThrownBy(() -> client.write(txn)).isInstanceOf(UnavailableException.class);
      assertThat(service.writeAttempts.get())
          .as(
              "a mutation must not be retried silently: a lost response would otherwise leave the"
                  + " caller believing a committed write had failed")
          .isEqualTo(1);
    } finally {
      server.shutdownNow().awaitTermination();
    }
  }

  @Test
  void resourceExhaustedIsNotRetriedEvenOnARead() throws Exception {
    // In SpiceDB this code means memory load-shed or a deterministic MaxDepthExceeded. Retrying
    // the first makes the overload worse; the second can never succeed however many times it is
    // tried. So it is deliberately absent from the retryable set even though the call is a read.
    var service = new CountingService(99, Status.RESOURCE_EXHAUSTED);
    Server server = serve(service);
    try (SpiceDBClient client =
        SpiceDBClient.createPlaintext("127.0.0.1:" + server.getPort(), "t")) {
      assertThatThrownBy(() -> client.checkPermission(Consistency.full(), "view", DOC))
          .isInstanceOf(ResourceExhaustedException.class);
      assertThat(service.checkAttempts.get())
          .as(
              "RESOURCE_EXHAUSTED must not be retried: retrying turns a load-shedding SpiceDB into"
                  + " a client-driven retry storm")
          .isEqualTo(1);
    } finally {
      server.shutdownNow().awaitTermination();
    }
  }
}
