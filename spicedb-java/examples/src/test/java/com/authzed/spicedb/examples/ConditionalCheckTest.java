package com.authzed.spicedb.examples;

import static com.authzed.spicedb.Consistency.*;
import static org.assertj.core.api.Assertions.*;

import com.authzed.spicedb.CheckResult;
import com.authzed.spicedb.Filter;
import com.authzed.spicedb.LookupResult;
import com.authzed.spicedb.Relationship;
import com.authzed.spicedb.Transaction;
import java.util.List;
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

  /**
   * C5 (spec D3b, the payoff test): the SAME relationship that comes back CONDITIONAL_PERMISSION
   * above resolves into a genuine grant when the missing caveat context is supplied at CHECK time
   * (not write time) via the new {@code checkPermission(..., Map)} overload. This is what makes
   * {@code missingContext} actionable rather than merely observable.
   */
  @Test
  void check_with_context_supplied_at_check_time_resolves_to_a_grant() {
    CheckResult result =
        client.checkPermission(
            full(),
            "view",
            Relationship.of("doc", "firstdoc", "view", "user", "alice"),
            Map.of("now", 42));

    assertThat(result.permissionship()).isEqualTo(LookupResult.Permissionship.HAS_PERMISSION);
    assertThat(result.hasPermission()).isTrue();
    assertThat(result.missingContext()).isEmpty();
    assertThat(result.checkedAt()).isNotEmpty();
  }

  /**
   * Bonus live proof of the merge rule (C3) against a real caveat evaluation, not just a captured
   * request: a call-level default of {@code now=200} fails the caveat ({@code now < 100}), but the
   * checked relationship overrides it with its own {@code now=42} via {@link
   * Relationship#withCheckContext}, which must win per-key over the call-level default.
   */
  @Test
  void check_context_merge_item_override_wins_over_call_level_default() {
    Relationship overridden =
        Relationship.of("doc", "firstdoc", "view", "user", "alice")
            .withCheckContext(Map.of("now", 42));

    List<CheckResult> results =
        client.checkPermissions(full(), "view", Map.of("now", 200), overridden);

    assertThat(results).hasSize(1);
    assertThat(results.get(0).hasPermission())
        .as("item-level now=42 must win over the call-level now=200 default")
        .isTrue();
  }
}
