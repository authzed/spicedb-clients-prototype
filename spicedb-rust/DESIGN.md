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

**TLS roots.** `new_system_tls` uses tonic's `tls-native-roots` feature and calls
`ClientTlsConfig::new().with_native_roots()`, so the OS trust store is read at runtime,
satisfying the root `DESIGN.md` rule *"A system-TLS constructor must reach a real
server."* Each sibling client delegates to its own ecosystem's default trust source,
which is not the same source everywhere: the OS store for Go
(`credentials.NewTLS(nil)`) and C# (`ChannelCredentials.SecureSsl`), the JDK's
`cacerts` for Java (`useTransportSecurity()`), gRPC C-core's compiled-in `roots.pem`
for Python and Ruby, and Node's bundled Mozilla roots for TypeScript. All of those
satisfy the rule's clause 1 — the clause forbids a client library supplying its own
roots, not delegating to the runtime's.

The trade-off in this client is therefore accepted deliberately and is not universal: a
`FROM scratch` image with no OS trust store will fail here, whereas a distroless Python
or Node image connects fine on its compiled-in bundle. A caller-supplied CA bundle is
the general remedy and is tracked separately.

Do **not** substitute `ClientTlsConfig::with_enabled_roots()`. Its body begins
`let config = ClientTlsConfig::new()`, discarding `self`, so chaining it after
`.domain(...)` or any other builder call silently drops that configuration.

No test can assert the tonic feature is enabled — `cfg!(feature = ...)` reads the
*current* crate's features, and `tls-native-roots` belongs to `tonic`. None is needed:
`with_native_roots` is itself `#[cfg(feature = "tls-native-roots")]`, so removing the
feature makes this crate fail to compile.

**Running the handshake test.** `test_system_tls_completes_real_handshake`
(`tests/client_test.rs`) is gated behind the `SPICEDB_TLS_INTEGRATION` environment
variable and returns early unless it is set, so a plain `cargo test` skips it. Run it
locally with:

```console
SPICEDB_TLS_INTEGRATION=1 cargo test --test client_test test_system_tls_completes_real_handshake
```

CI runs it in the `unit` job's "TLS handshake test (requires network)" step in
`.github/workflows/rust.yaml`, which greps the output for a passing test so that
renaming or deleting the test fails the step rather than silently running nothing.

The env-var gate is used instead of this repo's usual `#[ignore]` + `mage
integrationTest` convention (`magefile.go`, which runs `cargo test -- --ignored`)
because this test needs a real endpoint on the public internet, not the dockerised local
SpiceDB that `integrationTest` starts — a local server's self-signed certificate is
exactly what the platform trust store under test would, correctly, reject.

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
    pub check_context: Option<HashMap<String, serde_json::Value>>,
}
```

All types derive `Debug, Clone, PartialEq, Eq`.

`check_context` is per-item caveat context used only when this relationship
is passed to a check call (`check_permission_with_context`/
`check_permissions_with_context`/`check_any_with_context`/
`check_all_with_context`) -- see "Checks" below. It is a **different
concept** from `caveat_context`, which is stored with the relationship as
part of a write and supplies values for the caveat baked into that specific
tuple: `check_context` is never sent on a write (`Relationship::to_proto`
does not reference it) and instead supplies values for evaluating whatever
caveat a permission check encounters at check time. Keeping them on separate
fields prevents a check-time value from silently leaking into a write and
altering a stored relationship's caveat context.

Constructors: `Relationship::new()`, `from_objects()`, `from_tuple()`

Immutable modifiers: `.with_caveat()`, `.with_expiration()`, `.with_check_context()`, `.filter()`

### Ownership and Borrowing

Transaction methods take `&Relationship` (borrow, not move), so callers can
reuse relationship values. This is idiomatic Rust -- relationships are cloned
internally when needed for the transaction.

### Checks

All checks use `BulkCheckPermissions` under the hood:

- `check_permission(&self, cs, permission, &rel)` -- single check, returns
  `CheckResult` (marked `#[must_use]`)
- `check_permissions(&self, cs, permission, &[rel])` -- batch check, returns
  `Vec<CheckResult>` (one per input relationship, same order)
- `check_any(&self, cs, permission, &[rel])` -- returns `true` if any
  relationship's result has `has_permission() == true`
- `check_all(&self, cs, permission, &[rel])` -- returns `true` if every
  relationship's result has `has_permission() == true`

All permission parameters are `&str`, not `String`.

#### Caveat context on the check surface

`CheckPermissionRequest.context` (proto field 5) and
`CheckBulkPermissionsRequestItem.context` (proto field 4) are the wire
locations for check-time caveat context -- values SpiceDB needs to evaluate a
caveat expression encountered during a check (e.g. `"now"`). Without it, a
caveated match comes back as `Permissionship::ConditionalPermission` instead
of a grant, and `CheckResult::missing_context` names what was needed.
`CheckBulkPermissionsRequest` itself has **no** context field, so a
call-level default is fanned out onto every item at request-build time.

Rust has no default arguments and no overloading, so adding a `context`
parameter to `check_permission`/`check_permissions`/`check_any`/`check_all`
directly would break every existing call site. Following this client's
existing convention for optional call-shaped parameters (an explicit
`Option<...>` parameter, as `export_relationships`'s `filter: Option<&Filter>`
already does), each gained a parallel `_with_context` method instead --
mirroring the shape Go needed for the same reason (Go permits only one
trailing variadic):

- `check_permission_with_context(&self, cs, permission, &rel, context: Option<&HashMap<String, serde_json::Value>>)`
- `check_permissions_with_context(&self, cs, permission, &[rel], context: Option<&HashMap<String, serde_json::Value>>)`
- `check_any_with_context(&self, cs, permission, &[rel], context: Option<&HashMap<String, serde_json::Value>>)`
- `check_all_with_context(&self, cs, permission, &[rel], context: Option<&HashMap<String, serde_json::Value>>)`

`context` is the call-level default applied to every relationship in the
call. Per-item context is supplied by building the relationship with
`Relationship::with_check_context(context)` beforehand -- distinct from
`with_caveat`'s write-time context (see "Relationships" above). The two are
merged **key by key, item wins**: the item's keys override matching
call-level keys, and any call-level keys the item doesn't specify are
retained. For example, a call-level `{now: 42, region: "us"}` plus a
per-item `{region: "eu"}` sends `{now: 42, region: "eu"}` for that item,
while a sibling item with no per-item context still gets the untouched
call-level default `{now: 42, region: "us"}`. Wholesale replacement (an item
supplying one key silently dropping every call-level key it didn't mention)
is deliberately not the behavior -- it would make a caveat evaluation fail
for context the caller thought it had already supplied at the call level.
When neither a call-level nor a per-item context applies to a given item, no
`context` field is set on that item's wire request (`None`, not an empty
`Struct`).

The non-context methods (`check_permission`, `check_permissions`,
`check_any`, `check_all`) are unchanged and delegate to their
`_with_context` counterpart with `context: None` -- no existing call site
changed.

`CheckResult` carries the server's three-valued (four with `Unspecified`)
answer, not a bare bool:

```rust
pub struct CheckResult {
    pub permissionship: Permissionship,
    pub missing_context: Vec<String>,
    pub checked_at: String,
}

impl CheckResult {
    pub fn has_permission(&self) -> bool { /* true only for Permissionship::HasPermission */ }
}
```

A `ConditionalPermission` result means the server needed caveat context that
was not supplied at check time -- it is **not** a grant, and
`has_permission()` is `false` for it. This distinguishes "denied" from "you
forgot to pass context," which a bare bool collapsed. `missing_context` lists
the caveat parameter names the server needed (from
`partial_caveat_info.missing_required_context`) and is only non-empty for a
`ConditionalPermission` result. `checked_at` is the revision (ZedToken) the
check was evaluated at -- thread it into `consistency::at_least` for
read-your-writes on a subsequent read.

`check_any`/`check_all` deliberately gate on `has_permission()`, not on
`permissionship != Unspecified`/truthiness -- a `ConditionalPermission` result
does **not** count as granted for either. This is fail-closed by design: an
unevaluated caveat should never silently widen a bulk any/all check into a
grant.

`Permissionship` serves both the check surface (`CheckResult`) and the lookup
surface (`LookupResource`, `ResolvedSubject`):

```rust
pub enum Permissionship {
    Unspecified,
    NoPermission,
    HasPermission,
    ConditionalPermission,
}
```

Lookups never yield `NoPermission` -- a resource/subject that lacks the
permission is simply absent from a lookup stream rather than yielded with
that permissionship. `NoPermission` only appears on `CheckResult`, where the
server is answering a yes/no question about one specific pair.

`check_permission` and `check_permissions` both route through
`BulkCheckPermissions` (there is no separate call site for the single-item
`CheckPermission` RPC in this client); a per-item error in the batch response
is mapped through the same `error::from_grpc_status` used by every other RPC
in this client, so a per-item `PERMISSION_DENIED` surfaces as
`SpiceDBError::PermissionDenied`, not a generic fallback.
`CheckBulkPermissionsResponse.checked_at` is one token for the whole
response (not per-item), so every `CheckResult` in a batch call shares the
same `checked_at`.

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
| `delete_relationships` | 1,000 | auto-repeats until all matched rels deleted; matches SpiceDB's default `--max-delete-relationships-limit` |
| `import_relationships` | 1,000 | batches into client-streaming sends |

`lookup_resources` and `lookup_subjects` yield native result structs (`LookupResource` /
`LookupSubject`), not bare IDs -- each carries `permissionship` (full grant vs. conditional
on caveat context), where applicable `partial_caveat`, and `looked_up_at` (the revision
the result was computed at -- identical for every item in a single call, since it's a
property of the call rather than of the individual resource/subject; thread it into
`consistency::at_least` for read-your-writes). `LookupSubject` additionally
carries `excluded_subjects`: when `subject.subject_id` is the wildcard `"*"`, those excluded
subjects MUST be treated as NOT holding the permission even though the wildcard would
otherwise suggest a blanket grant.

### Writes

Every write RPC that has a `ZedToken` in its proto response already returns
it as the revision: `write` (`WriteRelationshipsResponse.written_at`),
`delete_relationships`/`delete_relationships_with`
(`DeleteRelationshipsResponse.deleted_at`), and `write_schema`
(`WriteSchemaResponse.written_at`). `import_relationships` is the one
exception: it returns `u64` (the count loaded), not a revision --
`ImportBulkRelationshipsResponse` has exactly one field, `num_loaded`, with no
`ZedToken` in the proto at all, so there is nothing to expose.

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
limit of 1,000 per RPC call (matches SpiceDB's default
`--max-delete-relationships-limit`, so the default works against a stock
server). It repeats until the server reports all matching relationships are
deleted. Returns the final revision.

`delete_relationships_with(filter, &options)` adds optional preconditions and
a page-size override via a `DeleteOptions` builder:

```rust
let options = DeleteOptions::new()
    .with_must_match(filter_that_must_exist)
    .with_must_not_match(filter_that_must_not_exist)
    .with_limit(500);
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

### Deadlines

Every unary method has a `_with_timeout(..., timeout: Duration)` sibling
(`delete_relationships_with` instead takes `DeleteOptions::with_timeout`),
mirroring the existing `_with_context` convention. The timeout is set on the
request via `tonic::Request::set_timeout`. `SpiceDBClient::builder(...)
.default_timeout(Duration)` overrides the default (30s, see
`client::DEFAULT_TIMEOUT`) applied to any unary call that doesn't use a
`_with_timeout` variant — mirroring `authzed-node`'s
`DEFAULT_DEADLINE_MS = 30_000` (its comment cites `grpc/grpc-node#541`). See
root DESIGN.md, "RULE: A unary call must have a deadline".

```rust
let client = SpiceDBClient::builder(endpoint, token)
    .default_timeout(Duration::from_secs(5))
    .build()
    .await?;
let result = client.check_permission(&full(), "view", &rel).await?;             // bound by the 5s default
let result = client.check_permission_with_timeout(&full(), "view", &rel, Duration::from_secs(1)).await?; // overrides it
```

Streaming methods (`read_relationships`, `lookup_resources`,
`lookup_subjects`, `updates`, `export_relationships`) have no `_with_timeout`
variant and are NOT bound by `default_timeout` — they are long-lived by
design (`updates` may run for the life of the process), and applying the
unary default to them would make the stream itself the outage.

tonic's own client-side timeout enforcement (`tonic::transport::Channel`'s
`GrpcTimeout` middleware, triggered by the `grpc-timeout` header
`set_timeout` sets) surfaces a purely local timeout as
`Status::cancelled("Timeout expired")`, not `Status::deadline_exceeded` --
`error::from_grpc_status` special-cases that exact `(code, message)` pair so
`SpiceDBError::DeadlineExceeded` is what callers actually see.

### Performance

- BulkCheck for all check operations (even single)
- Transparent cursor-based pagination with sensible default page sizes
- Batched deletions (1,000-item limit, matching SpiceDB's default `--max-delete-relationships-limit`) to avoid server-side timeouts
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
- `check_permission_with_context(&self, cs, permission, &rel, context: Option<&HashMap<String, serde_json::Value>>) -> Result<CheckResult, SpiceDBError>`
- `check_permissions(&self, cs, permission, &[rel]) -> Result<Vec<CheckResult>, SpiceDBError>`
- `check_permissions_with_context(&self, cs, permission, &[rel], context: Option<&HashMap<String, serde_json::Value>>) -> Result<Vec<CheckResult>, SpiceDBError>` -- call-level context default, merged key-by-key (item wins) with any per-item `Relationship::with_check_context`
- `check_any(&self, cs, permission, &[rel]) -> Result<bool, SpiceDBError>` -- counts only `has_permission()` results
- `check_any_with_context(&self, cs, permission, &[rel], context: Option<&HashMap<String, serde_json::Value>>) -> Result<bool, SpiceDBError>`
- `check_all(&self, cs, permission, &[rel]) -> Result<bool, SpiceDBError>` -- counts only `has_permission()` results
- `check_all_with_context(&self, cs, permission, &[rel], context: Option<&HashMap<String, serde_json::Value>>) -> Result<bool, SpiceDBError>`

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
  -- `ExpandResult.tree` is the native `PermissionTree` root (no proto types leaked); walk
  `PermissionTree.intermediate`/`PermissionTree.leaf` (exactly one is `Some`) to reach the
  resolved `SubjectRef`s

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

- `Relationship` struct + constructors + modifiers -- `check_context` (set via `with_check_context`) is per-item check-time caveat context, distinct from `caveat_context`'s write-time context (see "Relationships" above)
- `RelationshipError` enum
- `Filter` struct + builder methods
- `Transaction` struct + `create`/`touch`/`delete`/`must_not_match`/`must_match`
- `Precondition`, `PreconditionOperation`
- `DeleteOptions` struct (`must_match`, `must_not_match`, `limit`) + `with_must_match`/`with_must_not_match`/`with_limit` builder methods -- used by `delete_relationships_with`
- `Update`, `UpdateOperation`
- `CheckResult` (`#[must_use]`; `permissionship`, `missing_context`, `checked_at`, `has_permission()`)
- `Permissionship` (`Unspecified` / `NoPermission` / `HasPermission` / `ConditionalPermission` --
  shared by the check and lookup surfaces; lookups never yield `NoPermission`), `PartialCaveatInfo`
- `LookupResource` (result of `lookup_resources`; carries `looked_up_at`)
- `ResolvedSubject`, `LookupSubject` (results of `lookup_subjects`; `LookupSubject.excluded_subjects`
  carries wildcard exclusions -- see Lookups above; `LookupSubject.looked_up_at` is the call's revision)
- `SchemaDefinition`, `SchemaRelation`, `SchemaPermission`
- `SchemaCaveat`, `SchemaCaveatParameter`
- `ReflectSchemaResult`, `RelationReference`, `SchemaDiff`
- `ExpandResult` (`tree: PermissionTree`, `revision`), `PermissionTree` (`expanded_object: ObjectRef`,
  `expanded_relation`, `intermediate: Option<IntermediateNode>`, `leaf: Option<LeafNode>`),
  `IntermediateNode` (`operation: TreeOperation`, `children: Vec<PermissionTree>`), `LeafNode`
  (`subjects: Vec<SubjectRef>`), `TreeOperation`, `ObjectRef`, `SubjectRef`
- `CountResult`

### `error` module

- `SpiceDBError` enum
- `from_grpc_status(code, message) -> SpiceDBError`
- `is_transient(&SpiceDBError) -> bool`

## Examples Manifest

(To be added when examples are implemented)

| Directory | Demonstrates |
|-----------|-------------|
| `check_permission/` | Basic permission check, plus a caveated check that comes back `ConditionalPermission` and is then resolved to a grant via `check_permission_with_context` |
| `write_relationships/` | Writing relationships with transaction builder |
| `read_relationships/` | Reading relationships with stream |
| `lookup_resources/` | Finding resources a subject can access |
| `lookup_subjects/` | Finding subjects with access to a resource |
| `watch_changes/` | Watching for relationship changes |
| `schema_management/` | Reading and writing schema |
| `bulk_operations/` | Bulk checks and imports |
| `schema_reflection/` | Schema reflection, computable permissions, dependent relations, diff |
| `expand_permission_tree/` | Expanding a permission tree and walking the native `PermissionTree` |
| `relationship_counters/` | Registering, reading, and unregistering relationship counters |

## Changelog

See [CHANGELOG.md](CHANGELOG.md) for release notes.
