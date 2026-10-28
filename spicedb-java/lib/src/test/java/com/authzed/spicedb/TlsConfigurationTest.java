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
}
