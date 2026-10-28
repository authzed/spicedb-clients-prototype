package com.authzed.spicedb;

import java.util.Collection;
import java.util.List;
import java.util.stream.Stream;

/**
 * The relationship and check operations shared by {@link SpiceDBClient} and the in-memory test
 * double {@link com.authzed.spicedb.testing.InMemorySpiceDB}. Depend on this interface where code
 * should accept either (root DESIGN.md, "Each client ships an in-memory test double ... Its surface
 * matches the real client's").
 */
public interface PermissionsApi {
  WriteResult write(Transaction txn);

  WriteResult write(Transaction txn, WriteOptions options);

  Stream<ReadRelationship> readRelationships(Consistency consistency, Filter filter);

  Stream<ReadRelationship> readRelationships(
      Consistency consistency, Filter filter, ReadOptions options);

  SpiceDBClient.DeleteResult deleteRelationships(Filter filter, int limit);

  SpiceDBClient.DeleteResult deleteRelationships(Filter filter, int limit, DeleteOptions options);

  SpiceDBClient.DeleteResult deleteRelationships(Filter filter, DeleteOptions options);

  CheckResult check(PermissionCheck request);

  CheckResult check(PermissionCheck request, CheckOptions options);

  List<CheckResult> checkBulk(Collection<PermissionCheck> requests);

  List<CheckResult> checkBulk(Collection<PermissionCheck> requests, CheckOptions options);
}
