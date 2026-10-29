package com.authzed.spicedb;

/**
 * How {@code checkBulk} reports a per-item error. {@code FAIL_CALL} (default) throws the first
 * item's error with its absolute index; {@code RETURNED} returns every item, with errored items
 * carrying {@link CheckResult#error()} and {@code UNSPECIFIED} permissionship (an errored item
 * never grants).
 */
public enum BulkItemErrors {
  FAIL_CALL,
  RETURNED
}
