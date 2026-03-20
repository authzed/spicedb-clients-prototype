# Changelog

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
