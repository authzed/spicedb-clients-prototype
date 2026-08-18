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
  Rust, Ruby, and C#. Only which typed class the code maps to changed here;
  both new error classes are exported from the package root.
  **`RESOURCE_EXHAUSTED` was subsequently removed from the retryable set** --
  see the 2026-08-18 retry-safety entry below, which is the shipped behavior.
  (This entry originally read "was already, and remains, treated as
  transient".)

- **2026-08-15**: `readRelationships()`, `lookupResources()`,
  `lookupSubjects()`, `exportBulkRelationships()`, and `watch()` now retry
  stream ESTABLISHMENT on transient errors -- as shipped, `UNAVAILABLE` and
  `ABORTED`; `RESOURCE_EXHAUSTED` was in that set when this landed and was
  removed by the 2026-08-18 retry-safety entry below -- reusing the same
  `isTransientError`
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

- **2026-08-18**: `importBulkRelationships` required a materialized array, and then held the
  dataset twice. The signature was `relationships: Relationship[]`, and the body ran
  `relationships.map(toProtoRelationship)` before streaming, so the caller's array and a full
  array of protos were both resident before a single byte went out. That is the wrong shape for
  the one method whose entire purpose is volume: a caller with a dataset larger than memory had
  no way to import it, however lazily they could produce it. It now accepts
  `Iterable<Relationship> | AsyncIterable<Relationship>` -- an array, a generator, an async
  generator, anything with `Symbol.iterator` or `Symbol.asyncIterator` -- and converts and batches
  relationships (1,000 per request message, unchanged) as they are pulled, so only one batch is
  ever resident.

  Widening only; every existing call site is unaffected, since arrays are iterable, and an array
  is still the right choice when the data is already in memory. This brings the last client into
  line: Go takes `iter.Seq`, C# `IAsyncEnumerable`, Java `Iterable`, Python `Iterable`, Ruby
  Enumerable, Rust `IntoIterator`.

  ```ts
  // Before and after -- unchanged:
  await client.importBulkRelationships([rel1, rel2, rel3]);

  // New: stream from a cursor without materializing anything.
  async function* fromCursor() {
    for await (const row of db.query("SELECT ...")) {
      yield relationship(`document:${row.id}`, "viewer", `user:${row.userId}`);
    }
  }
  await client.importBulkRelationships(fromCursor());
  ```

  The sequence is consumed exactly once, which is safe because a bulk import is a mutation and is
  never retried automatically (root DESIGN.md, "RULE: Automatic retry is for idempotent operations
  only"). A caller retrying by hand must pass a fresh iterable -- a spent generator yields nothing
  and would import zero relationships. New tests in `src/__tests__/import-streaming.test.ts` assert
  on *when* the caller's sequence is pulled (the first batch reaches the server before the
  generator is exhausted), not just on the returned count, which a buffering implementation would
  satisfy too.

- **2026-08-18**: Every streaming call (`readRelationships`, `lookupResources`, `lookupSubjects`,
  `watch`, `exportBulkRelationships`) leaked one HTTP/2 stream and one server-side SpiceDB
  dispatch, permanently, whenever a caller stopped consuming before the stream was exhausted --
  the single most common streaming idiom ("take the first N results, stop"). Connect-ES's
  server-streaming iterator deliberately omits `return()`/`throw()` (see its `run-call.js`, "We
  deliberately omit throw/return"), so a `for await` `break` never reached the transport, and the
  client never passed `CallOptions.signal` at all. Each streaming method now accepts an optional
  `signal?: AbortSignal` (a new `options` parameter for `readRelationships`/
  `exportBulkRelationships`, a new field on `LookupResourcesParams`/`LookupSubjectsParams`/
  `WatchOptions`) and internally links its own `AbortController` through `CallOptions.signal` on
  every attempt, aborting it in a `finally` block that runs on normal completion, a thrown error,
  AND a caller `break` (which resumes the generator via `.return()`, unwinding through the same
  `finally`) -- so the underlying HTTP/2 stream is released on abandonment regardless of whether
  the caller passes its own signal. See root DESIGN.md, "RULE: Abandoning a stream must release
  it".
- **2026-08-18**: Added `SpiceDBClient.close(): void` (and `SpiceDBProtoClient.close(): void` in
  `@spicedb/proto`) to release the underlying HTTP/2 connection deterministically. Idempotent --
  safe to call more than once. Previously there was no way to release the transport at all short
  of process exit; every streaming call shares one connection via `Http2SessionManager`, which
  `createGrpcTransport` now receives pre-built (rather than building one internally) specifically
  so `close()` has a handle to abort.
- **2026-08-18**: Watch resumability. `WatchOptions` gains
  `includeCheckpoints?: boolean`, which requests
  `WATCH_KIND_INCLUDE_CHECKPOINTS` (alongside
  `WATCH_KIND_INCLUDE_RELATIONSHIP_UPDATES`, since a non-empty
  `optionalUpdateKinds` replaces the server's implicit default rather than
  adding to it).
  - `WatchEvent.isCheckpoint` and `WatchEvent.schemaUpdated` both already
    existed and were both already assigned from the response on every event
    (`client.ts`'s `watch()`) — but since nothing could ever request
    `WATCH_KIND_INCLUDE_CHECKPOINTS` or `WATCH_KIND_INCLUDE_SCHEMA_UPDATES`,
    the server never had a reason to send either, so both were always
    `false` in practice and the `examples/watch_changes/`
    `if (event.isCheckpoint)`/`if (event.schemaUpdated)` branches were both
    unreachable. This task is in scope for checkpoints only: wired up
    `includeCheckpoints` to make `isCheckpoint` reachable, rather than
    adding a parallel field. `schemaUpdated` remains permanently `false` —
    still unreachable, since nothing yet requests
    `WATCH_KIND_INCLUDE_SCHEMA_UPDATES`; that field's `if` branch in the
    example is left as documented but presently dead code, same as before
    this change, pending a future schema-update-support task.
  - `WatchEvent.revision` was already populated from
    `WatchResponse.changesThrough` — the proto's resume token ("This token
    can be used in a subsequent WatchRequest to resume watching from this
    point") — but undocumented as such. Documented its resumability role on
    both `WatchEvent.revision` and `WatchOptions.startRevision`.
  - Without checkpoints, a watch on a quiet namespace behind an idle-timeout
    proxy (ALB, nginx, Envoy) is killed with no changes to resume from
    beyond the original `startRevision` — reprocessing everything since,
    possibly past the GC window.
  - `examples/watch_changes/index.ts` updated to request checkpoints and
    exercise both branches; `src/__tests__/watch-operation-mapping.test.ts`
    gains coverage asserting `includeCheckpoints` reaches the built
    `WatchRequest` and that a checkpoint event is distinguishable from one
    carrying updates.
- **2026-08-18**: Call deadlines, per root `DESIGN.md` "RULE: A unary call must have a
  deadline". Previously no method accepted a timeout and no client-level default existed, so a
  SpiceDB instance that accepted a connection but never answered hung every caller forever — the
  connection looks fine at the transport level, so no error is produced and there is nothing for
  retry logic to act on.
  - Every unary method gained an optional `timeoutMs` (milliseconds) — either a trailing
    `options?: { timeoutMs?: number }` parameter (`write`, `readSchema`, `writeSchema`,
    `diffSchema`, the three experimental counter methods), or a new
    field on an existing options type (`CheckOptions`, `DeleteOptions`,
    `ExpandPermissionTreeParams`, `ReflectSchemaOptions`, `ComputablePermissionsParams`,
    `DependentRelationsParams`) — passed straight through as Connect's `CallOptions.timeoutMs`.
    Additive; existing call sites are unaffected. `checkPermissions`/`checkAny`/`checkAll`'s
    classic variadic form still carries no options (unchanged, per its existing doc comment);
    the explicit-array form picks up `timeoutMs` via `CheckOptions` automatically.
  - `SpiceDBClientOptions` gained `defaultTimeoutMs`, applied to any unary call that doesn't
    supply its own `timeoutMs`. Defaults to 30 seconds (`DEFAULT_TIMEOUT_MS`), mirroring
    `authzed-node`'s `DEFAULT_DEADLINE_MS = 30_000` (its comment cites `grpc/grpc-node#541`).
    There is deliberately no way to construct a client whose unary calls have no bound at all.
  - Streaming methods (`readRelationships`, `lookupResources`, `lookupSubjects`, `watch`,
    `exportBulkRelationships`) do **not** accept `timeoutMs` and are **not** bound by
    `defaultTimeoutMs` — DESIGN.md's "Streaming calls MUST NOT inherit the unary default": these
    are long-lived by design (`watch` may legitimately run for the life of the process), and a
    30s cutoff would end a legitimate stream, which is a worse defect than the one this change
    fixes.
  - `DeadlineExceededError` (added earlier, but never actually produced by this client since
    nothing enforced a deadline) is now reachable: a timed-out call rejects with it, not a
    generic `SpiceDBError`. `Code.DeadlineExceeded` is not in `TRANSIENT_CODES`, so a timeout is
    never auto-retried.
  - **Fix round 1 correction:** `importBulkRelationships` also gained an `options?: {
    timeoutMs?: number }` parameter, but — unlike the unary methods above — it is
    client-streaming, not unary, and is now explicitly **excluded** from `defaultTimeoutMs`: its
    duration scales with the size of the caller's dataset, not with server latency, so no fixed
    default is correct for it (root DESIGN.md, "RULE: A unary call must have a deadline",
    clause 3, amended to cover client-streaming and bidirectional RPCs, not only
    server-streaming). Omitting `options.timeoutMs` now means unbounded (Connect's
    `createDeadlineSignal` sets no timer when `timeoutMs` is `undefined`); passing it still
    bounds the call. An earlier version of this fix incorrectly resolved `timeoutMs` against
    `defaultTimeoutMs` for this call, which would have silently aborted large, legitimate
    multi-minute imports at 30 seconds.
  - **Fix round 1 correction (H1):** `createSpiceDBClient`'s inline options type — the documented
    construction path this file's own example above uses — had NOT been widened to include
    `defaultTimeoutMs`, so the documented `createSpiceDBClient(endpoint, token, {
    defaultTimeoutMs: 5000 })` literally failed to type-check (`TS2353`). Nothing in the test
    suite constructed through that factory, so the gap went uncaught. Widened the factory's
    options type, regenerated `etc/client.api.md` (purely additive), and `spicedb-gen`'s
    generated `TypedClient.create()` — which forwarded the same narrow `{ insecure?: boolean }`
    type — now forwards the full options object (`headers`, `maxRetries`, `defaultTimeoutMs`)
    too.
  - `spicedb-gen`'s TypeScript typed-client template needed one change (see the H1 correction
    above for `TypedClient.create()`); its generated `check()` already forwarded a
    caller-supplied `options?: CheckOptions` straight through to `checkPermission`, so it picked
    up `timeoutMs` automatically with no template change. Verified by regenerating
    `testdata/typescript/permissions.ts` against the updated package and type-checking clean
    (including `type_errors.ts`'s `@ts-expect-error` assertions, unaffected).
  - New `src/__tests__/deadline.test.ts`, using `createRouterTransport` (from
    `@connectrpc/connect`) instead of the `vi.fn()`-mocked `proto` field used elsewhere in this
    suite — deadline enforcement lives in Connect's own transport machinery
    (`protocol/signals.js`'s `createDeadlineSignal`), which a mock bypasses entirely.
    `createRouterTransport` runs the real client-side transport stack against a real (in-process)
    handler that deliberately stalls: a unary call against a stub that never responds rejects
    with `DeadlineExceededError` well before the stall completes, a per-call `timeoutMs`
    overrides a much larger client default, a streaming call outlives a tiny unary default
    instead of inheriting it, bulk import is both unbounded by the default and still honors an
    explicit `timeoutMs`, and constructing through `createSpiceDBClient` (not just `new
    SpiceDBClient`) with `defaultTimeoutMs` actually applies it end-to-end. Every test is wrapped
    in a watchdog so a regression fails the suite instead of hanging CI.
  - New `examples/call_deadlines/`, run against a real SpiceDB rather than a mock: constructs a
    client via the documented `defaultTimeoutMs` option on `createSpiceDBClient`, overrides it
    per-call, and confirms bulk import is unbounded by default.
- **2026-08-18**: Retry safety, per root `DESIGN.md` "RULE: Automatic retry is for idempotent
  operations only". Three changes:
  - `RESOURCE_EXHAUSTED` is no longer retried. In SpiceDB it signals memory load-shed (retrying
    adds load to an already-overloaded server) or a deterministic `MaxDepthExceeded` (retrying can
    never succeed — it re-runs the most expensive class of check several times before surfacing
    the same error). Previously `Code.ResourceExhausted` was in `errors.ts`'s `TRANSIENT_CODES`.
  - Mutations (`write`, `deleteRelationships`, `writeSchema`, `importBulkRelationships`, and the
    experimental counter register/unregister calls) are no longer retried on a transient error,
    even though the underlying code is retryable. A `write()` carrying `OPERATION_CREATE` or
    preconditions is not idempotent: if it commits and the response is lost (a rolling restart, a
    proxy dropping the connection), a retry would surface `ALREADY_EXISTS`/`FAILED_PRECONDITION`
    for a write that in fact succeeded, and the caller would wrongly conclude it had failed. Reads
    still retry automatically. All six mutation call sites previously routed through `withRetry`;
    they now go through a new private `callOnce`, which converts the error without retrying.
  - Backoff is now full-jitter (`Math.random() * cap`) instead of plain exponential doubling.
    Without jitter, every client in a fleet retries on the same schedule after a server restart,
    turning the recovery into a thundering herd.

  `src/__tests__/errors.test.ts`'s `isTransientError` suite had an `it("returns true for
  ResourceExhausted", ...)` case; it is renamed `"returns false for ResourceExhausted"` and
  inverted, since the old assertion was exactly the defect this fixes. New coverage in
  `src/__tests__/unary-retry.test.ts` (a mutation is attempted exactly once on a retryable error;
  a read is retried; `RESOURCE_EXHAUSTED` is never retried; backoff varies between calls).
- **2026-08-18**: `checkPermissions`/`checkAny`/`checkAll` did not verify that `checkBulkPermissions`
  returned as many pairs as were requested — the result array came straight from `resp.pairs.map(...)`,
  and nothing compared its length to the request's. The proto guarantees pairs are returned in
  request order but says nothing about count, so a short response silently desynced `results[i]`
  from `checks[i]` for every item after the gap: one resource's answer attributed to another.
  `checkAll` then ran `.every()` over that short array and returned `true` where the dropped checks
  would have denied. The `checkPermissions` doc already promised "one per check request, in the same
  order" — a claim nothing enforced. A length mismatch in either direction now throws a
  `SpiceDBError` naming both counts. This is the same guard the other six clients received.
- **2026-08-18**: a malformed `CheckBulkPermissionsPair` — the `response` oneof set to neither `item`
  nor `error` — now throws a `SpiceDBError` instead of degrading to an `unspecified` `CheckResult`.
  `.map()` preserves index alignment so this case never caused the desync above, and `unspecified`
  is non-granting, so the old behavior was fail-closed rather than unsafe. It was changed anyway
  because an `unspecified` result is indistinguishable from a genuine "no permission" answer from
  the server, which hid a broken server behind a plausible-looking denial — and because it was the
  one remaining client out of seven that did not throw here.
- **2026-08-18**: **`watch()` mapped any unrecognized relationship-update operation — including
  `OPERATION_UNSPECIFIED` and any future wire value — to `"touch"`.** `"touch"` was the `switch`
  statement's `default` arm rather than a `case`, so an operation the client could not interpret
  was reported to the caller as a write. A cache or index mirror consuming the watch stream would
  upsert a relationship on an update it did not understand — one that may in fact have been a
  delete. `WatchChange["operation"]` gains a fourth member, `"unspecified"` (a type change, but
  this client is unreleased), `TOUCH` becomes an explicit `case`, and the `default` arm now yields
  `"unspecified"`. Root `DESIGN.md`, "RULE: A conversion that cannot preserve meaning must fail",
  clause 2: server-supplied values the client does not recognise MUST NOT raise, and MUST map to
  the safe, non-permissive default — never a grant, and never a write. Callers switching on
  `operation` must now handle `"unspecified"`; treat it as "re-read the relationship" or fail the
  mirror closed, never as a write.
- **2026-08-18**: `toProtoRelationshipFilter()` (used by `readRelationships`, `deleteRelationships`,
  `exportBulkRelationships`, `Transaction.mustMatch`/`mustNotMatch`, and
  `experimentalRegisterRelationshipCounter`) silently dropped `subjectId`/`subjectRelation` when
  `subjectType` was not set, instead of throwing. `optionalSubjectFilter` was only built inside
  `if (filter.subjectType)`, so `{ resourceType: "document", subjectId: "alice" }` produced a
  proto `RelationshipFilter` with **no subject constraint at all**, while the filter object itself
  still carried `subjectId: "alice"` — a caller reading the object back would see the constraint
  they set; the server would not. `deleteRelationships(cs, filter)` called with that filter
  deleted every relationship on every document, not just alice's — a correct-looking
  user-offboarding call that wipes the whole system. The wire's `SubjectFilter.subjectType` is a
  required field, so there is no way to express a subject ID/relation constraint without it,
  which makes silent widening the one unsafe resolution — `toProtoRelationshipFilter()` now
  throws `InvalidArgumentError` naming the field that was set without `subjectType`, per root
  `DESIGN.md` "RULE: A conversion that cannot preserve meaning must fail", clause 1
  (caller-supplied data the client cannot represent MUST raise a typed error). No signature
  change: the function already threw for other invalid inputs elsewhere in this file, and every
  call site already propagates synchronous throws (async generators throw on first `.next()`;
  `withRetry`'s `isTransientError` check correctly does not retry a `SpiceDBError`). No
  pre-existing test asserted the silent-drop behavior, so none needed replacing.

- **2026-08-18**: `checkAll()` returned `true` for zero checks —
  `Array.prototype.every` is vacuously `true` over an empty array. Root
  `DESIGN.md`'s "An aggregate over zero checks is not a grant" clause names
  the hazard: a gate like `checkAll(cs, "edit", ...docs.map(toCheck))` was
  silently granted whenever the derived checks array came up empty — a
  filter that matched nothing, an upstream returning `[]`. `checkAll` now
  guards the empty case before the aggregate and returns `false` without
  calling `checkBulkPermissions`. `checkAny` is unchanged — it was already
  correctly `false` on empty (`Array.prototype.some`).

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

- **2026-08-18** (behavioral; no signature change): the two entries below change what existing,
  unmodified call sites do. They are listed here because neither announces itself -- nothing
  fails to compile, and the difference only shows up under load or against a slow query.
  - **Unary calls are now bounded by a 30-second default** -- see "Call deadlines" in this
    release. A call that legitimately takes longer than 30 s (most plausibly a deep
    `expandPermissionTree` on a large graph, or a filtered delete sweeping many pages) now fails
    with a deadline error where it previously ran to completion. Raise it with
    `createSpiceDBClient(..., { defaultTimeoutMs })`, or pass `timeoutMs` on the individual call.
    There is deliberately no way to ask for no bound at all on a unary call.
  - **Mutations are no longer retried automatically** -- see "Retry safety" in this release.
    `write`, `deleteRelationships`, `writeSchema`, `importBulkRelationships`, and the experimental
    counter register/unregister calls now surface a transient `UNAVAILABLE` to the caller on the
    first attempt rather than retrying. This is the correct default (replaying a non-idempotent
    write can report failure for a write that in fact committed), but a caller who was relying on
    the client to ride out a rolling restart must now retry themselves, knowing their own
    idempotency. Reads are unaffected. `RESOURCE_EXHAUSTED` is no longer retried either, on reads
    or mutations.

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
