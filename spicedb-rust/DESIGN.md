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

Per root DESIGN.md, "RULE: Credentials over insecure transport require an
explicit opt-in": `.plaintext()`/`new_plaintext` only permit a plaintext
connection to a loopback endpoint (`localhost`, `127.0.0.0/8`, or `::1`) —
the local-development case that is the entire reason they exist. A `unix:`
target is NOT loopback here and is refused outright: tonic dials a URI, so it
would resolve the DNS name `unix` rather than a socket path. Anything else
needs `.allow_insecure_remote_credentials()` called explicitly on the builder,
or `build()` returns
`SpiceDBError::InvalidArgument` before any channel is created.

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
or Node image connects fine on its compiled-in bundle.

A caller-supplied CA bundle is the general remedy, and the Python, TypeScript and Ruby
clients now have one (`ca_cert=`, `tls: { caCert }`, `new_custom_tls(ca_cert:)`) —
because on those runtimes the *default* is the wrong trust source for a private CA, and
the rule above permits that default only because such an escape hatch exists. This
client has the opposite problem: its default already reads the host's store, so the
private-CA case works with `new_system_tls` alone.

**Two things remain uncovered here, and no option is offered for either today:**

1. **An image with no OS trust store at all** (`FROM scratch`), which has nothing for
   `with_native_roots` to read, where a distroless Python or Node image would still
   connect on its compiled-in bundle.
2. **Mutual TLS.** There is no way to present a client certificate. This gap is
   independent of the trust-store one and is *not* closed by `tls-native-roots`: reading
   the host's roots says nothing about proving this client's own identity to a server
   that demands one. The Python, TypeScript and Ruby clients each take a client
   certificate and key alongside the CA (`client_cert=`/`client_key=`,
   `tls: { clientCert, clientKey }`, `new_custom_tls(client_cert:, client_key:)`); this
   client takes neither.

`SpiceDBClientBuilder` therefore exposes exactly `.plaintext()`,
`.allow_insecure_remote_credentials()` and `.default_timeout()` — no CA bundle, no client
identity — and the module doc on `src/client.rs` says so. It previously claimed "full
control over TLS configuration", which was never true of any version of this builder.
Closing either gap means adding options to that builder; the decision on record is to
state the gaps honestly rather than imply they do not exist.

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

### Escape hatch: raw proto access

`SpiceDBClient::raw_proto()` returns `&SpiceDBProtoClient` — the four generated tonic
clients (`permissions`, `schema`, `watch`, `experimental`) this crate makes its own calls
through. The generated crate is re-exported as `spicedb::spicedb_proto`, so a caller can
name those types without adding a dependency that could drift to a different version of
the generated code:

```rust,ignore
use spicedb::spicedb_proto::authzed::api::v1 as proto;

// Clone: the generated clients take `&mut self`, and a tonic clone shares the
// same channel rather than opening a second connection.
let mut permissions = client.raw_proto().permissions.clone();
let response = permissions.check_permission(request).await?;
```

Clearly-marked **secondary** API, which is what root DESIGN.md's "What NOT To Do"
permits: channels, stubs and metadata stay out of the primary surface, and "escape
hatches for advanced use are acceptable as clearly marked secondary API". It exists so a
request the idiomatic surface cannot express — an RPC or proto field not wrapped here,
such as `WriteRelationshipsRequest::optional_transaction_metadata`, or the single-check
`CheckPermission` RPC that `check_permission()` deliberately routes around — has a
workaround short of forking the crate.

Four properties, all deliberate:

- **The bearer token comes free.** Each generated client is wrapped in this crate's
  interceptor, so a raw call is authenticated exactly as an idiomatic one is.
- **A raw call is a raw call.** No `SpiceDBError` mapping (you handle `tonic::Status`),
  no retry, and no `default_timeout` — set a deadline on the request yourself.
- **The connection belongs to the client.** It is released when the `SpiceDBClient`
  drops; a clone taken from here must not outlive it.
- **It is an accessor, never a constructor.** It takes no endpoint, token, or transport
  setting, so channel construction stays on the single guarded path in
  `SpiceDBClientBuilder::build` and the hatch cannot become a way around root DESIGN.md,
  "RULE: Credentials over insecure transport require an explicit opt-in".

**This does not close either TLS gap listed above.** The channel already exists by the
time `raw_proto()` can be called, and TLS is configured before that, so neither the
`FROM scratch` trust-store gap nor the missing mutual-TLS support is affected. Closing
those still means adding options to `SpiceDBClientBuilder`, exactly as stated there.

No stability promise beyond tonic's and the generated code's. Setting a per-call deadline
or reading response metadata means depending on `tonic` (and `prost-types` for well-known
types) yourself, at versions compatible with the ones this crate builds against.

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
response (not per-item), so every `CheckResult` mapped from a given response
shares the same `checked_at`. `check_permissions` splits an input larger than
`DEFAULT_CHECK_BATCH_SIZE` into one request per chunk, so the returned `Vec`
can carry more than one distinct `checked_at` — uniform within a chunk, not
across the call. See root DESIGN.md, invariant 2 under bulk checks.

### Streaming and Transparent Cursor Pagination

Streaming operations return `impl Stream<Item = Result<T, SpiceDBError>>`.
**Cursors are fully internal** -- the caller sees a single stream, and the
client transparently re-fetches pages using the `AfterResultCursor` from each
response. Default page sizes use sensible defaults:

| Method | Default page size | Notes |
|--------|------------------|-------|
| `read_relationships.rs` | 512 | cursor-based auto-pagination |
| `lookup_resources.rs` | 512 | cursor-based auto-pagination |
| `lookup_subjects.rs` | -- | no cursor support in SpiceDB yet; single streaming call |
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

Automatic retry with jittered exponential backoff, for **reads only**, on
**`UNAVAILABLE` and `ABORTED`**.

`RESOURCE_EXHAUSTED` is deliberately NOT retryable. In SpiceDB it means
either memory load-shed — where retrying adds load to an already-overloaded
server — or a deterministic `MaxDepthExceeded`, which can never succeed and
whose retries re-run the most expensive class of check several times before
surfacing the same error. See root `DESIGN.md`, "RULE: Automatic retry is
for idempotent operations only".

**Mutations are never auto-retried.** `WriteRelationships` carrying
`OPERATION_CREATE`, or any request with preconditions, is not idempotent: if
it commits and the response is lost — a rolling restart, a proxy dropping the
connection — the retry returns `ALREADY_EXISTS`/`FAILED_PRECONDITION` and the
caller concludes a write failed that in fact succeeded. Writes, deletes,
schema writes, bulk import, and the counter registration calls therefore
never enter the retry loop: their errors are mapped to this client's typed
form and raised on the first attempt. A caller who wants a mutation retried
must decide that themselves, knowing their own idempotency.

**Timeout shape**: the per-call timeout is a per-*attempt* budget, applied
fresh to each retry rather than shrinking across them, so a call that
legitimately needs several retries is not made more likely to fail than one
that needs none. Worst-case latency for a timeout `t` is therefore
`t × (retries + 1)` plus backoff, and an auto-paging call spends a fresh `t`
per page. Root `DESIGN.md`, "On worst-case latency", covers why this differs
from Go's; a caller needing a true end-to-end bound must impose it above this
client.

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

Server-streaming methods (`read_relationships`, `lookup_resources`,
`lookup_subjects`, `updates`, `export_relationships`) have no `_with_timeout`
variant and are NOT bound by `default_timeout` — they are long-lived by
design (`updates` may run for the life of the process), and applying the
unary default to them would make the stream itself the outage.

`import_relationships` (`ImportBulkRelationships`) is client-streaming, not
server-streaming, but the same exclusion applies for the mirror-image
reason: its duration scales with the size of the caller's dataset, not with
server latency, so no fixed default is correct for it either. Unlike the
server-streaming methods above, it DOES have a `_with_timeout` sibling
(`import_relationships_with_timeout`) — there is simply no default to
override, so `import_relationships` itself is unbounded and
`import_relationships_with_timeout` is the only way to bound it.

Note for callers reasoning about worst-case latency: the timeout is a
per-*attempt* budget, applied fresh on each retry, so a call that retries
can take up to `timeout × (retries + 1)` plus backoff, and an auto-paging
call (e.g. `delete_relationships_with`) applies the same timeout fresh to
each page.

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

**Escape hatch:**
- `raw_proto(&self) -> &SpiceDBProtoClient` -- the generated tonic clients, as secondary
  API; see "Escape hatch: raw proto access" above

**Constructors:**
- `SpiceDBClient::new_plaintext(endpoint, token) -> Result<Self, SpiceDBError>`
- `SpiceDBClient::new_system_tls(endpoint, token) -> Result<Self, SpiceDBError>`
- `SpiceDBClient::builder(endpoint, token) -> SpiceDBClientBuilder`
- `SpiceDBClientBuilder::allow_insecure_remote_credentials(self) -> Self` -- explicit opt-in for
  `.plaintext()` against a non-loopback endpoint

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
- `updates(&self, object_types) -> impl Stream<Item = Result<WatchEvent, SpiceDBError>>`
  — watch from head, no checkpoints
- `updates_with(&self, object_types, &WatchOptions) -> impl Stream<Item = Result<WatchEvent, SpiceDBError>>`

`WatchOptions` follows the same builder shape as `DeleteOptions`:

```rust
let stream = client.updates_with(
    &object_types,
    &WatchOptions::new()
        .with_start_revision(resume_token)   // resume, instead of from head
        .with_checkpoints(),                 // WATCH_KIND_INCLUDE_CHECKPOINTS
);
```

`with_checkpoints()` requests `WATCH_KIND_INCLUDE_CHECKPOINTS`, recommended behind a proxy
that aborts idle connections, since a checkpoint keeps the stream alive with no changes to
report. It is a named builder rather than a positional `bool` for the reason the whole
options pattern exists here: `client.updates(&types, None, false)` gave a reader no way to
tell what the literal meant without opening the signature, and this was the only place in
the client where that was true.

`WatchEvent { updates: Vec<Update>, changes_through: String, is_checkpoint: bool }` is one
event per `WatchResponse`. `changes_through` is always populated -- proto: "This token can be
used in a subsequent WatchRequest to resume watching from this point" -- pass it as
`start_revision` to resume after a dropped stream instead of restarting from the original
`start_revision` (reprocessing, possibly past the GC window) or from head (silently losing
every change in the gap). `is_checkpoint` is true for a checkpoint event, which carries no
`updates`.

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

| Example | Demonstrates |
|-----------|-------------|
| `check_permission.rs` | Basic permission check, plus a caveated check that comes back `ConditionalPermission` and is then resolved to a grant via `check_permission_with_context` |
| `write_relationships.rs` | Writing relationships with transaction builder |
| `read_relationships.rs` | Reading relationships with stream |
| `lookup_resources.rs` | Finding resources a subject can access |
| `lookup_subjects.rs` | Finding subjects with access to a resource |
| `watch_changes.rs` | Watching for relationship changes with a bounded consumer: subscribe from a known revision, write, consume until that exact update arrives, drop the stream, then resume on a fresh one and require the same update |
| `schema_management.rs` | Reading and writing schema |
| `bulk_operations.rs` | Bulk checks and imports |
| `schema_reflection.rs` | Schema reflection, computable permissions, dependent relations, diff |
| `expand_permission_tree.rs` | Expanding a permission tree and walking the native `PermissionTree` |
| `relationship_counters.rs` | Registering, reading, and unregistering relationship counters, polling to a terminal state and asserting an exact count |
| `call_deadlines.rs` | Constructing a client with `default_timeout`, a per-call `_with_timeout` override, confirming bulk import isn't bounded by the unary default, and proving both deadlines bite against a listener that accepts the connection and never answers |
| `error_mapping` | Recovering from `OUT_OF_RANGE` (stale ZedToken) and `UNAUTHENTICATED` without parsing a message |
| `insecure_opt_in` | Why `.plaintext()` is loopback-only, and the named opt-in a remote plaintext host requires |
| `retry_policy` | Which calls are retried for you and which are not, counted server-side |
| `unrepresentable_values` | A filter the wire cannot express fails loudly; unknown server enums degrade safely |
| `raw_escape_hatch.rs` | `raw_proto()` — driving the generated tonic client directly for a proto field (`optional_transaction_metadata`) and an RPC (`CheckPermission`) the idiomatic API does not expose |

## Changelog

See [CHANGELOG.md](CHANGELOG.md) for release notes.
