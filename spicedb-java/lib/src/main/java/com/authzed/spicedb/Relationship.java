package com.authzed.spicedb;

import java.time.Instant;
import java.util.Map;
import java.util.Objects;

/**
 * A flat, immutable representation of a SpiceDB relationship.
 *
 * <p>Avoids nested proto types in favor of plain Java fields. Use {@link #of} or {@link #fromTuple}
 * to construct instances.
 *
 * @param resourceType the type of the resource (e.g. "document")
 * @param resourceID the ID of the resource
 * @param resourceRelation the relation on the resource (e.g. "viewer")
 * @param subjectType the type of the subject (e.g. "user")
 * @param subjectID the ID of the subject
 * @param subjectRelation optional relation on the subject (e.g. "member"), may be empty
 * @param caveatName optional caveat name, may be null
 * @param caveatContext optional caveat context, may be null
 * @param expiration optional expiration time, may be null
 * @param checkContext optional CHECK-TIME caveat context, may be null. Distinct from {@code
 *     caveatContext}: {@code caveatContext} is stored WITH the relationship at write time; {@code
 *     checkContext} is supplied fresh on a {@code checkPermission}/{@code checkPermissions}/{@code
 *     checkAny}/{@code checkAll} call and is never written. Set via {@link #withCheckContext}.
 */
public record Relationship(
    String resourceType,
    String resourceID,
    String resourceRelation,
    String subjectType,
    String subjectID,
    String subjectRelation,
    String caveatName,
    Map<String, Object> caveatContext,
    Instant expiration,
    Map<String, Object> checkContext) {

  /**
   * Creates a relationship with all required fields.
   *
   * @throws IllegalArgumentException if any required field is null or empty
   */
  public static Relationship of(
      String resourceType,
      String resourceID,
      String resourceRelation,
      String subjectType,
      String subjectID,
      String subjectRelation) {
    if (resourceType == null
        || resourceType.isEmpty()
        || resourceID == null
        || resourceID.isEmpty()
        || resourceRelation == null
        || resourceRelation.isEmpty()) {
      throw new IllegalArgumentException("resource type, id, and relation are required");
    }
    if (subjectType == null || subjectType.isEmpty() || subjectID == null || subjectID.isEmpty()) {
      throw new IllegalArgumentException("subject type and id are required");
    }
    return new Relationship(
        resourceType,
        resourceID,
        resourceRelation,
        subjectType,
        subjectID,
        subjectRelation != null ? subjectRelation : "",
        null,
        null,
        null,
        null);
  }

  /** Creates a relationship without a subject relation. */
  public static Relationship of(
      String resourceType,
      String resourceID,
      String resourceRelation,
      String subjectType,
      String subjectID) {
    return of(resourceType, resourceID, resourceRelation, subjectType, subjectID, "");
  }

  /**
   * Parses a relationship from a tuple string in the format: {@code
   * resourceType:resourceID#relation@subjectType:subjectID[#subjectRelation]}
   *
   * @throws IllegalArgumentException if the format is invalid
   */
  public static Relationship fromTuple(String tuple) {
    Objects.requireNonNull(tuple, "tuple must not be null");

    String[] atParts = tuple.split("@", 2);
    if (atParts.length != 2) {
      throw new IllegalArgumentException("invalid tuple format: missing '@' separator");
    }

    String resourcePart = atParts[0];
    String subjectPart = atParts[1];

    // Parse resource: type:id#relation
    String[] resourceHash = resourcePart.split("#", 2);
    if (resourceHash.length != 2) {
      throw new IllegalArgumentException("invalid tuple format: missing '#' in resource");
    }
    String[] resourceTypeId = resourceHash[0].split(":", 2);
    if (resourceTypeId.length != 2) {
      throw new IllegalArgumentException("invalid tuple format: missing ':' in resource type:id");
    }

    // Parse subject: type:id[#relation]
    String[] subjectHash = subjectPart.split("#", 2);
    String[] subjectTypeId = subjectHash[0].split(":", 2);
    if (subjectTypeId.length != 2) {
      throw new IllegalArgumentException("invalid tuple format: missing ':' in subject type:id");
    }
    String subjectRelation = subjectHash.length == 2 ? subjectHash[1] : "";

    return of(
        resourceTypeId[0],
        resourceTypeId[1],
        resourceHash[1],
        subjectTypeId[0],
        subjectTypeId[1],
        subjectRelation);
  }

  /** Returns a copy of this relationship with the given caveat. */
  public Relationship withCaveat(String name, Map<String, Object> context) {
    return new Relationship(
        resourceType,
        resourceID,
        resourceRelation,
        subjectType,
        subjectID,
        subjectRelation,
        name,
        context,
        expiration,
        checkContext);
  }

  /** Returns a copy of this relationship with the given expiration. */
  public Relationship withExpiration(Instant exp) {
    return new Relationship(
        resourceType,
        resourceID,
        resourceRelation,
        subjectType,
        subjectID,
        subjectRelation,
        caveatName,
        caveatContext,
        exp,
        checkContext);
  }

  /**
   * Returns a copy of this relationship with the given CHECK-TIME caveat context — used only when
   * this relationship is passed to {@link SpiceDBClient#checkPermission(Consistency, String,
   * Relationship, Map)}/{@link SpiceDBClient#checkPermissions(Consistency, String, Map,
   * Relationship...)} and friends. Never sent on a write; see {@link #withCaveat} for the
   * write-time equivalent.
   *
   * <p>When a call-level context is also supplied to the check call, this relationship's context
   * wins per-key over the call-level default; keys present only in the call-level context are still
   * applied to this relationship (key-level merge, not wholesale replacement).
   */
  public Relationship withCheckContext(Map<String, Object> context) {
    return new Relationship(
        resourceType,
        resourceID,
        resourceRelation,
        subjectType,
        subjectID,
        subjectRelation,
        caveatName,
        caveatContext,
        expiration,
        context);
  }

  /** Returns a {@link Filter} that matches the exact resource of this relationship. */
  public Filter toFilter() {
    return Filter.of(resourceType)
        .withResourceID(resourceID)
        .withRelation(resourceRelation)
        .withSubjectType(subjectType)
        .withSubjectID(subjectID);
  }

  /**
   * Returns the tuple string representation: {@code
   * resourceType:resourceID#relation@subjectType:subjectID[#subjectRelation]}
   */
  @Override
  public String toString() {
    var sb =
        new StringBuilder()
            .append(resourceType)
            .append(':')
            .append(resourceID)
            .append('#')
            .append(resourceRelation)
            .append('@')
            .append(subjectType)
            .append(':')
            .append(subjectID);
    if (subjectRelation != null && !subjectRelation.isEmpty()) {
      sb.append('#').append(subjectRelation);
    }
    return sb.toString();
  }
}
