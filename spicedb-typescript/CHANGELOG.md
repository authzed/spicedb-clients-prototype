# Changelog

## Unreleased

### Added

- **2026-08-17**: `checkPermission`/`checkPermissions`/`checkAny`/`checkAll`
  gain a call-level default caveat context via a new `CheckOptions` type
  (`{ context?: Record<string, unknown> }`). Previously the only way to
  supply caveat context was per-item, on each `CheckRequest.context` — there
  was no way to set one default across a whole check/bulk-check call, so a
  caller checking many items with the same caveat context had to repeat it
  on every `CheckRequest`. `checkPermission` accepts `CheckOptions` as a new
  optional third argument. `checkPermissions`/`checkAny`/`checkAll` gain a
  second, explicit-array overload — `(consistency, checks: CheckRequest[],
  options?: CheckOptions)` — since their existing variadic form
  (`consistency, ...checks`) has nowhere to put a trailing options argument;
  that variadic form is completely unchanged and never produces a
  call-level default. The proto wire has no request-level context field
  (`CheckBulkPermissionsRequest` carries no `context`, only
  `CheckBulkPermissionsRequestItem.context`), so `options.context` is fanned
  out onto every item at request-build time and merged key-by-key with that
  item's own `context`: the item's own keys win on conflict, and call-level
  keys the item doesn't mention are retained (not a wholesale replacement).
  If neither is supplied, no context field is set on the request (never an
  empty Struct). Purely additive — no existing call site changes.
  `CheckOptions` is exported from the package root.

  ```typescript
  // Per-item context (existing, unchanged):
  await client.checkPermission(consistency, { ...check, context: { now: 42 } });

  // New: a call-level default, applied to every item in a bulk check:
  await client.checkPermissions(
    consistency,
    [check1, check2],
    { context: { now: 42 } },
  );
  ```

- **2026-08-17**: `LookupResource` and `LookupSubject` gain a `lookedUpAt`
  field: the revision that result was computed at (from the response's
  `looked_up_at` ZedToken). It is identical for every item yielded by a
  single `lookupResources`/`lookupSubjects` call — a property of the call,
  not of the individual resource/subject. Thread it into
  `atLeast()`/`atLeastOrFull()` for read-your-writes on a later call.
  Additive — existing destructuring of `LookupResource`/`LookupSubject`
  continues to work unchanged. Mirrors spicedb-go's
  `LookupResource.LookedUpAt`/`LookupSubject.LookedUpAt`
  (`client/lookup_types.go`).

- **2026-08-16**: Added `DeadlineExceededError` and `ResourceExhaustedError`
  to the typed error hierarchy, and fixed `RESOURCE_EXHAUSTED` (e.g. a rate
  limit) to map to the new `ResourceExhaustedError` instead of being folded
  into `UnavailableError`. This brings TypeScript's error hierarchy in line
  with the canonical nine-type set already present in Go, Java, Python,
  Rust, Ruby, and C#. `isTransientError()`'s behavior is unchanged —
  `RESOURCE_EXHAUSTED` was already, and remains, treated as transient; only
  which typed class it maps to changed. Both new error classes are exported
  from the package root.

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

- **2026-08-17**: A per-item error from `checkPermissions()`'s underlying
  `CheckBulkPermissions` call (a permission-denied, an invalid-argument, an
  internal server error, etc. scoped to one specific check) is now thrown as
  a typed `SpiceDBError` via the same code -> error-class mapping as a
  top-level RPC failure. Previously it was silently coerced into `false` —
  indistinguishable from a real denial, and the caller never learned an
  error occurred at all. New `toSpiceDBErrorFromStatus()` in `errors.ts`
  converts the `google.rpc.Status`-shaped per-item error (its numeric
  `code` matches Connect's `Code` enum, since both mirror the standard gRPC
  status codes) through the existing `toSpiceDBError()` mapping.

- **2026-08-14**: Enabled `stripInternal` in `tsconfig.json` so `@internal`-tagged
  members are actually removed from the shipped `.d.ts` (previously `@internal`
  JSDoc had no emit effect on its own). `Consistency._toProto()`/`_wrap()` and
  `Transaction.updates`/`preconditions`/`metadata` — along with their
  `@spicedb/proto` type imports — no longer appear in `dist/consistency.d.ts`
  or `dist/types.d.ts`. No public API change; these members were never
  intended to be public.

### Breaking Changes

- **2026-08-17**: `checkPermission()` now returns `CheckResult` instead of
  `boolean`, and `checkPermissions()` now returns `CheckResult[]` instead of
  `boolean[]` — closing a fail-open. Previously, both methods collapsed
  `HAS_PERMISSION` and `CONDITIONAL_PERMISSION` together into `true`
  (`resp.permissionship === HAS_PERMISSION || resp.permissionship ===
  CONDITIONAL_PERMISSION`), so a caveated relationship whose context was not
  supplied at check time was granted exactly as if the server had confirmed
  it — this client's own JSDoc documented it as intentional
  ("Caveated permissions return `true`"). `CheckResult` — a class, so
  `hasPermission()` travels with the data — carries `permissionship`
  (`Permissionship`, now with a fourth value, `"noPermission"`, alongside
  `"unspecified"` | `"hasPermission"` | `"conditionalPermission"`),
  `missingContext: string[]` (from `partial_caveat_info`), `checkedAt:
  string` (from `checked_at`), and `hasPermission(): boolean` — `true` ONLY
  for `permissionship === "hasPermission"`. `checkAny()`/`checkAll()` keep
  returning `boolean` but now count only `hasPermission() === true` results
  as granted; a conditional result never counts, even for `checkAny()`. This
  is the TypeScript instance of a change applied identically across all
  seven SpiceDB clients; mirrors spicedb-go's `CheckResult`
  (`client/check_types.go`).

  Before:
  ```ts
  const allowed = await client.checkPermission(cs, check);
  if (allowed) grant(); // conditional (caveat context missing) ALSO ran this — the fail-open

  const results = await client.checkPermissions(cs, ...checks);
  if (results[0]) grant();
  ```
  After:
  ```ts
  const result = await client.checkPermission(cs, check);
  if (result.hasPermission()) grant(); // false for a conditional result — closed

  const results = await client.checkPermissions(cs, ...checks);
  if (results[0].hasPermission()) grant();
  // A conditional result also carries what's missing and when it was checked:
  if (result.permissionship === "conditionalPermission") {
    console.log("missing caveat context:", result.missingContext);
  }
  ```

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
