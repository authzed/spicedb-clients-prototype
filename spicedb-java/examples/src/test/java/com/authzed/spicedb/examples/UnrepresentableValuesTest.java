package com.authzed.spicedb.examples;

import static org.assertj.core.api.Assertions.*;

import build.buf.gen.authzed.api.v1.CheckBulkPermissionsPair;
import build.buf.gen.authzed.api.v1.CheckBulkPermissionsRequest;
import build.buf.gen.authzed.api.v1.CheckBulkPermissionsResponse;
import build.buf.gen.authzed.api.v1.CheckBulkPermissionsResponseItem;
import build.buf.gen.authzed.api.v1.PermissionsServiceGrpc;
import com.authzed.spicedb.CheckResult;
import com.authzed.spicedb.Consistency;
import com.authzed.spicedb.Filter;
import com.authzed.spicedb.Relationship;
import com.authzed.spicedb.SpiceDBClient;
import com.authzed.spicedb.Transaction;
import com.authzed.spicedb.errors.InvalidArgumentException;
import io.grpc.Server;
import io.grpc.ServerBuilder;
import io.grpc.stub.StreamObserver;
import java.io.IOException;
import java.util.Map;
import org.junit.jupiter.api.Test;

/**
 * Demonstrates both directions of root DESIGN.md, "RULE: A conversion that cannot preserve meaning
 * must fail".
 *
 * <p>The rule has two clauses that point opposite ways, and confusing them is the failure mode
 * either way:
 *
 * <ol>
 *   <li>Data the CALLER supplied that the client cannot represent must raise a typed error
 *       <em>naming what could not be converted</em>. The caller can see the failure and fix their
 *       input, so the client neither approximates the value nor drops it -- silently discarding it
 *       turns a caller's mistake into a silent wrong answer.
 *   <li>Values the SERVER supplied that the client does not recognise must NOT raise, and must map
 *       to the safe, non-permissive default -- never a grant. Raising would turn a routine SpiceDB
 *       upgrade that adds an enum value into a client-side outage.
 * </ol>
 *
 * <p>The last test covers clause 2, and needs a server that emits a permissionship this client has
 * never heard of -- which is why it stands up a stand-in rather than using the real SpiceDB.
 */
class UnrepresentableValuesTest {

  @Test
  void unconvertibleCaveatContextNamesTheKey() {
    // A value with no protobuf representation fails loudly, naming the key. Dropping it would
    // leave a caveat evaluating against context the caller believes it sent, and a caller with a
    // large context map should not have to bisect it to find the bad entry.
    Relationship rel =
        Relationship.of("document", "readme", "viewer", "user", "alice")
            .withCaveat("only_on_tuesday", Map.of("day", "tuesday", "impostor", new Object()));

    var txn = new Transaction();
    txn.touch(rel);

    try (SpiceDBClient client =
        SpiceDBClient.createPlaintext(
            SpiceDBIntegrationTest.ENDPOINT, SpiceDBIntegrationTest.TOKEN)) {
      assertThatThrownBy(() -> client.write(txn))
          // The client's own typed error, not a bare IllegalArgumentException, and it names the
          // offending key -- both of which clause 1 requires.
          .isInstanceOf(InvalidArgumentException.class)
          .hasMessageContaining("impostor")
          .hasMessageNotContaining("day");
    }
  }

  @Test
  void filterWithSubjectIdAndNoSubjectTypeIsRefused() {
    // A subject ID with no subject type is not a narrower filter -- the wire format simply drops
    // it, so the filter silently WIDENS. Applied to deleteRelationships that is the difference
    // between deleting alice's relationships and deleting every relationship on every document.
    try (SpiceDBClient client =
        SpiceDBClient.createPlaintext(
            SpiceDBIntegrationTest.ENDPOINT, SpiceDBIntegrationTest.TOKEN)) {
      assertThatThrownBy(
              () -> client.deleteRelationships(Filter.of("document").withSubjectID("alice")))
          .isInstanceOf(InvalidArgumentException.class)
          .hasMessageContaining("subjectType");

      // The same filter with the missing piece supplied converts fine, which is what makes the
      // check above a real constraint rather than a blanket ban.
      client.deleteRelationships(
          Filter.of("document").withSubjectType("user").withSubjectID("alice"));
    }
  }

  @Test
  void unknownServerPermissionshipNeitherRaisesNorGrants()
      throws IOException, InterruptedException {
    // Clause 2: the opposite posture. Raising here would break forward compatibility -- a SpiceDB
    // rolling out a new enum value would make every deployed client throw on every check.
    Server server =
        ServerBuilder.forPort(0)
            .addService(
                new PermissionsServiceGrpc.PermissionsServiceImplBase() {
                  @Override
                  public void checkBulkPermissions(
                      CheckBulkPermissionsRequest request,
                      StreamObserver<CheckBulkPermissionsResponse> responseObserver) {
                    var builder = CheckBulkPermissionsResponse.newBuilder();
                    for (int i = 0; i < request.getItemsCount(); i++) {
                      builder.addPairs(
                          CheckBulkPermissionsPair.newBuilder()
                              .setItem(
                                  CheckBulkPermissionsResponseItem.newBuilder()
                                      // 4242 is not a value this client's enum knows. A SpiceDB
                                      // that added a permissionship after this client shipped
                                      // would look exactly like this on the wire.
                                      .setPermissionshipValue(4242)
                                      .build())
                              .build());
                    }
                    responseObserver.onNext(builder.build());
                    responseObserver.onCompleted();
                  }
                })
            .build()
            .start();
    try (SpiceDBClient client =
        SpiceDBClient.createPlaintext("127.0.0.1:" + server.getPort(), "some-token")) {
      CheckResult result =
          client.checkPermission(
              Consistency.full(),
              "view",
              Relationship.of("document", "readme", "view", "user", "alice"));
      assertThat(result.hasPermission())
          .as("SECURITY: an unrecognised permissionship was treated as a grant")
          .isFalse();
    } finally {
      server.shutdownNow().awaitTermination();
    }
  }
}
