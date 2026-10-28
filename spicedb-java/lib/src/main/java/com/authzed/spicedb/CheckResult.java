package com.authzed.spicedb;

import com.authzed.spicedb.errors.SpiceDBException;
import java.util.List;

/**
 * The result of one permission check.
 *
 * @param error the item's error when a bulk check runs with {@link BulkItemErrors#RETURNED};
 *     otherwise {@code null}. An errored item has {@code UNSPECIFIED} permissionship and never
 *     grants.
 */
public record CheckResult(
    Permissionship permissionship,
    List<String> missingContext,
    ZedToken checkedAt,
    DebugInformation debugTrace,
    java.time.Instant expiration,
    SpiceDBException error) {
  public CheckResult {
    missingContext = List.copyOf(missingContext);
    if (error != null && permissionship != Permissionship.UNSPECIFIED)
      throw new com.authzed.spicedb.errors.InvalidArgumentException(
          "an errored check result must have UNSPECIFIED permissionship");
  }

  /** A successful result (no error). */
  public CheckResult(
      Permissionship permissionship,
      List<String> missingContext,
      ZedToken checkedAt,
      DebugInformation debugTrace,
      java.time.Instant expiration) {
    this(permissionship, missingContext, checkedAt, debugTrace, expiration, null);
  }

  /** An errored bulk item ({@link BulkItemErrors#RETURNED}). */
  public static CheckResult ofError(SpiceDBException error, ZedToken checkedAt) {
    if (error == null)
      throw new com.authzed.spicedb.errors.InvalidArgumentException("error must not be null");
    return new CheckResult(Permissionship.UNSPECIFIED, List.of(), checkedAt, null, null, error);
  }

  public boolean hasPermission() {
    return error == null && permissionship == Permissionship.HAS_PERMISSION;
  }
}
