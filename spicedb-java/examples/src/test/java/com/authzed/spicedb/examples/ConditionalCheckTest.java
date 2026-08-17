package com.authzed.spicedb.examples;

import static com.authzed.spicedb.Consistency.*;
import static org.assertj.core.api.Assertions.*;

import com.authzed.spicedb.CheckResult;
import com.authzed.spicedb.Filter;
import com.authzed.spicedb.LookupResult;
import com.authzed.spicedb.Relationship;
import com.authzed.spicedb.Transaction;
import java.util.Map;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

/**
 * Demonstrates the case that motivates root DESIGN.md's "RULE: Only an unconditional grant is true"
 * against a live SpiceDB server: a caveated relationship exists, but the caveat context required to
 * evaluate it was never supplied — at write time OR check time. The server responds {@code
 * CONDITIONAL_PERMISSION}, meaning "I need more information," NOT a grant.
 *
 * <p>{@link CheckResult#hasPermission()} MUST be false here even though a matching relationship
 * exists — collapsing this to a bare boolean (the bug this rule exists to prevent) would silently
 * grant access on a caveat that was never evaluated.
 *
 * <p>Extends the shared schema with a caveat plus a standalone {@code doc} definition for the
 * duration of this test class (WriteSchema is a full replace, and SpiceDB refuses to drop a
 * definition — here, the shared {@code document} one — while relationships still exist under it, so
 * the shared definitions must stay present rather than being swapped out). Cleans up the {@code
 * doc} relationship and restores the plain shared schema in {@code @AfterEach}, for the same
 * reason, so later tests are unaffected — mirroring the restore pattern in {@link
 * SchemaManagementTest}.
 */
class ConditionalCheckTest extends SpiceDBIntegrationTest {

  private static final String CAVEATED_SCHEMA =
      SCHEMA
          + """


              caveat active(now int) {
                  now < 100
              }

              definition doc {
                  relation viewer: user with active
                  permission view = viewer
              }""";

  @BeforeEach
  void setUp() {
    client.writeSchema(CAVEATED_SCHEMA);

    var txn = new Transaction();
    // The caveat context is left empty at write time — "now" is not supplied here, and (see the
    // test below) not supplied at check time either, so the server cannot evaluate `now < 100`.
    txn.touch(
        Relationship.of("doc", "firstdoc", "viewer", "user", "alice")
            .withCaveat("active", Map.of()));
    client.write(txn);
  }

  @AfterEach
  void restoreSharedSchema() {
    // WriteSchema refuses to drop the `doc` definition while a relationship exists under it, so
    // the relationship must be cleared first.
    client.deleteRelationships(Filter.of("doc"));
    client.writeSchema(SCHEMA);
  }

  @Test
  void check_with_no_context_is_conditional_and_not_a_grant() {
    CheckResult result =
        client.checkPermission(
            full(), "view", Relationship.of("doc", "firstdoc", "view", "user", "alice"));

    assertThat(result.permissionship())
        .isEqualTo(LookupResult.Permissionship.CONDITIONAL_PERMISSION);
    assertThat(result.hasPermission()).isFalse();
    assertThat(result.missingContext()).containsExactly("now");
    assertThat(result.checkedAt()).isNotEmpty();
  }
}
