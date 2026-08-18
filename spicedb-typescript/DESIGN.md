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

Automatic retry with exponential backoff for transient errors.

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
| `watch_changes/` | Watching for changes |
| `schema_management/` | Schema read/write |
| `bulk_operations/` | Bulk checks and imports |

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
