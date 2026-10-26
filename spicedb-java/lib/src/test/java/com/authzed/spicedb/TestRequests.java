package com.authzed.spicedb;

import java.util.*;

final class TestRequests {
  static PermissionCheck check(Relationship r, String permission, Consistency consistency) {
    return new PermissionCheck(r.resource(), permission, r.subject(), consistency);
  }

  static PermissionCheck withContext(Relationship r, Map<String, Object> context) {
    return new PermissionCheck(
        r.resource(), r.relation(), r.subject(), Consistency.full(), context);
  }

  static PermissionCheck check(PermissionCheck r, String permission, Consistency consistency) {
    return new PermissionCheck(r.resource(), permission, r.subject(), consistency, r.context());
  }

  static List<PermissionCheck> checks(
      Relationship[] values, String permission, Consistency consistency) {
    return Arrays.stream(values).map(r -> check(r, permission, consistency)).toList();
  }
}
