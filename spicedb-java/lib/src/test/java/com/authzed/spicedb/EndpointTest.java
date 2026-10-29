package com.authzed.spicedb;

import static org.junit.jupiter.api.Assertions.*;

import com.authzed.spicedb.errors.InvalidArgumentException;
import com.authzed.spicedb.errors.UnavailableException;
import io.grpc.Status;
import java.time.Duration;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.params.ParameterizedTest;
import org.junit.jupiter.params.provider.CsvSource;

/**
 * Regression tests for root DESIGN.md, "Accept the endpoint forms deployments use, or reject them
 * at construction".
 */
class EndpointTest {

  private static final ClientOptions ALLOW_REMOTE =
      ClientOptions.builder().remotePlaintextCredentials(RemotePlaintextCredentials.ALLOW).build();

  @ParameterizedTest
  @CsvSource({
    "127.0.0.1:50101, 127.0.0.1:50101, NONE",
    "[::1]:50051, [::1]:50051, NONE",
    "http://127.0.0.1:50101, 127.0.0.1:50101, HTTP",
    "HTTP://127.0.0.1:50101/, 127.0.0.1:50101, HTTP",
    "http://[::1]:50051, [::1]:50051, HTTP",
    "https://example.com, example.com:443, HTTPS",
    "https://example.com:8443, example.com:8443, HTTPS",
    "unix:/var/run/spicedb.sock, unix:/var/run/spicedb.sock, UNIX",
  })
  void parsesAcceptedForms(String endpoint, String target, Endpoint.Scheme scheme) {
    var parsed = Endpoint.parse(endpoint);
    assertEquals(target, parsed.target());
    assertEquals(scheme, parsed.scheme());
  }

  @ParameterizedTest
  @CsvSource(
      delimiter = '|',
      value = {
        "http://localhost | needs an explicit port",
        "http://127.0.0.1:50101/x | a path is not allowed",
        "127.0.0.1:50101/x | a path is not allowed",
        "http://u@127.0.0.1:50101 | userinfo",
        "http://127.0.0.1:50101?x=1 | a query is not allowed",
        "http://127.0.0.1:50101#x | a fragment is not allowed",
        "http://127.0.0.1:abc | invalid port \"abc\"",
        "example.com:abc | invalid port \"abc\"",
        "https://example.com:0 | invalid port \"0\"",
        "https://example.com:70000 | invalid port \"70000\"",
        "ftp://host:1 | unsupported scheme \"ftp\"",
        "dns:///example.com:443 | unsupported scheme \"dns\"",
        "http://::1:50051 | must be bracketed",
        "http:// | missing host",
      })
  void rejectsMalformedFormsInBothConstructorsNamingTheProblem(String endpoint, String problem) {
    for (var ctor :
        java.util.List.<org.junit.jupiter.api.function.Executable>of(
            () -> SpiceDBClient.createPlaintext(endpoint, "k"),
            // Malformed forms are rejected before the remote-plaintext policy: the opt-in does
            // not change the error.
            () -> SpiceDBClient.createPlaintext(endpoint, "k", ALLOW_REMOTE),
            () -> SpiceDBClient.createSystemTls(endpoint, "k"))) {
      var ex = assertThrows(InvalidArgumentException.class, ctor);
      assertTrue(ex.getMessage().contains(problem), ex.getMessage());
      assertTrue(ex.getMessage().contains("\"" + endpoint + "\""), ex.getMessage());
      assertTrue(ex.getMessage().contains("expected host:port"), ex.getMessage());
      assertFalse(ex.getMessage().contains("allowInsecureRemoteCredentials"), ex.getMessage());
    }
  }

  @ParameterizedTest
  @CsvSource(
      delimiter = '|',
      value = {
        "localhost | localhost:50051",
        "127.0.0.1 | 127.0.0.1:50051",
        "[::1] | [::1]:50051",
        "::1 | [::1]:50051",
      })
  void plaintextBareHostNeedsAnExplicitPort(String endpoint, String example) {
    for (var ctor :
        java.util.List.<org.junit.jupiter.api.function.Executable>of(
            () -> SpiceDBClient.createPlaintext(endpoint, "k"),
            () -> SpiceDBClient.createPlaintext(endpoint, "k", ALLOW_REMOTE),
            () -> SpiceDBClient.create(endpoint, "k", SpiceDBClient.withInsecure()))) {
      var ex = assertThrows(InvalidArgumentException.class, ctor);
      assertTrue(
          ex.getMessage().contains("a plaintext endpoint needs an explicit port, e.g. " + example),
          ex.getMessage());
      assertFalse(ex.getMessage().contains("allowInsecureRemoteCredentials"), ex.getMessage());
    }
  }

  @ParameterizedTest
  @CsvSource(
      delimiter = '|',
      value = {
        "localhost | localhost:443",
        "example.com | example.com:443",
        "[::1] | [::1]:443",
        "::1 | [::1]:443",
        "example.com:8443 | example.com:8443",
      })
  void tlsBareHostDialsPort443(String endpoint, String target) {
    assertEquals(target, Endpoint.parse(endpoint).forTransport(false, "tls", "plaintext").target());
    try (var client = SpiceDBClient.createSystemTls(endpoint, "k")) {
      assertNotNull(client);
    }
  }

  @Test
  void unixEndpointIsUnaffectedByThePortRule() {
    var parsed =
        Endpoint.parse("unix:/var/run/spicedb.sock").forTransport(true, "tls", "plaintext");
    assertEquals("unix:/var/run/spicedb.sock", parsed.target());
  }

  /** A Docker Compose service name: remote, so plaintext needs the transport and the opt-in. */
  @Test
  void remotePlaintextRefusalNamesEveryOptionNeeded() {
    String expected =
        "spicedb: refusing to send credentials over plaintext to non-loopback endpoint"
            + " \"spicedb:50051\". To allow it, choose plaintext and opt in to remote plaintext"
            + " credentials: SpiceDBClient.createPlaintext(endpoint, key, ClientOptions.builder()"
            + ".remotePlaintextCredentials(RemotePlaintextCredentials.ALLOW).build()), or"
            + " SpiceDBClient.create(endpoint, key, SpiceDBClient.withInsecure(),"
            + " SpiceDBClient.allowInsecureRemoteCredentials()). Otherwise use TLS:"
            + " SpiceDBClient.createSystemTls(endpoint, key)";
    var plain =
        assertThrows(
            InvalidArgumentException.class,
            () -> SpiceDBClient.createPlaintext("spicedb:50051", "k"));
    assertEquals(expected, plain.getMessage());
    var advanced =
        assertThrows(
            InvalidArgumentException.class,
            () -> SpiceDBClient.create("spicedb:50051", "k", SpiceDBClient.withInsecure()));
    assertEquals(expected, advanced.getMessage());

    // Both spelled-out opt-ins work.
    try (var client = SpiceDBClient.createPlaintext("spicedb:50051", "k", ALLOW_REMOTE)) {
      assertNotNull(client);
    }
    try (var client =
        SpiceDBClient.create(
            "spicedb:50051",
            "k",
            SpiceDBClient.withInsecure(),
            SpiceDBClient.allowInsecureRemoteCredentials())) {
      assertNotNull(client);
    }
  }

  @Test
  void rejectsEmptyEndpoint() {
    assertThrows(InvalidArgumentException.class, () -> SpiceDBClient.createPlaintext(null, "k"));
    assertThrows(InvalidArgumentException.class, () -> SpiceDBClient.createSystemTls("", "k"));
  }

  @Test
  void httpUrlToLoopbackBuildsAPlaintextClient() {
    // The loopback check runs on the parsed host, so no opt-in is needed.
    try (var client = SpiceDBClient.createPlaintext("http://127.0.0.1:50101", "k")) {
      assertNotNull(client);
    }
  }

  @Test
  void httpsUrlBuildsATlsClient() {
    try (var client = SpiceDBClient.createSystemTls("https://example.com", "k")) {
      assertNotNull(client);
    }
  }

  @Test
  void schemeMustAgreeWithTheConstructor() {
    var plain =
        assertThrows(
            InvalidArgumentException.class,
            () -> SpiceDBClient.createPlaintext("https://example.com", "k", ALLOW_REMOTE));
    assertTrue(plain.getMessage().contains("createSystemTls"), plain.getMessage());

    var tls =
        assertThrows(
            InvalidArgumentException.class,
            () -> SpiceDBClient.createSystemTls("http://127.0.0.1:50101", "k"));
    assertTrue(tls.getMessage().contains("createPlaintext"), tls.getMessage());

    assertThrows(
        InvalidArgumentException.class,
        () -> SpiceDBClient.create("https://example.com", "k", SpiceDBClient.withInsecure()));
    assertThrows(
        InvalidArgumentException.class, () -> SpiceDBClient.create("http://127.0.0.1:50101", "k"));
  }

  @Test
  void remoteHttpUrlNeedsTheOptIn() {
    var ex =
        assertThrows(
            InvalidArgumentException.class,
            () -> SpiceDBClient.createPlaintext("http://example.com:50051", "k"));
    assertTrue(ex.getMessage().contains("allowInsecureRemoteCredentials"), ex.getMessage());
    assertTrue(ex.getMessage().contains("http://example.com:50051"), ex.getMessage());

    try (var client =
        SpiceDBClient.createPlaintext("http://example.com:50051", "k", ALLOW_REMOTE)) {
      assertNotNull(client);
    }
  }

  @Test
  void refusedConnectionNamesTheEndpointAndRootCause() {
    // Port 1 (tcpmux) is privileged and unused on development and CI hosts, so a connection to
    // it is refused; a freshly released ephemeral port can be reused by another process.
    String endpoint = "127.0.0.1:1";
    var options =
        ClientOptions.builder()
            .retries(Retries.DISABLED)
            .defaultTimeout(Duration.ofSeconds(5))
            .build();
    try (var client = SpiceDBClient.createPlaintext(endpoint, "k", options)) {
      var ex = assertThrows(UnavailableException.class, client::readSchema);
      assertTrue(ex.getMessage().contains("\"" + endpoint + "\""), ex.getMessage());
      assertTrue(ex.getMessage().contains("Connection refused"), ex.getMessage());
      assertEquals(Status.Code.UNAVAILABLE, ex.code());
    }
  }

  @Test
  void serverSentUnavailableIsLeftAlone() {
    var sent = Status.UNAVAILABLE.withDescription("server is draining");
    assertSame(sent, Endpoint.describeConnectionFailure("h:1", sent));

    var local =
        Status.UNAVAILABLE
            .withDescription("io exception\nChannel Pipeline: [SslHandler#0]")
            .withCause(new java.io.IOException("wrap", new javax.net.ssl.SSLException("bad cert")));
    var described = Endpoint.describeConnectionFailure("h:1", local).getDescription();
    assertEquals(
        "connecting to SpiceDB at \"h:1\" failed: io exception: SSLException: bad cert", described);
  }
}
