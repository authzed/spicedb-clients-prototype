# spicedb-go — Idiomatic Go Client Design

## Inherits

Root DESIGN.md (`../DESIGN.md`) — read it for the overall vision and backwards
compatibility mandate. This document takes precedence for Go-specific decisions.

## Language-Specific Goals

### Philosophy: Pit of Success

The API should make the correct thing easy and the wrong thing hard. Users
should fall into correct usage patterns by default.

### Package Structure

Four packages:

- **`spicedb`** (root) — doc-only package, godoc entry point
- **`client`** — the `Client` struct and all SpiceDB operations
- **`consistency`** — strategy constructors for consistency modes
- **`rel`** — relationship types, filters, transactions, parsing

Types (`rel`, `consistency`) are independent of `client`. Users can construct
relationships and filters without importing the client.

### Constructors

Security-obvious named constructors:

- `client.NewPlaintext(endpoint, presharedKey string) (*Client, error)` — for
  testing, makes insecure connection obvious
- `client.NewSystemTLS(endpoint, presharedKey string) (*Client, error)` — for
  production
- `client.NewWithOpts(endpoint string, opts ...Option) (*Client, error)` —
  escape hatch with functional options

### Consistency

ZedTokens are opaque `string` values, never proto types. Consistency is an
**explicit required parameter** on every read operation — never silently
defaulted.

Named constructors in the `consistency` package:
- `Full()` — fully consistent, least performant
- `MinLatency()` — SpiceDB's preferred revision, optimal performance
- `AtLeast(revision string)` — read-after-write
- `Snapshot(revision string)` — exact revision

All write operations return `(revision string, error)`.

### Relationships

Flat `rel.Relationship` struct (not nested protos):

```go
type Relationship struct {
    ResourceType, ResourceID, ResourceRelation string
    SubjectType, SubjectID, SubjectRelation    string
    CaveatName    string
    CaveatContext map[string]any
    Expiration    *time.Time
}
```

`rel.Interface` trait for user-defined domain types:

```go
type Interface interface { Relationship() Relationship }
```

Constructors: `FromTriple()`, `MustFromTriple()`, `FromTuple()`, `FromObjects()`

Immutable modifiers: `r.WithCaveat()`, `r.WithExpiration()`, `r.Filter()`

### Checks

All checks use `BulkCheckPermissions` under the hood:
- `Check(ctx, cs, rs...) ([]bool, error)`
- `CheckOne(ctx, cs, r) (bool, error)`
- `CheckAny(ctx, cs, rs...) (bool, error)`
- `CheckAll(ctx, cs, rs...) (bool, error)`
- `CheckIter(ctx, cs, iter) iter.Seq2[bool, error]` — auto-batches in chunks
  of 1000

### Streaming & Transparent Cursor Pagination

Go 1.23+ `iter.Seq2` for all streaming RPCs. **Cursors are fully internal** —
the caller sees a single iterator, and the client transparently re-fetches
pages using the `AfterResultCursor` from each response. Default page sizes
use sensible defaults:

| Method | Default page size | Notes |
|--------|------------------|-------|
| `ReadRelationships` | 512 | cursor-based auto-pagination |
| `LookupResources` | 512 | cursor-based auto-pagination |
| `LookupSubjects` | — | no cursor support in SpiceDB yet; single streaming call |
| `ExportRelationships` | 512 | cursor-based auto-pagination |
| `DeleteRelationships` | 10,000 | auto-repeats until all matched rels deleted |
| `CheckIter` | 1,000 | batches input rels into bulk check calls |
| `ImportRelationships` | 1,000 | batches into client-streaming sends |
| `Updates` | — | server-streaming, no pagination needed |

Iterators:
- `ReadRelationships(...)` → `iter.Seq2[rel.Relationship, error]`
- `LookupResources(...)` → `iter.Seq2[string, error]`
- `LookupSubjects(...)` → `iter.Seq2[string, error]`
- `ExportRelationships(...)` → `iter.Seq2[rel.Relationship, error]`
- `Updates(...)` → `iter.Seq2[rel.Update, error]`

### Writes

Transaction builder pattern:

```go
var txn rel.Txn
txn.Create(relationship)
txn.Touch(relationship)
txn.Delete(relationship)
txn.MustNotMatch(filter) // precondition
revision, err := client.Write(ctx, txn)
```

### Deletions

`DeleteRelationships` automatically pages through large result sets using a
limit of 10,000 per RPC call. It repeats until the server reports all matching
relationships are deleted. Returns the final revision.

### Testing

Use `github.com/stretchr/testify/require` for all assertions in tests and
examples.

### Error Handling

- Standard Go `(result, error)` returns
- Sentinel errors in `rel` package:
  - `ErrInvalidResource` — resource type, ID, or relation is empty
  - `ErrInvalidRelation` — relation string is empty
  - `ErrInvalidSubject` — subject type or ID is empty
- `Must*` variants that panic (for tests/initialization)
- Automatic retry with exponential backoff for transient gRPC errors

### Performance

- S2 compression by default
- BulkCheck for all check operations (even single)
- Transparent cursor-based pagination with sensible default page sizes
- Batched deletions (10,000-item limit) to avoid server-side timeouts

### Escape Hatches

Proto fields are semi-exposed on builder types (`Txn.V1Updates`,
`Filter.V1Filter`, `Strategy.V1Consistency`) for advanced use cases.

## Public API Surface

See package sections above for the complete API manifest.

## Examples Manifest

| Directory | Demonstrates |
|-----------|-------------|
| `check_permission/` | Basic permission check with CheckOne |
| `write_relationships/` | Writing relationships with transaction builder |
| `read_relationships/` | Reading relationships with iterator |
| `lookup_resources/` | Finding resources a subject can access |
| `lookup_subjects/` | Finding subjects with access to a resource |
| `watch_changes/` | Watching for relationship changes |
| `schema_management/` | Reading and writing schema |
| `bulk_operations/` | Bulk checks and imports |
| `schema_reflection/` | Schema reflection, computable permissions, dependent relations, diff |
| `relationship_counters/` | Registering, reading, and unregistering relationship counters |

## Changelog

<!-- Claude appends here when making changes, with date + what changed -->

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
