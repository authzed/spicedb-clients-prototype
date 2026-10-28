package com.authzed.spicedb;

/**
 * One operation of {@link SpiceDBClient}, named after its method. It selects the operation a
 * client-wide per-operation retry policy applies to ({@link
 * ClientOptions.Builder#operationRetryPolicy}) and names the calls the in-memory test double
 * records and fails ({@code com.authzed.spicedb.testing.InMemorySpiceDB}).
 *
 * <p>{@code checkAny} and {@code checkAll} are {@link #CHECK_BULK} calls.
 */
public enum ClientOperation {
  CHECK,
  CHECK_BULK,
  WRITE,
  READ_RELATIONSHIPS,
  DELETE_RELATIONSHIPS,
  DELETE_ALL_RELATIONSHIPS_NON_ATOMIC,
  LOOKUP_RESOURCES,
  LOOKUP_SUBJECTS,
  READ_SCHEMA,
  WRITE_SCHEMA,
  REFLECT_SCHEMA,
  COMPUTABLE_PERMISSIONS,
  DEPENDENT_RELATIONS,
  DIFF_SCHEMA,
  EXPAND_PERMISSION_TREE,
  IMPORT_RELATIONSHIPS,
  EXPORT_RELATIONSHIPS,
  /** {@code updates}: watching relationship changes. */
  WATCH,
  REGISTER_RELATIONSHIP_COUNTER,
  COUNT_RELATIONSHIPS,
  UNREGISTER_RELATIONSHIP_COUNTER,
  ROARING_LOOKUP_RESOURCES,
  COUNT_RELATIONSHIPS_BY_FILTER,
  WATCH_PERMISSION_SETS,
  LOOKUP_PERMISSION_SETS,
  DOWNLOAD_PERMISSION_SETS;

  /**
   * Whether replaying this operation is never safe, so no retry policy with retries may apply to
   * it: deletes, delete-all, imports and counter registration and unregistration. A {@link #WRITE}
   * is idempotent only when its transaction is ({@link Transaction#isIdempotent()}).
   */
  public boolean isNeverIdempotent() {
    return switch (this) {
      case DELETE_RELATIONSHIPS,
              DELETE_ALL_RELATIONSHIPS_NON_ATOMIC,
              IMPORT_RELATIONSHIPS,
              REGISTER_RELATIONSHIP_COUNTER,
              UNREGISTER_RELATIONSHIP_COUNTER ->
          true;
      default -> false;
    };
  }
}
