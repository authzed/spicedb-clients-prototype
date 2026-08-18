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
  **ESTABLISHMENT** on transient errors (as shipped, `{UNAVAILABLE, ABORTED}`;
  `RESOURCE_EXHAUSTED` was in that set when this landed and was removed by the
  2026-08-18 retry-safety entry below),
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

- **2026-08-18** (behavioral; new overloads): per root DESIGN.md, "RULE: Credentials over
  insecure transport require an explicit opt-in" -- `createPlaintext` and `create(..., withInsecure())`
  now refuse to construct a client for a non-loopback endpoint (loopback means `localhost`,
  `127.0.0.0/8`, `::1`, or a `unix:` socket target). Previously an insecure connection would send
  its bearer token in cleartext to any host -- grpc-java's `CallCredentials` contract has no
  built-in "refuse over an insecure channel" check the way some other language bindings do, so
  nothing checked where the connection actually went. New overloads,
  `createPlaintext(endpoint, key, allowInsecureRemoteCredentials)` / `createPlaintext(endpoint,
  key, defaultTimeout, allowInsecureRemoteCredentials)`, and a new `ClientOption`,
  `SpiceDBClient.allowInsecureRemoteCredentials()` (for use with `create(..., withInsecure(),
  allowInsecureRemoteCredentials())`), opt in explicitly when a caller genuinely means to send
  credentials in cleartext to a remote host. `createPlaintext`/`withInsecure()` against `localhost`
  are unaffected -- no code change needed for local development. Thrown as
  `IllegalArgumentException`, before any channel is created. `create(...)` only recognizes the
  named `withInsecure()`/`allowInsecureRemoteCredentials()` options by reference; a fully custom
  `ClientOption` lambda that calls `usePlaintext()` directly on the raw `ManagedChannelBuilder`
  escape hatch is outside what this guard can see.

- **2026-08-18** (behavioral; no signature change): the two entries below change what existing,
  unmodified call sites do. They are listed here because neither announces itself -- nothing
  fails to compile, and the difference only shows up under load or against a slow query.
  - **Unary calls are now bounded by a 30-second default** -- see "Call deadlines" in this
    release. A call that legitimately takes longer than 30 s (most plausibly a deep
    `expandPermissionTree` on a large graph, or a filtered delete sweeping many pages) now fails
    with a deadline error where it previously ran to completion. Raise it with
    `createPlaintext(endpoint, key, Duration)` / `createSystemTls(...)`, or pass `Duration timeout` on the
    individual call. There is deliberately no way to ask for no bound at all on a unary call.
  - **Mutations are no longer retried automatically** -- see "Retry safety" in this release.
    `write`, `deleteRelationships`, `writeSchema`, `importRelationships`, and the
    experimental counter register/unregister calls now surface a transient `UNAVAILABLE` to the
    caller on the first attempt rather than retrying. This is the correct default (replaying a
    non-idempotent write can report failure for a write that in fact committed), but a caller who
    was relying on the client to ride out a rolling restart must now retry themselves, knowing
    their own idempotency. Reads are unaffected. `RESOURCE_EXHAUSTED` is no longer retried either,
    on reads or mutations.

- **2026-08-18**: Watch resumability. `updates` previously dropped
  `WatchResponse.changes_through` entirely and had no way to request
  `WATCH_KIND_INCLUDE_CHECKPOINTS`.
  - **Breaking**: `updates(List<String> objectTypes, String startRevision)` now returns
    `Stream<SpiceDBClient.WatchEvent>` instead of `Stream<SpiceDBClient.Update>`, and yields
    once per server response (a batch of updates) rather than flattening to one item per
    relationship update — a checkpoint response carries zero updates, so a per-update-only
    stream has no way to surface one at all.

    ```java
    public record WatchEvent(List<Update> updates, String changesThrough, boolean isCheckpoint) {}
    ```
  - `WatchEvent.changesThrough` is the proto's `changes_through` -- "This token can be used in
    a subsequent WatchRequest to resume watching from this point." Without it, a consumer whose
    stream dropped could only restart from its original `startRevision` (reprocessing
    everything since, possibly past the GC window) or from head (silently losing every change
    in the gap).
  - New overload `updates(List<String> objectTypes, String startRevision, boolean
    includeCheckpoints)` requests `WATCH_KIND_INCLUDE_CHECKPOINTS` (plus
    `WATCH_KIND_INCLUDE_RELATIONSHIP_UPDATES`, since `optionalUpdateKinds` is
    empty-means-default and a non-empty list replaces rather than extends it) -- no prior way
    existed to ask for this at all. `WatchEvent.isCheckpoint` lets a caller tell "nothing
    changed, here is a fresh resume point" from "here are changes". Recommended if this
    SpiceDB instance is running behind a proxy that aborts idle connections.
  - `examples/watch_changes/` (`WatchChangesTest`) updated for the new `WatchEvent` shape and
    extended with a checkpoint-request test. New `lib/src/test/.../WatchResumabilityTest.java`:
    a watch event exposes a usable resume token, `includeCheckpoints` reaches the built
    `WatchRequest`, and a checkpoint event is distinguishable from one carrying updates.
    `WatchUpdateMappingTest`, `StreamCancellationTest`, `StreamingErrorMappingTest`, and
    `StreamEstablishmentRetryTest`'s watch cases updated for the new return type without
    weakening any existing assertion.

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

- **2026-08-18**: **Security — a bypass in the guard that refuses to send credentials over
  plaintext to a non-loopback host was fixed.** `SpiceDBClient.createPlaintext(endpoint,
  presharedKey)` accepted `"127.0.0.1:443@evil.com"` as loopback and sent the bearer token to
  `evil.com` in cleartext, with no opt-in and nothing reported. `isLoopbackEndpoint` split the
  endpoint on its last colon and read the host as `127.0.0.1`; grpc-java's `DnsNameResolver`
  derives its host as `URI.create("//" + name).getHost()`, which reads `127.0.0.1:443` as
  *userinfo* and returns `evil.com` — then resolves and connects there on the default port. (An
  RPC to `"127.0.0.1:443@evil.invalid"` fails with `UNAVAILABLE: Unable to resolve host
  evil.invalid`, naming the host it actually went looking for.) The `:authority` header carried
  the whole undivided string, so nothing in the request made the redirection visible.
  `"[::1]:443@evil.com"` and `"[::1]:0@127.0.0.1:19999"` bypassed it the same way through the
  bracketed branch, which never validated what followed the `]`.

  The root cause was that the guard parsed the endpoint differently than the transport did, so
  the fix is not a tighter split: `isLoopbackEndpoint` now derives its host with the same
  `URI.create("//" + …).getHost()` expression `DnsNameResolver` itself uses. Guard and
  transport can no longer disagree. Endpoints containing `@`, `/`, `?`, `#`, or whitespace are
  additionally refused outright, since a legitimate SpiceDB target contains none of them. A
  bare IPv6 literal (`"::1"`) is bracketed before parsing and keeps working, as do `unix:`
  targets, `localhost`, and 127.0.0.0/8. Both this client and `spicedb-java-proto` carried
  independent copies of the guard; both are fixed.

- **2026-08-18**: `exportRelationships` drained the ENTIRE server stream into an in-memory buffer
  before yielding a single `Relationship` to the caller -- an OOM risk for the one API most likely
  to face the largest dataset in the system (a full multi-million-relationship export).
  `ExportBulkRelationships`' `optional_limit` bounds the number of relationships in a single
  response MESSAGE, unlike every other paginated RPC's `optional_limit`, which bounds the WHOLE
  stream and ends the call. The loop shape that is correct for every other paginated method on
  this client (drain the current page, check the count against the page size, reissue with a new
  cursor if needed) is therefore wrong for export: a single `ExportBulkRelationships` call keeps
  streaming further response messages until the whole dataset has been sent, so "drain the current
  page" meant "drain the entire export." `exportRelationships` now pulls exactly one response
  message per internal refill, mirroring `updates`' single-message-at-a-time model, so the first
  relationship is available as soon as the first response message arrives.
- **2026-08-18**: Call deadlines, per root `DESIGN.md` "RULE: A unary call must have a
  deadline". Previously no method accepted a timeout and no client-level default existed, so a
  SpiceDB instance that accepted a connection but never answered hung every caller forever — the
  connection looks fine at the transport level, so no error is produced and there is nothing for
  retry logic to act on.
  - Every unary method gained an overload taking a trailing `Duration timeout` (e.g.
    `checkPermission(consistency, permission, r, timeout)`, `write(txn, timeout)`,
    `readSchema(timeout)`), mirroring the existing `checkPermission(..., Map<String, Object>
    context)` overload convention. `deleteRelationships` instead reads a new
    `DeleteOptions.withTimeout(Duration)`. Additive — existing call sites are unaffected. Applied
    via grpc-java's `stub.withDeadlineAfter(millis, TimeUnit.MILLISECONDS)`, called fresh on each
    retry attempt.
  - `SpiceDBClient.createPlaintext`/`createSystemTls`/`create` all gained a `Duration
    defaultTimeout` overload, applied to any unary call that doesn't pass its own `timeout`. New
    public `SpiceDBClient.DEFAULT_TIMEOUT = Duration.ofSeconds(30)` mirrors `authzed-node`'s
    `DEFAULT_DEADLINE_MS = 30_000` (its comment cites `grpc/grpc-node#541`). There is deliberately
    no way to construct a client whose unary calls have no bound at all.
  - Streaming methods (`readRelationships`, `lookupResources`, `lookupSubjects`, `updates`,
    `exportRelationships`) gained **no** `timeout` overload and are **not** bound by
    `defaultTimeout` — DESIGN.md's "Streaming calls MUST NOT inherit the unary default": these
    are long-lived by design (`updates` may legitimately run for the life of the process), and a
    30s cutoff would end a legitimate stream, which is a worse defect than the one this change
    fixes.
  - **Fix round 1 correction:** `importRelationships` also gained a `Duration timeout` overload,
    but — unlike the unary methods above — it is client-streaming, not unary, and is now
    explicitly **excluded** from `defaultTimeout`: its duration scales with the size of the
    caller's dataset, not with server latency, so no fixed default is correct for it (root
    DESIGN.md, "RULE: A unary call must have a deadline", clause 3, amended to cover
    client-streaming and bidirectional RPCs, not only server-streaming).
    `importRelationships(Iterable)` is now unbounded; `importRelationships(Iterable, Duration)`
    still bounds the call explicitly. An earlier version of this fix incorrectly resolved the
    no-argument overload against `defaultTimeout`, which would have silently aborted large,
    legitimate multi-minute imports at 30 seconds.
  - `DeadlineExceededException` (added earlier, but never actually produced by this client since
    nothing enforced a deadline) is now reachable: a timed-out call throws it, not a generic
    `SpiceDBException`. `Status.Code.DEADLINE_EXCEEDED` was already excluded from
    `ErrorMapper.TRANSIENT_CODES`, so a timeout is never auto-retried.
  - New `DeadlineTest`, against a real in-process gRPC server (grpc-java's in-process transport,
    same style as the existing `TestServers` harness) whose handlers deliberately stall: a unary
    call against a stub that never responds throws `DeadlineExceededException` well before the
    stall completes (not a hang), a per-call `timeout` overrides a much larger client default
    (proven in both directions -- shrinking it, and letting a slower-but-legitimate call outlive
    a small default), a streaming call outlives a tiny unary default instead of inheriting it,
    and bulk import is both unbounded by the default and still honors an explicit `timeout`.
    Every call is run on a background thread and joined with a bounded `Future.get(...)`, so a
    regression fails the suite instead of hanging CI.
  - New `examples/.../CallDeadlinesTest`, run against a real SpiceDB rather than a mock:
    constructs a client via the documented `Duration defaultTimeout` overload, overrides it
    per-call, and confirms bulk import is unbounded by default.
  - `spicedb-gen`'s Java typed-client template needed no change: its generated `check()`/
    `touch()`/`create()`/`delete()` already call the pre-existing (unchanged) overloads, so
    generated clients continue to compile unmodified and inherit the new client-level default
    automatically. Verified end-to-end: regenerated `testdata/java`'s typed client via the
    generator's composite Gradle build (which substitutes `spicedb-java:lib` for the published
    artifact) and confirmed `compileJava`/`compileTestJava`/`compileTypeErrors` all still behave
    as expected.
- **2026-08-18**: Retry safety, per root `DESIGN.md` "RULE: Automatic retry is for idempotent
  operations only". Three changes:
  - `RESOURCE_EXHAUSTED` is no longer retried. In SpiceDB it signals memory load-shed (retrying
    adds load to an already-overloaded server) or a deterministic `MaxDepthExceeded` (retrying can
    never succeed — it re-runs the most expensive class of check several times before surfacing
    the same error). Previously `ErrorMapper.TRANSIENT_CODES`/`isTransient` treated
    `Status.Code.RESOURCE_EXHAUSTED` as transient.
  - Mutations (`write`, `deleteRelationships`, `writeSchema`, and the
    `experimentalRegisterRelationshipCounter`/`experimentalUnregisterRelationshipCounter` calls)
    are no longer retried on a transient error, even though the underlying gRPC code is retryable.
    A `WriteRelationships` carrying `OPERATION_CREATE` or preconditions is not idempotent: if it
    commits and the response is lost (a rolling restart, a proxy dropping the connection), a retry
    would surface `ALREADY_EXISTS`/`FAILED_PRECONDITION` for a write that in fact succeeded, and
    the caller would wrongly conclude it had failed. Reads still retry automatically. All five
    mutation call sites previously went through `withRetry`; they now go through a new
    `callOnce`, which converts the gRPC error without retrying.
  - Backoff is now full-jitter (`jitteredBackoffMs`, `uniform(0, cap)`) instead of plain
    exponential doubling. Without jitter, every client in a fleet retries on the same schedule
    after a server restart, turning the recovery into a thundering herd.

  `ErrorMapperTest`'s `isTransientForResourceExhausted` (`assertTrue`) is renamed
  `isNotTransientForResourceExhausted` and inverted to `assertFalse`, since the old assertion was
  exactly the defect this fixes. New coverage in `UnaryRetrySafetyTest` (a mutation is attempted
  exactly once on a retryable error and on `RESOURCE_EXHAUSTED`; a read is retried;
  `RESOURCE_EXHAUSTED` is never retried on a read; backoff varies between calls).
- **2026-08-18**: **`toRelationshipFilter` silently dropped `subjectID`/`subjectRelation` when
  `subjectType` was not set, instead of raising.** `optionalSubjectFilter` was only built inside
  `if (f.subjectType() != null && !f.subjectType().isEmpty())`, so
  `Filter.of("document").withSubjectID("alice")` produced a proto `RelationshipFilter` with **no
  subject constraint at all**, while the `Filter` record itself still reported
  `subjectID() == "alice"` — a caller reading the record back would see the constraint they set;
  the server would not. `deleteRelationships(filter)` called with that filter deleted every
  relationship on every document, not just alice's — a correct-looking user-offboarding call that
  wipes the whole system. The wire's `SubjectFilter.subject_type` is a required field, so there is
  no way to express a subject ID/relation constraint without it, which makes silent widening the
  one unsafe resolution — `toRelationshipFilter` now throws `InvalidArgumentException` naming the
  field that was set without `subjectType`, per root `DESIGN.md` "RULE: A conversion that cannot
  preserve meaning must fail", clause 1 (caller-supplied data the client cannot represent MUST
  raise a typed error). `InvalidArgumentException` is unchecked (extends `SpiceDBException`
  extends `RuntimeException`), so no signature change was needed at any of `toRelationshipFilter`'s
  five call sites; `deleteRelationships` converts preconditions and the primary filter before any
  RPC is attempted, so a bad filter — whether the primary one or a `mustMatch`/`mustNotMatch`
  guard — is rejected with zero requests sent. No pre-existing test asserted the silent-drop
  behavior, so none needed replacing.
- **2026-08-18**: **`toProtoValue`'s final `else` still silently stringified a value it could not
  otherwise represent** (`Value.newBuilder().setStringValue(value.toString())`) — a custom class
  instance, say — instead of raising. This fallback is shared by both the check path
  (`toProtoStruct`) and the write path (`toProtoRelationship`), and it was inherited unchanged by
  the write-time fix below rather than introduced by it. Caveat context is caller-supplied, so per
  root `DESIGN.md` "RULE: A conversion that cannot preserve meaning must fail", clause 1, an
  unrepresentable value must raise a typed error naming what could not be converted, not degrade
  to a guess (clause 2's server-data carve-out does not apply here). The fallback now throws
  `InvalidArgumentException` naming the unsupported type
  (`"unsupported caveat context value type: " + value.getClass().getName()`). A new
  `toProtoValueForKey` wrapper, used at every per-key call site (`toProtoStruct`'s loop,
  `toProtoRelationship`'s loop, and `toProtoValue`'s own nested-`Map` recursion), catches that and
  re-raises with the offending key named (`"caveat context key \"K\": ..."`), matching
  `spicedb-ruby`'s message shape; a nested map's error traces the full key path, since each
  enclosing call adds its own key in turn. No existing test depended on the old stringify-fallback
  behavior — the full suite passed unchanged before new tests for the throw were added.
- **2026-08-18**: **`updateFromProto` mapped any unrecognized watch operation — including
  `OPERATION_UNSPECIFIED` and any future wire value — to `UpdateOperation.TOUCH`.** A cache or
  index mirror consuming the watch stream would upsert a relationship on an update it could not
  actually interpret, which may in fact have been a delete. `UpdateOperation` gains a new
  `UNSPECIFIED` value (purely additive), and the mapper's `default` arm now returns it instead of
  `TOUCH`, matching what every other server-enum mapper in this file already does
  (`toTreeOperation`, both `permissionship` mappers) — server-supplied data the client does not
  recognise must degrade to a safe, non-permissive default, never a write. Root `DESIGN.md`,
  "RULE: A conversion that cannot preserve meaning must fail", clause 2.
- **2026-08-18**: **`checkPermissions` did not verify that `checkBulkPermissions` returned as
  many pairs as were requested.** The result `List<CheckResult>` was sized off
  `resp.getPairsCount()` instead of the request's item count, and nothing compared the two. The
  proto guarantees pairs are returned in request order but says nothing about count, so a response
  with fewer pairs than items would silently produce a `List` shorter than `relationships` — every
  `results.get(i)` after the gap misaligned with `relationships[i]`, attributing one resource's
  answer to another. `checkPermissions` now throws `SpiceDBException` naming both counts
  (`"checkBulkPermissions returned N pair(s) for M request item(s)"`) when they differ, before
  mapping any pair. It also now guards the malformed-oneof case — a `CheckBulkPermissionsPair`
  whose `response` oneof is unset (neither `item` nor `error`) — the same way `spicedb-rust`
  already did, instead of silently falling through to `pair.getItem()`'s zero-value default.
- **2026-08-18**: **Write-time caveat context: every value was stringified**: `toProtoRelationship`'s caveat-context conversion (`toProtoValue`) stringified every value via its `else -> value.toString()` fallback -- numbers, booleans, `null`, and nested `Map`/`List` values all landed on the wire as a plain string, not the matching `google.protobuf.Value` `kind` (`number_value`, `bool_value`, `null_value`, `struct_value`, `list_value`). A caveat like `now < 100` stored against a stringified `"50"` fails to evaluate, and fails *silently* -- as a `CONDITIONAL_PERMISSION` result rather than an error. This is worse than the equivalent check-time gap fixed above: a bad check-time context fails one call, but a bad **write-time** context is *persisted* -- every future check against that relationship mis-evaluates, and re-checking with correct context never repairs it, only rewriting the relationship does. `toProtoValue` and `checkContextToProtoValue` are now one converter (kept the `toProtoValue` name; `checkContextToProtoValue` is removed), used by both `toProtoRelationship` (write) and `toProtoStruct`/`mergeCheckContext` (check). Its Javadoc previously instructed "Do not reuse this for the write-time path -- write-time stringification is intentional there and out of scope for this conversion"; that instruction was the defect's documentation and has been removed rather than merely contradicted. The read side (`fromProtoValue`, used by `fromProtoRelationship`) is fixed to match: it previously handled `null`/`bool`/`number`/`string` correctly but fell back to the proto `Value`'s debug `toString()` for nested `Struct`/`ListValue`, so a written nested map or list didn't round-trip either; it now recurses. No public API shape change -- `toProtoRelationship`/`fromProtoRelationship`/`Relationship.caveatContext()` signatures are unchanged, only the values they produce/consume.

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
- **Check-time caveat context: nested `Map`/`List` values no longer stringified**: `toProtoStruct`'s per-value conversion (used to build the `context` field on `CheckBulkPermissionsRequestItem`) previously delegated every value, including nested `Map`/`List`, to `toProtoValue`, whose fallback `value.toString()` case handled anything it didn't recognize -- correct for scalars, but a nested map's `toString()` is Java's `{key=value, ...}` debug format, and a caveat expecting a proper nested object or list received that string instead, so evaluation failed or misbehaved in a way the caller couldn't diagnose. A new check-time-only `checkContextToProtoValue` recurses into `Map`/`List`, converting them to a proper protobuf `Struct`/`ListValue`. `toProtoValue` itself was left untouched at the time, continuing to stringify nested values for the **write-time** relationship caveat-context path (`toProtoRelationship`). **Superseded by the 2026-08-18 entry below**, which converges both paths onto the same converter -- write-time stringification was never actually intentional; it was simply out of scope for this particular fix, and the Javadoc claim to the contrary (added alongside this entry) has been removed.
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
