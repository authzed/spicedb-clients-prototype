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

Per root DESIGN.md, "RULE: Credentials over insecure transport require an
explicit opt-in": `createPlaintext`/`withInsecure()` only permit plaintext to
a loopback endpoint (`localhost`, `127.0.0.0/8`, `::1`, or a `unix:` socket
target) — the local-development case that is the entire reason they exist.
Anything else needs the separately-named `allowInsecureRemoteCredentials`
overload/`ClientOption` passed explicitly, or the constructor throws
`IllegalArgumentException` before any channel is created.

#### Custom TLS trust material

There is deliberately **no** dedicated CA-bundle option: `ClientOption` already
is one. It is a functional interface over `apply(ManagedChannelBuilder<?>)`, so a
caller reaches grpc-java's own TLS configuration directly:

```java
import io.grpc.netty.shaded.io.grpc.netty.GrpcSslContexts;
import io.grpc.netty.shaded.io.grpc.netty.NettyChannelBuilder;

ClientOption privateCa = builder ->
    ((NettyChannelBuilder) builder).sslContext(
        GrpcSslContexts.forClient().trustManager(caFile).keyManager(certFile, keyFile).build());
var client = SpiceDBClient.create(endpoint, presharedKey, privateCa);
```

The cast lands on a real runtime type — `create` builds the channel with
`ManagedChannelBuilder.forTarget(endpoint)`, which resolves through grpc-java's
`ManagedChannelProvider` SPI to `NettyChannelBuilder` when `grpc-netty-shaded` is
the transport on the classpath — and those shaded symbols are on the consumer's
compile classpath, because `proto-clients/spicedb-java-proto/build.gradle.kts`
declares `io.grpc:grpc-netty-shaded` as `api`, not `implementation`, so it flows
transitively to anyone depending on this client. (An audit claimed consumers
"cannot even cast to the shaded builder" — that is false, and the `api`
declaration is what makes it false. Keep it `api`.)

This is what satisfies root DESIGN.md, "RULE: A system-TLS constructor must
reach a real server", whose clause 1 permits `createSystemTls` to delegate to
`useTransportSecurity()` only because a caller can supply their own trust
material instead. A parallel CA option would be a second way to set the same
builder state, resolved by application order.

Note the security caveat already documented on `ClientOption#apply`: a custom
option gets the raw builder and can do anything to it, including
`usePlaintext()`, which the credential guard cannot see. Supplying trust
material is a TLS concern and must not be used to switch the transport — root
DESIGN.md, "RULE: Credentials over insecure transport require an explicit
opt-in".

Note also that the JDK's `cacerts` is a trust store an operator can install into,
so unlike the Python, TypeScript and Ruby clients — whose runtimes use a
compiled-in or bundled root set the operator cannot touch — this hatch is for
pinning a CA outside `cacerts`, and for mutual TLS.

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
    Instant expiration,
    Map<String, Object> checkContext
) { }
```

Constructors: `Relationship.of(...)`, `Relationship.fromTuple(String)`

Immutable modifiers: `withCaveat(...)`, `withExpiration(...)`, `withCheckContext(...)`, `toFilter()`

`checkContext` is a **CHECK-TIME-only** caveat context, distinct from `caveatContext` (write-time,
stored WITH the relationship). `withCaveat`'s context is read only by the write path
(`toProtoRelationship`); `withCheckContext`'s context is read only by the check path
(`checkItemFromRel`) — the two never mix, so setting one can never leak into the other's request.

### Checks

All checks use `BulkCheckPermissions` under the hood:
- `checkPermission(consistency, permission, relationship)` → `CheckResult`
- `checkPermission(consistency, permission, relationship, context)` → `CheckResult`
- `checkPermissions(consistency, permission, relationships...)` → `List<CheckResult>`
- `checkPermissions(consistency, permission, context, relationships...)` → `List<CheckResult>`
- `checkAny(consistency, permission, relationships...)` → `boolean`
- `checkAny(consistency, permission, context, relationships...)` → `boolean`
- `checkAll(consistency, permission, relationships...)` → `boolean`
- `checkAll(consistency, permission, context, relationships...)` → `boolean`

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
response-level token is propagated onto every `CheckResult` mapped from that response. A check over
more than `DEFAULT_CHECK_BATCH_SIZE` relationships is split into one request per chunk, so the
returned list can carry more than one distinct `checkedAt` — uniform within a chunk, not across the
call. See root DESIGN.md, invariant 2 under bulk checks. A per-item error in a
`CheckBulkPermissionsPair` is routed through `ErrorMapper` (using the item's own gRPC code) so
callers get the specific typed exception (e.g. `PermissionDeniedException`), with the item's
**absolute** index preserved in the message (`"check item %d: ..."`, matching `spicedb-go`) — the
index into the caller's own array, not into the chunk that happened to carry it.

#### Caveat context on checks

A caveated relationship's check can come back `CONDITIONAL_PERMISSION` — the server found a
matching relationship but couldn't evaluate its caveat because the required context wasn't
supplied (`CheckResult.missingContext()` names what's missing). Both check-time forms let a caller
resolve that:

- **Call-level** — a `Map<String, Object> context` overload on each of `checkPermission`,
  `checkPermissions`, `checkAny`, `checkAll`, applied as a default to every relationship checked in
  that call.
- **Per-item** — `Relationship.withCheckContext(context)`, which flows through even the plain
  (no-context-parameter) overloads.

**Merge rule (key-level, item wins):** for each relationship, the context sent to the server is the
call-level map with that relationship's own `checkContext` entries overwriting matching keys —
call-level keys absent from the item are retained, never wholesale-replaced.

```java
var callLevel = Map.<String, Object>of("now", 42, "region", "us");
var item0 = Relationship.of("document", "doc1", "viewer", "user", "alice")
    .withCheckContext(Map.of("region", "eu"));               // overrides "region" only
var item1 = Relationship.of("document", "doc2", "viewer", "user", "bob"); // no per-item context

// item0 gets {now: 42, region: "eu"}; item1 gets {now: 42, region: "us"} unchanged.
List<CheckResult> results = client.checkPermissions(consistency, "view", callLevel, item0, item1);
```

When neither call-level nor per-item context is supplied, no `context` field is set on the wire at
all (never an empty `Struct`).

**Additive, no existing call site changed.** All four context-accepting forms are new *overloads*
alongside the untouched originals — Java has overloading (unlike C#/TypeScript, it has no default
arguments), so this needed no `WithContext`-suffixed sibling methods the way Go does. The context
parameter sits immediately before the variadic `relationships...` on the three plural methods
(matching where `spicedb-go`'s `CheckWithContext` puts its `checkContext` parameter, relative to
its own variadic) and as a trailing parameter on the singular `checkPermission` (no variadic to
avoid there).

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
to typed exceptions.

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

Every unary method has an overload taking a trailing `Duration timeout`
(`deleteRelationships` instead reads `DeleteOptions.withTimeout(Duration)`),
mirroring the existing `checkPermission(..., Map<String, Object> context)`
overload convention. The timeout is applied via grpc-java's
`stub.withDeadlineAfter(millis, TimeUnit.MILLISECONDS)`, called fresh on
each retry attempt so a retried call gets a full new window per attempt.
`SpiceDBClient.createPlaintext`/`createSystemTls`/`create` all gained a
`Duration defaultTimeout` overload, applied to any unary call that doesn't
pass its own `timeout` — both default to `SpiceDBClient.DEFAULT_TIMEOUT`
(30s), mirroring `authzed-node`'s `DEFAULT_DEADLINE_MS = 30_000` (its
comment cites `grpc/grpc-node#541`). See root DESIGN.md, "RULE: A unary
call must have a deadline".

```java
try (var client = SpiceDBClient.createPlaintext("localhost:50051", "token", Duration.ofSeconds(5))) {
    CheckResult r = client.checkPermission(Consistency.full(), "view", rel);                         // bound by the 5s default
    CheckResult r2 = client.checkPermission(Consistency.full(), "view", rel, Duration.ofSeconds(1));  // overrides it
}
```

Server-streaming methods (`readRelationships`, `lookupResources`,
`lookupSubjects`, `updates`, `exportRelationships`) take no `timeout`
overload and are NOT bound by `defaultTimeout` — they are long-lived by
design (`updates` may run for the life of the process), and applying the
unary default to them would make the stream itself the outage.

`importRelationships` (`ImportBulkRelationships`) is client-streaming, not
server-streaming, but the same exclusion applies for the mirror-image
reason: its duration scales with the size of the caller's dataset, not with
server latency, so no fixed default is correct for it either. Unlike the
server-streaming methods above, it DOES have a `Duration timeout` overload
— there is simply no default to override, so the no-argument
`importRelationships(Iterable)` is unbounded and the
`importRelationships(Iterable, Duration)` overload is the only way to
bound it.

Note for callers reasoning about worst-case latency: the timeout is a
per-*attempt* budget, applied fresh on each retry, so a call that retries
can take up to `timeout × (retries + 1)` plus backoff, and an auto-paging
call (e.g. `deleteRelationships`) applies the same timeout fresh to each
page.

### Performance

- BulkCheck for all check operations (even single)
- Transparent cursor-based pagination with sensible default page sizes
- Batched deletions (1,000-item limit, matching SpiceDB's default `--max-delete-relationships-limit`) to avoid server-side timeouts
- Batched imports (1,000-item chunks)
- Exponential backoff retry for transient gRPC errors

### Escape hatch: raw channel access

`client.rawChannel()` returns this client's own `io.grpc.Channel`, with its bearer
metadata already attached, so any generated stub is one `newStub` call away:

```java
var stub = PermissionsServiceGrpc.newBlockingStub(client.rawChannel());
CheckPermissionResponse response = stub.checkPermission(request);
```

Clearly-marked **secondary** API, which is what root DESIGN.md's "What NOT To Do"
permits: channels, stubs and metadata stay out of the primary surface, and "escape
hatches for advanced use are acceptable as clearly marked secondary API". It exists so a
request the idiomatic surface cannot express — an RPC or proto field not wrapped here,
such as `WriteRelationshipsRequest.optionalTransactionMetadata`, or the single-check
`CheckPermission` RPC that `checkPermission` deliberately routes around — has a workaround
short of forking the client.

A `Channel` rather than the stubs themselves, because it is strictly more: every generated
stub, including for a service this client does not wrap, is one call away from it. Note this
is *not* for want of a proto-client type — `proto-clients/spicedb-java-proto` does define
`SpiceDBProtoClient` — but the idiomatic client has never used it: it builds its own channel,
stubs and guard, so there is no such object here to hand back. Prefer
it over rebuilding a `ManagedChannel` of your own, which means replicating this client's
transport configuration exactly (including whatever a `ClientOption` did to the builder)
and re-attaching the token by hand — get either wrong and the raw path runs with different
transport security than the idiomatic one while the call site reads as though it were the
same server.

Four properties, all deliberate:

- **The bearer token comes free.** The returned channel attaches this client's metadata to
  every call made through it, so a raw call is authenticated exactly as an idiomatic one.
- **A raw call is a raw call.** No `SpiceDBException` mapping (you catch
  `StatusRuntimeException`), no retry, and no `DEFAULT_TIMEOUT` — call `withDeadlineAfter`
  yourself.
- **The connection belongs to the client.** `close()` shuts it down, and a stub built here
  must not outlive it. What actually keeps it that way is the *wrapper*, not the declared
  type: `ClientInterceptors.intercept` returns a package-private `Channel` subclass holding
  the real channel as an unreachable delegate, so `(ManagedChannel) client.rawChannel()`
  throws `ClassCastException` instead of yielding `shutdown()`. The `Channel` return type
  makes that honest at compile time; the wrapper makes it true at runtime. Returning the
  bare channel typed as `Channel` would compile and read identically while silently losing
  the guarantee.
- **It is an accessor, never a constructor.** It takes no endpoint, preshared key, or
  transport setting, so channel construction stays on the single guarded path in `create`
  and the hatch cannot become a way around root DESIGN.md, "RULE: Credentials over insecure
  transport require an explicit opt-in".

This is separate from, and complementary to, `ClientOption.apply(ManagedChannelBuilder)`
above: that one *configures* the channel before it exists (and is where custom TLS and any
other builder-level setting go), while this one *hands back* the channel that was built.

No stability promise beyond grpc-java's and the generated code's.

### Experimental APIs

Methods from SpiceDB's `ExperimentalService` are clearly annotated:
- `experimentalRegisterRelationshipCounter`
- `experimentalCountRelationships`
- `experimentalUnregisterRelationshipCounter`

These may change without following the backwards compatibility mandate.

## Public API Surface

### SpiceDBClient

**Escape hatch:**
- `rawChannel()` — this client's own `io.grpc.Channel`, bearer metadata attached, as
  secondary API; see "Escape hatch: raw channel access" above

**Constructors:**
- `createPlaintext(String endpoint, String presharedKey)`
- `createPlaintext(String endpoint, String presharedKey, boolean allowInsecureRemoteCredentials)`
- `createSystemTls(String endpoint, String presharedKey)`
- `create(String endpoint, String presharedKey, ClientOption... options)` — recognizes
  `withInsecure()` and `allowInsecureRemoteCredentials()` among `options`

**Checks:**
- `checkPermission(Consistency, String permission, Relationship)` → `CheckResult`
- `checkPermission(Consistency, String permission, Relationship, Map<String, Object> context)` → `CheckResult`
- `checkPermissions(Consistency, String permission, Relationship...)` → `List<CheckResult>`
- `checkPermissions(Consistency, String permission, Map<String, Object> context, Relationship...)` → `List<CheckResult>`
- `checkAny(Consistency, String permission, Relationship...)` → `boolean`
- `checkAny(Consistency, String permission, Map<String, Object> context, Relationship...)` → `boolean`
- `checkAll(Consistency, String permission, Relationship...)` → `boolean`
- `checkAll(Consistency, String permission, Map<String, Object> context, Relationship...)` → `boolean`

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
- `updates(List<String> objectTypes, String startRevision)` → `Stream<WatchEvent>`
- `updates(List<String> objectTypes, String startRevision, boolean includeCheckpoints)` →
  `Stream<WatchEvent>` — `includeCheckpoints` requests `WATCH_KIND_INCLUDE_CHECKPOINTS`
  (recommended behind a proxy that aborts idle connections, since a checkpoint keeps the
  stream alive with no changes to report)

`WatchEvent(List<Update> updates, String changesThrough, boolean isCheckpoint)` is one event
per `WatchResponse`. `changesThrough` is always populated -- proto: "This token can be used in
a subsequent WatchRequest to resume watching from this point" -- pass it as `startRevision` to
resume after a dropped stream instead of restarting from the original `startRevision`
(reprocessing, possibly past the GC window) or from head (silently losing every change in the
gap). `isCheckpoint` is true for a checkpoint event, which carries no `updates`.

**Experimental:**
- `experimentalRegisterRelationshipCounter(String name, Filter)`
- `experimentalCountRelationships(String name)` → `CountResult`
- `experimentalUnregisterRelationshipCounter(String name)`

## Examples Manifest

Java's examples are JUnit classes in one source set (`examples/src/test/java/...`), not
per-example directories — the rows below name the class, matching `examples/README.md`.

| Test class | Demonstrates |
|------------|-------------|
| `CheckPermissionTest` | Basic permission check with `checkPermission`, returning `CheckResult` |
| `ConditionalCheckTest` | `CONDITIONAL_PERMISSION` against a live caveated relationship whose context was never supplied — `hasPermission()` must be false |
| `WriteRelationshipsTest` | Writing relationships with the `Transaction` builder |
| `ReadRelationshipsTest` | Reading relationships with cursor-based auto-pagination |
| `LookupResourcesTest` | Finding resources a subject can access |
| `LookupSubjectsTest` | Finding subjects with access to a resource |
| `WatchChangesTest` | Watching for relationship changes |
| `SchemaManagementTest` | Reading and writing schema |
| `BulkOperationsTest` | Bulk checks with `checkPermissions`/`checkAll`/`checkAny`, plus bulk import/export |
| `SchemaReflectionTest` | Schema reflection, computable permissions, dependent relations, diff |
| `RelationshipCountersTest` | Registering, reading, and unregistering relationship counters |
| `ExpandPermissionTreeTest` | Expanding a permission with `expandPermissionTree` and walking the native `PermissionTree` (intermediate/leaf nodes, subjects) |
| `CallDeadlinesTest` | The `Duration defaultTimeout` construction overload, a per-call `timeout` override, and confirming bulk import isn't bounded by the unary default |
| `RawEscapeHatchTest` | `rawChannel()` — driving a generated stub directly for a proto field (`optionalTransactionMetadata`) and an RPC (`CheckPermission`) the idiomatic API does not expose |

(`SpiceDBIntegrationTest` is the shared base class, not an example.)

## Changelog

See [CHANGELOG.md](CHANGELOG.md) for release notes.
