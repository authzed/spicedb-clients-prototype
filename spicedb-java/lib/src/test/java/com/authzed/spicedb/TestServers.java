package com.authzed.spicedb;

import io.grpc.BindableService;
import io.grpc.ManagedChannel;
import io.grpc.Server;
import io.grpc.inprocess.InProcessChannelBuilder;
import io.grpc.inprocess.InProcessServerBuilder;
import java.io.IOException;
import java.util.concurrent.TimeUnit;

/**
 * Reusable in-process gRPC test harness. Starts a real gRPC {@link Server} backed by grpc-java's
 * in-process transport (no sockets, no network flakiness), serving one or more mock service
 * implementations, and wires a real {@link SpiceDBClient} to it over an in-process channel.
 *
 * <p>Package-private and test-scoped: lives alongside {@link SpiceDBClient} so it can use the
 * package-private {@link SpiceDBClient#forChannel(ManagedChannel)} test seam.
 *
 * <p>Usage:
 *
 * <pre>{@code
 * try (TestServers servers = TestServers.start(myMockPermissionsService)) {
 *   SpiceDBClient client = servers.client();
 *   // exercise client against the mock service
 * }
 * }</pre>
 */
final class TestServers implements AutoCloseable {

  private final Server server;
  private final SpiceDBClient client;

  private TestServers(Server server, SpiceDBClient client) {
    this.server = server;
    this.client = client;
  }

  /**
   * Starts an in-process gRPC server exposing the given service implementation(s) and returns a
   * harness with a {@link SpiceDBClient} wired to it.
   */
  static TestServers start(BindableService... services) throws IOException {
    String name = InProcessServerBuilder.generateName();

    InProcessServerBuilder serverBuilder = InProcessServerBuilder.forName(name).directExecutor();
    for (BindableService service : services) {
      serverBuilder.addService(service);
    }
    Server server = serverBuilder.build().start();

    ManagedChannel channel = InProcessChannelBuilder.forName(name).directExecutor().build();
    SpiceDBClient client = SpiceDBClient.forChannel(channel);

    return new TestServers(server, client);
  }

  /** Returns the client wired to the in-process server. */
  SpiceDBClient client() {
    return client;
  }

  /** Shuts down the client (and its channel) and the in-process server. */
  @Override
  public void close() {
    client.close();
    server.shutdownNow();
    try {
      server.awaitTermination(5, TimeUnit.SECONDS);
    } catch (InterruptedException e) {
      Thread.currentThread().interrupt();
    }
  }
}
