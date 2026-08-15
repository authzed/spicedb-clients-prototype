package com.authzed.spicedb;

import java.util.List;

/**
 * A native node of an expanded permission tree.
 *
 * <p>Avoids leaking the proto {@code PermissionRelationshipTree} type in favor of plain Java
 * records. Returned by {@link SpiceDBClient#expandPermissionTree}. Exactly one of {@link
 * #intermediate} or {@link #leaf} is non-null.
 *
 * @param expandedObject the resource or subject object this node describes
 * @param expandedRelation the relation being expanded
 * @param intermediate non-null when this node combines child subtrees with a set operation
 * @param leaf non-null when this node holds the concrete subjects with access
 */
public record PermissionTree(
    ObjectRef expandedObject,
    String expandedRelation,
    IntermediateNode intermediate,
    LeafNode leaf) {

  /** The set operation combining an {@link IntermediateNode}'s children. */
  public enum Operation {
    UNSPECIFIED,
    UNION,
    INTERSECTION,
    EXCLUSION
  }

  /** Identifies a resource or subject object. */
  public record ObjectRef(String objectType, String objectID) {}

  /** A subject with access at a leaf of a {@link PermissionTree}. */
  public record SubjectRef(String subjectType, String subjectID, String optionalRelation) {}

  /** Combines child subtrees with a set operation. */
  public record IntermediateNode(Operation operation, List<PermissionTree> children) {}

  /** Holds the concrete subjects at a leaf of a {@link PermissionTree}. */
  public record LeafNode(List<SubjectRef> subjects) {}
}
