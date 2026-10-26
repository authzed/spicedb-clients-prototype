package com.authzed.spicedb;

/**
 * An immutable filter for matching SpiceDB relationships.
 *
 * <p>Use the builder-style methods to narrow the filter. Each method returns a new {@code Filter}
 * instance (immutable).
 *
 * <pre>{@code
 * Filter filter = Filter.of("document")
 *     .withResourceId("doc1")
 *     .withRelation("viewer")
 *     .withSubjectType("user");
 * }</pre>
 */
public record Filter(
    String resourceType,
    String resourceID,
    String resourceIDPrefix,
    String relation,
    String subjectType,
    String subjectID,
    SubjectRelationFilter subjectRelation) {

  public Filter {
    JsonContext.required(resourceType, "resourceType");
    if (resourceID != null
        && !resourceID.isEmpty()
        && resourceIDPrefix != null
        && !resourceIDPrefix.isEmpty())
      throw new com.authzed.spicedb.errors.InvalidArgumentException(
          "resource ID and prefix are mutually exclusive");
    if (subjectRelation == null) subjectRelation = SubjectRelationFilter.any();
    if ((subjectID != null && !subjectID.isEmpty()
            || !(subjectRelation instanceof SubjectRelationFilter.Any))
        && (subjectType == null || subjectType.isEmpty()))
      throw new com.authzed.spicedb.errors.InvalidArgumentException(
          "subject constraint requires subjectType");
  }

  /**
   * Creates a filter that matches relationships of the given resource type.
   *
   * @throws IllegalArgumentException if resourceType is null or empty
   */
  public static Filter of(String resourceType) {
    JsonContext.required(resourceType, "resourceType");
    if (resourceType.isEmpty()) {
      throw new com.authzed.spicedb.errors.InvalidArgumentException(
          "resourceType must not be empty");
    }
    return new Filter(resourceType, "", "", "", "", "", SubjectRelationFilter.any());
  }

  /** Narrows the filter to a specific resource ID. */
  public Filter withResourceId(String id) {
    return new Filter(
        resourceType, id, resourceIDPrefix, relation, subjectType, subjectID, subjectRelation);
  }

  /** Narrows the filter to resource IDs with the given prefix. */
  public Filter withResourceIdPrefix(String prefix) {
    return new Filter(
        resourceType, resourceID, prefix, relation, subjectType, subjectID, subjectRelation);
  }

  /** Narrows the filter to a specific relation. */
  public Filter withRelation(String rel) {
    return new Filter(
        resourceType, resourceID, resourceIDPrefix, rel, subjectType, subjectID, subjectRelation);
  }

  /** Narrows the filter to a specific subject type. */
  public Filter withSubjectType(String type) {
    return new Filter(
        resourceType, resourceID, resourceIDPrefix, relation, type, subjectID, subjectRelation);
  }

  /** Narrows the filter to a specific subject ID. */
  public Filter withSubjectId(String id) {
    return new Filter(
        resourceType, resourceID, resourceIDPrefix, relation, subjectType, id, subjectRelation);
  }

  /** Narrows the filter to a specific subject relation. */
  public Filter withSubjectRelation(SubjectRelationFilter rel) {
    return new Filter(
        resourceType, resourceID, resourceIDPrefix, relation, subjectType, subjectID, rel);
  }
}
