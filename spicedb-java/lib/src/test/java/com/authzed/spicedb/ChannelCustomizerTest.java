package com.authzed.spicedb;

import static org.junit.jupiter.api.Assertions.*;

import com.authzed.spicedb.errors.InvalidArgumentException;
import io.grpc.ManagedChannelBuilder;
import java.time.Duration;
import java.util.ArrayList;
import java.util.List;
import java.util.concurrent.TimeUnit;
import org.junit.jupiter.api.Test;

/**
 * Keepalive and other channel settings combine with {@link ClientOptions} on the convenience
 * constructors, without the advanced {@code create(..., ClientOption...)} factory.
 */
class ChannelCustomizerTest {

  private static final ClientOptions OPTIONS =
      ClientOptions.builder().defaultTimeout(Duration.ofSeconds(5)).build();

  private static void keepalive(ManagedChannelBuilder<?> b) {
    b.keepAliveTime(60, TimeUnit.SECONDS)
        .keepAliveTimeout(10, TimeUnit.SECONDS)
        .keepAliveWithoutCalls(true);
  }

  @Test
  void plaintextAppliesOptionsAndCustomizer() {
    List<ManagedChannelBuilder<?>> seen = new ArrayList<>();
    try (var client =
        SpiceDBClient.createPlaintext(
            "localhost:50051",
            "key",
            OPTIONS,
            b -> {
              seen.add(b);
              keepalive(b);
            })) {
      assertEquals(1, seen.size());
      assertNotNull(client.rawChannel());
    }
  }

  @Test
  void systemTlsAppliesOptionsAndCustomizer() {
    List<ManagedChannelBuilder<?>> seen = new ArrayList<>();
    try (var client =
        SpiceDBClient.createSystemTls(
            "spicedb.example.com:443",
            "key",
            OPTIONS,
            b -> {
              seen.add(b);
              keepalive(b);
            })) {
      assertEquals(1, seen.size());
      assertNotNull(client.rawChannel());
    }
  }

  @Test
  void remotePlaintextGuardRunsBeforeTheCustomizer() {
    List<ManagedChannelBuilder<?>> seen = new ArrayList<>();
    assertThrows(
        InvalidArgumentException.class,
        () ->
            SpiceDBClient.createPlaintext("spicedb.example.com:50051", "key", OPTIONS, seen::add));
    assertTrue(seen.isEmpty());
  }

  @Test
  void nullCustomizerIsRejected() {
    assertThrows(
        InvalidArgumentException.class,
        () -> SpiceDBClient.createPlaintext("localhost:50051", "key", OPTIONS, null));
    assertThrows(
        InvalidArgumentException.class,
        () -> SpiceDBClient.createSystemTls("localhost:50051", "key", OPTIONS, null));
  }
}
