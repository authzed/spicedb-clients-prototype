package com.authzed.spicedb;

import java.util.List;

/**
 * Result of a {@link SpiceDBClient#checkPermission} or {@link SpiceDBClient#checkPermissions} call.
 *
 * <p><b>RULE (root DESIGN.md, "Only an unconditional grant is true"):</b> only {@link
 * #hasPermission()} may be treated as a grant. A {@code CONDITIONAL_PERMISSION} result means the
 * server found a matching relationship but could not evaluate its caveat, because the required
 * context was not supplied — that is the server asking for more information, not granting access.
 * {@code if (result)} does not compile in Java, so this type is safe by construction: callers must
 * go through {@link #hasPermission()} (or compare {@link #permissionship()} explicitly) to get a
 * boolean answer.
 *
 * @param permissionship the server's answer; prefer {@link #hasPermission()} for the common case
 * @param missingContext caveat context keys the server needed and did not receive; empty unless
 *     {@code permissionship} is {@code CONDITIONAL_PERMISSION}
 * @param checkedAt the revision this check was evaluated at; thread it into {@link
 *     Consistency#atLeast} to make a later read observe this check (read-your-writes)
 */
public record CheckResult(
    LookupResult.Permissionship permissionship, List<String> missingContext, String checkedAt) {

  public CheckResult {
    missingContext = missingContext == null ? List.of() : List.copyOf(missingContext);
  }

  /**
   * Reports whether the subject has the permission outright. False for a {@code
   * CONDITIONAL_PERMISSION} result: the server could not evaluate the caveat, so treating it as a
   * grant would authorize on a caveat that was never evaluated. A single equality comparison, never
   * a disjunction — see root DESIGN.md, "RULE: Only an unconditional grant is true", clause 2.
   */
  public boolean hasPermission() {
    return permissionship == LookupResult.Permissionship.HAS_PERMISSION;
  }
}
