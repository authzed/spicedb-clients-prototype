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
const results = await client.checkPermissions(consistency, ...rels); // boolean[]
const allowed = await client.checkPermission(consistency, rel);      // boolean
const any = await client.checkAny(consistency, ...rels);             // boolean
const all = await client.checkAll(consistency, ...rels);             // boolean
```

All use BulkCheckPermissions under the hood.

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
  permissionship: Permissionship; // "unspecified" | "hasPermission" | "conditionalPermission"
  partialCaveat?: PartialCaveatInfo; // set when permissionship is "conditionalPermission"
}

interface ResolvedSubject {
  subjectId: string;
  permissionship: Permissionship;
  partialCaveat?: PartialCaveatInfo;
}

interface LookupSubject {
  subject: ResolvedSubject;
  excludedSubjects: ResolvedSubject[]; // wildcard "*" exclusions — MUST check
}
```

Callers MUST check `permissionship` before treating a result as a full
grant, and — critically — when `subject.subjectId` is the wildcard `"*"`,
MUST check `excludedSubjects` before treating the wildcard as a blanket
grant. Mirrors spicedb-go's `client/lookup_types.go`.

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
| `check_permission/` | Basic permission check |
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
