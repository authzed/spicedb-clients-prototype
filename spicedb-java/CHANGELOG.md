# Changelog

## Unreleased

### Added

- **2026-08-17**: Caveat CHECK-TIME context on the check surface. Two forms, both additive
  overloads — the existing `checkPermission`/`checkPermissions`/`checkAny`/`checkAll` signatures
  are byte-for-byte unchanged:
  - **Call-level**: `checkPermission(consistency, permission, relationship, context)`,
    `checkPermissions(consistency, permission, context, relationships...)`,
    `checkAny(consistency, permission, context, relationships...)`,
    `checkAll(consistency, permission, context, relationships...)` — a `Map<String, Object>`
    default applied to every relationship in the call.
  - **Per-item**: `Relationship.withCheckContext(context)` — flows through even the plain,
    context-less overloads.

  **Merge rule (key-level, item wins)**: `{...callLevel, ...item}` — an item's own context
  overrides the call-level default per-key; call-level keys the item doesn't mention are retained,
  never wholesale-replaced. When neither is supplied, no `context` field is set on the wire at all
  (never an empty `Struct`). This is what makes `CheckResult.missingContext()` actionable: a caller
  can now resolve a `CONDITIONAL_PERMISSION` into a grant by supplying the named keys at check time,
  instead of only being able to observe that they were missing.

  `Relationship` gains a `checkContext` field (10th, distinct from the existing write-time
  `caveatContext`) and `withCheckContext(Map<String, Object>)`, mirroring `withCaveat`/
  `withExpiration`. `checkContext` is read ONLY by the check-request builder
  (`checkItemFromRel`); `caveatContext` is read ONLY by the write-request builder
  (`toProtoRelationship`) — the two are pinned distinct so a check-time context can never leak into
  a stored relationship.

  ```java
  var callLevel = Map.<String, Object>of("now", 42, "region", "us");
  var item0 = Relationship.of("document", "doc1", "viewer", "user", "alice")
      .withCheckContext(Map.of("region", "eu"));
  var item1 = Relationship.of("document", "doc2", "viewer", "user", "bob");
  // item0 -> {now: 42, region: "eu"}; item1 -> {now: 42, region: "us"} (call-level default retained)
  List<CheckResult> results = client.checkPermissions(consistency, "view", callLevel, item0, item1);
  ```

- **2026-08-17**: `checkPermission`/`checkPermissions` now return a `CheckResult`/`List<CheckResult>` instead of `boolean`/`List<Boolean>`. `CheckResult` carries the server's full three-valued `permissionship` (`HAS_PERMISSION`, `NO_PERMISSION`, `CONDITIONAL_PERMISSION`), `missingContext` (the caveat context keys the server needed and did not receive), and `checkedAt` (the `ZedToken` revision the check was evaluated at — feed it to `Consistency.atLeast` for read-your-writes). `hasPermission()` is true ONLY for `HAS_PERMISSION`, per root DESIGN.md's "RULE: Only an unconditional grant is true". `checkAny`/`checkAll` are unchanged in shape (still `boolean`) but now explicitly count only `hasPermission()` results — a `CONDITIONAL_PERMISSION` never contributes to a `true`. See **Breaking Changes** below for the full migration.

  `checkPermission`/`checkPermissions` always call `CheckBulkPermissions` — there was and is no production call site for the non-bulk `CheckPermission` RPC (matches `spicedb-go`/`spicedb-python`/`spicedb-ruby`/`spicedb-csharp`). `CheckBulkPermissionsResponseItem` carries no per-item `checked_at` of its own; the single response-level token is now propagated onto every `CheckResult` in a batch.

- **`LookupResult.Permissionship` gains `NO_PERMISSION`** and is now shared by both the check and lookup surfaces (previously lookup-only, with three values). Lookups still never yield `NO_PERMISSION` — a subject/resource pair lacking the permission is simply absent from a lookup stream — but a check is always answering a yes/no/conditional question about one specific pair, so `NO_PERMISSION` is a real, expected `CheckResult.permissionship()` value. This is purely additive to the enum; existing `HAS_PERMISSION`/`CONDITIONAL_PERMISSION`/`UNSPECIFIED` usages are unaffected other than needing to handle the new case in an exhaustive `switch`.

- **`LookupResult.LookupResource` and `LookupResult.LookupSubject` gain a `lookedUpAt` field** — the `ZedToken` revision the result was computed at (identical for every item yielded by a single `lookupResources`/`lookupSubjects` call; it is a property of the call, not of the individual resource/subject). Threadable into `Consistency.atLeast` the same way `CheckResult.checkedAt` and `write()`'s returned revision are.

- **2026-08-16**: 5 new typed exceptions — `FailedPreconditionException`, `UnavailableException`, `CancelledException`, `DeadlineExceededException`, `ResourceExhaustedException` — plus the matching `ErrorMapper` switch arms, bringing Java's mapped gRPC status codes from 4 to the canonical 9 (matching Ruby/C#). Previously these 5 codes fell through to the untyped base `SpiceDBException`, so e.g. a `FAILED_PRECONDITION` from a schema mismatch was indistinguishable from any other unmapped error without string-matching the message.

- **2026-08-15**: The 5 streaming methods (`readRelationships`, `lookupResources`,
  `lookupSubjects`, `exportRelationships`, `updates`) now retry stream/page
  **ESTABLISHMENT** on transient errors (`{UNAVAILABLE, RESOURCE_EXHAUSTED, ABORTED}`),
  reusing the same backoff and `MAX_RETRIES` budget as unary calls (reset per page for
  the paginated methods; per-stream for `lookupSubjects`/`updates`, which have no
  cursor). A transient error is retried ONLY while nothing has been read yet from the
  current stream/page — once any item has been read, the error is mapped to the typed
  `SpiceDBException` and rethrown instead, never retried, so callers can never see a
  replayed/duplicated item. `updates` in particular only retries the initial watch
  open — never mid-watch — and, to make that safe without blocking the caller on the
  first watch event, now opens its underlying gRPC call lazily (on the first pull from
  the returned `Stream`) instead of eagerly inside the `updates()` call itself. No API
  shape change.

  Previously, wrapping only the blocking-stub call in the existing retry helper did
  **not** actually retry anything for grpc-java's blocking server-streaming: the RPC's
  outcome only surfaces on the returned iterator's first `hasNext()`/`next()` call,
  which happened outside the retry loop.

- **`deleteRelationships` preconditions and limit**: added `deleteRelationships(Filter, DeleteOptions)`, an additive overload accepting optional MUST_MATCH/MUST_NOT_MATCH preconditions and a per-request page-size override, mirroring `spicedb-go`'s `WithDeleteMustMatch`/`WithDeleteMustNotMatch`/`WithDeleteLimit` (`client/relationships.go`). `DeleteOptions` is an immutable record with `Filter`-style `withMustMatch`/`withMustNotMatch`/`withLimit` builder methods. The existing `deleteRelationships(Filter)` overload is unchanged — it delegates to `DeleteOptions.none()` (no preconditions, 1,000-item page size), same as before.

  ```java
  var options = SpiceDBClient.DeleteOptions.none()
      .withMustMatch(existsFilter)
      .withLimit(500);
  String revision = client.deleteRelationships(filter, options);
  ```

### Breaking Changes

- **2026-08-17**: `checkPermission` now returns `CheckResult` instead of `boolean`, and `checkPermissions` now returns `List<CheckResult>` instead of `List<Boolean>`. This client is unreleased, so there is no deprecated boolean-returning overload — callers must migrate to `hasPermission()`. `checkAny`/`checkAll` are unchanged in shape.

  A previously-shipped client collapsed `CONDITIONAL_PERMISSION` to `true`, granting access on a caveat that was never evaluated — see root DESIGN.md, "RULE: Only an unconditional grant is true". `CheckResult` makes that state impossible to silently ignore: `if (result)` does not compile (`CheckResult` is a record, not a `boolean`), so every caller must go through `hasPermission()` or compare `permissionship()` explicitly.

  Before:
  ```java
  boolean allowed = client.checkPermission(
      consistency, "view", Relationship.of("document", "doc1", "view", "user", "alice"));
  ```
  After:
  ```java
  CheckResult result = client.checkPermission(
      consistency, "view", Relationship.of("document", "doc1", "view", "user", "alice"));
  boolean allowed = result.hasPermission(); // false for CONDITIONAL_PERMISSION, NOT just NO_PERMISSION

  // The three-valued answer is now inspectable directly when the distinction matters:
  if (result.permissionship() == LookupResult.Permissionship.CONDITIONAL_PERMISSION) {
      // the server found a matching caveated relationship but couldn't evaluate it —
      // result.missingContext() lists which caveat parameters were not supplied
  }
  ```

- **2026-08-15**: `lookupResources`/`lookupSubjects` now yield native `LookupResult` records instead of bare `String`s. Each result carries the `permissionship` (full grant vs conditional on caveat context) and, for `lookupSubjects`, the `excludedSubjects` of a wildcard (`"*"`) match — previously dropped entirely, which meant callers treating a wildcard `Stream<String>` result as a blanket grant had **no way to know which subjects were actually excluded from it** (an over-grant risk). Mirrors `spicedb-go`'s `LookupResource`/`LookupSubject`/`ResolvedSubject`/`PartialCaveatInfo` types.

  Before:
  ```java
  try (Stream<String> stream = client.lookupResources(consistency, "document", "view", "user", "alice")) {
      List<String> resourceIDs = stream.toList();
  }
  try (Stream<String> stream = client.lookupSubjects(consistency, "document", "doc1", "view", "user")) {
      List<String> subjectIDs = stream.toList(); // a "*" here silently included excluded subjects
  }
  ```
  After:
  ```java
  try (Stream<LookupResult.LookupResource> stream =
      client.lookupResources(consistency, "document", "view", "user", "alice")) {
      stream.forEach(r -> {
          if (r.permissionship() == LookupResult.Permissionship.CONDITIONAL_PERMISSION) {
              return; // not a full grant until r.partialCaveat().missingRequiredContext() is supplied
          }
          use(r.resourceId());
      });
  }
  try (Stream<LookupResult.LookupSubject> stream =
      client.lookupSubjects(consistency, "document", "doc1", "view", "user")) {
      stream.forEach(s -> {
          Set<String> excludedIds =
              s.excludedSubjects().stream().map(LookupResult.ResolvedSubject::subjectId).collect(toSet());
          if (s.subject().subjectId().equals("*") && excludedIds.contains(myUserId)) {
              return; // explicitly excluded from the wildcard grant — do NOT treat as authorized
          }
          use(s.subject().subjectId());
      });
  }
  ```

- **2026-08-14**: `ExpandResult.treeRoot` (the leaked proto `PermissionRelationshipTree`) is replaced with `ExpandResult.tree()`, a native `PermissionTree` record family (`ObjectRef`, `SubjectRef`, `IntermediateNode`, `LeafNode`, `Operation`), mirroring `spicedb-go`'s native expand tree. No protobuf types are exposed from `expandPermissionTree` anymore.

  Before:
  ```java
  ExpandResult result = client.expandPermissionTree(consistency, "document", "1", "view");
  var root = result.treeRoot(); // PermissionRelationshipTree (proto)
  ```
  After:
  ```java
  ExpandResult result = client.expandPermissionTree(consistency, "document", "1", "view");
  PermissionTree tree = result.tree(); // native record
  ```

### Fixes

- **`checkAll` returned `true` for zero relationships**: Java's `for`-loop
  aggregate never executes its body on an empty array and falls through to
  `return true` — the same vacuous-truth trap every language's `all`/`every`
  primitive has on an empty sequence. Root `DESIGN.md`'s "An aggregate over
  zero checks is not a grant" clause names the hazard: a gate like
  `checkAll(cs, "edit", docs.stream().map(Doc::toRel).toArray(Relationship[]::new))`
  was silently granted whenever the derived relationships array came up
  empty — a filter that matched nothing, an upstream returning an empty
  array. `checkAll` now guards the empty case before the aggregate and
  returns `false` — it never reached the server for this case even before
  the fix, since `checkPermissions` already short-circuits (`return
  List.of();`) on an empty relationships array. `checkAny` is unchanged — it
  was already correctly `false` on empty.
- **Check-time caveat context: nested `Map`/`List` values no longer stringified**: `toProtoStruct`'s per-value conversion (used to build the `context` field on `CheckBulkPermissionsRequestItem`) previously delegated every value, including nested `Map`/`List`, to `toProtoValue`, whose fallback `value.toString()` case handled anything it didn't recognize -- correct for scalars, but a nested map's `toString()` is Java's `{key=value, ...}` debug format, and a caveat expecting a proper nested object or list received that string instead, so evaluation failed or misbehaved in a way the caller couldn't diagnose. A new check-time-only `checkContextToProtoValue` recurses into `Map`/`List`, converting them to a proper protobuf `Struct`/`ListValue`. `toProtoValue` itself is untouched and continues to stringify nested values for the **write-time** relationship caveat-context path (`toProtoRelationship`) -- that stringification is intentional there and out of scope for this fix.
- **Per-item bulk-check error mapping**: a `CheckBulkPermissionsPair` error (`pair.hasError()`) is now routed through `ErrorMapper`, so callers get the SPECIFIC typed exception (e.g. `PermissionDeniedException`, `FailedPreconditionException`) instead of the untyped base `SpiceDBException`. The per-item gRPC code was previously discarded — only the message was used, forcing callers to string-match it to tell a schema mismatch from a permission denial, the exact problem the typed exception hierarchy exists to solve. The item's index is still preserved in the exception message (`"check item %d: ..."`), matching `spicedb-go`'s `fmt.Sprintf("check item %d", i)`.

  Before:
  ```java
  // pair.getError().getMessage() only -- the gRPC code was thrown away, always the base type:
  throw new SpiceDBException("check item " + i + ": " + pair.getError().getMessage());
  ```
  After:
  ```java
  // pair.getError().getCode() is preserved and mapped, so a PERMISSION_DENIED item throws
  // PermissionDeniedException (catchable specifically), not the base SpiceDBException:
  throw ErrorMapper.toSpiceDBException(syntheticStatusRuntimeExceptionFor(pair.getError(), i));
  ```

- **Streaming error mapping**: `readRelationships`, `lookupResources`, `lookupSubjects`, `exportRelationships`, and `updates` now map mid-stream gRPC errors (raised while iterating `serverStream.hasNext()`/`next()`) to the typed `SpiceDBException` hierarchy via `ErrorMapper`, instead of leaking a raw `io.grpc.StatusRuntimeException` to stream consumers
- **Cancelable streams**: `readRelationships`, `lookupResources`, `exportRelationships`, and `updates` now bind their underlying gRPC server-streaming call to a per-stream `io.grpc.Context.CancellableContext` and cancel it when the returned `Stream` is closed (e.g. via try-with-resources), so `close()` actually stops the server from producing further results instead of leaving the call open (`lookupSubjects` streams eagerly and has no open call to cancel)
- **Delete page size correction**: `DEFAULT_DELETE_PAGE_SIZE` is now 1,000 (matching SpiceDB's default `--max-delete-relationships-limit`, so the default `deleteRelationships` call works against a stock server), not 10,000 — the earlier "10,000" correction in this file was itself wrong
- **Escape-hatch documentation**: `ClientOption.apply()` and `create(...)` are now documented as advanced escape hatches for gRPC channel configuration. Removed unused `DEFAULT_CHECK_BATCH_SIZE` constant
- **Retry count alignment**: transient gRPC errors are now retried up to 4 total attempts (1 initial + 3 retries), matching the retry behavior of the other SpiceDB clients (was 3 total attempts / 2 retries)
- **`Consistency.toProto()` visibility**: made package-private (was `public`), since proto types must never appear in the public API surface. It was only used internally by `SpiceDBClient`

  Before:
  ```java
  Consistency cs = Consistency.full();
  var proto = cs.toProto(); // public accessor, callable from any package
  ```
  After:
  ```java
  Consistency cs = Consistency.full();
  // toProto() is package-private; construct via full()/atLeast()/etc. and
  // pass `cs` directly to SpiceDBClient methods
  ```

## 0.1.0 (2026-03-18)

Initial release of the idiomatic Java SpiceDB client.

### Features

- **Security-obvious constructors**: `SpiceDBClient.createPlaintext()` / `SpiceDBClient.createSystemTls()` make TLS posture explicit
- **Full API coverage**: all non-deprecated SpiceDB APIs exposed through idiomatic Java types
  - Permission checks (`checkPermission`, `checkPermissions`, `checkAny`, `checkAll`) via BulkCheckPermissions
  - Relationship CRUD with `Transaction` builder and preconditions
  - Streaming reads via `Stream<T>` (AutoCloseable) with transparent cursor pagination
  - `lookupResources` and `lookupSubjects`
  - Schema read, write, reflect, diff, computable permissions, dependent relations
  - Bulk import/export
  - Watch for relationship changes
  - Experimental relationship counters
- **Java records**: `Relationship` and `Filter` are immutable records with `of`, `fromTuple`, `withCaveat`, `withExpiration`
- **Native `PermissionTree`**: `expandPermissionTree`/`ExpandResult` return a native `PermissionTree` record family (`ObjectRef`, `SubjectRef`, `IntermediateNode`, `LeafNode`, `Operation`) instead of the proto `PermissionRelationshipTree`
- **Explicit consistency**: every read requires a `ConsistencyStrategy` (`full()`, `minLatency()`, `atLeast()`, `snapshot()`, `atLeastOrFull()`, `atLeastOrMinLatency()`)
- **Unchecked exceptions**: `SpiceDBException` hierarchy extending `RuntimeException`
- **Automatic retry**: exponential backoff for transient gRPC errors
- **`AutoCloseable`**: proper resource cleanup via try-with-resources
- **BSR Generated SDKs**: proto dependencies resolved from Buf Schema Registry Maven packages
- **10 examples** covering all major API surfaces, doubling as JUnit 5 integration tests
- **Requires Java 17+**
