package com.authzed.spicedb.proto;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertNotNull;
import static org.junit.jupiter.api.Assertions.assertSame;
import static org.junit.jupiter.api.Assertions.assertTrue;

import com.authzed.api.v1.CheckPermissionRequest;
import com.authzed.api.v1.CheckPermissionResponse;
import com.authzed.api.v1.PermissionsServiceGrpc;
import com.authzed.api.v1.ZedToken;
import com.authzed.spicedb.proto.legacyfixture.LegacyAuthzedJavaCaller;
import io.grpc.ManagedChannel;
import io.grpc.Metadata;
import io.grpc.Server;
import io.grpc.ServerCall;
import io.grpc.ServerCallHandler;
import io.grpc.ServerInterceptor;
import io.grpc.ServerInterceptors;
import io.grpc.inprocess.InProcessChannelBuilder;
import io.grpc.inprocess.InProcessServerBuilder;
import io.grpc.stub.StreamObserver;
import java.io.File;
import java.net.URL;
import java.net.URLClassLoader;
import java.util.Collections;
import java.util.List;
import java.util.concurrent.atomic.AtomicReference;
import java.util.jar.JarFile;
import org.junit.jupiter.api.Test;

/**
 * Loads spicedb-java-proto and authzed-java in one JVM (root DESIGN.md, "Be the single copy of the
 * protocol bindings in any process").
 *
 * <p>The test class path holds this module's classes, the bridged authzed-java jar produced by the
 * {@code bridgeAuthzedJava} task, and a fixture compiled against the ORIGINAL authzed-java jar. The
 * original jar is never on the class path; its path is passed in only so the test can also show
 * that the unbridged pair would carry two copies.
 */
class LegacyBridgeTest {

  /** One class from every generated package that both artifacts define. */
  private static final List<String> SHARED_CLASSES =
      List.of(
          "com.authzed.api.v1.CheckPermissionRequest",
          "com.authzed.api.v1.PermissionsServiceGrpc",
          "com.authzed.api.v1.Core",
          "com.authzed.api.materialize.v0.WatchPermissionSetsRequest",
          "grpc.gateway.protoc_gen_openapiv2.options.Openapiv2");

  private static String resource(String className) {
    return className.replace('.', '/') + ".class";
  }

  private static File legacyJar() {
    return new File(System.getProperty("spicedb.legacyAuthzedJavaJar"));
  }

  private static File bridgedJar() {
    return new File(System.getProperty("spicedb.bridgedAuthzedJavaJar"));
  }

  @Test
  void everySharedClassResolvesToExactlyOneCopyOnTheClassPath() throws Exception {
    ClassLoader loader = getClass().getClassLoader();
    URL canonical =
        CheckPermissionRequest.class.getProtectionDomain().getCodeSource().getLocation();
    for (String name : SHARED_CLASSES) {
      List<URL> copies = Collections.list(loader.getResources(resource(name)));
      assertEquals(1, copies.size(), name + " has copies: " + copies);
      Class<?> loaded = Class.forName(name, false, loader);
      assertEquals(
          canonical,
          loaded.getProtectionDomain().getCodeSource().getLocation(),
          name + " must come from spicedb-java-proto, not the bridged jar");
    }
  }

  @Test
  void unbridgedAuthzedJavaWouldDuplicateEverySharedClass() throws Exception {
    // Negative control: proves the single-copy assertion above can fail. The published
    // authzed-java jar placed beside this module carries a second copy of every generated class.
    URL canonical =
        CheckPermissionRequest.class.getProtectionDomain().getCodeSource().getLocation();
    try (URLClassLoader both =
        new URLClassLoader(new URL[] {legacyJar().toURI().toURL(), canonical}, null)) {
      for (String name : SHARED_CLASSES) {
        assertEquals(
            2, Collections.list(both.getResources(resource(name))).size(), name + " unbridged");
      }
    }
  }

  @Test
  void bridgedJarKeepsOnlyHandWrittenHelpers() throws Exception {
    try (JarFile jar = new JarFile(bridgedJar())) {
      var names = jar.stream().map(e -> e.getName()).toList();
      assertTrue(names.contains("com/authzed/grpcutil/BearerToken.class"), names.toString());
      for (String name : names) {
        assertFalse(name.startsWith("com/authzed/api/"), name);
        assertFalse(name.startsWith("grpc/gateway/"), name);
        assertFalse(name.endsWith(".proto"), name);
      }
    }
  }

  @Test
  void legacyCodeCompiledAgainstAuthzedJavaRunsOnTheCanonicalBinding() throws Exception {
    // The fixture was compiled against authzed-java; at run time its CheckPermissionRequest is
    // this module's class, so values cross between the two without conversion.
    assertSame(CheckPermissionRequest.class, LegacyAuthzedJavaCaller.requestClass());

    var authorization = new AtomicReference<String>();
    var received = new AtomicReference<CheckPermissionRequest>();
    var service =
        new PermissionsServiceGrpc.PermissionsServiceImplBase() {
          @Override
          public void checkPermission(
              CheckPermissionRequest request, StreamObserver<CheckPermissionResponse> response) {
            received.set(request);
            response.onNext(
                CheckPermissionResponse.newBuilder()
                    .setPermissionship(
                        CheckPermissionResponse.Permissionship.PERMISSIONSHIP_HAS_PERMISSION)
                    .setCheckedAt(ZedToken.newBuilder().setToken("rev-1"))
                    .build());
            response.onCompleted();
          }
        };
    ServerInterceptor captureAuth =
        new ServerInterceptor() {
          @Override
          public <Q, R> ServerCall.Listener<Q> interceptCall(
              ServerCall<Q, R> call, Metadata headers, ServerCallHandler<Q, R> next) {
            authorization.set(
                headers.get(Metadata.Key.of("authorization", Metadata.ASCII_STRING_MARSHALLER)));
            return next.startCall(call, headers);
          }
        };
    String name = InProcessServerBuilder.generateName();
    Server server =
        InProcessServerBuilder.forName(name)
            .directExecutor()
            .addService(ServerInterceptors.intercept(service, captureAuth))
            .build()
            .start();
    ManagedChannel channel = InProcessChannelBuilder.forName(name).directExecutor().build();
    try {
      // The bridged jar's hand-written BearerToken and the fixture's stubs both work.
      CheckPermissionResponse response = LegacyAuthzedJavaCaller.check(channel, "t0ken");
      assertEquals(
          CheckPermissionResponse.Permissionship.PERMISSIONSHIP_HAS_PERMISSION,
          response.getPermissionship());
      assertEquals("rev-1", response.getCheckedAt().getToken());
      assertEquals("Bearer t0ken", authorization.get());
      assertNotNull(received.get());
      assertEquals("doc1", received.get().getResource().getObjectId());
    } finally {
      channel.shutdownNow();
      server.shutdownNow();
    }
  }
}
