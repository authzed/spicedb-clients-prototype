package com.authzed.spicedb.proto.legacyfixture;

import com.authzed.api.v1.CheckPermissionRequest;
import com.authzed.api.v1.CheckPermissionResponse;
import com.authzed.api.v1.Consistency;
import com.authzed.api.v1.ObjectReference;
import com.authzed.api.v1.PermissionsServiceGrpc;
import com.authzed.api.v1.SubjectReference;
import com.authzed.grpcutil.BearerToken;
import io.grpc.Channel;

/**
 * Code written for authzed-java, exactly as its README shows it.
 *
 * <p>This source set is compiled against the ORIGINAL published authzed-java jar (see the
 * legacyFixture source set in build.gradle.kts), the way an existing consumer's code was compiled.
 * The bridge test then runs it with only spicedb-java-proto and the bridged authzed-java jar on the
 * class path, proving the bridge is binary compatible for legacy callers.
 */
public final class LegacyAuthzedJavaCaller {
  private LegacyAuthzedJavaCaller() {}

  public static CheckPermissionResponse check(Channel channel, String token) {
    PermissionsServiceGrpc.PermissionsServiceBlockingStub stub =
        PermissionsServiceGrpc.newBlockingStub(channel).withCallCredentials(new BearerToken(token));
    return stub.checkPermission(
        CheckPermissionRequest.newBuilder()
            .setConsistency(Consistency.newBuilder().setFullyConsistent(true).build())
            .setResource(
                ObjectReference.newBuilder().setObjectType("document").setObjectId("doc1").build())
            .setPermission("view")
            .setSubject(
                SubjectReference.newBuilder()
                    .setObject(
                        ObjectReference.newBuilder()
                            .setObjectType("user")
                            .setObjectId("alice")
                            .build())
                    .build())
            .build());
  }

  /** The class authzed-java resolved CheckPermissionRequest to when this code ran. */
  public static Class<?> requestClass() {
    return CheckPermissionRequest.class;
  }
}
