package com.authzed.spicedb.examples;

import static org.assertj.core.api.Assertions.*;

import build.buf.gen.authzed.api.v1.CheckBulkPermissionsPair;
import build.buf.gen.authzed.api.v1.CheckBulkPermissionsRequest;
import build.buf.gen.authzed.api.v1.CheckBulkPermissionsResponse;
import build.buf.gen.authzed.api.v1.CheckBulkPermissionsResponseItem;
import build.buf.gen.authzed.api.v1.CheckPermissionResponse;
import build.buf.gen.authzed.api.v1.PermissionsServiceGrpc;
import com.authzed.spicedb.CheckResult;
import com.authzed.spicedb.Consistency;
import com.authzed.spicedb.Relationship;
import com.authzed.spicedb.SpiceDBClient;
import com.authzed.spicedb.errors.OutOfRangeException;
import com.authzed.spicedb.errors.PermissionDeniedException;
import com.authzed.spicedb.errors.UnauthenticatedException;
import com.authzed.spicedb.errors.UnavailableException;
import io.grpc.Server;
import io.grpc.ServerBuilder;
import io.grpc.Status;
import io.grpc.stub.StreamObserver;
import java.io.IOException;
import org.junit.jupiter.api.Test;

/**
 * Demonstrates the two error codes a caller actually recovers from -- see root DESIGN.md, "RULE:
 * Error mapping must not lose the server's detail".
 *
 * <p>The rule names both consequences, and this example is those two recoveries written out as
 * running code:
 *
 * <ul>
 *   <li>{@code OUT_OF_RANGE} is SpiceDB's signal that a ZedToken has expired or been
 *       garbage-collected. Recovery is mechanical: discard the stale token and re-read at full
 *       consistency. Collapsed into a generic error, every caller would have to string-match a
 *       message to recover something the client already knew the shape of.
 *   <li>{@code UNAUTHENTICATED} is the most common error a new integration produces. Distinguishing
 *       it is what lets a caller write "refresh credentials on auth failure, page someone on
 *       internal error".
 * </ul>
 *
 * <p><b>Why this example stands up its own server.</b> Neither code is reachable from the SpiceDB
 * the integration job starts, which was verified rather than assumed: a garbage ZedToken returns
 * {@code INVALID_ARGUMENT}, and the in-memory datastore never collects the revision (with a 5s GC
 * window and 35s elapsed, a snapshot read at the old token still succeeded). And a wrong preshared
 * key comes back {@code PERMISSION_DENIED}, not {@code UNAUTHENTICATED} -- which the last test
 * asserts against the real server, so a reader does not write a credential-refresh branch that can
 * never run.
 */
class ErrorMappingTest {

  private static final String STALE_TOKEN = "stale-zedtoken";
  private static final Relationship DOC =
      Relationship.of("document", "readme", "view", "user", "alice");

  /** A minimal SpiceDB that answers only what this example asks of it. */
  private static Server standIn() throws IOException {
    return ServerBuilder.forPort(0)
        .addService(
            new PermissionsServiceGrpc.PermissionsServiceImplBase() {
              @Override
              public void checkBulkPermissions(
                  CheckBulkPermissionsRequest request,
                  StreamObserver<CheckBulkPermissionsResponse> responseObserver) {
                // A read pinned to a token the server no longer has.
                if (STALE_TOKEN.equals(request.getConsistency().getAtLeastAsFresh().getToken())) {
                  responseObserver.onError(
                      Status.OUT_OF_RANGE
                          .withDescription(
                              "the specified revision has expired or been garbage collected")
                          .asRuntimeException());
                  return;
                }
                // Anything else: re-reading at full consistency succeeds. That is the whole point
                // of the recovery -- dropping the stale token is sufficient.
                var builder = CheckBulkPermissionsResponse.newBuilder();
                for (int i = 0; i < request.getItemsCount(); i++) {
                  builder.addPairs(
                      CheckBulkPermissionsPair.newBuilder()
                          .setItem(
                              CheckBulkPermissionsResponseItem.newBuilder()
                                  .setPermissionship(
                                      CheckPermissionResponse.Permissionship
                                          .PERMISSIONSHIP_HAS_PERMISSION)
                                  .build())
                          .build());
                }
                responseObserver.onNext(builder.build());
                responseObserver.onCompleted();
              }
            })
        .build()
        .start();
  }

  private static Server rotatedTokenServer() throws IOException {
    return ServerBuilder.forPort(0)
        .addService(
            new PermissionsServiceGrpc.PermissionsServiceImplBase() {
              @Override
              public void checkBulkPermissions(
                  CheckBulkPermissionsRequest request,
                  StreamObserver<CheckBulkPermissionsResponse> responseObserver) {
                responseObserver.onError(
                    Status.UNAUTHENTICATED.withDescription("invalid token").asRuntimeException());
              }
            })
        .build()
        .start();
  }

  @Test
  void staleZedTokenIsRecoverableWithoutParsingAMessage() throws Exception {
    Server server = standIn();
    try (SpiceDBClient client =
        SpiceDBClient.createPlaintext("127.0.0.1:" + server.getPort(), "some-token")) {
      assertThatThrownBy(
              () -> client.checkPermission(Consistency.atLeast(STALE_TOKEN), "view", DOC))
          .as(
              "a check pinned to a collected ZedToken must surface as OutOfRangeException, not a"
                  + " generic failure a caller has to string-match")
          .isInstanceOf(OutOfRangeException.class)
          // Clause 2: the underlying status survives the mapping, so google.rpc.Status details and
          // SpiceDB's ErrorReason stay reachable rather than being reduced to a code and a rebuilt
          // string.
          .hasCauseInstanceOf(io.grpc.StatusRuntimeException.class);

      // The recovery the rule calls mechanical, in full. Nothing here parses a message.
      CheckResult result = client.checkPermission(Consistency.full(), "view", DOC);
      assertThat(result.hasPermission()).isTrue();
    } finally {
      server.shutdownNow().awaitTermination();
    }
  }

  @Test
  void rotatedTokenIsDistinctFromATransportFault() throws Exception {
    Server server = rotatedTokenServer();
    try (SpiceDBClient client =
        SpiceDBClient.createPlaintext("127.0.0.1:" + server.getPort(), "rotated-token")) {
      assertThatThrownBy(() -> client.checkPermission(Consistency.full(), "view", DOC))
          .isInstanceOf(UnauthenticatedException.class)
          // Asserting the negative is the half that would silently rot if every code collapsed
          // into one class.
          .isNotInstanceOf(UnavailableException.class);
    } finally {
      server.shutdownNow().awaitTermination();
    }
  }

  @Test
  void realSpiceDBRejectsABadPresharedKeyWithPermissionDenied() {
    // PERMISSION_DENIED, not UNAUTHENTICATED. Recorded here because it is the case a reader will
    // actually hit first, and assuming otherwise is how a credential-refresh branch ends up
    // unreachable in production code.
    try (SpiceDBClient client =
        SpiceDBClient.createPlaintext(
            SpiceDBIntegrationTest.ENDPOINT, "definitely-the-wrong-key")) {
      assertThatThrownBy(client::readSchema)
          .as(
              "SpiceDB rejects a bad preshared key with PERMISSION_DENIED; if this now reports"
                  + " something else, this example's guidance is stale and must be updated")
          .isInstanceOf(PermissionDeniedException.class);
    }
  }
}
