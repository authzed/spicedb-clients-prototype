package com.authzed.spicedb;

import static org.junit.jupiter.api.Assertions.*;

import build.buf.gen.authzed.api.v1.PermissionsServiceGrpc;
import build.buf.gen.authzed.api.v1.ReadSchemaRequest;
import build.buf.gen.authzed.api.v1.ReadSchemaResponse;
import build.buf.gen.authzed.api.v1.SchemaServiceGrpc;
import build.buf.gen.authzed.api.v1.WriteRelationshipsRequest;
import build.buf.gen.authzed.api.v1.WriteRelationshipsResponse;
import com.authzed.spicedb.errors.ResourceExhaustedException;
import com.authzed.spicedb.errors.UnavailableException;
import io.grpc.Status;
import io.grpc.stub.StreamObserver;
import java.io.IOException;
import java.util.HashSet;
import java.util.Set;
import java.util.concurrent.atomic.AtomicInteger;
import org.junit.jupiter.api.Test;

/**
 * Retry safety, per root DESIGN.md "RULE: Automatic retry is for idempotent operations only":
 *
 * <ul>
 *   <li>Reads (e.g. {@code readSchema}) retry on a transient error.
 *   <li>Mutations (e.g. {@code write}/{@code WriteRelationships}) are attempted exactly once, even
 *       on a retryable error -- a {@code WriteRelationships} carrying {@code OPERATION_CREATE} or
 *       preconditions is not idempotent, and retrying a lost response would surface {@code
 *       ALREADY_EXISTS}/{@code FAILED_PRECONDITION} for a write that in fact succeeded.
 *   <li>{@code RESOURCE_EXHAUSTED} is never retried, on either a read or a mutation.
 * </ul>
 *
 * <p>See {@link ErrorMapperTest} for the inverted {@code isTransient} coverage of the {@code
 * RESOURCE_EXHAUSTED} half of this guarantee, and {@link StreamEstablishmentRetryTest} for the same
 * guarantees on streaming RPC establishment.
 */
class UnaryRetrySafetyTest {

  private static Relationship aRelationship() {
    return Relationship.of("document", "doc1", "viewer", "user", "alice");
  }

  // ---------------------------------------------------------------------
  // Reads retry
  // ---------------------------------------------------------------------

  @Test
  void readSchemaRetriesOnTransientErrorThenSucceeds() throws IOException {
    var attempts = new AtomicInteger(0);
    var service =
        new SchemaServiceGrpc.SchemaServiceImplBase() {
          @Override
          public void readSchema(
              ReadSchemaRequest request, StreamObserver<ReadSchemaResponse> responseObserver) {
            if (attempts.incrementAndGet() == 1) {
              responseObserver.onError(
                  Status.UNAVAILABLE.withDescription("try again").asRuntimeException());
              return;
            }
            responseObserver.onNext(
                ReadSchemaResponse.newBuilder().setSchemaText("definition user {}").build());
            responseObserver.onCompleted();
          }
        };

    try (TestServers servers = TestServers.start(service)) {
      SpiceDBClient client = servers.client();
      SpiceDBClient.SchemaResult result = client.readSchema();

      assertEquals("definition user {}", result.schema());
      assertEquals(2, attempts.get(), "a transient error on a read must be retried");
    }
  }

  @Test
  void readSchemaNeverRetriesResourceExhausted() throws IOException {
    var attempts = new AtomicInteger(0);
    var service =
        new SchemaServiceGrpc.SchemaServiceImplBase() {
          @Override
          public void readSchema(
              ReadSchemaRequest request, StreamObserver<ReadSchemaResponse> responseObserver) {
            attempts.incrementAndGet();
            responseObserver.onError(
                Status.RESOURCE_EXHAUSTED.withDescription("quota").asRuntimeException());
          }
        };

    try (TestServers servers = TestServers.start(service)) {
      SpiceDBClient client = servers.client();

      assertThrows(ResourceExhaustedException.class, client::readSchema);
      assertEquals(1, attempts.get(), "RESOURCE_EXHAUSTED must never be retried");
    }
  }

  // ---------------------------------------------------------------------
  // Mutations do not retry
  // ---------------------------------------------------------------------

  @Test
  void writeAttemptsExactlyOnceOnRetryableError() throws IOException {
    var attempts = new AtomicInteger(0);
    var service =
        new PermissionsServiceGrpc.PermissionsServiceImplBase() {
          @Override
          public void writeRelationships(
              WriteRelationshipsRequest request,
              StreamObserver<WriteRelationshipsResponse> responseObserver) {
            attempts.incrementAndGet();
            responseObserver.onError(
                Status.UNAVAILABLE.withDescription("try again").asRuntimeException());
          }
        };

    try (TestServers servers = TestServers.start(service)) {
      SpiceDBClient client = servers.client();
      Transaction txn = new Transaction();
      txn.create(aRelationship());

      assertThrows(UnavailableException.class, () -> client.write(txn));
      assertEquals(
          1,
          attempts.get(),
          "a mutation must be attempted exactly once, even on a retryable error");
    }
  }

  @Test
  void writeNeverRetriesResourceExhausted() throws IOException {
    var attempts = new AtomicInteger(0);
    var service =
        new PermissionsServiceGrpc.PermissionsServiceImplBase() {
          @Override
          public void writeRelationships(
              WriteRelationshipsRequest request,
              StreamObserver<WriteRelationshipsResponse> responseObserver) {
            attempts.incrementAndGet();
            responseObserver.onError(
                Status.RESOURCE_EXHAUSTED.withDescription("quota").asRuntimeException());
          }
        };

    try (TestServers servers = TestServers.start(service)) {
      SpiceDBClient client = servers.client();
      Transaction txn = new Transaction();
      txn.create(aRelationship());

      assertThrows(ResourceExhaustedException.class, () -> client.write(txn));
      assertEquals(1, attempts.get());
    }
  }

  // ---------------------------------------------------------------------
  // Backoff jitter
  // ---------------------------------------------------------------------

  @Test
  void jitteredBackoffVariesBetweenCalls() throws Exception {
    var method = SpiceDBClient.class.getDeclaredMethod("jitteredBackoffMs", long.class);
    method.setAccessible(true);

    long cap = 400;
    Set<Long> seen = new HashSet<>();
    for (int i = 0; i < 50; i++) {
      seen.add((Long) method.invoke(null, cap));
    }

    assertTrue(seen.size() > 1, "backoff should vary between calls");
    for (long v : seen) {
      assertTrue(v >= 0 && v <= cap, "backoff " + v + " out of [0, " + cap + "]");
    }
  }
}
