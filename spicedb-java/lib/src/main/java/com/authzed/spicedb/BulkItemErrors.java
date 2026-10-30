package com.authzed.spicedb;

/**
 * How {@code checkBulk} reports a per-item error. {@code FAIL_CALL} (default) throws the first
 * item's error with its absolute index; {@code RETURNED} returns a result for every item, with
 * errored items carrying {@link CheckResult#error()} and {@code UNSPECIFIED} permissionship (an
 * errored item never grants).
 *
 * <p>An item error is a status the server reported for that item. A malformed item in the server's
 * response (such as an unset permissionship, or a pair with neither item nor error) is not an item
 * error: in either mode it fails the whole call with the client-detected malformed-response {@code
 * INTERNAL} error.
 */
public enum BulkItemErrors {
  FAIL_CALL,
  RETURNED
}
