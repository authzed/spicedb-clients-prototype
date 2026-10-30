package com.authzed.spicedb;

import static org.junit.jupiter.api.Assertions.*;

import com.authzed.api.v1.*;
import io.grpc.netty.shaded.io.grpc.netty.*;
import io.grpc.netty.shaded.io.netty.handler.ssl.ClientAuth;
import io.grpc.stub.StreamObserver;
import java.nio.file.Path;
import java.time.Duration;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

class TlsConfigurationTest {
  @TempDir Path directory;

  @Test
  void customCaAndMutualTlsPerformRealHandshake() throws Exception {
    var cert = directory.resolve("certificate.pem");
    var key = directory.resolve("key.pem");
    var process =
        new ProcessBuilder(
                "openssl",
                "req",
                "-x509",
                "-newkey",
                "rsa:2048",
                "-nodes",
                "-days",
                "1",
                "-keyout",
                key.toString(),
                "-out",
                cert.toString(),
                "-subj",
                "/CN=localhost",
                "-addext",
                "subjectAltName=DNS:localhost")
            .redirectErrorStream(true)
            .start();
    var output =
        new String(
            process.getInputStream().readAllBytes(), java.nio.charset.StandardCharsets.UTF_8);
    assertEquals(0, process.waitFor(), output);
    var tls =
        GrpcSslContexts.forServer(cert.toFile(), key.toFile())
            .trustManager(cert.toFile())
            .clientAuth(ClientAuth.REQUIRE)
            .build();
    var server =
        NettyServerBuilder.forPort(0)
            .sslContext(tls)
            .addService(
                new SchemaServiceGrpc.SchemaServiceImplBase() {
                  public void readSchema(
                      ReadSchemaRequest request, StreamObserver<ReadSchemaResponse> out) {
                    out.onNext(
                        ReadSchemaResponse.newBuilder()
                            .setSchemaText("definition user {}")
                            .build());
                    out.onCompleted();
                  }
                })
            .build()
            .start();
    try (var client =
        SpiceDBClient.createSystemTls(
            "localhost:" + server.getPort(),
            "test",
            ClientOptions.builder()
                .caCertificate(cert)
                .clientCertificate(cert)
                .clientKey(key)
                .defaultTimeout(Duration.ofSeconds(3))
                .build())) {
      assertEquals("definition user {}", client.readSchema().schema());
    } finally {
      server.shutdownNow();
      server.awaitTermination();
    }
    assertThrows(
        com.authzed.spicedb.errors.InvalidArgumentException.class,
        () ->
            SpiceDBClient.createPlaintext(
                "localhost:1", "test", ClientOptions.builder().caCertificate(cert).build()));
    assertThrows(
        com.authzed.spicedb.errors.InvalidArgumentException.class,
        () -> ClientOptions.builder().clientCertificate(cert).build());
  }

  /**
   * Material that does not parse fails at construction with a local validation error naming the
   * input, never as an unexplained handshake failure on the first call. See root DESIGN.md, "Reject
   * unusable credentials and trust material at construction".
   */
  @Test
  void unparseableTlsMaterialIsRejectedAtConstructionNamingTheInput() throws Exception {
    var cert = directory.resolve("certificate.pem");
    var key = directory.resolve("key.pem");
    var process =
        new ProcessBuilder(
                "openssl",
                "req",
                "-x509",
                "-newkey",
                "rsa:2048",
                "-nodes",
                "-days",
                "1",
                "-keyout",
                key.toString(),
                "-out",
                cert.toString(),
                "-subj",
                "/CN=localhost")
            .redirectErrorStream(true)
            .start();
    process.getInputStream().readAllBytes();
    assertEquals(0, process.waitFor());
    var garbage = directory.resolve("garbage.pem");
    java.nio.file.Files.writeString(garbage, "not a certificate");
    var corrupt = directory.resolve("corrupt.pem");
    java.nio.file.Files.writeString(
        corrupt, "-----BEGIN CERTIFICATE-----\nAAAA\n-----END CERTIFICATE-----\n");

    record Case(ClientOptions options, String message) {}
    var cases =
        java.util.List.of(
            new Case(
                ClientOptions.builder().caCertificate(garbage).build(),
                "caCertificate " + garbage + " is not a PEM-encoded X.509 certificate"),
            new Case(
                ClientOptions.builder().caCertificate(corrupt).build(),
                "caCertificate " + corrupt + " is not a PEM-encoded X.509 certificate"),
            new Case(
                ClientOptions.builder().caCertificate(directory.resolve("missing.pem")).build(),
                "caCertificate " + directory.resolve("missing.pem") + " cannot be read"),
            new Case(
                ClientOptions.builder().clientCertificate(garbage).clientKey(key).build(),
                "clientCertificate " + garbage + " is not a PEM-encoded X.509 certificate"),
            new Case(
                ClientOptions.builder().clientCertificate(cert).clientKey(garbage).build(),
                "clientKey " + garbage + " is not a PEM-encoded private key"));
    for (var c : cases) {
      var e =
          assertThrows(
              com.authzed.spicedb.errors.LocalValidationException.class,
              () -> SpiceDBClient.createSystemTls("localhost:50116", "test", c.options()));
      assertTrue(e.getMessage().contains(c.message()), e.getMessage());
    }
    // Parseable material still builds (construction does not connect).
    SpiceDBClient.createSystemTls(
            "localhost:50116",
            "test",
            ClientOptions.builder()
                .caCertificate(cert)
                .clientCertificate(cert)
                .clientKey(key)
                .build())
        .close();
  }

  /**
   * An empty or whitespace-only key is a local validation error at construction, never a bare
   * "Bearer" header the server rejects on the first call.
   */
  @Test
  void blankPresharedKeyIsRejectedAtConstruction() {
    for (var key : java.util.List.of("", " ", "\t\n")) {
      for (org.junit.jupiter.api.function.Executable build :
          java.util.List.<org.junit.jupiter.api.function.Executable>of(
              () -> SpiceDBClient.createPlaintext("localhost:50116", key),
              () -> SpiceDBClient.createSystemTls("localhost:50116", key),
              () -> SpiceDBClient.create("localhost:50116", key, SpiceDBClient.withInsecure()))) {
        var e = assertThrows(com.authzed.spicedb.errors.LocalValidationException.class, build);
        assertTrue(e.getMessage().contains("presharedKey must not be empty or whitespace"));
      }
    }
  }
}
