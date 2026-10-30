package com.authzed.spicedb;

import com.authzed.spicedb.errors.LocalValidationException;

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
      throw new LocalValidationException("resource ID and prefix are mutually exclusive");
    if (subjectRelation == null) subjectRelation = SubjectRelationFilter.any();
    if ((subjectID != null && !subjectID.isEmpty()
            || !(subjectRelation instanceof SubjectRelationFilter.Any))
        && (subjectType == null || subjectType.isEmpty()))
      throw new LocalValidationException("subject constraint requires subjectType");
  }

  /**
   * Creates a filter that matches relationships of the given resource type.
   *
   * @throws com.authzed.spicedb.errors.LocalValidationException if resourceType is null or empty
   */
  public static Filter of(String resourceType) {
    JsonContext.required(resourceType, "resourceType");
    if (resourceType.isEmpty()) {
      throw new LocalValidationException("resourceType must not be empty");
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

  private static boolean set(String s) {
    return s != null && !s.isEmpty();
  }

  /**
   * Reports whether SpiceDB would match {@code r} with this filter: resource type, resource ID or
   * ID prefix, relation, subject type and ID, and the subject relation's {@code Any} (any
   * relation), {@code Direct} (no relation) or {@code Named} distinction. Caveat and expiration are
   * not filterable identity. For test doubles; never a substitute for a server read.
   */
  public boolean matches(Relationship r) {
    if (r == null) return false;
    if (!resourceType.equals(r.resourceType())) return false;
    if (set(resourceID) && !resourceID.equals(r.resourceId())) return false;
    if (set(resourceIDPrefix) && !r.resourceId().startsWith(resourceIDPrefix)) return false;
    if (set(relation) && !relation.equals(r.relation())) return false;
    if (set(subjectType)) {
      if (!subjectType.equals(r.subjectType())) return false;
      if (set(subjectID) && !subjectID.equals(r.subjectId())) return false;
    }
    if (subjectRelation instanceof SubjectRelationFilter.Direct) return r.subjectRelation() == null;
    if (subjectRelation instanceof SubjectRelationFilter.Named n)
      return n.name().equals(r.subjectRelation());
    return true;
  }

  /**
   * A readable form: {@code type:id#relation@subjectType:subjectId[#subjectRelation]}, with {@code
   * *} for unconstrained parts, {@code prefix%*} for an ID prefix, and {@code #...} for a direct
   * subject relation.
   */
  @Override
  public String toString() {
    String id = set(resourceIDPrefix) ? resourceIDPrefix + "%*" : or(resourceID);
    String s = resourceType + ":" + id + "#" + or(relation) + "@" + or(subjectType);
    if (set(subjectType)) {
      s += ":" + or(subjectID);
      if (subjectRelation instanceof SubjectRelationFilter.Direct) s += "#...";
      if (subjectRelation instanceof SubjectRelationFilter.Named n) s += "#" + n.name();
    }
    return s;
  }

  private static String or(String v) {
    return set(v) ? v : "*";
  }

  /** Structured JSON diagnostic form listing only the constrained fields. */
  public String toJson() {
    var m = new java.util.LinkedHashMap<String, Object>();
    m.put("resourceType", resourceType);
    if (set(resourceID)) m.put("resourceId", resourceID);
    if (set(resourceIDPrefix)) m.put("resourceIdPrefix", resourceIDPrefix);
    if (set(relation)) m.put("relation", relation);
    if (set(subjectType)) m.put("subjectType", subjectType);
    if (set(subjectID)) m.put("subjectId", subjectID);
    if (subjectRelation instanceof SubjectRelationFilter.Direct) m.put("subjectRelation", "...");
    if (subjectRelation instanceof SubjectRelationFilter.Named n)
      m.put("subjectRelation", n.name());
    return Json.write(m);
  }
}
