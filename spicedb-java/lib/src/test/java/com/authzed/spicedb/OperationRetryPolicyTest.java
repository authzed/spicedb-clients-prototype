package com.authzed.spicedb;

import static org.junit.jupiter.api.Assertions.assertDoesNotThrow;
import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertNull;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

import com.authzed.api.v1.PermissionsServiceGrpc;
import com.authzed.api.v1.ReadSchemaRequest;
import com.authzed.api.v1.ReadSchemaResponse;
import com.authzed.api.v1.SchemaServiceGrpc;
import com.authzed.api.v1.WatchRequest;
import com.authzed.api.v1.WatchResponse;
import com.authzed.api.v1.WatchServiceGrpc;
import com.authzed.api.v1.WriteRelationshipsRequest;
import com.authzed.api.v1.WriteRelationshipsResponse;
import com.authzed.spicedb.errors.InvalidArgumentException;
import com.authzed.spicedb.errors.UnavailableException;
import io.grpc.BindableService;
import io.grpc.ManagedChannel;
import io.grpc.Server;
import io.grpc.Status;
import io.grpc.inprocess.InProcessChannelBuilder;
import io.grpc.inprocess.InProcessServerBuilder;
import io.grpc.stub.StreamObserver;
import java.time.Duration;
import java.util.List;
import java.util.concurrent.atomic.AtomicInteger;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.Test;

/** Client-wide per-operation retry policies and the public idempotence rule. */
class OperationRetryPolicyTest {

  private static final RetryPolicy NO_RETRIES = RetryPolicy.builder().maxRetries(0).build();
  private static final RetryPolicy RETRY_ONCE =
      RetryPolicy.builder().maxRetries(1).initialBackoff(Duration.ofMillis(1)).build();
  private static final RetryPolicy RETRY_THREE =
      RetryPolicy.builder().maxRetries(3).initialBackoff(Duration.ofMillis(1)).build();

  private Server server;
  private ManagedChannel channel;

  private SpiceDBClient client(ClientOptions options, BindableService... services)
      throws Exception {
    String name = InProcessServerBuilder.generateName();
    var b = InProcessServerBuilder.forName(name).directExecutor();
    for (var s : services) b.addService(s);
    server = b.build().start();
    channel = InProcessChannelBuilder.forName(name).directExecutor().build();
    return SpiceDBClient.fromChannel(channel, options);
  }

  @AfterEach
  void stop() {
    if (channel != null) channel.shutdownNow();
    if (server != null) server.shutdownNow();
  }

  private static final class FailingWatch extends WatchServiceGrpc.WatchServiceImplBase {
    final AtomicInteger calls = new AtomicInteger();

    @Override
    public void watch(WatchRequest request, StreamObserver<WatchResponse> out) {
      calls.incrementAndGet();
      out.onError(Status.UNAVAILABLE.asRuntimeException());
    }
  }

  @Test
  void aZeroRetryWatchPolicyNeverReopensAWatch() throws Exception {
    var watch = new FailingWatch();
    try (var client =
        client(
            ClientOptions.builder()
                .retryPolicy(RETRY_THREE)
                .operationRetryPolicy(ClientOperation.WATCH, NO_RETRIES)
                .build(),
            watch)) {
      try (var events = client.updates(List.of(), null)) {
        assertThrows(UnavailableException.class, () -> events.iterator().hasNext());
      }
      assertEquals(1, watch.calls.get(), "no per-call option, and the watch opened once");

      // A per-call policy still wins over the per-operation one.
      watch.calls.set(0);
      try (var events =
          client.updates(List.of(), null, WatchOptions.builder().retryPolicy(RETRY_ONCE).build())) {
        assertThrows(UnavailableException.class, () -> events.iterator().hasNext());
      }
      assertEquals(2, watch.calls.get());
    }
  }

  @Test
  void anOperationPolicyGovernsStreamsEvenWhenStreamRetriesAreDisabled() throws Exception {
    var watch = new FailingWatch();
    try (var client =
        client(
            ClientOptions.builder()
                .streamRetries(StreamRetries.DISABLED)
                .operationRetryPolicy(ClientOperation.WATCH, RETRY_ONCE)
                .build(),
            watch)) {
      try (var events = client.updates(List.of(), null)) {
        assertThrows(UnavailableException.class, () -> events.iterator().hasNext());
      }
      assertEquals(2, watch.calls.get());
    }
  }

  private static final class FailingPermissionSetWatch
      extends com.authzed.api.materialize.v0.WatchPermissionSetsServiceGrpc
          .WatchPermissionSetsServiceImplBase {
    final AtomicInteger calls = new AtomicInteger();

    @Override
    public void watchPermissionSets(
        com.authzed.api.materialize.v0.WatchPermissionSetsRequest request,
        StreamObserver<com.authzed.api.materialize.v0.WatchPermissionSetsResponse> out) {
      calls.incrementAndGet();
      out.onError(Status.UNAVAILABLE.asRuntimeException());
    }
  }

  @Test
  void theClientWidePolicyNeverReopensAWatch() throws Exception {
    // Neither the default client nor an explicit retrying client-wide policy with stream retries
    // reopens either watch; only a per-call or per-operation policy does.
    for (var options :
        List.of(
            ClientOptions.builder().build(),
            ClientOptions.builder()
                .retryPolicy(RETRY_THREE)
                .retries(Retries.ENABLED)
                .streamRetries(StreamRetries.ENABLED)
                .build())) {
      var watch = new FailingWatch();
      var sets = new FailingPermissionSetWatch();
      try (var client = client(options, watch, sets)) {
        try (var events = client.updates(List.of(), null)) {
          assertThrows(UnavailableException.class, () -> events.iterator().hasNext());
        }
        assertEquals(1, watch.calls.get());
        try (var events = client.experimentalWatchPermissionSets(null)) {
          assertThrows(UnavailableException.class, () -> events.iterator().hasNext());
        }
        assertEquals(1, sets.calls.get());

        try (var events =
            client.updates(
                List.of(), null, WatchOptions.builder().retryPolicy(RETRY_ONCE).build())) {
          assertThrows(UnavailableException.class, () -> events.iterator().hasNext());
        }
        assertEquals(3, watch.calls.get(), "a per-call policy reopens the watch once");
        try (var events =
            client.experimentalWatchPermissionSets(
                null, CallOptions.builder().retryPolicy(RETRY_ONCE).build())) {
          assertThrows(UnavailableException.class, () -> events.iterator().hasNext());
        }
        assertEquals(3, sets.calls.get(), "a per-call policy reopens the watch once");
      } finally {
        stop();
      }
    }
  }

  @Test
  void anOperationPolicyOverridesTheClientWidePolicyForThatOperationOnly() throws Exception {
    var reads = new AtomicInteger();
    var schema =
        new SchemaServiceGrpc.SchemaServiceImplBase() {
          @Override
          public void readSchema(
              ReadSchemaRequest request, StreamObserver<ReadSchemaResponse> out) {
            reads.incrementAndGet();
            out.onError(Status.UNAVAILABLE.asRuntimeException());
          }
        };
    var watch = new FailingWatch();
    try (var client =
        client(
            ClientOptions.builder()
                .retries(Retries.DISABLED)
                .operationRetryPolicy(ClientOperation.READ_SCHEMA, RETRY_ONCE)
                .build(),
            schema,
            watch)) {
      assertThrows(UnavailableException.class, client::readSchema);
      assertEquals(2, reads.get(), "the operation policy wins over Retries.DISABLED");
      try (var events = client.updates(List.of(), null)) {
        assertThrows(UnavailableException.class, () -> events.iterator().hasNext());
      }
      assertEquals(1, watch.calls.get(), "other operations keep the client-wide behavior");
    }
  }

  @Test
  void aWritePolicyRetriesOnlyIdempotentWrites() throws Exception {
    var writes = new AtomicInteger();
    var permissions =
        new PermissionsServiceGrpc.PermissionsServiceImplBase() {
          @Override
          public void writeRelationships(
              WriteRelationshipsRequest request, StreamObserver<WriteRelationshipsResponse> out) {
            if (writes.incrementAndGet() % 2 == 1) {
              out.onError(Status.UNAVAILABLE.asRuntimeException());
              return;
            }
            out.onNext(
                WriteRelationshipsResponse.newBuilder()
                    .setWrittenAt(com.authzed.api.v1.ZedToken.newBuilder().setToken("w"))
                    .build());
            out.onCompleted();
          }
        };
    try (var client =
        client(
            ClientOptions.builder().operationRetryPolicy(ClientOperation.WRITE, RETRY_ONCE).build(),
            permissions)) {
      var touch = new Transaction();
      touch.touch(Relationship.fromTuple("document:a#viewer@user:alice"));
      assertEquals("w", client.write(touch).revision().value());
      assertEquals(2, writes.get(), "an idempotent write is retried");

      writes.set(0);
      var create = new Transaction();
      create.create(Relationship.fromTuple("document:a#viewer@user:alice"));
      assertThrows(UnavailableException.class, () -> client.write(create));
      assertEquals(1, writes.get(), "a non-idempotent write is sent once, without a usage error");
    }
  }

  @Test
  void neverIdempotentOperationsRejectARetryingPolicyAtConstruction() {
    for (var op : ClientOperation.values()) {
      var builder = ClientOptions.builder().operationRetryPolicy(op, RETRY_ONCE);
      if (op.isNeverIdempotent()) {
        var e = assertThrows(InvalidArgumentException.class, builder::build, op.name());
        assertTrue(e.getMessage().contains(op.name()), e.getMessage());
      } else {
        assertDoesNotThrow(builder::build, op.name());
      }
      assertDoesNotThrow(
          () -> ClientOptions.builder().operationRetryPolicy(op, NO_RETRIES).build(), op.name());
    }
    var options =
        ClientOptions.builder()
            .operationRetryPolicy(ClientOperation.DELETE_RELATIONSHIPS, NO_RETRIES)
            .build();
    assertEquals(NO_RETRIES, options.operationRetryPolicy(ClientOperation.DELETE_RELATIONSHIPS));
    assertNull(options.operationRetryPolicy(ClientOperation.CHECK));
    assertThrows(
        InvalidArgumentException.class,
        () -> ClientOptions.builder().operationRetryPolicy(null, NO_RETRIES));
    assertThrows(
        InvalidArgumentException.class,
        () -> ClientOptions.builder().operationRetryPolicy(ClientOperation.CHECK, null));
  }

  @Test
  void transactionIdempotenceIsTouchOnlyWithoutPreconditions() {
    var r = Relationship.fromTuple("document:a#viewer@user:alice");
    assertTrue(new Transaction().isIdempotent());
    var touch = new Transaction();
    touch.touch(r);
    assertTrue(touch.isIdempotent());
    var create = new Transaction();
    create.create(r);
    assertFalse(create.isIdempotent());
    var delete = new Transaction();
    delete.delete(r);
    assertFalse(delete.isIdempotent());
    var guarded = new Transaction();
    guarded.touch(r);
    guarded.mustMatch(Filter.of("document"));
    assertFalse(guarded.isIdempotent());
  }
}
