package com.authzed.spicedb.examples;

import static com.authzed.spicedb.Consistency.*;
import static org.assertj.core.api.Assertions.*;

import com.authzed.spicedb.CheckResult;
import com.authzed.spicedb.Filter;
import com.authzed.spicedb.LookupResult;
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
    client.deleteRelationships(Filter.of("document"));

    var txn = new Transaction();
    txn.touch(Relationship.of("document", "firstdoc", "viewer", "user", "alice"));
    txn.touch(Relationship.of("document", "firstdoc", "editor", "user", "bob"));
    client.write(txn);
  }

  @Test
  void alice_can_view_document() {
    CheckResult result =
        client.checkPermission(
            full(), "view", Relationship.of("document", "firstdoc", "view", "user", "alice"));

    assertThat(result.hasPermission()).isTrue();
    assertThat(result.permissionship()).isEqualTo(LookupResult.Permissionship.HAS_PERMISSION);
    assertThat(result.checkedAt()).isNotEmpty();
  }

  @Test
  void alice_cannot_edit_document() {
    CheckResult result =
        client.checkPermission(
            full(), "edit", Relationship.of("document", "firstdoc", "edit", "user", "alice"));

    assertThat(result.hasPermission()).isFalse();
    assertThat(result.permissionship()).isEqualTo(LookupResult.Permissionship.NO_PERMISSION);
  }

  @Test
  void bob_can_edit_and_view_document() {
    CheckResult canEdit =
        client.checkPermission(
            full(), "edit", Relationship.of("document", "firstdoc", "edit", "user", "bob"));
    CheckResult canView =
        client.checkPermission(
            full(), "view", Relationship.of("document", "firstdoc", "view", "user", "bob"));

    assertThat(canEdit.hasPermission()).isTrue();
    assertThat(canView.hasPermission()).isTrue();
  }
}
