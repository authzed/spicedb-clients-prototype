package com.authzed.spicedb.examples;

import static com.authzed.spicedb.Consistency.*;
import static org.assertj.core.api.Assertions.*;

import build.buf.gen.authzed.api.v1.CheckPermissionRequest;
import build.buf.gen.authzed.api.v1.CheckPermissionResponse;
import build.buf.gen.authzed.api.v1.Consistency;
import build.buf.gen.authzed.api.v1.ObjectReference;
import build.buf.gen.authzed.api.v1.PermissionsServiceGrpc;
import build.buf.gen.authzed.api.v1.RelationshipUpdate;
import build.buf.gen.authzed.api.v1.SubjectReference;
import build.buf.gen.authzed.api.v1.WriteRelationshipsRequest;
import build.buf.gen.authzed.api.v1.WriteRelationshipsResponse;
import com.authzed.spicedb.CheckResult;
import com.authzed.spicedb.Filter;
import com.authzed.spicedb.Relationship;
import com.google.protobuf.Struct;
import com.google.protobuf.Value;
import java.util.concurrent.TimeUnit;
import org.junit.jupiter.api.Test;

/**
 * Demonstrates reaching past the idiomatic API with {@link
 * com.authzed.spicedb.SpiceDBClient#rawChannel()}.
 *
 * <p>Every wrapper eventually meets a request the wrapper does not express. This client's answer is
 * {@code rawChannel()}: its own gRPC channel, already carrying its bearer metadata, from which any
 * generated stub is one {@code newBlockingStub} call away — a workaround short of forking the
 * library. Root DESIGN.md, "What NOT To Do", allows exactly this as "clearly marked secondary API".
 *
 * <p>The gaps demonstrated here are real, not hypothetical:
 *
 * <ol>
 *   <li>{@code WriteRelationshipsRequest.optionalTransactionMetadata} is a proto field this client
 *       does not surface anywhere. Applications use it to stamp an audit correlation ID onto a
 *       write, which comes back out of the Watch stream.
 *   <li>{@code CheckPermission} — the single-check RPC. The idiomatic {@code checkPermission}
 *       routes every check through {@code CheckBulkPermissions}, so the raw stub is how you drive
 *       the unary RPC itself.
 * </ol>
 *
 * <p>Note what this example does NOT do: build a second {@code ManagedChannel}. {@code
 * rawChannel()} returns THIS client's connection, configured exactly as it was configured
 * (including anything a {@code ClientOption} did to the builder), so the raw path cannot end up on
 * a different transport than the idiomatic one — and the bearer token comes free.
 *
 * <p>What you give up on the raw path, and why the idiomatic methods stay the default: no {@code
 * SpiceDBException} mapping (you catch {@code StatusRuntimeException}), no retry on a transient
 * failure, and no {@code DEFAULT_TIMEOUT} — call {@code withDeadlineAfter} yourself.
 */
class RawEscapeHatchTest extends SpiceDBIntegrationTest {

  // Note the fully-qualified `build.buf.gen.authzed.api.v1.Relationship` and
  // `com.authzed.spicedb.Transaction` below. `Relationship` exists in both the idiomatic
  // package and the generated one, and a raw call takes the generated type; a static import
  // would hide exactly the tier boundary this example is about, so each name says which tier
  // it means.
  @Test
  void rawChannel_sends_a_field_the_idiomatic_api_does_not_expose() {
    client.deleteRelationships(Filter.of("document"));

    var stub = PermissionsServiceGrpc.newBlockingStub(client.rawChannel());

    Struct metadata =
        Struct.newBuilder()
            .putFields("correlation_id", Value.newBuilder().setStringValue("example-42").build())
            .putFields("actor", Value.newBuilder().setStringValue("billing-job").build())
            .build();

    WriteRelationshipsResponse written =
        stub.writeRelationships(
            WriteRelationshipsRequest.newBuilder()
                .addUpdates(
                    RelationshipUpdate.newBuilder()
                        .setOperation(RelationshipUpdate.Operation.OPERATION_TOUCH)
                        .setRelationship(
                            build.buf.gen.authzed.api.v1.Relationship.newBuilder()
                                .setResource(
                                    ObjectReference.newBuilder()
                                        .setObjectType("document")
                                        .setObjectId("ledger"))
                                .setRelation("viewer")
                                .setSubject(
                                    SubjectReference.newBuilder()
                                        .setObject(
                                            ObjectReference.newBuilder()
                                                .setObjectType("user")
                                                .setObjectId("jimmy")))))
                .setOptionalTransactionMetadata(metadata)
                .build());

    String revision = written.getWrittenAt().getToken();
    System.out.println("raw write committed at revision " + revision);
    assertThat(revision).isNotEmpty();

    // The idiomatic API picks up right where the raw call left off — same client, same
    // connection, including read-your-writes on the raw revision.
    CheckResult result =
        client.checkPermission(
            atLeast(revision),
            "view",
            Relationship.of("document", "ledger", "view", "user", "jimmy"));
    System.out.println("user:jimmy can view document:ledger: " + result.hasPermission());
    assertThat(result.hasPermission()).isTrue();

    client.deleteRelationships(Filter.of("document").withResourceID("ledger"));
  }

  @Test
  void rawChannel_calls_an_rpc_the_idiomatic_api_routes_around() {
    client.deleteRelationships(Filter.of("document"));

    var txn = new com.authzed.spicedb.Transaction();
    txn.touch(Relationship.of("document", "ledger", "viewer", "user", "jimmy"));
    client.write(txn);

    // A raw call gets no client default deadline — set one yourself.
    CheckPermissionResponse response =
        PermissionsServiceGrpc.newBlockingStub(client.rawChannel())
            .withDeadlineAfter(30, TimeUnit.SECONDS)
            .checkPermission(
                CheckPermissionRequest.newBuilder()
                    .setConsistency(Consistency.newBuilder().setFullyConsistent(true))
                    .setResource(
                        ObjectReference.newBuilder()
                            .setObjectType("document")
                            .setObjectId("ledger"))
                    .setPermission("view")
                    .setSubject(
                        SubjectReference.newBuilder()
                            .setObject(
                                ObjectReference.newBuilder()
                                    .setObjectType("user")
                                    .setObjectId("jimmy")))
                    .build());

    System.out.println("raw CheckPermission permissionship: " + response.getPermissionship());
    assertThat(response.getPermissionship())
        .isEqualTo(CheckPermissionResponse.Permissionship.PERMISSIONSHIP_HAS_PERMISSION);

    // Close the CLIENT, never the channel rawChannel() handed out — the base class does
    // that in @AfterEach, and it is what releases this connection.
    client.deleteRelationships(Filter.of("document").withResourceID("ledger"));
  }
}
