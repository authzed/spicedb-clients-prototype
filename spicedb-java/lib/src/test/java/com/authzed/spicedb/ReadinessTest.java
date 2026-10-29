package com.authzed.spicedb;

import static org.junit.jupiter.api.Assertions.*;

import com.authzed.api.v1.ReadSchemaRequest;
import com.authzed.api.v1.ReadSchemaResponse;
import com.authzed.api.v1.SchemaServiceGrpc;
import com.authzed.spicedb.errors.InvalidArgumentException;
import com.authzed.spicedb.errors.UnavailableException;
import com.authzed.spicedb.testing.InMemorySpiceDB;
import io.grpc.ClientInterceptors;
import io.grpc.Status;
import io.grpc.inprocess.InProcessChannelBuilder;
import io.grpc.inprocess.InProcessServerBuilder;
import io.grpc.stub.StreamObserver;
import java.time.Duration;
import org.junit.jupiter.api.Test;

/** Root DESIGN.md: a lazily connecting client offers a startup readiness check. */
class ReadinessTest {

  @Test
  void readyWhenTheServerIsReachable() throws Exception {
    try (var servers = TestServers.start(new SchemaServiceGrpc.SchemaServiceImplBase() {})) {
      servers.client().awaitReady(Duration.ofSeconds(5));
    }
  }

  @Test
  void refusedEndpointFailsAsUnavailableNamingIt() {
    // Nothing listens on port 1; the connection is refused at once and retried until the timeout.
    try (var client = SpiceDBClient.createPlaintext("localhost:1", "key")) {
      long start = System.nanoTime();
      var e =
          assertThrows(UnavailableException.class, () -> client.awaitReady(Duration.ofMillis(500)));
      long elapsedMs = (System.nanoTime() - start) / 1_000_000;
      assertEquals(Status.Code.UNAVAILABLE, e.code());
      assertTrue(e.getMessage().contains("localhost:1"), e.getMessage());
      assertTrue(elapsedMs < 5_000, "bounded by the timeout (elapsed=" + elapsedMs + ")");
    }
  }

  @Test
  void unobservableChannelIsReadyOnceTheServerAnswers() throws Exception {
    String name = InProcessServerBuilder.generateName();
    var server =
        InProcessServerBuilder.forName(name)
            .addService(
                new SchemaServiceGrpc.SchemaServiceImplBase() {
                  @Override
                  public void readSchema(
                      ReadSchemaRequest r, StreamObserver<ReadSchemaResponse> out) {
                    // No schema yet: an error, but one the server sent.
                    out.onError(Status.NOT_FOUND.asRuntimeException());
                  }
                })
            .build()
            .start();
    var managed = InProcessChannelBuilder.forName(name).build();
    try {
      // An intercepted channel is not a ManagedChannel, so its state cannot be observed.
      var client = SpiceDBClient.fromChannel(ClientInterceptors.intercept(managed));
      client.awaitReady(Duration.ofSeconds(5));
    } finally {
      managed.shutdownNow();
      server.shutdownNow();
    }
  }

  @Test
  void timeoutMustBePositiveAndTheDoubleIsAlwaysReady() throws Exception {
    try (var servers = TestServers.start(new SchemaServiceGrpc.SchemaServiceImplBase() {})) {
      assertThrows(InvalidArgumentException.class, () -> servers.client().awaitReady(null));
      assertThrows(
          InvalidArgumentException.class, () -> servers.client().awaitReady(Duration.ZERO));
    }
    var double_ = new InMemorySpiceDB();
    double_.awaitReady(Duration.ofMillis(1));
    assertTrue(double_.calls().isEmpty(), "readiness is not a recorded operation");
    assertThrows(InvalidArgumentException.class, () -> double_.awaitReady(Duration.ZERO));
  }
}
