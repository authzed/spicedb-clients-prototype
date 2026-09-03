# spicedb-typescript — Idiomatic TypeScript Client Design

## Inherits

Root DESIGN.md (`../DESIGN.md`) — read it for the overall vision and backwards
compatibility mandate. This document takes precedence for TypeScript-specific
decisions.

## Language-Specific Goals

### Philosophy

Fully typed, Promise-based API that leverages TypeScript's type system for
safety. No `any` types in the public API. ESM-first.

### Package Structure

- **`@spicedb/client`** — main package
  - `src/client.ts` — SpiceDBClient class
  - `src/types.ts` — relationship types, filters, transactions
  - `src/consistency.ts` — consistency strategy constructors
  - `src/errors.ts` — typed error classes
  - `src/index.ts` — public re-exports

### Client Construction

```typescript
// For production
const client = createSpiceDBClient("grpc.example.com:443", "my-token");

// For testing
const client = createSpiceDBClient("localhost:50051", "testtoken", {
  insecure: true,
});

// Or class-based
const client = new SpiceDBClient({ endpoint, token, insecure: true });
```

Per root DESIGN.md, "RULE: Credentials over insecure transport require an
explicit opt-in": `insecure: true` only permits plaintext to a loopback
endpoint (`localhost`, `127.0.0.0/8`, or `::1`) — the local-development case
that is the entire reason it exists. A `unix:` target is NOT loopback here and
is refused outright: Node http2 dials a URL origin, so it would resolve the
DNS name `unix` rather than a socket path. Anything else needs
`allowInsecureRemoteCredentials: true` passed explicitly, or
`createSpiceDBClient`/`new SpiceDBClient(...)` throws before any connection
is created.

### Custom TLS trust material

```typescript
// A SpiceDB behind a private or corporate CA
const client = createSpiceDBClient("spicedb.internal:443", "my-token", {
  tls: { caCert: readFileSync("/etc/ssl/certs/internal-ca.pem") },
});

// ...and where the server requires mutual TLS
const client = createSpiceDBClient("spicedb.internal:443", "my-token", {
  tls: { caCert, clientCert, clientKey },
});
```

`caCert`/`clientCert`/`clientKey` are passed through to `node:tls`'s `ca`,
`cert` and `key` on the HTTP/2 connection, and are typed as exactly what Node
accepts there (a PEM string, a `Buffer`, or an array of either) so the option
cannot drift from what the transport supports.

Why these exist. Root DESIGN.md, "RULE: A system-TLS constructor must reach a
real server", requires the default secure path to delegate to the ecosystem's
default trust source, and names the hazard that leaves visible: Node ships a
bundled Mozilla root store, so a CA an operator installed in the host's own
store is **not** honoured. That rule permits delegating to the bundled set
precisely *because* a caller can supply their own material instead; `tls` is
what makes that true here.

`caCert` **replaces** Node's bundled roots for that client rather than adding
to them (`node:tls`'s own behavior, and generally what a deployment pinning a
private CA wants). Omitting `tls` entirely leaves the transport's trust source
untouched — the session manager is constructed with no session options at all,
not with an object of `undefined`s — so this library never selects a root set
of its own, which clause 1 of that rule prohibits.

The material reaches the transport through `new Http2SessionManager(baseUrl,
undefined, { ca, cert, key })`, **not** `createGrpcTransport`'s `nodeOptions`.
Connect-ES documents that supplying a `sessionManager` "makes nodeOptions as
well as the HTTP/2 session options ineffective", and this client always
supplies one (so `close()` has a handle to abort) — trust material handed to
`nodeOptions` would be silently dropped and every private-CA handshake would
still fail.

Two combinations throw at construction, before any session manager exists:

- **`insecure: true` with any `tls` field.** A plaintext connection performs no
  handshake, so `node:tls` would ignore the material and put the bearer token
  on the wire in cleartext behind a call site that reads as though TLS were
  configured — the failure root DESIGN.md, "RULE: Credentials over insecure
  transport require an explicit opt-in", exists to prevent. Supplying trust
  material is never a second, quieter route to an insecure transport, and never
  a construction path that skips that rule's guard (which still runs first, and
  whose message is what a caller sees). It throws rather than silently turning
  TLS on, since an implicit upgrade is just as surprising.
- **`clientCert` without `clientKey`, or vice versa.** Neither half is usable
  alone; `node:tls` fails later, from a layer with no idea which option was
  wrong.

Testing this needs `openssl` on `PATH`: the `custom-tls.test.ts` fixtures in both
tiers, and `examples/custom_tls/`, generate a throwaway CA and sign a server
certificate with it, then complete a real handshake against a real
gRPC-over-TLS server. They fail rather than skip without it, deliberately — a
skipped handshake test reads as coverage while proving nothing, which is the
failure mode root DESIGN.md, "RULE: A system-TLS constructor must reach a real
server", clause 3 warns about one layer up.

### Escape hatch: raw proto access

`client.raw()` returns the underlying `SpiceDBProtoClient` — the four generated
Connect clients (`permissions`, `schema`, `watch`, `experimental`) this library
makes its own calls through:

```typescript
const { permissionship } = await client.raw().permissions.checkPermission({
  consistency: { requirement: { case: "fullyConsistent", value: true } },
  resource: { objectType: "document", objectId: "readme" },
  permission: "view",
  subject: { object: { objectType: "user", objectId: "jimmy" } },
});
```

Clearly-marked **secondary** API, which is what root DESIGN.md's "What NOT To
Do" permits: channels, stubs and metadata stay out of the primary surface, and
"escape hatches for advanced use are acceptable as clearly marked secondary
API". It exists so a request the idiomatic surface cannot express — an RPC or
proto field not wrapped here, such as
`WriteRelationshipsRequest.optionalTransactionMetadata` — has a workaround
short of forking the client.

Four properties, all deliberate:

- **The `authorization` header comes free.** It is set by a transport
  interceptor, so a raw call is authenticated exactly as an idiomatic one is.
- **A raw call is a raw call.** No `SpiceDBError` mapping (you catch Connect's
  `ConnectError`), no retry, and no `defaultTimeoutMs` — pass
  `CallOptions.timeoutMs` yourself. That is the cost of the hatch, and the
  reason the idiomatic methods remain the default.
- **Do not call `close()` on it.** It is this client's own connection;
  `SpiceDBClient.close()` is what releases it.
- **It is an accessor, never a constructor.** It takes no endpoint, token, or
  transport setting, so transport construction stays on the single guarded path
  in the proto tier's `createSpiceDBClient` and the hatch cannot become a way
  around root DESIGN.md, "RULE: Credentials over insecure transport require an
  explicit opt-in".

No stability promise beyond what `@connectrpc/connect` and the generated
`@spicedb/proto` clients give. The type is re-exported as `SpiceDBProtoClient`
so a caller can name it without depending on `@spicedb/proto` directly.

### Consistency

Explicit, never defaulted:

```typescript
import { full, minLatency, atLeast, snapshot } from "@spicedb/client";

const result = await client.checkPermission(rel, full());
const result = await client.checkPermission(rel, atLeast(revision));
```

All write operations return `Promise<string>` (revision).

### Types

Interface-based types:

```typescript
interface Relationship {
  resourceType: string;
  resourceId: string;
  resourceRelation: string;
  subjectType: string;
  subjectId: string;
  subjectRelation?: string;
  caveatName?: string;
  caveatContext?: Record<string, unknown>;
  expiration?: Date;
}
```

Builder helpers:
- `relationship("document:example", "viewer", "user:jimmy")`
- `relationshipFromTuple("document:example#viewer", "user:jimmy")`

### Checks

```typescript
const results = await client.checkPermissions(consistency, ...rels); // CheckResult[]
const result = await client.checkPermission(consistency, rel);       // CheckResult
const any = await client.checkAny(consistency, ...rels);             // boolean
const all = await client.checkAll(consistency, ...rels);             // boolean
```

`checkPermission`/`checkPermissions` return `CheckResult` — never a bare
`boolean` — so a caveated relationship whose context wasn't supplied at check
time is distinguishable from a real denial instead of being silently
collapsed to `true` or `false`:

```typescript
interface CheckResult {
  permissionship: Permissionship; // "unspecified" | "hasPermission" | "conditionalPermission" | "noPermission"
  missingContext: string[];       // caveat context keys the server needed; empty unless conditionalPermission
  checkedAt: string;              // revision this check was evaluated at
  hasPermission(): boolean;       // true ONLY for permissionship === "hasPermission"
}
```

`CheckResult` is a class (not a plain interface, unlike the lookup result
types below) so `hasPermission()` travels with the data. Always prefer
`result.hasPermission()` over comparing `permissionship` directly — a
`"conditionalPermission"` result means the server needed caveat context that
was not supplied and is NOT a grant.

**Never use a `CheckResult` as a bare condition.** Objects are unconditionally
truthy in JavaScript, and TypeScript offers no hook to override that (there is
no equivalent of Python's `__bool__`, and no compile error like Go's or Rust's).
So `if (result)` is `true` for *every* result — including a
`"conditionalPermission"` one, which would silently grant access on a caveat
the server never evaluated. This is the exact fail-open this client used to
ship (`checkPermission` returned `true` for `CONDITIONAL_PERMISSION` by design),
and it is also the shape a naive migration from the old `boolean` API produces:

```typescript
// WRONG — always true, grants on an unevaluated caveat
const result = await client.checkPermission(consistency, rel);
if (result) grant();

// RIGHT — false for conditional, denied, and unspecified alike
if (result.hasPermission()) grant();
```

Because the language cannot enforce this, documentation is the only mitigation:
no docstring, README, or example in this client may show a check result used
directly as a condition. Every sample goes through `hasPermission()`. See root
`DESIGN.md`, "RULE: Only an unconditional grant is true", clause 5.

`checkAny`/`checkAll` stay `boolean` and count ONLY `hasPermission() ===
true` results as granted — a conditional result never counts, even for
`checkAny`. This is deliberate and fail-closed.

`checkPermission` uses the single-check `CheckPermission` RPC directly;
`checkPermissions`/`checkAny`/`checkAll` use `BulkCheckPermissions`. A
per-item error from `CheckBulkPermissions` is surfaced by throwing a typed
error, never coerced into a result.

### Caveat context: per-item and call-level

`CheckRequest.context` supplies per-item caveat context — the values a
caveat needs, scoped to one specific check. All four check surfaces also
accept an optional trailing `CheckOptions` with a call-level default
`context`, applied to every check the call evaluates:

```typescript
const result = await client.checkPermission(consistency, check, {
  context: { now: Date.now() / 1000 },
});

const results = await client.checkPermissions(
  consistency,
  [check1, check2],           // explicit-array form — required to pass options
  { context: { now: Date.now() / 1000 } },
);
```

`checkPermissions`/`checkAny`/`checkAll` keep their original variadic form
(`consistency, ...checks`) unchanged — no existing call site needs to
change. `CheckOptions` is only reachable through a second, explicit-array
overload (`consistency, checks, options?`), since a call-level default has
nowhere to go in a trailing-variadic call.

The proto wire has no request-level context field — `CheckBulkPermissionsRequest`
carries no `context`, only `CheckBulkPermissionsRequestItem.context` — so a
call-level default is fanned out onto every item at request-build time and
merged **key-by-key** with that item's own `context`: the item's own keys
win on conflict, and call-level keys the item doesn't mention are retained.
This is not a wholesale replacement — an item supplying one key does not
drop every other call-level key:

```typescript
// call-level: { now: 42, region: "us" }
// item-level: { region: "eu" }
// sent for that item: { now: 42, region: "eu" }
```

If neither a call-level nor an item-level context is supplied, no context
field is set on the request (never an empty Struct).

### Streaming

AsyncIterableIterator for streaming RPCs:

```typescript
for await (const rel of client.readRelationships(filter, consistency)) {
  // ...
}

for await (const resource of client.lookupResources(params, consistency)) {
  // resource: LookupResource — { resourceId, permissionship, partialCaveat? }
}
```

### Lookup Results

`lookupResources`/`lookupSubjects` yield native result objects — never bare
IDs — so callers can't accidentally treat a caveated or wildcard-excluded
match as an unconditional grant:

```typescript
interface LookupResource {
  resourceId: string;
  permissionship: Permissionship; // "unspecified" | "hasPermission" | "conditionalPermission" | "noPermission"
  partialCaveat?: PartialCaveatInfo; // set when permissionship is "conditionalPermission"
  lookedUpAt: string; // revision this result was computed at
}

interface ResolvedSubject {
  subjectId: string;
  permissionship: Permissionship;
  partialCaveat?: PartialCaveatInfo;
}

interface LookupSubject {
  subject: ResolvedSubject;
  excludedSubjects: ResolvedSubject[]; // wildcard "*" exclusions — MUST check
  lookedUpAt: string; // revision this result was computed at
}
```

Callers MUST check `permissionship` before treating a result as a full
grant, and — critically — when `subject.subjectId` is the wildcard `"*"`,
MUST check `excludedSubjects` before treating the wildcard as a blanket
grant. `permissionship` is shared with `CheckResult` (see Checks above) —
lookups never yield `"noPermission"`: a subject/resource pair that lacks the
permission is simply absent from the stream. Mirrors spicedb-go's
`client/lookup_types.go`.

Results from `lookupResources`/`lookupSubjects` are streamed and **not
guaranteed to be unique**: the same resource or subject may be returned more
than once (e.g. via caveated/conditional results, or when a limit is set),
possibly with differing permissionship. A caller that requires uniqueness
must deduplicate results itself.

`LookupResourcesParams.debug` sets the proto's `with_debug` field, which asks
SpiceDB to attach additional debug context to the error when the call fails
by exceeding the maximum permission-check recursion depth — the only case
the proto uses it for as of this writing. There is no separate accessor for
the extra detail: it rides the same `google.rpc.ErrorInfo` status detail this
client already parses onto `SpiceDBError.reason`/`reasonMetadata` (see
"Error Handling" below), so a caller who wants it needs only to set the flag
and read the returned error as usual. Mirrors spicedb-go's
`WithLookupResourcesDebug()`.

### Writes

Transaction builder:

```typescript
const txn = new Transaction();
txn.create(relationship);
txn.touch(relationship);
txn.delete(relationship);
txn.mustNotMatch(filter);
const revision = await client.write(txn);
```

`write`, `deleteRelationships`, and `writeSchema` all return the revision
the mutation occurred at. `importBulkRelationships` (bulk import) is the one
exception: it returns `Promise<bigint>` (the number of relationships
loaded) with no revision, because `ImportBulkRelationshipsResponse` carries
no `ZedToken` field at all — the proto itself gives the client nothing to
expose there, not a client-side gap.

#### Bulk import takes any iterable

`importBulkRelationships(relationships)` accepts
`Iterable<Relationship> | AsyncIterable<Relationship>` — an array, a
generator, an async generator, anything with `Symbol.iterator` or
`Symbol.asyncIterator`. Relationships are converted to protos and batched
(1,000 per request message) as they are pulled, so only one batch is
resident at a time:

```typescript
async function* fromCursor() {
  for await (const row of db.query("SELECT ...")) {
    yield relationship(`document:${row.id}`, "viewer", `user:${row.userId}`);
  }
}
await client.importBulkRelationships(fromCursor());
```

This is the one method whose whole purpose is volume, so requiring a
materialized array made the likeliest large dataset the hardest one to
import — and the earlier `relationships.map(toProtoRelationship)` held it
twice, once as the caller's relationships and once as protos. Every other
SpiceDB client takes a lazy sequence here (Go `iter.Seq`, C#
`IAsyncEnumerable`, Java `Iterable`, Python `Iterable`, Ruby Enumerable,
Rust `IntoIterator`); this one now matches.

Arrays are iterable, so existing call sites are unaffected, and an array is
still the right answer when the data is already in memory.

The sequence is consumed exactly once, which is safe because this call is
never retried automatically — a bulk import is a mutation, per root
`DESIGN.md` "RULE: Automatic retry is for idempotent operations only". A
caller retrying by hand must supply a fresh iterable; a spent generator
yields nothing, and would silently import zero relationships.

### Testing

Use `vitest` for all tests. Examples should also be runnable as vitest tests.

### Error Handling

Typed error classes:
```typescript
class SpiceDBError extends Error {}
class PermissionDeniedError extends SpiceDBError {}
class NotFoundError extends SpiceDBError {}
class AlreadyExistsError extends SpiceDBError {}
class InvalidArgumentError extends SpiceDBError {}
```

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

Every unary method takes an optional `timeoutMs` (milliseconds) — either as
a trailing `options?: { timeoutMs?: number }` parameter, or as a field on an
existing options type (`CheckOptions`, `DeleteOptions`,
`ExpandPermissionTreeParams`, `ReflectSchemaOptions`,
`ComputablePermissionsParams`, `DependentRelationsParams`) — passed straight
through as Connect's `CallOptions.timeoutMs`. `SpiceDBClientOptions` gains
`defaultTimeoutMs`, applied to any unary call that doesn't supply its own;
both default to 30 seconds, mirroring `authzed-node`'s
`DEFAULT_DEADLINE_MS = 30_000` (its comment cites `grpc/grpc-node#541`). See
root DESIGN.md, "RULE: A unary call must have a deadline" — without a
finite default, a SpiceDB instance that accepts a connection but never
answers hangs every caller that didn't opt in to a timeout forever, since
the connection looks fine at the transport level and nothing is ever
produced to retry.

```typescript
const client = createSpiceDBClient(endpoint, token, { defaultTimeoutMs: 5000 });
const result = await client.checkPermission(full(), check);                         // bound by the 5s default
const result = await client.checkPermission(full(), check, { timeoutMs: 1000 });     // overrides it for this call
```

Server-streaming methods (`readRelationships`, `lookupResources`,
`lookupSubjects`, `watch`, `exportBulkRelationships`) do NOT take
`timeoutMs` and are NOT bound by `defaultTimeoutMs` — they are long-lived by
design (`watch` may run for the life of the process), and applying the
unary default to them would make the stream itself the outage.

`importBulkRelationships` is client-streaming, not server-streaming, but the
same exclusion applies for the mirror-image reason: its duration scales
with the size of the caller's dataset, not with server latency, so no fixed
default is correct for it either. Unlike the server-streaming methods
above, it DOES still take `options?.timeoutMs` — `undefined` (the default)
means unbounded there (Connect's `createDeadlineSignal` sets no timer at
all when `timeoutMs` is `undefined`), not "use `defaultTimeoutMs`"; pass it
explicitly to bound a bulk import.

Note for callers reasoning about worst-case latency: `timeoutMs` is a
per-*attempt* budget, applied fresh on each retry, so a call that retries
can take up to `timeoutMs × (retries + 1)` plus backoff, and an auto-paging
call (e.g. `deleteRelationships`) applies the same `timeoutMs` fresh to
each page.

### Experimental API Naming Convention

Methods wrapping experimental proto APIs MUST be prefixed with `experimental`
(e.g., `experimentalCountRelationships`). This reserves the unprefixed name
(e.g., `countRelationships`) for when the API is promoted to stable. On
promotion, add the unprefixed method and mark the prefixed one as
`@deprecated`.

## Public API Surface

See package sections above.

## Examples Manifest

| Directory | Demonstrates |
|-----------|-------------|
| `check_permission/` | Basic permission check, plus a caveated check with no context to show a `conditionalPermission` CheckResult, then resolving that conditional into a grant by supplying the missing context via `CheckOptions` (single-check and bulk) |
| `write_relationships/` | Writing relationships with transaction builder |
| `read_relationships/` | Reading relationships with async iterator |
| `lookup_resources/` | Resource lookup, incl. reading `permissionship`/`partialCaveat` |
| `lookup_subjects/` | Subject lookup, incl. wildcard `"*"` + `excludedSubjects` |
| `watch_changes/` | Watching for changes with a bounded consumer: subscribe, write, consume until the expected update arrives, `abort()`, and require the stream to release |
| `schema_management/` | Schema read/write |
| `bulk_operations/` | Bulk checks and imports |
| `expand_permission_tree/` | `expandPermissionTree` and walking the native `PermissionTree` |
| `call_deadlines/` | `defaultTimeoutMs` on `createSpiceDBClient`, a per-call `timeoutMs` override, and confirming bulk import isn't bounded by the unary default |
| `error_mapping/` | Recovering from `OUT_OF_RANGE` (stale ZedToken) and `UNAUTHENTICATED` without parsing a message |
| `insecure_opt_in/` | Why `insecure` is loopback-only, and the named opt-in a remote plaintext host requires |
| `retry_policy/` | Which calls are retried for you and which are not, counted server-side |
| `unrepresentable_values/` | Caller data that cannot convert fails loudly; unknown server enums degrade safely |
| `custom_tls/` | Reaching a SpiceDB behind a private CA with `tls.caCert`, and mutual TLS with `tls.clientCert`/`tls.clientKey`. Brings up its own TLS-terminated endpoint |
| `raw_escape_hatch/` | `raw()` — driving the generated Connect client directly for a proto field (`optionalTransactionMetadata`) and an RPC (`CheckPermission`) the idiomatic API does not expose |

## Typed Client Generation

`spicedb-gen` generates a type-safe wrapper for `@spicedb/client` from a
SpiceDB schema (`.zed` file). The generated code provides compile-time
validation of resource types, permissions, relations, and subject types.

### TypedClient

`TypedClient` wraps `SpiceDBClient`. It delegates all calls to the untyped
client after mapping typed arguments to the untyped API.

- `new TypedClient(client)` — wraps an existing `SpiceDBClient`
- `TypedClient.create(endpoint, token, options?)` — convenience constructor
  that creates a `SpiceDBClient` internally
- `tc.client` — escape hatch for untyped operations (schema management,
  deleteRelationships, experimental APIs, preconditions, watch, etc.)

### Factory Functions

Each schema definition generates a factory function:
- `Document(id)` — returns instance with `.view`, `.edit` (permissions) and
  `.viewer(subject)`, `.editor(subject)` (relations)
- `Document.view` — static property for `lookupResources` (no ID)
- Relation methods enforce subject type constraints at compile time

### Subject Type Constraints

- **Relations** accept only the directly declared subject types
- **Permissions** accept reachable subject types (computed from the transitive
  relation tree)
- Invalid subject types produce TypeScript compilation errors

### Intentionally Untyped

These operations are accessed via `tc.client`:
- Schema management (`writeSchema`, `readSchema`, `reflectSchema`, etc.)
- `deleteRelationships` (bulk filter-based)
- `expandPermissionTree`
- `importRelationships` / `exportRelationships`
- `watch` / `updates`
- Experimental APIs
- Preconditions (`mustNotMatch`, `mustMatch`)

## Changelog

See [CHANGELOG.md](CHANGELOG.md) for release notes.
