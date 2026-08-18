# Changelog

## Unreleased

### Added

- **2026-08-15**: `readRelationships()`, `lookupResources()`,
  `lookupSubjects()`, `exportBulkRelationships()`, and `watch()` now retry
  stream ESTABLISHMENT on transient errors (`UNAVAILABLE`,
  `RESOURCE_EXHAUSTED`, `ABORTED`), reusing the same `isTransientError`
  predicate and exponential backoff as `withRetry`. Retry is scoped strictly
  to (re-)opening the stream: once any item has been yielded to the caller
  from the current stream, a later transient error is never retried — it is
  surfaced as-is, since retrying after a yield would replay/duplicate
  already-delivered items. `watch()` in particular never retries mid-watch,
  only before the first update of a given `watch()` call is yielded. Mirrors
  spicedb-python's `_should_retry_establishment` approach
  (`spicedb-python/spicedb/client.py`). No public API change.

- **2026-08-15**: `deleteRelationships()` now accepts an optional
  `DeleteOptions` second argument with `mustMatch`/`mustNotMatch`
  (each `RelationshipFilterOptions[]`) and `limit`, mirroring spicedb-go's
  `WithDeleteMustMatch`/`WithDeleteMustNotMatch`/`WithDeleteLimit`
  (`spicedb-go/client/relationships.go`) and spicedb-python's
  `delete_relationships` keyword arguments. Previously the proto's
  `optionalPreconditions`/`optionalLimit` fields were unreachable, so there
  was no way to do a precondition-guarded or bounded delete. Preconditions
  are built the same way as `Transaction.mustMatch`/`mustNotMatch`. Setting
  `limit` also sets `optionalAllowPartialDeletions: true` — the server
  otherwise rejects a limited delete that finds more matches than the limit.
  Additive — existing `deleteRelationships(filter)` call sites are
  unaffected (no preconditions, no limit, `optionalAllowPartialDeletions:
  false`, same as before). `DeleteOptions` is exported from the package
  root.

  ```typescript
  // Only delete if an `owner` relationship still exists on the resource:
  const revision = await client.deleteRelationships(filter, {
    mustMatch: [{ resourceType: "document", resourceId: "1", resourceRelation: "owner" }],
    limit: 1000,
  });
  ```

### Fixed

- **2026-08-14**: Enabled `stripInternal` in `tsconfig.json` so `@internal`-tagged
  members are actually removed from the shipped `.d.ts` (previously `@internal`
  JSDoc had no emit effect on its own). `Consistency._toProto()`/`_wrap()` and
  `Transaction.updates`/`preconditions`/`metadata` — along with their
  `@spicedb/proto` type imports — no longer appear in `dist/consistency.d.ts`
  or `dist/types.d.ts`. No public API change; these members were never
  intended to be public.

### Breaking Changes

- **2026-08-15**: `lookupResources`/`lookupSubjects` now yield native result
  objects instead of bare `string` IDs, closing an over-grant risk: the
  previous `string`-only shape silently dropped `excludedSubjects` for
  wildcard (`user:*`) matches, so a caller iterating IDs alone could treat a
  wildcard-excluded subject as granted. `lookupResources` now yields
  `LookupResource` (`resourceId`, `permissionship`, `partialCaveat?`);
  `lookupSubjects` now yields `LookupSubject` (`subject: ResolvedSubject`,
  `excludedSubjects: ResolvedSubject[]`). Both use the shared `Permissionship`
  (`"unspecified" | "hasPermission" | "conditionalPermission"`) and
  `PartialCaveatInfo` types. Mirrors spicedb-go's
  `client/lookup_types.go`/`lookup.go`, including its fallback to the
  deprecated `subjectObjectId`/`excludedSubjectIds` proto fields for servers
  that don't yet populate the modern `subject`/`excludedSubjects` fields.
  All new types are exported from the package root.

  Before:
  ```ts
  for await (const resourceId of client.lookupResources(params, cs)) {
    grant(resourceId); // string only — no permissionship signal
  }
  for await (const subjectId of client.lookupSubjects(params, cs)) {
    grant(subjectId); // wildcard "*" treated as unconditional — over-grant risk
  }
  ```
  After:
  ```ts
  for await (const resource of client.lookupResources(params, cs)) {
    if (resource.permissionship !== "hasPermission") continue; // skip conditional
    grant(resource.resourceId);
  }
  for await (const result of client.lookupSubjects(params, cs)) {
    const excluded = new Set(result.excludedSubjects.map((s) => s.subjectId));
    if (result.subject.subjectId === "*" && excluded.has(callerId)) continue;
    grant(result.subject.subjectId);
  }
  ```
- **2026-08-14**: Removed `@bufbuild/protobuf`'s `JsonObject` from the public
  API. `Relationship.caveatContext`, `CheckRequest.context`,
  `LookupResourcesParams.context`, `LookupSubjectsParams.context`,
  `WatchEvent.metadata`, and `Transaction.withMetadata()` now use the native
  `Record<string, unknown>` type instead. No call-site changes are required
  for plain object literals; only code that explicitly imported `JsonObject`
  from `@bufbuild/protobuf` to type these values needs to switch to
  `Record<string, unknown>`.

  Before:
  ```ts
  import type { JsonObject } from "@bufbuild/protobuf";
  const ctx: JsonObject = { key: "value" };
  ```
  After:
  ```ts
  const ctx: Record<string, unknown> = { key: "value" };
  ```
- **2026-08-14**: `expandPermissionTree`, `reflectSchema`, `diffSchema`,
  `computablePermissions`, and `dependentRelations` now return fully-typed
  native structures instead of `unknown`/`unknown[]` proto leakage.
  `expandPermissionTree`'s `treeRoot` is now a native `PermissionTree`
  (mirrors spicedb-go's `PermissionTree`/`IntermediateNode`/`LeafNode`/
  `ObjectRef`/`SubjectRef`/`TreeOperation`); `reflectSchema`'s `definitions`/
  `caveats` are now `SchemaDefinition[]`/`SchemaCaveat[]`; `diffSchema`'s
  `diffs` are now `SchemaDiff[]`; `computablePermissions`/`dependentRelations`
  continue to return `RelationReference[]`, now built via a shared mapper.
  All new types are exported from the package root.

  Before:
  ```ts
  const { treeRoot } = await client.expandPermissionTree(cs, params);
  const objId = (treeRoot as any).expandedObject.objectId; // unknown, required casting
  ```
  After:
  ```ts
  const { treeRoot } = await client.expandPermissionTree(cs, params);
  const objId = treeRoot.expandedObject.objectId; // fully-typed PermissionTree
  ```
- **2026-08-14**: `Consistency` is now an opaque native class instead of a
  re-exported protobuf-es type. `full()`, `minLatency()`, `atLeast()`,
  `snapshot()`, `atLeastOrFull()`, and `atLeastOrMinLatency()` now return the
  native `Consistency` class; the underlying proto message is no longer part
  of the public API. All `SpiceDBClient` methods that accept `consistency`
  unwrap it internally via an `@internal` `_toProto()` method before building
  the proto request. No call-site changes are required — construct consistency
  values only via the exported helper functions, never directly.

  Before:
  ```ts
  import type { Consistency as ProtoConsistency } from "@spicedb/proto";
  const cs: ProtoConsistency = full()._toProto(); // reaching into internals
  ```
  After:
  ```ts
  const cs = full(); // opaque Consistency; pass directly to client calls
  ```

## 0.1.0 (2026-03-16)

Initial release of the idiomatic TypeScript SpiceDB client.

### Features

- **2026-03-16**: Initial implementation of the idiomatic TypeScript client.
  Created `src/client.ts`, `src/types.ts`, `src/consistency.ts`, `src/errors.ts`,
  `src/index.ts`. Full API coverage for all non-deprecated proto APIs:
  PermissionsService (checkPermission, checkPermissions, checkAny, checkAll,
  readRelationships, write, deleteRelationships, lookupResources,
  lookupSubjects, expandPermissionTree, importBulkRelationships,
  exportBulkRelationships), SchemaService (readSchema, writeSchema,
  reflectSchema, computablePermissions, dependentRelations, diffSchema),
  WatchService (watch), and ExperimentalService relationship counters
  (experimentalRegisterRelationshipCounter, experimentalCountRelationships,
  experimentalUnregisterRelationshipCounter). Added 8 examples covering all
  major use cases. Added experimental naming convention to DESIGN.md.
