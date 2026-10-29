package com.authzed.spicedb;

import static org.junit.jupiter.api.Assertions.*;

import com.authzed.spicedb.errors.InvalidArgumentException;
import org.junit.jupiter.api.Test;

class NativeOptionsContractTest {
  @Test
  void opaqueTokensRoundTrip() {
    for (String text : new String[] {"", "  opaque/+==\n", "日本語\u0000revision"}) {
      var token = new ZedToken(text);
      assertEquals(text, token.value());
      assertEquals(text, token.toString());
      assertEquals(token, new ZedToken(token.toString()));
    }
    assertThrows(InvalidArgumentException.class, () -> new ZedToken(null));
  }

  @Test
  void enumDefaultsAreExplicit() {
    assertEquals(CheckTracing.DISABLED, CheckOptions.builder().build().tracing());
    assertEquals(LookupDebug.DISABLED, LookupOptions.builder().build().debug());
    assertEquals(WatchContent.RELATIONSHIPS, WatchOptions.builder().build().content());
    assertEquals(
        RemotePlaintextCredentials.REJECT,
        ClientOptions.builder().build().remotePlaintextCredentials());
  }

  @Test
  void nullModesAreRejected() {
    assertThrows(
        InvalidArgumentException.class, () -> CheckOptions.builder().tracing(null).build());
    assertThrows(InvalidArgumentException.class, () -> LookupOptions.builder().debug(null).build());
    assertThrows(
        InvalidArgumentException.class, () -> WatchOptions.builder().content(null).build());
    assertThrows(
        InvalidArgumentException.class,
        () -> ClientOptions.builder().remotePlaintextCredentials(null).build());
  }

  @Test
  void absentAndOpaqueRevisionsSurviveRpcMapping() throws Exception {
    var seen = new java.util.ArrayList<com.authzed.api.v1.CheckPermissionRequest>();
    var service =
        new com.authzed.api.v1.PermissionsServiceGrpc.PermissionsServiceImplBase() {
          @Override
          public void checkPermission(
              com.authzed.api.v1.CheckPermissionRequest request,
              io.grpc.stub.StreamObserver<com.authzed.api.v1.CheckPermissionResponse> out) {
            seen.add(request);
            var response =
                com.authzed.api.v1.CheckPermissionResponse.newBuilder()
                    .setPermissionship(
                        com.authzed.api.v1.CheckPermissionResponse.Permissionship
                            .PERMISSIONSHIP_NO_PERMISSION);
            if (seen.size() > 1)
              response.setCheckedAt(
                  com.authzed.api.v1.ZedToken.newBuilder().setToken("  opaque/日本語==\n"));
            out.onNext(response.build());
            out.onCompleted();
          }
        };
    try (var server = TestServers.start(service)) {
      var token = new ZedToken("  opaque/日本語==\n");
      var request =
          new PermissionCheck(
              new ObjectRef("document", "one"),
              "view",
              SubjectRef.direct(new ObjectRef("user", "one")),
              Consistency.atLeast(token));
      assertNull(server.client().check(request).checkedAt());
      assertEquals(
          token,
          server
              .client()
              .check(request, CheckOptions.builder().tracing(CheckTracing.ENABLED).build())
              .checkedAt());
      assertEquals(token.value(), seen.get(0).getConsistency().getAtLeastAsFresh().getToken());
      assertFalse(seen.get(0).getWithTracing());
      assertTrue(seen.get(1).getWithTracing());
    }
  }
}
