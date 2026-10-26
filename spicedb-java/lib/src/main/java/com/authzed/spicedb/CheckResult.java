package com.authzed.spicedb;

import java.util.List;

public record CheckResult(
    Permissionship permissionship,
    List<String> missingContext,
    ZedToken checkedAt,
    DebugInformation debugTrace,
    java.time.Instant expiration) {
  public CheckResult {
    missingContext = List.copyOf(missingContext);
  }

  public boolean hasPermission() {
    return permissionship == Permissionship.HAS_PERMISSION;
  }
}
