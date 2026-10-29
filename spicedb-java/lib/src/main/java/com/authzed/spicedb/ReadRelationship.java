package com.authzed.spicedb;

import com.authzed.spicedb.errors.LocalValidationException;

/**
 * One relationship from {@link SpiceDBClient#readRelationships}, with its paging state.
 *
 * @param relationship the relationship
 * @param afterResultCursor resume after this item with {@link ReadOptions.Builder#cursor},
 *     repeating the original filter, consistency and page size; {@code null} if the server sent
 *     none
 * @param readAt the revision of the response page this item came from
 */
public record ReadRelationship(
    Relationship relationship, String afterResultCursor, ZedToken readAt) {
  public ReadRelationship {
    if (relationship == null) throw new LocalValidationException("relationship is required");
  }

  @Override
  public String toString() {
    return relationship.toString();
  }
}
