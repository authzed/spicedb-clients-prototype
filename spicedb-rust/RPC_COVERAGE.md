# Native RPC coverage — 2026-10-26 reset

All methods take one owned native request. All unary options accept a total
operation timeout; streams/import accept an optional total lifetime timeout.
Every listed wrapper uses the authenticated existing transport. Protocol types
are confined to conversion and the documented `raw_proto()` escape hatch.

| Service / RPC | Rust method |
| --- | --- |
| Permissions / CheckPermission | `check(PermissionCheck)` |
| Permissions / CheckBulkPermissions | `check_bulk(BulkCheckRequest)` |
| Permissions / WriteRelationships | `write(WriteRequest)` |
| Permissions / ReadRelationships | `read_relationships(ReadRelationshipsRequest)` |
| Permissions / DeleteRelationships | `delete_relationships(DeleteRequest)`; `delete_all_relationships_non_atomic(DeleteAllRequest)` |
| Permissions / LookupResources | `lookup_resources(LookupResourcesRequest)` |
| Permissions / LookupSubjects | `lookup_subjects(LookupSubjectsRequest)` |
| Permissions / ExpandPermissionTree | `expand_permission_tree(ExpandPermissionTreeRequest)` |
| Permissions / ImportBulkRelationships | `import_relationships(ImportRelationshipsRequest)` |
| Permissions / ExportBulkRelationships | `export_relationships(ExportRelationshipsRequest)` |
| Schema / ReadSchema | `read_schema(ReadSchemaRequest)` |
| Schema / WriteSchema | `write_schema(WriteSchemaRequest)` |
| Schema / ReflectSchema | `reflect_schema(ReflectSchemaRequest)` |
| Schema / ComputablePermissions | `computable_permissions(ComputablePermissionsRequest)` |
| Schema / DependentRelations | `dependent_relations(DependentRelationsRequest)` |
| Schema / DiffSchema | `diff_schema(DiffSchemaRequest)` |
| Watch / Watch | `updates(UpdatesRequest)` |
| Experimental / ExperimentalRegisterRelationshipCounter | `experimental_register_relationship_counter(ExperimentalRegisterRelationshipCounterRequest)` |
| Experimental / ExperimentalCountRelationships | `experimental_count_relationships(ExperimentalCountRelationshipsRequest)` |
| Experimental / ExperimentalUnregisterRelationshipCounter | `experimental_unregister_relationship_counter(ExperimentalUnregisterRelationshipCounterRequest)` |
| Materialize RoaringLookupResources / ExperimentalRoaringLookupResources | `experimental_roaring_lookup_resources(ExperimentalRoaringLookupResourcesRequest)` |
| Materialize Relationships / ExperimentalCountRelationshipsByFilter | `experimental_count_relationships_by_filter(ExperimentalCountRelationshipsByFilterRequest)` |
| Materialize WatchPermissionSets / WatchPermissionSets | `watch_permission_sets(WatchPermissionSetsRequest)` |
| Materialize WatchPermissionSets / LookupPermissionSets | `lookup_permission_sets(LookupPermissionSetsRequest)` |
| Materialize WatchPermissionSets / DownloadPermissionSets | `download_permission_sets(DownloadPermissionSetsRequest)` |

Deprecated experimental equivalents are raw-only. Deprecated Materialize
WatchPermissions is not introduced into the native API. Generated protocol
source remains generated; the handwritten transport adds cloned authenticated
handles for the Materialize Relationships and WatchPermissionSets services.

Cursor paging pins response revisions for reads/lookups; export cursors carry
server snapshot semantics. LookupSubjects has no invented cursor pagination.
Permission-set lookup is an explicitly bounded page with the complete native
cursor for caller-controlled resume. Deletion is non-atomic across pages and
only acknowledged pages contribute to `DeleteProgress`; unacknowledged outcomes
can be ambiguous. Dropping an async future cannot return progress, so resumable
cancellation uses bounded calls and persists each returned result.

TLS uses tonic's native trust store. Custom CA injection and mTLS are not
provided by this builder. Non-loopback plaintext credentials require the
separate `remote_credentials_policy(RemoteCredentialsPolicy::AllowInsecureRemote)` opt-in.
