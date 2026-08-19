# Changelog

## Unreleased

### Added

- **2026-08-19: `createSystemTls` now has a test at all, and it completes a real TLS
  handshake.** Root DESIGN.md, "RULE: A system-TLS constructor must reach a real server".
  This client had **no TLS test at any tier** — a grep for
  `Tls|TLS|useTransportSecurity|handshake` across `lib/src/test/` returned nothing. The
  new `SystemTlsHandshakeTest` drives `createSystemTls` against `grpc.authzed.com:443`
  and forces the connection with a real RPC, since `ManagedChannelBuilder` connects
  lazily (clause 2).

  It does not pin a status code, on purpose: gRPC reports a failed handshake and a live
  server's "no healthy upstream" alike, so the status cannot discriminate. The `getCause`
  chain can, and it is flattened because grpc-java wraps the SSL failure below the
  `StatusRuntimeException` this client then maps again.

  Gated with `@EnabledIfEnvironmentVariable("SPICEDB_TLS_INTEGRATION")`, which JUnit
  reports as *skipped* rather than passed — the distinction that makes the CI check
  honest. Its own CI step sets the variable and then reads the JUnit XML for
  `tests="1" skipped="0" failures="0" errors="0"`, because Gradle reports a build whose
  only test was skipped as SUCCESSFUL. Verified by mutation: building the channel with a
  `TrustManagerFactory` initialised on an empty `KeyStore` fails the test with exactly
  the "truststore is probably not loaded" message it exists to produce.

- **2026-08-19: three examples that ran without being able to fail now assert something
  that does.** Root DESIGN.md, "RULE: An example must be executed by CI and must be able
  to fail", clause 2. No example was renamed or removed; the example set goes from 43
  tests to 46 across the same 14 classes.

  - `CallDeadlinesTest` proves a deadline instead of only showing fast local calls
    succeeding. Its three existing tests pass identically whether or not the timeout ever
    reaches the wire — `bulkImportIsNotBoundedByTheUnaryDefault` is the sharpest case,
    since its own comment claims to guard against the unary default leaking into the
    import path, but a 50-relationship import finishes far inside the 30-second default,
    so the regression it describes could not fail it. Two new tests stand up a
    `ServerSocket` that accepts connections and never speaks gRPC — what a wedged SpiceDB
    looks like from a client — and require `DeadlineExceededException` from both the
    `Duration defaultTimeout` construction parameter and the per-call `timeout` override.
    Each runs under a 17-second watchdog, comfortably below `DEFAULT_TIMEOUT`, so a
    per-call timeout that was accepted and dropped trips the watchdog rather than
    passing.
  - `RelationshipCountersTest` polls to a terminal state instead of sleeping three
    seconds and then wrapping every assertion in `if (!result.stillCalculating())`, which
    asserts nothing on a slow run and nothing on any run if the still-calculating mapping
    is inverted — the likeliest bug on that exact field. The count asserted is now exact
    (three viewers, with an `editor` written that the relation filter must exclude), and
    `unregister_counter` requires the subsequent read to raise
    `FailedPreconditionException` rather than merely asserting that unregistering "does
    not throw", which a no-op unregister satisfies just as well. The cleanup hooks now
    catch `FailedPreconditionException` rather than `Exception`, so an unreachable server
    or a bad token still fails the example.
  - `WatchChangesTest` requires the exact update just written — resource, relation and
    subject — rather than only that its resource type is `document`, which the seed write
    or any leftover document relationship would satisfy. A new test also proves the
    release half of "RULE: Abandoning a stream must release it": a consumer parked on a
    quiet stream must end when the stream is closed, and must surface
    `CancelledException` rather than a raw `StatusRuntimeException`.

  Verified by mutation, not by inspection: making `effectiveTimeout` ignore its argument,
  inverting `getCounterStillCalculating`, dropping the relation from the relationship
  filter, removing the `onClose` handler that cancels the gRPC context, and mapping
  `OPERATION_TOUCH` to `UNSPECIFIED` each fail the example that covers them, and each was
  confirmed to compile first so the failure is the assertion rather than the build.

  Worth recording for contrast: the same stream-release test in the C# client **cannot**
  fail, because `await foreach` disposes the async iterator and with it the gRPC call, so
  the release there is a language guarantee rather than client code. In Java it is this
  client's own `onClose` handler, and removing it does fail the example — which is why
  the test exists here in the form it does.

- **2026-08-19**: An escape hatch, `SpiceDBClient.rawChannel()`. It returns this client's own
  `io.grpc.Channel` with its bearer metadata already attached, so any generated stub is one
  `newStub` call away and a request the idiomatic API cannot express has a workaround short of
  forking the client:

  ```java
  var stub = PermissionsServiceGrpc.newBlockingStub(client.rawChannel());
  CheckPermissionResponse response = stub.checkPermission(request);
  ```

  Two real examples of such a request: `WriteRelationshipsRequest.optionalTransactionMetadata`, a
  proto field this client does not surface, and the single-check `CheckPermission` RPC, which
  `checkPermission` deliberately routes around (every check goes through `CheckBulkPermissions`).
  Purely additive.

  Clearly-marked **secondary** API — root DESIGN.md's "What NOT To Do" keeps channels, stubs and
  metadata out of the primary surface and permits exactly this ("escape hatches for advanced use
  are acceptable as clearly marked secondary API"). No stability promise beyond grpc-java's and the
  generated code's.

  Prefer it over rebuilding a `ManagedChannel` of your own: that means replicating this client's
  transport configuration exactly and re-attaching the token by hand, and getting either wrong
  gives the raw path different transport security than the idiomatic one. The bearer token comes
  free here, but a raw call gets no `SpiceDBException` mapping, no retry, and no `DEFAULT_TIMEOUT`
  — call `withDeadlineAfter` yourself. The connection's lifecycle stays with the
  client: `close()` is what releases it, and the returned object is the wrapper
  `ClientInterceptors.intercept` builds — a package-private `Channel` subclass whose delegate is
  unreachable — so a cast to `ManagedChannel` throws rather than handing out `shutdown()`.

  It is an accessor, not a constructor: it takes no endpoint, preshared key, or transport setting,
  so channel construction stays on the single guarded path in `create` and this cannot become a
  route around root DESIGN.md, "RULE: Credentials over insecure transport require an explicit
  opt-in". It is complementary to the existing `ClientOption.apply(ManagedChannelBuilder)` hatch,
  which configures the channel *before* it exists.

  New example: `RawEscapeHatchTest`.

- **2026-08-18**: Error mapping now carries the server's detail all the way to the caller, per root
  DESIGN.md, "RULE: Error mapping must not lose the server's detail". Purely additive.
  - Two new exception types, both `SpiceDBException` subclasses:
    - `OutOfRangeException` for `OUT_OF_RANGE`, SpiceDB's code for an expired or
      garbage-collected ZedToken. It previously fell through to the base `SpiceDBException`, so
      the one recoverable error in a token-threading application was indistinguishable from an
      internal fault. Recovery is mechanical: discard the stale token and re-read at full
      consistency.
    - `UnauthenticatedException` for `UNAUTHENTICATED` — a wrong, expired, or rotated API token,
      previously also indistinguishable from an internal fault. Distinct from
      `PermissionDeniedException`, which means the caller was identified but not allowed.
  - Every `SpiceDBException` now exposes the `google.rpc.ErrorInfo` detail SpiceDB attaches to a
    status, via three new methods: `getReason()` (the name of an `authzed.api.v1.ErrorReason` enum
    value, e.g. `"ERROR_REASON_MAXIMUM_DEPTH_EXCEEDED"`), `getReasonDomain()` (`"authzed.com"` for
    SpiceDB), and `getReasonMetadata()` (the specifics behind the reason, such as which
    precondition failed). They are derived from the preserved `cause`, so no subclass constructor
    changed and the reason can never drift from the status the exception was built out of. The
    reason is surfaced exactly as the server sent it: a value a newer server knows and this client
    does not is passed through unchanged rather than coerced or rejected, per root DESIGN.md's
    "RULE: A conversion that cannot preserve meaning must fail", which requires server-supplied
    unknowns to degrade rather than throw. `getReason()` returns `""` and `getReasonMetadata()` an
    empty map when the server attached no `ErrorInfo`.
  - `checkBulkPermissions` per-item errors now keep their own details on the way to a typed
    exception. The per-item `google.rpc.Status` was previously reduced to a code and a message
    before mapping, discarding the item's `ErrorInfo`.

  ```java
  try {
    client.write(txn);
  } catch (FailedPreconditionException e) {
    if (e.getReason().equals("ERROR_REASON_WRITE_OR_DELETE_PRECONDITION_FAILURE")) {
      System.out.println(e.getReasonMetadata().get("precondition_resource_id"));
    }
  } catch (OutOfRangeException e) {
    // ZedToken expired or GC'd: drop it and re-read at full consistency.
  }
  ```

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

- **2026-08-19**: **The clear-before-write delete tolerates one error, not all of them.** It
  swallowed every failure, so an unreachable server or a rotated token read as "nothing to clear"
  and the test carried on to fail somewhere less obvious. Only
  `FAILED_PRECONDITION`/`ERROR_REASON_UNKNOWN_DEFINITION` -- a fresh server with no `document`
  definition -- is tolerated now; anything else fails the test where it happened.

- **2026-08-19**: **The example set is pinned by name, not by count.** `wantExampleCount` passed
  unchanged when an example class was *renamed* -- only deletion was caught, and a manifest
  can drift from disk with no signal. `wantExamples` now lists every example by name and is
  reconciled with the glob in both directions, the same shape the skip targets already used.
  Verified by renaming `WatchChangesTest.java`: `expected but absent: [WatchChangesTest];
  present but not expected: [WatchStreamTest]`.

- **2026-08-19**: **A skipped case no longer counts as an executed one.** The report reader
  behind the executed-count assertion counted every reported case, so a fully-`@Disabled` example
  would have satisfied "this example contributed a test case" while running nothing. Skipped
  cases are now excluded and the reported total is the executed total. No such example exists
  today; the point is that adding one cannot go unnoticed.

- **2026-08-19**: **`CallDeadlinesTest` could not run after any example that writes an `editor`
  relationship.** It writes its own schema, narrower than `SpiceDBIntegrationTest.SCHEMA`, and
  cleared `document` relationships *after* that write rather than before -- but SpiceDB refuses a
  `WriteSchema` that drops a relation while a relationship still exists under it, so all three of
  its tests failed with `cannot delete relation 'editor' in object definition 'document', as at
  least one relationship exists under it: document:report#editor@user:alice`. Whether it failed
  depended on JUnit's class discovery order relative to `BulkOperationsTest`, which is not stable
  across runs: the same commit passed on one run of `mage integrationTest` and failed three tests
  on the next. It now calls the new `SpiceDBIntegrationTest.clearDocumentRelationships` helper
  before writing its schema.

- **2026-08-19**: **`mage integrationTest` now proves the examples ran.** It ran `gradle test` and
  reported whatever Gradle's exit code said. Gradle skips a task it believes is up to date and
  still reports the build successful, so a green run did not establish that any example executed.
  The runner now deletes `examples/build/test-results/test` before invoking Gradle -- which both
  makes `:examples:test` out of date, so it cannot be skipped, and guarantees any report present
  afterwards came from this run -- and then reads those reports to confirm every example class on
  disk contributed at least one test case. New `mage checkExamples` (also run by `mage test`, so it
  needs neither a server nor Gradle) asserts the expected number of example classes are on disk, so
  a rename fails loudly instead of quietly shrinking the run. Root DESIGN.md, "RULE: An example
  must be executed by CI and must be able to fail", clause 1.

- **2026-08-19**: **The examples now read `SPICEDB_ENDPOINT` and `SPICEDB_TOKEN`**, defaulting to
  `localhost:50051` and `somerandomkeyhere`, via `SpiceDBIntegrationTest.ENDPOINT`/`TOKEN`. Both
  were hardcoded, so the suite could not run on a host whose 50051 was already taken.
  `docker-compose.test.yml` takes its published port from `SPICEDB_TEST_PORT` and its key from
  `SPICEDB_TEST_TOKEN` (same defaults), and `mage integrationTest` derives the port from
  `SPICEDB_ENDPOINT`.

- **2026-08-19** (documentation only): `examples/README.md` told the reader to run
  `./gradlew :examples:test`. **There is no `gradlew` wrapper anywhere in `spicedb-java`** -- the
  only one in the repo belongs to `spicedb-gen/testdata/java` -- so the documented command fails
  outright. It also claimed `mage test` "starts a SpiceDB container automatically"; it does not,
  and never did, and it does not run the examples either. The README now names
  `mage integrationTest`, uses plain `gradle`, and records what CI checks about the examples.

- **2026-08-19**: **The Java API-compatibility gate now actually fails on breaking changes.**
  `spicedb-java/Magefile.go`'s `apiCompat` ran japicmp with `--only-incompatible
  --ignore-missing-classes` but without `--error-on-binary-incompatibility`, so japicmp printed the
  incompatibilities it found and still exited 0 — `sh.RunV` returned nil and the next line printed
  "API compatible" regardless. Since `.github/workflows/java.yaml` runs this as the only Java API
  gate, it had been decorative. The flag is now passed, and the failure message no longer implies
  `mage updateAllowBreak` is a target of this module — it is a root-level target
  (`Magefile.go:168`), so it must be run from the repository root.

  Enabling it surfaced exactly one real break across this branch, since fixed rather than waived:
  adding the `Duration timeout` component to the `DeleteOptions` record had silently removed the
  generated three-argument constructor. That constructor is now declared explicitly, delegating to
  the canonical form with a `null` timeout, so the deadline work above is additive after all and
  the gate is green on its merits rather than by exemption.

- **2026-08-19**: **The gRPC stack now resolves to a single version (1.79.0) instead of three.**
  This client declared `io.grpc:*` at 1.68.0 while `spicedb-java-proto` declared it at 1.72.0, but
  neither number was what the build actually used. The BSR-generated stubs
  (`build.buf.gen:authzed_api_grpc_java:1.79.0.2...`) depend on `io.grpc:grpc-core:1.79.0`, and
  Gradle's default conflict resolution picks the highest, so `grpc-api`, `grpc-core`, `grpc-stub`,
  `grpc-protobuf`, `grpc-protobuf-lite` and `grpc-context` all resolved to **1.79.0** — a version
  neither build file mentioned.

  What did *not* get pulled up were the artifacts nothing else depends on. `grpc-netty-shaded` and
  `grpc-util` sat at **1.72.0**, and `grpc-inprocess` at **1.68.0**, against a 1.79.0 core. grpc-java
  supports no such mixture: every `io.grpc:*` artifact is released in lockstep against the others'
  internal SPIs, and `grpc-netty-shaded` shades *Netty*, not gRPC — the `io.grpc.netty` transport
  classes inside it are compiled against `grpc-core`'s internals and were being loaded against a core
  eleven minor versions newer. That is the transport every real connection goes through.

  The stack is now pinned by `io.grpc:grpc-bom:1.79.0` — declared
  `api(platform(...))` in both modules, with every `io.grpc:*` coordinate versionless — matching the
  version the generated stubs are built against. That replaces ten hand-synchronized version
  literals with one per module and makes a partial bump structurally impossible rather than merely
  documented, which matters because the ten-literal arrangement is what produced this bug. The
  resolved graph is uniform: `grpc-util` and `grpc-inprocess` move up to 1.79.0 with everything
  else. The BOM is `api` rather than `implementation` because the `api` configuration does not
  extend `implementation`, so an implementation-scoped platform would not govern lib's `api`
  `grpc-api` coordinate. Dependency *scopes* are unchanged — `grpc-netty-shaded` stays `api` in
  the proto client so the shaded `NettyChannelBuilder` cast documented under "Custom TLS trust
  material" keeps compiling, and `grpc-api` stays `api` here so `rawChannel()`'s `io.grpc.Channel`
  return type stays usable. No public API change: japicmp reports "No changes", because the compile
  classpath already resolved to 1.79.0 before this fix.

- **2026-08-19**: **A large bulk check is no longer sent as one oversized request.**
  `checkPermissions`, `checkPermission`, `checkAny` and `checkAll` built a single
  `CheckBulkPermissions` request from however many relationships the caller passed. SpiceDB caps
  a request at `maxBulkCheckCount` -- 10,000, a hard-coded const in
  `internal/services/v1/bulkcheck.go` with no flag to raise or lower it -- and rejects anything
  larger with `ERROR_REASON_TOO_MANY_CHECKS_IN_REQUEST`. Nothing in the proto enforced the cap
  either (`CheckBulkPermissionsRequest.items` carries only a per-item `required` rule, not a
  collection-size rule), so the failure surfaced only at runtime, on the largest inputs.

  Checks are now split into requests of at most 1,000 items -- the same batch size the import
  path already uses, and the value `spicedb-rust` (the one client that already chunked) picked
  -- and the responses are concatenated in input order, so `results[i]` still corresponds to the
  caller's i-th relationship across a chunk boundary. The response-length guard added earlier on
  this branch now runs per chunk. A caller passing fewer than 1,000 relationships still makes
  exactly one request.

  Three consequences of the split are worth stating outright, because they change contracts
  callers may already depend on:

  - **A per-item error message now names the caller's own index.** The `check item N` prefix is
    computed from an absolute offset, not from the position within whichever request carried the
    failure. Without that, a failure at relationship 1,003 reported as `check item 3` — the same
    misattribution the response-length guard exists to prevent, relocated into the diagnostic.
  - **`checked_at` is per response, and a response is now one chunk.** Results from a single
    request still share one token; an input large enough to be split carries more than one across
    the returned collection. Root DESIGN.md's bulk-check invariant has been re-scoped to match.
  - **A per-call timeout, and the retry budget, bound each request rather than the whole call.**
    Worst-case wall time for `n` checks is `ceil(n / 1000) x timeout`. This is deliberate: one
    deadline spanning every chunk would make a large check fail purely for being large, and a
    retry budget shared across chunks would let one flaky chunk exhaust the allowance for the
    rest. The docs now say so.

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
