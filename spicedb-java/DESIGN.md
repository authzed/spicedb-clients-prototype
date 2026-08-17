# spicedb-java — Idiomatic Java Client Design

## Inherits

Root DESIGN.md (`../DESIGN.md`) — read it for the overall vision and backwards
compatibility mandate. This document takes precedence for Java-specific decisions.

## Language-Specific Goals

### Philosophy: Pit of Success

The API should make the correct thing easy and the wrong thing hard. Users
should fall into correct usage patterns by default. Java idioms (records,
sealed types, streams, try-with-resources) should be used throughout.

### Package Structure

Single package: `com.authzed.spicedb`

- **`SpiceDBClient`** — the client and all SpiceDB operations (AutoCloseable)
- **`CheckResult`** — native result record for `checkPermission`/`checkPermissions`
- **`LookupResult`** — native result records for `lookupResources`/`lookupSubjects`, and the shared `Permissionship` enum used by both the check and lookup surfaces
- **`Relationship`** — Java record for relationships
- **`Filter`** — immutable filter builder
- **`Transaction`** — builder for batching writes
- **`Consistency`** — static factory methods for consistency strategies
- **`errors/`** — unchecked exception hierarchy and gRPC error mapper

### Constructors

Security-obvious static factory methods:

- `SpiceDBClient.createPlaintext(endpoint, presharedKey)` — for testing,
  makes insecure connection obvious
- `SpiceDBClient.createSystemTls(endpoint, presharedKey)` — for production
- `SpiceDBClient.create(endpoint, presharedKey, options...)` — escape hatch

The client implements `AutoCloseable` for use with try-with-resources:
```java
try (var client = SpiceDBClient.createPlaintext("localhost:50051", "test")) {
    // ...
}
```

### Consistency

ZedTokens are opaque `String` values, never proto types. Consistency is an
**explicit required parameter** on every read operation — never silently
defaulted.

Static factory methods on `Consistency`:
- `Consistency.full()` — fully consistent, least performant
- `Consistency.minLatency()` — SpiceDB's preferred revision, optimal performance
- `Consistency.atLeast(revision)` — read-after-write
- `Consistency.snapshot(revision)` — exact revision
- `Consistency.atLeastOrFull(revision)` — AtLeast if non-empty, Full otherwise
- `Consistency.atLeastOrMinLatency(revision)` — AtLeast if non-empty, MinLatency otherwise

All write operations return a `String` revision.

### Relationships

Java record with flat fields (not nested protos):

```java
public record Relationship(
    String resourceType,
    String resourceID,
    String resourceRelation,
    String subjectType,
    String subjectID,
    String subjectRelation,
    String caveatName,
    Map<String, Object> caveatContext,
    Instant expiration
) { }
```

Constructors: `Relationship.of(...)`, `Relationship.fromTuple(String)`

Immutable modifiers: `withCaveat(...)`, `withExpiration(...)`, `toFilter()`

### Checks

All checks use `BulkCheckPermissions` under the hood:
- `checkPermission(consistency, permission, relationship)` → `CheckResult`
- `checkPermissions(consistency, permission, relationships...)` → `List<CheckResult>`
- `checkAny(consistency, permission, relationships...)` → `boolean`
- `checkAll(consistency, permission, relationships...)` → `boolean`

`CheckResult` (a record, mirroring `LookupResult`'s shape) carries the server's full answer instead
of collapsing it to a `boolean`:

```java
public record CheckResult(
    LookupResult.Permissionship permissionship, List<String> missingContext, String checkedAt) {
  public boolean hasPermission() { ... } // true ONLY for HAS_PERMISSION
}
```

`LookupResult.Permissionship` is shared by both the check and lookup surfaces and has four values:
`UNSPECIFIED`, `NO_PERMISSION`, `HAS_PERMISSION`, `CONDITIONAL_PERMISSION`. Lookups never yield
`NO_PERMISSION` — a subject/resource pair lacking the permission is simply absent from a lookup
stream, whereas a check is always answering a yes/no/conditional question about one specific pair.

**RULE (root DESIGN.md, "Only an unconditional grant is true"):** only `HAS_PERMISSION` is a grant.
`checkPermission`/`checkPermissions` callers MUST use `CheckResult.hasPermission()` (a single
equality comparison against `HAS_PERMISSION`, never a disjunction) rather than treating any other
value — especially `CONDITIONAL_PERMISSION` — as authorized. `checkAny`/`checkAll` enforce this
internally: they count only `hasPermission()` results, so a conditional never contributes to a
`true`. `if (result)` does not compile in Java, so `CheckResult` is safe by construction — there is
no bare-boolean coercion to guard against in documentation, unlike Ruby/TypeScript.

`checkPermission` and `checkPermissions` always call `CheckBulkPermissions` — there is no
production call site for the non-bulk `CheckPermission` RPC (matches Go/Python/Ruby/C#).
`CheckBulkPermissionsResponseItem` carries no per-item `checked_at` of its own; the single
response-level token is propagated onto every `CheckResult` in the batch. A per-item error in a
`CheckBulkPermissionsPair` is routed through `ErrorMapper` (using the item's own gRPC code) so
callers get the specific typed exception (e.g. `PermissionDeniedException`), with the item's index
preserved in the message (`"check item %d: ..."`, matching `spicedb-go`).

### Streaming & Transparent Cursor Pagination

`Stream<T>` (AutoCloseable) for all streaming RPCs. **Cursors are fully
internal** — the caller sees a single stream, and the client transparently
re-fetches pages using the `AfterResultCursor` from each response.

| Method | Default page size | Notes |
|--------|------------------|-------|
| `readRelationships` | 512 | cursor-based auto-pagination |
| `lookupResources` | 512 | cursor-based auto-pagination |
| `lookupSubjects` | — | single streaming call |
| `exportRelationships` | 512 | cursor-based auto-pagination |
| `deleteRelationships` | 1,000 | auto-repeats until all deleted; matches SpiceDB's default `--max-delete-relationships-limit` |
| `importRelationships` | 1,000 | batches into streaming sends |
| `updates` | — | server-streaming, no pagination needed |

### Writes

Transaction builder pattern:

```java
var txn = new Transaction();
txn.create(relationship);
txn.touch(relationship);
txn.delete(relationship);
txn.mustNotMatch(filter); // precondition
String revision = client.write(txn);
```

### Deletions

`deleteRelationships` automatically pages through large result sets using a
limit of 1,000 per RPC call (matches SpiceDB's default
`--max-delete-relationships-limit`, so the default works against a stock
server). It repeats until the server reports all matching relationships are
deleted. Returns the final revision.

`deleteRelationships(Filter, DeleteOptions)` additionally accepts optional
MUST_MATCH/MUST_NOT_MATCH preconditions and a per-request page-size override,
mirroring `spicedb-go`'s `WithDeleteMustMatch`/`WithDeleteMustNotMatch`/
`WithDeleteLimit`. `DeleteOptions` is an immutable record with `Filter`-style
`withMustMatch`/`withMustNotMatch`/`withLimit` builder methods; start from
`DeleteOptions.none()`, which reproduces the single-argument overload's
behavior exactly. Preconditions are re-evaluated by the server on every page
of a multi-page delete — pair them with `withLimit` for a single-shot,
all-or-nothing guarded delete.

### Error Handling

Unchecked exceptions extending `RuntimeException`:
- `SpiceDBException` — base class
- `PermissionDeniedException`
- `NotFoundException`
- `AlreadyExistsException`
- `InvalidArgumentException`

`ErrorMapper.toSpiceDBException(StatusRuntimeException)` maps gRPC status codes
to typed exceptions. Transient errors (UNAVAILABLE, RESOURCE_EXHAUSTED, ABORTED)
are retried with exponential backoff.

### Performance

- BulkCheck for all check operations (even single)
- Transparent cursor-based pagination with sensible default page sizes
- Batched deletions (1,000-item limit, matching SpiceDB's default `--max-delete-relationships-limit`) to avoid server-side timeouts
- Batched imports (1,000-item chunks)
- Exponential backoff retry for transient gRPC errors

### Experimental APIs

Methods from SpiceDB's `ExperimentalService` are clearly annotated:
- `experimentalRegisterRelationshipCounter`
- `experimentalCountRelationships`
- `experimentalUnregisterRelationshipCounter`

These may change without following the backwards compatibility mandate.

## Public API Surface

### SpiceDBClient

**Constructors:**
- `createPlaintext(String endpoint, String presharedKey)`
- `createSystemTls(String endpoint, String presharedKey)`
- `create(String endpoint, String presharedKey, ClientOption... options)`

**Checks:**
- `checkPermission(Consistency, String permission, Relationship)` → `CheckResult`
- `checkPermissions(Consistency, String permission, Relationship...)` → `List<CheckResult>`
- `checkAny(Consistency, String permission, Relationship...)` → `boolean`
- `checkAll(Consistency, String permission, Relationship...)` → `boolean`

**Relationships:**
- `write(Transaction)` → `String` (revision)
- `readRelationships(Consistency, Filter)` → `Stream<Relationship>`
- `deleteRelationships(Filter)` → `String` (revision)
- `deleteRelationships(Filter, DeleteOptions)` → `String` (revision)

**Lookups:**
- `lookupResources(Consistency, String resourceType, String permission, String subjectType, String subjectID)` → `Stream<LookupResult.LookupResource>` (each result now also carries `lookedUpAt`, the revision it was computed at)
- `lookupSubjects(Consistency, String resourceType, String resourceID, String permission, String subjectType)` → `Stream<LookupResult.LookupSubject>` (each result now also carries `lookedUpAt`)

**Schema:**
- `readSchema()` → `SchemaResult` (schema + revision)
- `writeSchema(String schema)` → `String` (revision)
- `reflectSchema(Consistency)` → `ReflectSchemaResult`
- `computablePermissions(Consistency, String definitionName, String relationName)` → `ComputablePermissionsResult`
- `dependentRelations(Consistency, String definitionName, String permissionName)` → `DependentRelationsResult`
- `diffSchema(Consistency, String comparisonSchema)` → `DiffSchemaResult`

**Expand:**
- `expandPermissionTree(Consistency, String resourceType, String resourceID, String permission)` → `ExpandResult`

**Bulk:**
- `importRelationships(Iterable<Relationship>)` → `long` (numLoaded)
- `exportRelationships(Consistency, Filter)` → `Stream<Relationship>`

**Watch:**
- `updates(List<String> objectTypes, String startRevision)` → `Stream<Update>`

**Experimental:**
- `experimentalRegisterRelationshipCounter(String name, Filter)`
- `experimentalCountRelationships(String name)` → `CountResult`
- `experimentalUnregisterRelationshipCounter(String name)`

## Examples Manifest

| Directory | Demonstrates |
|-----------|-------------|
| `check_permission/` | Basic permission check with checkPermission |
| `conditional_check/` | `CONDITIONAL_PERMISSION` against a live caveated relationship whose context was never supplied — `hasPermission()` must be false |
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
