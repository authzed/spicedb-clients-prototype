package com.authzed.spicedb;

import java.util.Collection;
import java.util.List;
import java.util.stream.Stream;

/**
 * The relationship, import/export, schema and check operations shared by {@link SpiceDBClient} and
 * the in-memory test double {@link com.authzed.spicedb.testing.InMemorySpiceDB}. Depend on this
 * interface where code should accept either (root DESIGN.md, "Each client ships an in-memory test
 * double ... Its surface matches the real client's").
 */
public interface PermissionsApi {
  WriteResult write(Transaction txn);

  WriteResult write(Transaction txn, WriteOptions options);

  Stream<ReadRelationship> readRelationships(Consistency consistency, Filter filter);

  Stream<ReadRelationship> readRelationships(
      Consistency consistency, Filter filter, ReadOptions options);

  /**
   * One all-or-nothing delete with the server's default limit: the raw {@code DeleteRelationships}
   * request that carries only a filter. Same as {@link #deleteRelationships(Filter, DeleteOptions)}
   * with {@code PartialDeletion.DISALLOWED}.
   */
  SpiceDBClient.DeleteResult deleteRelationships(Filter filter);

  SpiceDBClient.DeleteResult deleteRelationships(Filter filter, int limit);

  SpiceDBClient.DeleteResult deleteRelationships(Filter filter, int limit, DeleteOptions options);

  SpiceDBClient.DeleteResult deleteRelationships(Filter filter, DeleteOptions options);

  CheckResult check(PermissionCheck request);

  CheckResult check(PermissionCheck request, CheckOptions options);

  List<CheckResult> checkBulk(Collection<PermissionCheck> requests);

  List<CheckResult> checkBulk(Collection<PermissionCheck> requests, CheckOptions options);

  long importRelationships(Iterable<Relationship> relationships);

  long importRelationships(Iterable<Relationship> relationships, ImportOptions options);

  Stream<ExportedRelationship> exportRelationships(Consistency consistency, Filter filter);

  Stream<ExportedRelationship> exportRelationships(
      Consistency consistency, Filter filter, ExportOptions options);

  SpiceDBClient.SchemaResult readSchema();

  SpiceDBClient.SchemaResult readSchema(CallOptions options);

  ZedToken writeSchema(String schema);

  ZedToken writeSchema(String schema, CallOptions options);

  SpiceDBClient.ReflectSchemaResult reflectSchema(Consistency consistency);

  SpiceDBClient.ReflectSchemaResult reflectSchema(Consistency consistency, CallOptions options);
}
