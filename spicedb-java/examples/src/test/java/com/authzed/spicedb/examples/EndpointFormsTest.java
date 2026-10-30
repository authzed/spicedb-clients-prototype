package com.authzed.spicedb.examples;

import static org.assertj.core.api.Assertions.*;

import com.authzed.spicedb.Consistency;
import com.authzed.spicedb.SpiceDBClient;
import com.authzed.spicedb.errors.InvalidArgumentException;
import com.authzed.spicedb.errors.UnavailableException;
import java.time.Duration;
import org.junit.jupiter.api.Assumptions;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.params.ParameterizedTest;
import org.junit.jupiter.params.provider.ValueSource;

/**
 * Every endpoint form the client accepts makes a real call, and {@code awaitReady} is the startup
 * readiness check: the client connects lazily, so without it a wrong endpoint would only fail on
 * the first call. Root DESIGN.md, "Accept the endpoint forms deployments use, or reject them at
 * construction".
 */
class EndpointFormsTest {

  @ParameterizedTest
  @ValueSource(strings = {"", "http://", "dns:///"})
  void everyAcceptedTcpFormMakesARealCall(String prefix) {
    String endpoint = prefix + SpiceDBIntegrationTest.ENDPOINT;
    try (var client = SpiceDBClient.createPlaintext(endpoint, SpiceDBIntegrationTest.TOKEN)) {
      // Fail at startup, not on the first request, if the endpoint is wrong.
      client.awaitReady(Duration.ofSeconds(10));
      assertThat(client.reflectSchema(Consistency.full())).isNotNull();
    }
  }

  /**
   * A Unix domain socket. It needs a SpiceDB serving one ({@code spicedb serve --grpc-network unix
   * --grpc-addr /path}) named by {@code SPICEDB_UNIX_ENDPOINT}, such as {@code unix:/path}.
   */
  @Test
  void unixSocketMakesARealCall() {
    String endpoint = System.getenv("SPICEDB_UNIX_ENDPOINT");
    Assumptions.assumeTrue(
        endpoint != null && !endpoint.isEmpty(), "SPICEDB_UNIX_ENDPOINT is not set");
    SpiceDBClient client;
    try {
      client = SpiceDBClient.createPlaintext(endpoint, SpiceDBIntegrationTest.TOKEN);
    } catch (InvalidArgumentException e) {
      // Where the transport cannot dial a socket (no native epoll, as on macOS), the endpoint is
      // refused at construction, naming the accepted forms; it never builds a client that fails.
      assertThat(e).hasMessageContaining("Unix domain sockets are not supported");
      Assumptions.abort("this platform's transport has no Unix domain sockets");
      return;
    }
    try (client) {
      client.awaitReady(Duration.ofSeconds(10));
      assertThat(client.reflectSchema(Consistency.full())).isNotNull();
    }
  }

  @Test
  void unreachableEndpointFailsAtStartup() {
    // Nothing listens on port 1 of the loopback interface.
    try (var client = SpiceDBClient.createPlaintext("localhost:1", SpiceDBIntegrationTest.TOKEN)) {
      assertThatThrownBy(() -> client.awaitReady(Duration.ofSeconds(1)))
          .isInstanceOf(UnavailableException.class)
          .hasMessageContaining("localhost:1");
    }
  }
}
