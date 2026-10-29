package com.authzed.spicedb;

import static org.junit.jupiter.api.Assertions.*;

import com.authzed.api.v1.PermissionsServiceGrpc;
import com.authzed.api.v1.WriteRelationshipsRequest;
import com.authzed.api.v1.WriteRelationshipsResponse;
import io.grpc.stub.StreamObserver;
import org.junit.jupiter.params.ParameterizedTest;
import org.junit.jupiter.params.provider.NullAndEmptySource;
import org.junit.jupiter.params.provider.ValueSource;

class WriteResultTest {
  @ParameterizedTest
  @NullAndEmptySource
  @ValueSource(strings = {"opaque-revision"})
  void relationshipWritesReturnNativeResultPreservingRevisionPresence(String revision)
      throws Exception {
    var service =
        new PermissionsServiceGrpc.PermissionsServiceImplBase() {
          @Override
          public void writeRelationships(
              WriteRelationshipsRequest request, StreamObserver<WriteRelationshipsResponse> out) {
            var response = WriteRelationshipsResponse.newBuilder();
            if (revision != null)
              response.setWrittenAt(com.authzed.api.v1.ZedToken.newBuilder().setToken(revision));
            out.onNext(response.build());
            out.onCompleted();
          }
        };
    try (var servers = TestServers.start(service)) {
      WriteResult result = servers.client().write(new Transaction());
      assertNotNull(result);
      assertEquals(revision == null ? null : new ZedToken(revision), result.revision());
      assertEquals(
          WriteResult.class,
          SpiceDBClient.class.getMethod("write", Transaction.class).getReturnType());
    }
  }
}
