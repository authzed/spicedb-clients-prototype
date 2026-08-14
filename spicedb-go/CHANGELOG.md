# Changelog

## Unreleased

### Breaking Changes

- **2026-08-14**: `ExpandResult.TreeRoot` (a leaked `*v1.PermissionRelationshipTree` proto type) is replaced with `ExpandResult.Tree`, a native `PermissionTree` (see `client/expand_tree.go`: `PermissionTree`, `IntermediateNode`, `LeafNode`, `ObjectRef`, `SubjectRef`, `TreeOperation`). No protobuf types are exposed from `ExpandPermissionTree` anymore.

### Features

- **2026-08-14**: Added automatic retry with exponential backoff for transient gRPC errors (`UNAVAILABLE`, `RESOURCE_EXHAUSTED`, `ABORTED`), configured via gRPC's built-in service-config `retryPolicy` in `NewClient`'s dial options (proto-client tier). Up to 3 retries (4 total attempts), 100ms initial backoff, 2x multiplier, 5s max backoff. No public API change; callers can still override via `WithDialOptions`.
- **2026-08-14**: RPC and stream errors are now mapped to native `*client.Error` values inspectable via `errors.Is`/`errors.As`, instead of raw `%w`-wrapped gRPC status errors. New sentinels: `client.ErrNotFound`, `client.ErrAlreadyExists`, `client.ErrInvalidArgument`, `client.ErrFailedPrecondition`, `client.ErrPermissionDenied`, `client.ErrUnauthenticated`. Applies to every RPC call and every `iter.Seq2` streaming iterator (`ReadRelationships`, `LookupResources`, `LookupSubjects`, `ExportRelationships`, `Updates`), so mid-stream errors are native too. `errors.Unwrap` still exposes the underlying gRPC status error for advanced inspection.
- **2026-08-14**: Per-item errors from `Check`/`CheckOne`/`CheckAny`/`CheckAll`/`CheckIter` (surfaced via `BulkCheckPermissions` response pairs) are now mapped to native `*client.Error` values through the same `mapGRPCError` path as top-level RPC errors, instead of being string-formatted. `errors.Is(err, client.ErrInvalidArgument)` (and the other sentinels) now works for per-item bulk-check failures, not just top-level RPC failures.

## 0.1.0 (2026-03-16)

Initial release of the idiomatic Go SpiceDB client.

### Features

- **2026-03-16**: Initial implementation of the idiomatic Go client.
  - `consistency` package: `Full()`, `MinLatency()`, `AtLeast()`, `Snapshot()` strategy constructors
  - `rel` package: `Relationship` struct, `Interface` trait, `FromTriple`/`MustFromTriple`/`FromTuple`/`FromObjects` constructors, `WithCaveat`/`WithExpiration` modifiers, `Filter` builder, `Txn` transaction builder with `Create`/`Touch`/`Delete`/`MustNotMatch`/`MustMatch`, `Update` type for watch events
  - `client` package: `NewPlaintext`/`NewSystemTLS`/`NewWithOpts` constructors, `Check`/`CheckOne`/`CheckAny`/`CheckAll`/`CheckIter` (all via BulkCheckPermissions), `Write`/`ReadRelationships`/`DeleteRelationships`, `LookupResources`/`LookupSubjects`, `ReadSchema`/`WriteSchema`, `Updates` (watch)
  - Examples: `check_permission`, `write_relationships`, `read_relationships`, `lookup_resources`, `lookup_subjects`, `watch_changes`, `schema_management`, `bulk_operations`
- **2026-03-16**: Added missing API methods for full non-deprecated coverage.
  - `client` package: `ReflectSchema`, `ComputablePermissions`, `DependentRelations`, `DiffSchema` (schema reflection), `ExpandPermissionTree`, `ImportRelationships`, `ExportRelationships` (bulk import/export), `RegisterRelationshipCounter`, `CountRelationships`, `UnregisterRelationshipCounter` (experimental counters)
  - New types: `SchemaDefinition`, `SchemaRelation`, `SchemaPermission`, `SchemaCaveat`, `SchemaCaveatParameter`, `ReflectSchemaResult`, `RelationReference`, `SchemaDiff`, `ExpandResult`, `CountResult`
  - Examples: `schema_reflection`, `relationship_counters`
- **2026-03-16**: Added transparent cursor-based pagination, batching, and sentinel errors.
  - `ReadRelationships`, `LookupResources`, `ExportRelationships` now auto-paginate with internal cursors (512-item pages); `LookupSubjects` uses a single streaming call (no cursor support in SpiceDB yet)
  - `DeleteRelationships` auto-pages in 10,000-item batches until all matching rels deleted
  - `CheckIter` now batches input relationships in chunks of 1,000 (instead of collecting all first)
  - `rel` package: added sentinel errors `ErrInvalidResource`, `ErrInvalidRelation`, `ErrInvalidSubject`
