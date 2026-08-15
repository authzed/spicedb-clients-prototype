# spicedb-ruby — Idiomatic Ruby Client Design

## Inherits

Root DESIGN.md (`../DESIGN.md`) — read it for the overall vision and backwards
compatibility mandate. This document takes precedence for Ruby-specific decisions.

## Language-Specific Goals

### Philosophy: Pit of Success

The API should make the correct thing easy and the wrong thing hard. Users
should fall into correct usage patterns by default. The API should feel like
hand-written, idiomatic Ruby — not a thin wrapper around gRPC.

### Gem & Module

- **Gem name**: `spicedb`
- **Top-level module**: `SpiceDB`
- **Minimum Ruby**: 3.2 (for `Data.define`)

### Module Structure

Six modules under `SpiceDB`:

- **`SpiceDB::Client`** — the client class and all SpiceDB operations
- **`SpiceDB::Consistency`** — module with factory methods for consistency modes
- **`SpiceDB::Relationship`** — `Data.define` value type for relationships
- **`SpiceDB::Filter`** — `Data.define` value type for relationship filters
- **`SpiceDB::Transaction`** — builder for batching relationship writes
- **`SpiceDB::Errors`** — exception hierarchy and gRPC error mapping

Types (`Relationship`, `Filter`, `Consistency`) are independent of `Client`.
Users can construct relationships and filters without requiring the client.

### Constructors

Security-obvious named constructors:

- `SpiceDB::Client.new_plaintext(endpoint, token)` — for testing, makes
  insecure connection obvious
- `SpiceDB::Client.new_system_tls(endpoint, token)` — for production
- Block form: `SpiceDB::Client.new_plaintext(...) { |client| ... }` — yields
  client and ensures cleanup

### Consistency

ZedTokens are opaque `String` values, never proto types. Consistency is an
**explicit required parameter** on every read operation — never silently
defaulted.

Module methods in `SpiceDB::Consistency`:
- `full` — fully consistent, least performant
- `min_latency` — SpiceDB's preferred revision, optimal performance
- `at_least(revision)` — read-after-write
- `snapshot(revision)` — exact revision
- `at_least_or_full(revision)` — AtLeast if revision present, Full otherwise
- `at_least_or_min_latency(revision)` — AtLeast if revision present, MinLatency otherwise

All write operations return a revision string.

### Relationships

Immutable `Data.define` value type:

```ruby
SpiceDB::Relationship = Data.define(
  :resource_type, :resource_id, :resource_relation,
  :subject_type, :subject_id, :subject_relation,
  :caveat_name, :caveat_context, :expiration
)
```

Constructors:
- `SpiceDB::Relationship.new(...)` — standard Data.define constructor
- `SpiceDB::Relationship.from_triple(...)` — from resource/subject triples
- `SpiceDB::Relationship.from_tuple(string)` — parse tuple string format

Immutable modifiers (return new instances):
- `r.with_caveat(name, context)` — returns new relationship with caveat
- `r.with_expiration(time)` — returns new relationship with expiration
- `r.to_filter` — returns a Filter matching this relationship's resource

### Checks

All checks use `BulkCheckPermissions` under the hood:
- `check_permission(consistency, permission, relationship)` → `Boolean`
- `check_permissions(consistency, permission, *relationships)` → `Array<Boolean>`
- `check_any(consistency, permission, *relationships)` → `Boolean`
- `check_all(consistency, permission, *relationships)` → `Boolean`

### Lookups

`lookup_resources`/`lookup_subjects` yield native result objects — never
bare ID strings — so callers can't accidentally treat a caveated or
wildcard-excluded result as a full grant. Mirrors spicedb-go's
`client/lookup_types.go`.

```ruby
SpiceDB::PartialCaveatInfo = Data.define(:missing_required_context)
SpiceDB::LookupResource    = Data.define(:resource_id, :permissionship, :partial_caveat)
SpiceDB::ResolvedSubject   = Data.define(:subject_id, :permissionship, :partial_caveat)
SpiceDB::LookupSubject     = Data.define(:subject, :excluded_subjects)
```

`permissionship` is a Symbol: `:unspecified`, `:has_permission`, or
`:conditional_permission`. `partial_caveat` is `nil` unless
`permissionship` is `:conditional_permission`, in which case it carries the
`missing_required_context` that must be supplied to fully evaluate the
grant.

- `lookup_resources(...)` → `Enumerator<SpiceDB::LookupResource>`
- `lookup_subjects(...)` → `Enumerator<SpiceDB::LookupSubject>`, where
  `LookupSubject#subject` is a `ResolvedSubject` and
  `LookupSubject#excluded_subjects` is an `Array<ResolvedSubject>`

Callers MUST check `permissionship` before treating a `LookupResource` as a
full grant. For `lookup_subjects`, when `subject.subject_id` is the
wildcard `"*"`, callers MUST check `excluded_subjects` before treating the
wildcard as a blanket grant — the server grants the permission to every
subject of the requested type EXCEPT those listed there.

### Streaming & Transparent Cursor Pagination

Ruby `Enumerator` for all streaming RPCs. **Cursors are fully internal** —
the caller sees a single Enumerator, and the client transparently re-fetches
pages using the cursor from each response. Default page sizes use sensible
defaults:

| Method | Default page size | Notes |
|--------|------------------|-------|
| `read_relationships` | 512 | cursor-based auto-pagination |
| `lookup_resources` | 512 | cursor-based auto-pagination |
| `lookup_subjects` | — | single streaming call |
| `export_relationships` | 512 | cursor-based auto-pagination |
| `delete_relationships` | 10,000 | auto-repeats until all deleted |
| `import_relationships` | 1,000 | batches into streaming sends |
| `updates` | — | server-streaming, no pagination |

Enumerators support `.lazy` for memory-efficient processing of large result sets.

### Writes

Transaction builder pattern:

```ruby
txn = SpiceDB::Transaction.new
txn.create(relationship)
txn.touch(relationship)
txn.delete(relationship)
txn.must_not_match(filter)  # precondition
txn.must_match(filter)      # precondition
revision = client.write(txn)
```

### Deletions

`delete_relationships` automatically pages through large result sets using a
limit of 10,000 per RPC call. It repeats until the server reports all matching
relationships are deleted. Returns the final revision.

### Error Handling

Exception hierarchy under `SpiceDB::Error`:
- `SpiceDB::PermissionDeniedError`
- `SpiceDB::NotFoundError`
- `SpiceDB::AlreadyExistsError`
- `SpiceDB::InvalidArgumentError`
- `SpiceDB::FailedPreconditionError`
- `SpiceDB::UnavailableError`
- `SpiceDB::CancelledError`
- `SpiceDB::DeadlineExceededError`
- `SpiceDB::ResourceExhaustedError`

Automatic retry with exponential backoff for transient gRPC errors
(UNAVAILABLE, RESOURCE_EXHAUSTED, ABORTED).

### Complete Method List

**Checks:**
- `check_permission(consistency, permission, relationship)` → `Boolean`
- `check_permissions(consistency, permission, *relationships)` → `Array<Boolean>`
- `check_any(consistency, permission, *relationships)` → `Boolean`
- `check_all(consistency, permission, *relationships)` → `Boolean`

**Relationships:**
- `write(transaction)` → `String` (revision)
- `read_relationships(consistency, filter)` → `Enumerator<Relationship>`
- `delete_relationships(filter)` → `String` (revision)

**Lookups:**
- `lookup_resources(consistency, resource_type, permission, subject_type, subject_id)` → `Enumerator<LookupResource>`
- `lookup_subjects(consistency, resource_type, resource_id, permission, subject_type)` → `Enumerator<LookupSubject>`

**Schema:**
- `read_schema` → `[String, String]` (schema, revision)
- `write_schema(schema)` → `String` (revision)
- `reflect_schema(consistency)` → `ReflectSchemaResult`
- `computable_permissions(consistency, definition_name, relation_name)` → `[Array<RelationReference>, String]`
- `dependent_relations(consistency, definition_name, permission_name)` → `[Array<RelationReference>, String]`
- `diff_schema(consistency, comparison_schema)` → `[Array<SchemaDiff>, String]`

**Expand:**
- `expand_permission_tree(consistency, resource_type, resource_id, permission)` → `ExpandResult`

**Bulk:**
- `import_relationships(enum)` → `Integer` (num_loaded)
- `export_relationships(consistency, filter = nil)` → `Enumerator<Relationship>`

**Watch:**
- `updates(object_types, start_revision: nil)` → `Enumerator<Update>`

**Experimental:**
- `experimental_register_relationship_counter(name, filter)` → `nil`
- `experimental_count_relationships(name)` → `CountResult`
- `experimental_unregister_relationship_counter(name)` → `nil`

### Escape Hatches

The proto client (`spicedb-proto` gem) is accessible via `client.proto_client`
for advanced use cases that need direct access to gRPC stubs.

## Public API Surface

See module sections above for the complete API manifest.

## Changelog

See [CHANGELOG.md](CHANGELOG.md) for release notes.
