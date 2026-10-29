package com.authzed.spicedb;

/**
 * One relationship from {@link SpiceDBClient#exportRelationships}, with its paging state. Export
 * responses carry no read-at revision.
 *
 * @param relationship the relationship
 * @param afterResultCursor resume after this item's batch with {@link
 *     ExportOptions.Builder#cursor}, repeating the original filter, consistency and page size;
 *     {@code null} if the server sent none
 */
public record ExportedRelationship(Relationship relationship, String afterResultCursor) {
  public ExportedRelationship {
    if (relationship == null)
      throw new com.authzed.spicedb.errors.InvalidArgumentException("relationship is required");
  }

  @Override
  public String toString() {
    return relationship.toString();
  }
}
