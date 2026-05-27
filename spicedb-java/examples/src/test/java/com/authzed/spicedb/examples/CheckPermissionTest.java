package com.authzed.spicedb.examples;

import static com.authzed.spicedb.Consistency.*;
import static org.assertj.core.api.Assertions.*;

import com.authzed.spicedb.Filter;
import com.authzed.spicedb.Relationship;
import com.authzed.spicedb.Transaction;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

/**
 * Demonstrates checking a single permission using {@link
 * com.authzed.spicedb.SpiceDBClient#checkPermission}.
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
    boolean allowed =
        client.checkPermission(
            full(), "view", Relationship.of("document", "firstdoc", "view", "user", "alice"));

    assertThat(allowed).isTrue();
  }

  @Test
  void alice_cannot_edit_document() {
    boolean allowed =
        client.checkPermission(
            full(), "edit", Relationship.of("document", "firstdoc", "edit", "user", "alice"));

    assertThat(allowed).isFalse();
  }

  @Test
  void bob_can_edit_and_view_document() {
    boolean canEdit =
        client.checkPermission(
            full(), "edit", Relationship.of("document", "firstdoc", "edit", "user", "bob"));
    boolean canView =
        client.checkPermission(
            full(), "view", Relationship.of("document", "firstdoc", "view", "user", "bob"));

    assertThat(canEdit).isTrue();
    assertThat(canView).isTrue();
  }
}
