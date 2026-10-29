package com.authzed.spicedb;

import io.grpc.Channel;
import java.util.Collection;
import java.util.List;
import java.util.stream.Stream;

/**
 * Every public instance method of {@link SpiceDBClient}: {@link PermissionsApi} plus lookups,
 * schema analysis, expand, watch, delete-all, the experimental operations, {@link #rawChannel()}
 * and {@link #close()}. Both {@link SpiceDBClient} and the in-memory test double {@link
 * com.authzed.spicedb.testing.InMemorySpiceDB} implement it, so code that needs more than {@link
 * PermissionsApi} can still take the double in tests. A reflection test keeps it identical to the
 * client's method set.
 */
public interface SpiceDBApi extends PermissionsApi, AutoCloseable {
  boolean checkAny(Collection<PermissionCheck> requests);

  boolean checkAny(Collection<PermissionCheck> requests, CheckOptions options);

  boolean checkAll(Collection<PermissionCheck> requests);

  boolean checkAll(Collection<PermissionCheck> requests, CheckOptions options);

  SpiceDBClient.DeleteResult deleteAllRelationshipsNonAtomic(Filter filter);

  SpiceDBClient.DeleteResult deleteAllRelationshipsNonAtomic(
      Filter filter, DeleteAllOptions options);

  Stream<LookupResult.LookupResource> lookupResources(
      Consistency consistency, String resourceType, String permission, SubjectRef subject);

  Stream<LookupResult.LookupResource> lookupResources(
      Consistency consistency,
      String resourceType,
      String permission,
      SubjectRef subject,
      LookupOptions options);

  Stream<LookupResult.LookupSubject> lookupSubjects(
      Consistency consistency, ObjectRef resource, String permission, String subjectType);

  Stream<LookupResult.LookupSubject> lookupSubjects(
      Consistency consistency,
      ObjectRef resource,
      String permission,
      String subjectType,
      LookupOptions options);

  SpiceDBClient.ComputablePermissionsResult computablePermissions(
      Consistency consistency, String definitionName, String relationName);

  SpiceDBClient.ComputablePermissionsResult computablePermissions(
      Consistency consistency, String definitionName, String relationName, CallOptions options);

  SpiceDBClient.DependentRelationsResult dependentRelations(
      Consistency consistency, String definitionName, String permissionName);

  SpiceDBClient.DependentRelationsResult dependentRelations(
      Consistency consistency, String definitionName, String permissionName, CallOptions options);

  SpiceDBClient.DiffSchemaResult diffSchema(Consistency consistency, String comparisonSchema);

  SpiceDBClient.DiffSchemaResult diffSchema(
      Consistency consistency, String comparisonSchema, CallOptions options);

  SpiceDBClient.ExpandResult expandPermissionTree(
      Consistency consistency, ObjectRef resource, String permission);

  SpiceDBClient.ExpandResult expandPermissionTree(
      Consistency consistency, ObjectRef resource, String permission, CallOptions options);

  Stream<SpiceDBClient.WatchEvent> updates(List<String> objectTypes, ZedToken startRevision);

  Stream<SpiceDBClient.WatchEvent> updates(
      List<String> objectTypes, ZedToken startRevision, WatchOptions options);

  void experimentalRegisterRelationshipCounter(String name, Filter filter);

  void experimentalRegisterRelationshipCounter(String name, Filter filter, CallOptions options);

  SpiceDBClient.CountResult experimentalCountRelationships(String name);

  SpiceDBClient.CountResult experimentalCountRelationships(String name, CallOptions options);

  void experimentalUnregisterRelationshipCounter(String name);

  void experimentalUnregisterRelationshipCounter(String name, CallOptions options);

  Materialize.RoaringLookupResourcesResult experimentalRoaringLookupResources(
      Consistency consistency, String resourceType, String permission, SubjectRef subject);

  Materialize.RoaringLookupResourcesResult experimentalRoaringLookupResources(
      Consistency consistency,
      String resourceType,
      String permission,
      SubjectRef subject,
      LookupOptions options);

  SpiceDBClient.CountResult experimentalCountRelationshipsByFilter(Filter filter);

  SpiceDBClient.CountResult experimentalCountRelationshipsByFilter(
      Filter filter, CallOptions options);

  Stream<Materialize.PermissionSetEvent> experimentalWatchPermissionSets(ZedToken startRevision);

  Stream<Materialize.PermissionSetEvent> experimentalWatchPermissionSets(
      ZedToken startRevision, CallOptions options);

  Stream<Materialize.LookupPermissionSet> experimentalLookupPermissionSets(ZedToken atRevision);

  Stream<Materialize.LookupPermissionSet> experimentalLookupPermissionSets(
      ZedToken atRevision, PermissionSetOptions options);

  Materialize.DownloadPermissionSetsResult experimentalDownloadPermissionSets(ZedToken atRevision);

  Materialize.DownloadPermissionSetsResult experimentalDownloadPermissionSets(
      ZedToken atRevision, CallOptions options);

  /**
   * Startup readiness check: connects now and waits up to {@code timeout} for the connection,
   * failing with {@code UNAVAILABLE} or {@code DEADLINE_EXCEEDED} when the endpoint is not
   * reachable. See {@link SpiceDBClient#awaitReady}; the in-memory double returns at once.
   */
  void awaitReady(java.time.Duration timeout);

  /** The client's authenticated transport; {@code null} on implementations without one. */
  Channel rawChannel();

  /** Releases the implementation's resources; never throws a checked exception. */
  @Override
  void close();
}
