# Native RPC coverage

This is the intentionally incompatible 2026-10-26 prototype baseline. Every
listed wrapper accepts native requests/results, `context.Context`, and trailing
operation-specific options. Protobuf conversion is private. `RawProto()` is the
explicit secondary accessor and shares the authenticated connection and ownership.

| Protocol service | RPC | Native wrapper |
| --- | --- | --- |
| PermissionsService | CheckPermission | Check |
| PermissionsService | CheckBulkPermissions | CheckBulk, CheckIter (1,000 per chunk) |
| PermissionsService | WriteRelationships | Write |
| PermissionsService | ReadRelationships | ReadRelationships |
| PermissionsService | DeleteRelationships | DeleteRelationships (one bound), DeleteAllRelationshipsNonAtomic (completion) |
| PermissionsService | ExpandPermissionTree | ExpandPermissionTree |
| PermissionsService | LookupResources | LookupResources |
| PermissionsService | LookupSubjects | LookupSubjects |
| PermissionsService | ImportBulkRelationships | ImportRelationships |
| PermissionsService | ExportBulkRelationships | ExportRelationships |
| SchemaService | ReadSchema | ReadSchema |
| SchemaService | WriteSchema | WriteSchema |
| SchemaService | ReflectSchema | ReflectSchema |
| SchemaService | ComputablePermissions | ComputablePermissions |
| SchemaService | DependentRelations | DependentRelations |
| SchemaService | DiffSchema | DiffSchema |
| WatchService | Watch | Updates |
| ExperimentalService | ExperimentalRegisterRelationshipCounter | RegisterRelationshipCounter |
| ExperimentalService | ExperimentalCountRelationships | CountRelationships |
| ExperimentalService | ExperimentalUnregisterRelationshipCounter | UnregisterRelationshipCounter |
| materialize.v0.RoaringLookupResourcesService | ExperimentalRoaringLookupResources | RoaringLookupResources |
| materialize.v0.RelationshipsService | ExperimentalCountRelationshipsByFilter | CountRelationshipsByFilter |
| materialize.v0.WatchPermissionSetsService | WatchPermissionSets | WatchPermissionSets |
| materialize.v0.WatchPermissionSetsService | LookupPermissionSets | LookupPermissionSets |
| materialize.v0.WatchPermissionSetsService | DownloadPermissionSets | DownloadPermissionSets |

Experimental operations carry native documentation markers. Deprecated generated
ExperimentalService predecessors (bulk import/export/check and schema reflection,
computable/dependent/diff), and materialize WatchPermissions, remain in the
unchanged generated tier with upstream deprecation markers; canonical wrappers
use their current replacements.

Tests exercise native unary routing/results, singular/per-item tracing, exact
filters, chunk order and error indices, context snapshots and JSON nulls, complete
reflection/diff variants, total retry/chunk/page budgets, mutation non-replay,
stream establishment retry, partial-stream failure, native cancellation,
acknowledged delete progress, unknown enum safety, authenticated materialize
connection reuse, server-observed stream release, and a successful TLS handshake
with custom trust. The system-trust public-endpoint TLS test additionally runs
when `SPICEDB_TLS_INTEGRATION=1`; ordinary local tests explicitly skip that external
network test. All 19 example directories are reconciled by name by the Mage runner;
none are skipped. Materialize examples use a local stand-in because the integration
container supplies SpiceDB rather than a materialize deployment.
