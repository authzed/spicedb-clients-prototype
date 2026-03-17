package com.authzed.spicedb;

import java.util.Objects;

/**
 * An immutable filter for matching SpiceDB relationships.
 *
 * <p>Use the builder-style methods to narrow the filter. Each method returns a
 * new {@code Filter} instance (immutable).
 *
 * <pre>{@code
 * Filter filter = Filter.of("document")
 *     .withResourceID("doc1")
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
    String subjectRelation
) {

    /**
     * Creates a filter that matches relationships of the given resource type.
     *
     * @throws IllegalArgumentException if resourceType is null or empty
     */
    public static Filter of(String resourceType) {
        Objects.requireNonNull(resourceType, "resourceType must not be null");
        if (resourceType.isEmpty()) {
            throw new IllegalArgumentException("resourceType must not be empty");
        }
        return new Filter(resourceType, "", "", "", "", "", "");
    }

    /** Narrows the filter to a specific resource ID. */
    public Filter withResourceID(String id) {
        return new Filter(resourceType, id, resourceIDPrefix, relation, subjectType, subjectID, subjectRelation);
    }

    /** Narrows the filter to resource IDs with the given prefix. */
    public Filter withResourceIDPrefix(String prefix) {
        return new Filter(resourceType, resourceID, prefix, relation, subjectType, subjectID, subjectRelation);
    }

    /** Narrows the filter to a specific relation. */
    public Filter withRelation(String rel) {
        return new Filter(resourceType, resourceID, resourceIDPrefix, rel, subjectType, subjectID, subjectRelation);
    }

    /** Narrows the filter to a specific subject type. */
    public Filter withSubjectType(String type) {
        return new Filter(resourceType, resourceID, resourceIDPrefix, relation, type, subjectID, subjectRelation);
    }

    /** Narrows the filter to a specific subject ID. */
    public Filter withSubjectID(String id) {
        return new Filter(resourceType, resourceID, resourceIDPrefix, relation, subjectType, id, subjectRelation);
    }

    /** Narrows the filter to a specific subject relation. */
    public Filter withSubjectRelation(String rel) {
        return new Filter(resourceType, resourceID, resourceIDPrefix, relation, subjectType, subjectID, rel);
    }
}
