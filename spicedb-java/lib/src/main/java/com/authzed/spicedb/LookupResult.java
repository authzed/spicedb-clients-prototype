package com.authzed.spicedb;

import java.util.List;

/**
 * Native result types for {@link SpiceDBClient#lookupResources} and {@link
 * SpiceDBClient#lookupSubjects}.
 *
 * <p>Avoids leaking the proto lookup response types in favor of plain Java records — grouped under
 * one wrapper the same way {@link PermissionTree} groups the expand-tree record family (and so that
 * these native names don't collide with the proto {@code PartialCaveatInfo}/{@code ResolvedSubject}
 * types wildcard-imported in {@code SpiceDBClient}).
 */
public final class LookupResult {

  private LookupResult() {}

  /**
   * Indicates whether a lookup result reflects a full grant or is conditional on caveat context
   * that was not fully evaluated by the server. Callers MUST check this before treating a result as
   * a full grant — a {@code CONDITIONAL_PERMISSION} result may resolve to false once the missing
   * caveat context is supplied.
   */
  public enum Permissionship {
    UNSPECIFIED,
    HAS_PERMISSION,
    CONDITIONAL_PERMISSION
  }

  /**
   * Lists caveat context that was missing to fully evaluate a conditional result.
   *
   * @param missingRequiredContext names of the missing caveat context parameters
   */
  public record PartialCaveatInfo(List<String> missingRequiredContext) {}

  /**
   * One result from {@link SpiceDBClient#lookupResources}.
   *
   * @param resourceId the object ID of the resource
   * @param permissionship whether the grant is full or conditional on caveat context
   * @param partialCaveat non-null when {@code permissionship} is {@code CONDITIONAL_PERMISSION}
   */
  public record LookupResource(
      String resourceId, Permissionship permissionship, PartialCaveatInfo partialCaveat) {}

  /**
   * A subject resolved by {@link SpiceDBClient#lookupSubjects} — either the matched subject, or
   * (when found in {@link LookupSubject#excludedSubjects}) a subject excluded from a wildcard
   * match.
   *
   * @param subjectId the object ID of the subject; may be the wildcard {@code "*"}
   * @param permissionship whether the grant is full or conditional on caveat context
   * @param partialCaveat non-null when {@code permissionship} is {@code CONDITIONAL_PERMISSION}
   */
  public record ResolvedSubject(
      String subjectId, Permissionship permissionship, PartialCaveatInfo partialCaveat) {}

  /**
   * One result from {@link SpiceDBClient#lookupSubjects}.
   *
   * <p>When {@link #subject}'s {@code subjectId} is the wildcard {@code "*"}, the server has
   * granted the permission to every subject of the requested subject type EXCEPT those listed in
   * {@link #excludedSubjects}. Callers MUST check {@link #excludedSubjects} before treating a
   * wildcard match as a blanket grant, or they risk granting access to subjects the server
   * explicitly excluded.
   *
   * @param subject the matched subject
   * @param excludedSubjects subjects excluded from a wildcard match (empty when there are none)
   */
  public record LookupSubject(ResolvedSubject subject, List<ResolvedSubject> excludedSubjects) {}
}
