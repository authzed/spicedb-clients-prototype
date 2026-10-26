package com.authzed.spicedb.examples;

import static com.authzed.spicedb.Consistency.*;
import static org.assertj.core.api.Assertions.*;

import com.authzed.spicedb.*;
import com.authzed.spicedb.CheckResult;
import com.authzed.spicedb.Filter;
import com.authzed.spicedb.Relationship;
import com.authzed.spicedb.Transaction;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

/**
 * Demonstrates checking a single permission using {@link
 * com.authzed.spicedb.SpiceDBClient#checkPermission}.
 *
 * <p>{@code checkPermission} returns a {@link CheckResult}, not a bare {@code boolean} — prefer
 * {@link CheckResult#hasPermission()} over comparing {@link CheckResult#permissionship()} directly.
 * A {@code CONDITIONAL_PERMISSION} result (see {@link ConditionalCheckTest}) means the server
 * needed caveat context that was not supplied, and {@code hasPermission()} is false for it — never
 * treat a conditional result as a grant.
 */
class CheckPermissionTest extends SpiceDBIntegrationTest {

  @BeforeEach
  void setUp() {
    client.deleteAllRelationshipsNonAtomic(Filter.of("document"));

    var txn = new Transaction();
    txn.touch(
        Relationship.of(
            new ObjectRef("document", "firstdoc"),
            "viewer",
            SubjectRef.direct(new ObjectRef("user", "alice"))));
    txn.touch(
        Relationship.of(
            new ObjectRef("document", "firstdoc"),
            "editor",
            SubjectRef.direct(new ObjectRef("user", "bob"))));
    client.write(txn);
  }

  @Test
  void alice_can_view_document() {
    CheckResult result =
        client.check(
            PermissionCheck.builder()
                .resource(new ObjectRef("document", "firstdoc"))
                .permission("view")
                .subject(SubjectRef.direct(new ObjectRef("user", "alice")))
                .consistency(full())
                .build());

    assertThat(result.hasPermission()).isTrue();
    assertThat(result.permissionship()).isEqualTo(Permissionship.HAS_PERMISSION);
    assertThat(result.checkedAt().value()).isNotEmpty();
  }

  @Test
  void alice_cannot_edit_document() {
    CheckResult result =
        client.check(
            new PermissionCheck(
                new ObjectRef("document", "firstdoc"),
                "edit",
                SubjectRef.direct(new ObjectRef("user", "alice")),
                full()));

    assertThat(result.hasPermission()).isFalse();
    assertThat(result.permissionship()).isEqualTo(Permissionship.NO_PERMISSION);
  }

  @Test
  void bob_can_edit_and_view_document() {
    CheckResult canEdit =
        client.check(
            new PermissionCheck(
                new ObjectRef("document", "firstdoc"),
                "edit",
                SubjectRef.direct(new ObjectRef("user", "bob")),
                full()));
    CheckResult canView =
        client.check(
            new PermissionCheck(
                new ObjectRef("document", "firstdoc"),
                "view",
                SubjectRef.direct(new ObjectRef("user", "bob")),
                full()));

    assertThat(canEdit.hasPermission()).isTrue();
    assertThat(canView.hasPermission()).isTrue();
  }

  @org.junit.jupiter.api.Test
  void singularCheckReturnsServerTrace() {
    var request =
        new PermissionCheck(
            new ObjectRef("document", "report"),
            "view",
            SubjectRef.direct(new ObjectRef("user", "alice")),
            Consistency.full());
    var result =
        client.check(
            request,
            CheckOptions.builder()
                .tracing(com.authzed.spicedb.CheckTracing.ENABLED)
                .timeout(java.time.Duration.ofSeconds(2))
                .build());
    assertThat(result.debugTrace()).isNotNull();
    assertThat(result.debugTrace().schemaUsed()).contains("definition document");
    assertThat(result.debugTrace().check().resource()).isEqualTo(request.resource());
  }
}
