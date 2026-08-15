# spicedb-rust -- Idiomatic Rust Client Design

## Inherits

Root DESIGN.md (`../DESIGN.md`) -- read it for the overall vision and backwards
compatibility mandate. This document takes precedence for Rust-specific decisions.

## Language-Specific Goals

### Philosophy: Pit of Success

The API should make the correct thing easy and the wrong thing hard. Users
should fall into correct usage patterns by default.

### Crate Structure

Single crate `spicedb` with four public modules:

- **`client`** -- the `SpiceDBClient` struct and all SpiceDB operations
- **`consistency`** -- strategy enum + factory functions for consistency modes
- **`types`** -- relationship types, filters, transactions, schema reflection types
- **`error`** -- `SpiceDBError` enum with thiserror

Types (`types`, `consistency`, `error`) are independent of `client`. Users can
construct relationships and filters without importing the client module.

### Constructors

Security-obvious named constructors:

- `SpiceDBClient::new_plaintext(endpoint, token)` -- for testing, makes insecure
  connection obvious
- `SpiceDBClient::new_system_tls(endpoint, token)` -- for production
- `SpiceDBClient::builder(endpoint, token).plaintext().build()` -- escape hatch
  with builder pattern

All constructors are `async` and return `Result<SpiceDBClient, SpiceDBError>`.
Endpoint and token parameters accept `impl Into<String>` for ergonomics.

### Consistency

ZedTokens are opaque `String` values, never proto types. Consistency is an
**explicit required parameter** on every read operation -- never silently defaulted.

Strategy is an `enum` in the `consistency` module with free function constructors:

- `full()` -- fully consistent, least performant
- `min_latency()` -- SpiceDB's preferred revision, optimal performance
- `at_least(revision)` -- read-after-write
- `snapshot(revision)` -- exact revision
- `at_least_or_full(option)` -- at_least if Some, full if None
- `at_least_or_min_latency(option)` -- at_least if Some, min_latency if None

All write operations return `Result<String, SpiceDBError>` where the `String` is
the revision (ZedToken).

### Relationships

Flat `Relationship` struct (not nested protos):

```rust
pub struct Relationship {
    pub resource_type: String,
    pub resource_id: String,
    pub resource_relation: String,
    pub subject_type: String,
    pub subject_id: String,
    pub subject_relation: String,
    pub caveat_name: String,
    pub caveat_context: Option<HashMap<String, serde_json::Value>>,
    pub expiration: Option<DateTime<Utc>>,
}
```

All types derive `Debug, Clone, PartialEq, Eq`.

Constructors: `Relationship::new()`, `from_objects()`, `from_tuple()`

Immutable modifiers: `.with_caveat()`, `.with_expiration()`, `.filter()`

### Ownership and Borrowing

Transaction methods take `&Relationship` (borrow, not move), so callers can
reuse relationship values. This is idiomatic Rust -- relationships are cloned
internally when needed for the transaction.

### Checks

All checks use `BulkCheckPermissions` under the hood:

- `check_permission(&self, cs, permission, &rel)` -- single check, returns
  `CheckResult` (marked `#[must_use]`)
- `check_permissions(&self, cs, permission, &[rel])` -- batch check
- `check_any(&self, cs, permission, &[rel])` -- returns true if any granted
- `check_all(&self, cs, permission, &[rel])` -- returns true if all granted

All permission parameters are `&str`, not `String`.

### Streaming and Transparent Cursor Pagination

Streaming operations return `impl Stream<Item = Result<T, SpiceDBError>>`.
**Cursors are fully internal** -- the caller sees a single stream, and the
client transparently re-fetches pages using the `AfterResultCursor` from each
response. Default page sizes use sensible defaults:

| Method | Default page size | Notes |
|--------|------------------|-------|
| `read_relationships` | 512 | cursor-based auto-pagination |
| `lookup_resources` | 512 | cursor-based auto-pagination |
| `lookup_subjects` | -- | no cursor support in SpiceDB yet; single streaming call |
| `export_relationships` | 512 | cursor-based auto-pagination |
| `delete_relationships` | 10,000 | auto-repeats until all matched rels deleted |
| `import_relationships` | 1,000 | batches into client-streaming sends |

`lookup_resources` and `lookup_subjects` yield native result structs (`LookupResource` /
`LookupSubject`), not bare IDs -- each carries `permissionship` (full grant vs. conditional
on caveat context) and, where applicable, `partial_caveat`. `LookupSubject` additionally
carries `excluded_subjects`: when `subject.subject_id` is the wildcard `"*"`, those excluded
subjects MUST be treated as NOT holding the permission even though the wildcard would
otherwise suggest a blanket grant.

### Writes

Transaction builder pattern:

```rust
let mut txn = Transaction::new();
txn.create(&relationship);
txn.touch(&relationship);
txn.delete(&relationship);
txn.must_not_match(filter); // precondition
let revision = client.write(&txn).await?;
```

### Deletions

`delete_relationships` automatically pages through large result sets using a
limit of 10,000 per RPC call. It repeats until the server reports all matching
relationships are deleted. Returns the final revision.

`delete_relationships_with(filter, &options)` adds optional preconditions and
a page-size override via a `DeleteOptions` builder:

```rust
let options = DeleteOptions::new()
    .with_must_match(filter_that_must_exist)
    .with_must_not_match(filter_that_must_not_exist)
    .with_limit(1_000);
let revision = client.delete_relationships_with(&filter, &options).await?;
```

`DeleteOptions::must_match`/`must_not_match` add `Precondition`s that guard the
delete: if a precondition fails, the server rejects that call and deletes
nothing for it. Preconditions are a per-request proto field, so when a delete
spans multiple pages, they are re-evaluated by the server on every page — pair
a precondition with a `DeleteOptions::with_limit` large enough to cover every
matching relationship in one call for single-shot, all-or-nothing semantics.
`delete_relationships(filter)` remains the ergonomic no-options path
(equivalent to `delete_relationships_with(filter, &DeleteOptions::default())`).

### Error Handling

- `SpiceDBError` enum with `thiserror` for all errors
- Variants: `PermissionDenied`, `NotFound`, `AlreadyExists`, `InvalidArgument`,
  `FailedPrecondition`, `Unavailable`, `Cancelled`, `Transport`, `Status`
- `from_grpc_status()` maps gRPC codes to variants
- `is_transient()` identifies retryable errors
- Validation errors in `types`: `RelationshipError` enum with `InvalidResource`,
  `InvalidSubject`, `InvalidFormat`

### Auto-Retry

Exponential backoff for transient gRPC errors (UNAVAILABLE, RESOURCE_EXHAUSTED,
ABORTED).

### Performance

- BulkCheck for all check operations (even single)
- Transparent cursor-based pagination with sensible default page sizes
- Batched deletions (10,000-item limit) to avoid server-side timeouts
- 1,000-item batching for import operations

### Dependencies

- `tonic` -- gRPC client
- `prost` -- protobuf types
- `tokio` -- async runtime
- `tokio-stream` -- Stream utilities
- `futures` -- Stream trait
- `thiserror` -- error derive macro
- `serde_json` -- caveat context serialization
- `chrono` -- timestamp/expiration handling

## Public API Surface

### `client` module

**Constructors:**
- `SpiceDBClient::new_plaintext(endpoint, token) -> Result<Self, SpiceDBError>`
- `SpiceDBClient::new_system_tls(endpoint, token) -> Result<Self, SpiceDBError>`
- `SpiceDBClient::builder(endpoint, token) -> SpiceDBClientBuilder`

**Checks:**
- `check_permission(&self, cs, permission, &rel) -> Result<CheckResult, SpiceDBError>`
- `check_permissions(&self, cs, permission, &[rel]) -> Result<Vec<bool>, SpiceDBError>`
- `check_any(&self, cs, permission, &[rel]) -> Result<bool, SpiceDBError>`
- `check_all(&self, cs, permission, &[rel]) -> Result<bool, SpiceDBError>`

**Relationships:**
- `write(&self, &txn) -> Result<String, SpiceDBError>`
- `read_relationships(&self, cs, &filter) -> impl Stream<Item = Result<Relationship, SpiceDBError>>`
- `delete_relationships(&self, &filter) -> Result<String, SpiceDBError>`
- `delete_relationships_with(&self, &filter, &DeleteOptions) -> Result<String, SpiceDBError>`

**Lookups:**
- `lookup_resources(&self, cs, resource_type, permission, subject_type, subject_id) -> impl Stream<Item = Result<LookupResource, SpiceDBError>>`
- `lookup_subjects(&self, cs, resource_type, resource_id, permission, subject_type) -> impl Stream<Item = Result<LookupSubject, SpiceDBError>>`
  -- `LookupSubject.excluded_subjects` MUST be checked whenever `LookupSubject.subject.subject_id`
  is the wildcard `"*"`: those subjects are explicitly excluded from the wildcard grant

**Schema:**
- `read_schema(&self) -> Result<(String, String), SpiceDBError>`
- `write_schema(&self, schema) -> Result<String, SpiceDBError>`
- `reflect_schema(&self, cs) -> Result<ReflectSchemaResult, SpiceDBError>`
- `computable_permissions(&self, cs, def_name, rel_name) -> Result<(Vec<RelationReference>, String), SpiceDBError>`
- `dependent_relations(&self, cs, def_name, perm_name) -> Result<(Vec<RelationReference>, String), SpiceDBError>`
- `diff_schema(&self, cs, comparison) -> Result<(Vec<SchemaDiff>, String), SpiceDBError>`

**Expand:**
- `expand_permission_tree(&self, cs, resource_type, resource_id, permission) -> Result<ExpandResult, SpiceDBError>`

**Bulk:**
- `import_relationships(&self, rels) -> Result<u64, SpiceDBError>`
- `export_relationships(&self, cs, filter) -> impl Stream<Item = Result<Relationship, SpiceDBError>>`

**Watch:**
- `updates(&self, object_types, start_revision) -> impl Stream<Item = Result<Update, SpiceDBError>>`

**Experimental:**
- `experimental_register_relationship_counter(&self, name, &filter) -> Result<(), SpiceDBError>`
- `experimental_count_relationships(&self, name) -> Result<Option<CountResult>, SpiceDBError>`
- `experimental_unregister_relationship_counter(&self, name) -> Result<(), SpiceDBError>`

### `consistency` module

- `full() -> Strategy`
- `min_latency() -> Strategy`
- `at_least(revision) -> Strategy`
- `snapshot(revision) -> Strategy`
- `at_least_or_full(option) -> Strategy`
- `at_least_or_min_latency(option) -> Strategy`

### `types` module

- `Relationship` struct + constructors + modifiers
- `RelationshipError` enum
- `Filter` struct + builder methods
- `Transaction` struct + `create`/`touch`/`delete`/`must_not_match`/`must_match`
- `Precondition`, `PreconditionOperation`
- `DeleteOptions` struct (`must_match`, `must_not_match`, `limit`) + `with_must_match`/`with_must_not_match`/`with_limit` builder methods -- used by `delete_relationships_with`
- `Update`, `UpdateOperation`
- `CheckResult` (`#[must_use]`)
- `Permissionship` (`Unspecified` / `HasPermission` / `ConditionalPermission`), `PartialCaveatInfo`
- `LookupResource` (result of `lookup_resources`)
- `ResolvedSubject`, `LookupSubject` (results of `lookup_subjects`; `LookupSubject.excluded_subjects`
  carries wildcard exclusions -- see Lookups above)
- `SchemaDefinition`, `SchemaRelation`, `SchemaPermission`
- `SchemaCaveat`, `SchemaCaveatParameter`
- `ReflectSchemaResult`, `RelationReference`, `SchemaDiff`
- `ExpandResult`, `CountResult`

### `error` module

- `SpiceDBError` enum
- `from_grpc_status(code, message) -> SpiceDBError`
- `is_transient(&SpiceDBError) -> bool`

## Examples Manifest

(To be added when examples are implemented)

| Directory | Demonstrates |
|-----------|-------------|
| `check_permission/` | Basic permission check |
| `write_relationships/` | Writing relationships with transaction builder |
| `read_relationships/` | Reading relationships with stream |
| `lookup_resources/` | Finding resources a subject can access |
| `lookup_subjects/` | Finding subjects with access to a resource |
| `watch_changes/` | Watching for relationship changes |
| `schema_management/` | Reading and writing schema |
| `bulk_operations/` | Bulk checks and imports |
| `schema_reflection/` | Schema reflection, computable permissions, dependent relations, diff |
| `relationship_counters/` | Registering, reading, and unregistering relationship counters |

## Changelog

See [CHANGELOG.md](CHANGELOG.md) for release notes.
