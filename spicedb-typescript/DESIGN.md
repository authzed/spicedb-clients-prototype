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

for await (const id of client.lookupResources(params, consistency)) {
  // ...
}
```

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

## Public API Surface

See package sections above.

## Examples Manifest

| Directory | Demonstrates |
|-----------|-------------|
| `check_permission/` | Basic permission check |
| `write_relationships/` | Writing relationships with transaction builder |
| `read_relationships/` | Reading relationships with async iterator |
| `lookup_resources/` | Resource lookup |
| `lookup_subjects/` | Subject lookup |
| `watch_changes/` | Watching for changes |
| `schema_management/` | Schema read/write |
| `bulk_operations/` | Bulk checks and imports |

## Changelog

<!-- Claude appends here when making changes, with date + what changed -->
