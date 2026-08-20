# Changelog

## Unreleased

### Added

- **2026-08-19: four new examples, one per root `DESIGN.md` RULE that had no executed
  coverage in any client.** 13 example projects -> 17, 34 tests -> 50, none renamed or
  removed. Group E Phase 3.

  - `examples/InsecureOptIn` — "RULE: Credentials over insecure transport require an
    explicit opt-in". Loopback plaintext needs no ceremony; a remote plaintext host is
    refused at construction, so the token never reaches a socket; and the named
    `allowInsecureRemoteCredentials` permits it. Four endpoints whose authority could
    move under URI parsing are each required to be refused — `127.0.0.1:443@evil.com`
    above all, where a last-colon split reads the host as `127.0.0.1` while `System.Uri`,
    what Grpc.Net.Client parses with, reads the authority as `evil.com`.
  - `examples/UnrepresentableValues` — "RULE: A conversion that cannot preserve meaning
    must fail", both directions. Unconvertible caveat context is refused naming the
    offending key (and *not* naming the innocent one beside it); a filter with
    `SubjectID` and no `SubjectType` is refused rather than silently widening, which for
    `DeleteRelationshipsAsync` is the difference between deleting alice's relationships
    and deleting every relationship on every document. In the other direction, a
    permissionship this client has never seen must neither raise nor grant.
  - `examples/ErrorMapping` — "RULE: Error mapping must not lose the server's detail",
    written as the two recoveries the rule names: a stale ZedToken surfaces as
    `OutOfRangeException` with the `RpcException` still reachable as `InnerException`,
    recovered by dropping the token and re-reading at full consistency; a rotated token
    surfaces as `UnauthenticatedException`, distinct from a transport fault.
  - `examples/RetryPolicy` — "RULE: Automatic retry is for idempotent operations only",
    with attempts counted **server-side**, the only way to tell a retry from its absence:
    at the caller a transparently-retried success and a first-try success are identical.
    A read failing twice with `UNAVAILABLE` is retried to success in 3 attempts; a write
    failing the same way is attempted exactly once; `RESOURCE_EXHAUSTED` exactly once even
    on a read.

  Verified by mutation, 5 of 5 killing their example: disabling the loopback guard;
  dropping `OUT_OF_RANGE` from the mapper; adding `StatusCode.ResourceExhausted` to
  `TransientCodes`; letting an under-specified filter widen; and routing `CallOnceAsync`
  through `RetryAsync`.

  The last three examples host a real Kestrel gRPC stand-in (the pattern
  `SpiceDB.Client.Tests` already uses), because neither R5 code is reachable from the real
  SpiceDB — verified, not assumed: a garbage ZedToken returns `INVALID_ARGUMENT` and the
  in-memory datastore never collects the revision, and a wrong preshared key returns
  **`PERMISSION_DENIED`, not `UNAUTHENTICATED`**. `ErrorMapping` asserts that real
  behaviour too, so a reader does not write a credential-refresh branch that can never
  run. Those three projects therefore carry a `Microsoft.AspNetCore.App` framework
  reference and `Grpc.AspNetCore.Server`; `InsecureOptIn` needs neither, since its
  refusals happen before any connection.

- **2026-08-19: `CreateSystemTls` now has a test that completes a real TLS handshake.**
  Root DESIGN.md, "RULE: A system-TLS constructor must reach a real server". The only TLS
  coverage was `CreateSystemTls_ReturnsClient`, which constructs against
  `grpc.example.com:443` — a reserved, non-routable name — and asserts the result is
  non-null. `GrpcChannel` connects lazily, so **it passed with an empty trust store**,
  which is the exact defect the rule exists to catch. The new
  `TlsHandshakeTests.CreateSystemTls_CompletesRealHandshake` drives `CreateSystemTls`
  against `grpc.authzed.com:443` and forces the connection with a real RPC, per clause 2.

  It does not pin a status code, on purpose: gRPC reports a failed handshake and a live
  server's "no healthy upstream" alike, so the status cannot discriminate. The exception
  chain can, and it is flattened through `InnerException` because Grpc.Net wraps the TLS
  failure in an `HttpRequestException` inside an `RpcException`, which this client then
  maps again.

  Gated by a `[Trait("Category", "TlsHandshake")]` rather than an environment variable,
  and **excluded from `mage test` and `mage integrationTest`** so the default runs stay
  offline. xunit 2 has no runtime skip, so an `if (env is null) return;` gate would report
  the test as *Passed* while doing nothing — the same "reads as coverage, provides none"
  failure the rule is about. Its own CI step selects the category and greps for
  `Passed: 1`; that grep is load-bearing, because `dotnet test` with a filter matching
  nothing exits 0. Verified by mutation: giving the channel an
  `SslClientAuthenticationOptions` that validates nothing fails the test.

- **2026-08-19: five examples that ran without being able to fail now assert something
  that does.** Root DESIGN.md, "RULE: An example must be executed by CI and must be able
  to fail", clause 2. No example was renamed or removed; the example count goes from 30
  tests to 34 across the same 13 projects.

  - `examples/CallDeadlines` proves a deadline instead of only showing fast local calls
    succeeding. Its three existing tests pass identically whether or not the timeout ever
    reaches the wire, so two new ones stand up a `TcpListener` that accepts connections
    and never speaks gRPC — what a wedged SpiceDB looks like from a client — and require
    both the `defaultTimeout` construction parameter and the per-call `timeout` override
    to raise `DeadlineExceededException`. Each runs under a watchdog, so a dropped
    deadline fails the example rather than hanging the job.
  - `examples/RelationshipCounters` polls to a terminal state instead of sleeping two
    seconds and then wrapping every assertion in `if (!stillCalculating)`. This is the
    only test in that assembly, so a 100%-broken counter feature previously shipped
    green — including if the still-calculating mapping were inverted, the likeliest bug
    on that exact field. The count asserted is now exact (two viewers, with an `editor`
    written that the relation filter must exclude), and unregistering is verified by
    requiring the subsequent read to raise `FailedPreconditionException`.
  - `examples/LookupSubjects` writes a wildcard. `Assert.Empty(result.ExcludedSubjects)`
    against a schema with no wildcards was the only thing exercising that field, and no
    C# example wrote a wildcard at all, so dropped exclusions — which turn a partial
    grant into a blanket grant — had nothing to catch them. A new test grants `viewer` to
    `user:*`, carves `eve` back out via `banned`, and requires `eve` in
    `ExcludedSubjects`. The pre-existing test now asserts the exact subject set rather
    than two `Contains` checks that a lookup ignoring the permission would satisfy.
  - `examples/SchemaReflection` asserts contents rather than four consecutive non-empty
    checks: exact definition, relation and permission names, that `document#viewer`
    computes exactly `document#view` and is reported as a permission, the full dependency
    set of `document#view`, and specific diff kinds — so a mapping that reported every
    diff as `unknown` fails.
  - `examples/WatchChanges` requires the update it just wrote (resource, relation,
    subject and operation) rather than only that its resource type is `document`, which
    the seed write or any leftover document relationship would satisfy.

  One assertion here is deliberately **not** claimed as proof. `WatchChanges` also
  cancels a consumer parked on a quiet stream, and mutation testing showed that cutting
  the cancellation token out of *both* places `UpdatesAsync` passes it still ends that
  consumer 5ms after the cancel: `await foreach` disposes the async iterator, which
  disposes the gRPC call. **In C# the release half of "RULE: Abandoning a stream must
  release it" is a language guarantee, not something this client implements**, so no
  assertion in that test can fail on it. What the test does pin — and what a mutation
  does break — is that abandoning the stream surfaces as the native `CancelledException`
  rather than leaking a raw `Grpc.Core.RpcException` out of the streaming path. The test
  says so in-comment rather than implying coverage it does not have.

- **2026-08-19**: An escape hatch, `SpiceDBClient.RawProto()`. It returns the underlying
  `SpiceDBProtoClient` — the four generated service clients (`Permissions`, `Schema`, `Watch`,
  `Experimental`) this library makes its own calls through — so a request the idiomatic API cannot
  express has a workaround short of forking the client:

  ```csharp
  var response = await client.RawProto().Permissions.CheckPermissionAsync(request);
  ```

  Two real examples of such a request: `WriteRelationshipsRequest.OptionalTransactionMetadata`, a
  proto field this client does not surface, and the single-check `CheckPermission` RPC, which
  `CheckPermissionAsync` deliberately routes around (every check goes through
  `CheckBulkPermissions`). Purely additive.

  Clearly-marked **secondary** API — root DESIGN.md's "What NOT To Do" keeps channels, stubs and
  metadata out of the primary surface and permits exactly this ("escape hatches for advanced use
  are acceptable as clearly marked secondary API"). No stability promise beyond
  `Grpc.Net.Client`'s and the generated code's.

  It complements `CreateFromChannel`, which configures the connection before it exists: until now a
  caller who used `CreatePlaintext` or `CreateSystemTls` had no hatch at all, since `_protoClient`
  was private with no getter. The bearer token comes free (each service client is built on an
  intercepted `CallInvoker`), but a raw call gets no `SpiceDBException` mapping, no retry, and no
  `DefaultTimeout` — pass a `deadline` yourself. Do not dispose the returned object; `DisposeAsync`
  is what releases the connection.

  It is an accessor, not a constructor: it takes no endpoint, preshared key, or transport setting,
  so channel construction stays on the single guarded path in `SpiceDBProtoClient` and this cannot
  become a route around root DESIGN.md, "RULE: Credentials over insecure transport require an
  explicit opt-in".

  New example: `examples/RawEscapeHatch/`.

- **2026-08-18**: Error mapping now carries the server's detail all the way to
  the caller, per root DESIGN.md, "RULE: Error mapping must not lose the
  server's detail". Purely additive.
  - Two new exception types, both `SpiceDBException` subclasses:
    - `OutOfRangeException` for `StatusCode.OutOfRange`, SpiceDB's code for an
      expired or garbage-collected ZedToken. It previously fell through to the
      base `SpiceDBException`, so the one recoverable error in a
      token-threading application was indistinguishable from an internal fault.
      Recovery is mechanical: discard the stale token and re-read at full
      consistency.
    - `UnauthenticatedException` for `StatusCode.Unauthenticated` — a wrong,
      expired, or rotated API token, previously also indistinguishable from an
      internal fault. Distinct from `PermissionDeniedException`, which means
      the caller was identified but not allowed.
  - Every `SpiceDBException` now exposes the `google.rpc.ErrorInfo` detail
    SpiceDB attaches to a status, via three new properties: `Reason` (the name
    of an `authzed.api.v1.ErrorReason` enum value, e.g.
    `"ERROR_REASON_MAXIMUM_DEPTH_EXCEEDED"`), `ReasonDomain` (`"authzed.com"`
    for SpiceDB), and `ReasonMetadata` (the specifics behind the reason, such
    as which precondition failed). They are derived from the preserved
    `InnerException`, so no subclass constructor changed and the reason can
    never drift from the status the exception was built out of. The reason is
    surfaced exactly as the server sent it: a value a newer server knows and
    this client does not is passed through unchanged rather than coerced or
    rejected, per root DESIGN.md's "RULE: A conversion that cannot preserve
    meaning must fail", which requires server-supplied unknowns to degrade
    rather than throw. `Reason` is `""` and `ReasonMetadata` empty when the
    server attached no `ErrorInfo`.
  - `CheckBulkPermissionsAsync` per-item errors now keep their own details on
    the way to a typed exception. The per-item `google.rpc.Status` was
    previously reduced to a code and a message before mapping, discarding the
    item's `ErrorInfo`.
  - New dependency `Grpc.StatusProto`, gRPC's own implementation of the
    `grpc-status-details-bin` bridge between `RpcException` and
    `Google.Rpc.Status`, used instead of reading and writing that trailer by
    hand. `Google.Api.CommonProtos` (already present transitively) is now
    referenced explicitly, since `Errors.cs` reads `ErrorInfo` directly.

  ```csharp
  try
  {
      await client.WriteAsync(txn);
  }
  catch (FailedPreconditionException ex)
      when (ex.Reason == "ERROR_REASON_WRITE_OR_DELETE_PRECONDITION_FAILURE")
  {
      Console.WriteLine(ex.ReasonMetadata["precondition_resource_id"]);
  }
  catch (OutOfRangeException)
  {
      // ZedToken expired or GC'd: drop it and re-read at full consistency.
  }
  ```

- **2026-08-17**: The check surface can now supply caveat context, in both
  forms. Previously `MissingContext` on a `ConditionalPermission` result
  told a caller what the server needed, but there was no parameter to
  supply it — this closes that gap. Purely additive; no existing call site
  changes.

  - **Call-level default**, fanned out onto every relationship in the
    call: `CheckPermissionsWithContextAsync`, `CheckAnyWithContextAsync`,
    and `CheckAllWithContextAsync` are new methods (the wire's
    `CheckBulkPermissionsRequestItem.context` is per-item — the request
    itself has no context field — so a call-level default has to be
    fanned out at request-build time). `CheckPermissionAsync` (the
    single-relationship form) instead gets a new **trailing optional**
    `context = null` parameter on the existing method — its shape has no
    `params` array in the way, so no new method name was needed there.
  - **Per-item**, overriding the call-level default for one relationship:
    `Relationship.WithCheckContext(context)` / the new
    `Relationship.CheckContext` field.

  **Merge rule (key-level, item wins):** an item's context is the
  call-level dictionary with the item's own entries overwriting matching
  keys — call-level keys the item doesn't mention are retained, never
  discarded wholesale. A call-level `{now: 42, region: "us"}` plus a
  per-item `{region: "eu"}` sends `{now: 42, region: "eu"}` for that item;
  a sibling item with no per-item context still gets
  `{now: 42, region: "us"}`. When neither is supplied, no `context` field
  is set on the wire (`null`, never an empty `Struct`).

  `Relationship.CheckContext` is check-time-only and distinct from the
  existing `Relationship.CaveatContext` (write-time, stored with the
  relationship's caveat) — `Relationship.ToProto()` never reads
  `CheckContext`, so it can never leak into a stored relationship via
  `WriteAsync`.

  Why new methods instead of overloading `CheckPermissionsAsync`/
  `CheckAnyAsync`/`CheckAllAsync` directly? Each ends in
  `params Relationship[]`, and C# forbids any parameter after `params` — a
  `context` parameter would have to land before it, adjacent to the
  existing `CancellationToken cancellationToken = default` slot that
  existing calls already fill positionally with `default`. Distinct method
  names avoid relying on overload-resolution betterness rules to keep
  those call sites compiling unchanged, and match how `spicedb-go`/
  `spicedb-rust` solved the identical shape problem with `*WithContext`
  methods.

  ```csharp
  // relB carries a per-item override (wins over any call-level default
  // for that item); relA carries none, so it inherits the call-level
  // default unchanged.
  var relB = Relationship.FromTriple("document", "doc2", "view", "user", "bob")
      .WithCheckContext(new Dictionary<string, object> { ["region"] = "eu" });

  var results = await client.CheckPermissionsWithContextAsync(
      consistency, "view", new Dictionary<string, object> { ["now"] = 42, ["region"] = "us" },
      default, relA, relB);

  // Single check:
  var result = await client.CheckPermissionAsync(
      consistency, "view", rel, default, new Dictionary<string, object> { ["now"] = 42 });
  ```


- **2026-08-15**: The 5 streaming methods (`ReadRelationshipsAsync`,
  `LookupResourcesAsync`, `LookupSubjectsAsync`, `ExportRelationshipsAsync`,
  `UpdatesAsync`) now retry stream/page **ESTABLISHMENT** on transient errors
  (as shipped, `{UNAVAILABLE, ABORTED}`; `RESOURCE_EXHAUSTED` was in that set
  when this landed and was removed by the 2026-08-18 retry-safety entry
  below), reusing the same backoff and
  `MaxRetryAttempts` budget as unary calls (reset per page for the paginated
  methods; per-stream for `LookupSubjectsAsync`/`UpdatesAsync`, which have no
  cursor). A transient error is retried ONLY while nothing has been yielded
  yet from the current stream/page — once any item has been yielded, the
  error is mapped to the typed `SpiceDBException` and rethrown instead, never
  retried, so callers can never see a replayed/duplicated item. `UpdatesAsync`
  in particular only retries the initial watch open — never mid-watch. No API
  shape change.

- **2026-08-15**: `DeleteRelationshipsAsync` now accepts optional `mustMatch`/
  `mustNotMatch`/`limit` parameters, reaching the proto's
  `optional_preconditions` and `optional_limit` fields that were previously
  unset by the client. Additive — existing `client.DeleteRelationshipsAsync(filter)`
  calls are unaffected (no preconditions, 1,000-item page size, partial
  deletions allowed, same as before). `mustMatch`/`mustNotMatch` add
  MUST_MATCH/MUST_NOT_MATCH preconditions (built the same way as
  `Transaction.MustMatch`/`MustNotMatch`, via the shared internal
  `Transaction.BuildPrecondition` helper) that guard the delete, rejecting it
  if unsatisfied; `limit` overrides the default 1,000-per-call page size.
  Mirrors `spicedb-go`'s `WithDeleteMustMatch`/`WithDeleteMustNotMatch`/
  `WithDeleteLimit` (`client/relationships.go`) — see `DESIGN.md`
  ("Deletions") for the semantics of combining preconditions with
  auto-paging.

  ```csharp
  // Before:
  var revision = await client.DeleteRelationshipsAsync(filter);

  // After — same call still works, plus optional guards:
  var revision = await client.DeleteRelationshipsAsync(
      filter,
      mustMatch: [ownerGuard],
      mustNotMatch: [lockedGuard],
      limit: 1000);
  ```

### Breaking Changes

- **2026-08-18** (behavioral; new optional parameter): per root DESIGN.md, "RULE: Credentials
  over insecure transport require an explicit opt-in" -- `CreatePlaintext` (and the underlying
  `SpiceDBProtoClient` constructor's `insecure: true`) now refuse to construct a client for a
  non-loopback endpoint (loopback means `localhost`, `127.0.0.0/8`, `::1`, or a `unix:` socket
  target). Previously a plaintext connection would send its bearer token in cleartext to any
  host -- the `CallInvoker.Intercept`/composite-credentials interceptor in `SpiceDBProtoClient`
  existed specifically because `CompositeChannelCredentials` requires secure transport, with
  nothing checking where the connection actually went. A new parameter,
  `allowInsecureRemoteCredentials: true`, opts in explicitly when a caller genuinely means to
  send credentials in cleartext to a remote host; it must be passed alongside `insecure`/
  `CreatePlaintext`, since neither alone is sufficient for a non-loopback endpoint anymore.
  `CreatePlaintext`/`CreateSystemTls` against `localhost` are unaffected -- no code change needed
  for local development. `CreateFromChannel` (the pre-configured-`GrpcChannel` escape hatch) is
  unaffected by this change; the caller already fully controls that channel's transport security.

- **2026-08-18** (behavioral; no signature change): the two entries below change what existing,
  unmodified call sites do. They are listed here because neither announces itself -- nothing
  fails to compile, and the difference only shows up under load or against a slow query.
  - **Unary calls are now bounded by a 30-second default** -- see "Call deadlines" in this
    release. A call that legitimately takes longer than 30 s (most plausibly a deep
    `ExpandPermissionTreeAsync` on a large graph, or a filtered delete sweeping many pages) now
    fails with a deadline error where it previously ran to completion. Raise it with
    `CreatePlaintext/CreateSystemTls(..., defaultTimeout:)`, or pass `timeout:` on the individual
    call. There is deliberately no way to ask for no bound at all on a unary call.
  - **Mutations are no longer retried automatically** -- see "Retry safety" in this release.
    `WriteAsync`, `DeleteRelationshipsAsync`, `WriteSchemaAsync`, `ImportRelationshipsAsync`, and
    the experimental counter register/unregister calls now surface a transient `UNAVAILABLE` to
    the caller on the first attempt rather than retrying. This is the correct default (replaying a
    non-idempotent write can report failure for a write that in fact committed), but a caller who
    was relying on the client to ride out a rolling restart must now retry themselves, knowing
    their own idempotency. Reads are unaffected. `RESOURCE_EXHAUSTED` is no longer retried either,
    on reads or mutations.

- **2026-08-18**: Watch resumability. `UpdatesAsync` previously dropped
  `WatchResponse.ChangesThrough` entirely and had no way to request
  `WATCH_KIND_INCLUDE_CHECKPOINTS`.
  - **Breaking**: `UpdatesAsync(objectTypes?, startRevision?, ...)` now returns
    `IAsyncEnumerable<WatchEvent>` instead of `IAsyncEnumerable<RelationshipUpdate>`, and yields
    once per server response (a batch of updates) rather than flattening to one item per
    relationship update — a checkpoint response carries zero updates, so a per-update-only
    enumerable has no way to surface one at all.

    ```csharp
    public sealed record WatchEvent
    {
        public IReadOnlyList<RelationshipUpdate> Updates { get; init; }
        public string ChangesThrough { get; init; } // resume token; pass as startRevision to resume after a dropped stream
        public bool IsCheckpoint { get; init; }      // true for a checkpoint event, which carries no Updates
    }
    ```
  - `WatchEvent.ChangesThrough` is the proto's `changes_through` -- "This token can be used in
    a subsequent WatchRequest to resume watching from this point." Without it, a consumer
    whose stream dropped could only restart from its original `startRevision` (reprocessing
    everything since, possibly past the GC window) or from head (silently losing every change
    in the gap).
  - New `includeCheckpoints` parameter (default `false`) requests
    `WATCH_KIND_INCLUDE_CHECKPOINTS` (plus `WATCH_KIND_INCLUDE_RELATIONSHIP_UPDATES`, since
    `OptionalUpdateKinds` is empty-means-default and a non-empty list replaces rather than
    extends it) -- no prior way existed to ask for this at all. `WatchEvent.IsCheckpoint` lets
    a caller tell "nothing changed, here is a fresh resume point" from "here are changes".
    Recommended if this SpiceDB instance is running behind a proxy that aborts idle
    connections.
  - `examples/WatchChanges/` updated for the new `WatchEvent` shape and extended with a
    checkpoint-request test. New `SpiceDB.Client.Tests/WatchResumabilityTests.cs`: a watch
    event exposes a usable resume token, `includeCheckpoints` reaches the built
    `WatchRequest`, and a checkpoint event is distinguishable from one carrying updates.
    `WatchUpdateMappingTests`, `StreamingEstablishmentRetryTests`'s watch cases updated for the
    new return type without weakening any existing assertion.

- **2026-08-16**: `CheckPermissionAsync` now returns `Task<CheckResult>`
  (was `Task<bool>`) and `CheckPermissionsAsync` now returns
  `Task<CheckResult[]>` (was `Task<bool[]>`). `CheckAnyAsync`/`CheckAllAsync`
  are unchanged (`Task<bool>`), but now count only `HasPermission` results —
  a `ConditionalPermission` never contributes to a `true`. This follows root
  DESIGN.md's "RULE: Only an unconditional grant is true": `permissionship`
  on `CheckPermissionResponse` is three-valued
  (`NO_PERMISSION`/`HAS_PERMISSION`/`CONDITIONAL_PERMISSION`), and a bare
  `bool` collapsed "denied" and "the server needed caveat context you didn't
  supply" into the same `false` — silently indistinguishable, and one client
  in this repo previously returned `true` for the conditional case by
  mistake.

  `Permissionship` (previously used only by the lookup surface) gains a
  fourth value, `NoPermission`, appended after `ConditionalPermission` so the
  underlying int values of the pre-existing members are not renumbered.
  Lookups never yield `NoPermission` — only `CheckResult` does.

  `CheckResult` carries `Permissionship`, `MissingContext` (the caveat
  context keys the server needed and didn't receive), `CheckedAt` (a ZedToken
  — thread it into `Consistency.AtLeast` for read-your-writes), and a derived
  `HasPermission` property that is true ONLY for `Permissionship.HasPermission`.
  `CheckResult` deliberately does NOT define `operator true`/`false` or a
  bool conversion — `if (result)` remains a compile error, forcing callers
  through `HasPermission` explicitly.

  Before:
  ```csharp
  var allowed = await client.CheckPermissionAsync(consistency, "view", rel);
  if (allowed) { /* ... */ }
  ```
  After:
  ```csharp
  var result = await client.CheckPermissionAsync(consistency, "view", rel);
  if (result.HasPermission) { /* ... */ }
  // A conditional result carries the missing context and the revision:
  if (result.Permissionship == Permissionship.ConditionalPermission)
      Log($"missing: {string.Join(", ", result.MissingContext)}");
  ```

- **2026-08-16**: `LookupResource` and `LookupSubject` gain a `LookedUpAt`
  field — the ZedToken revision the result was computed at (maps the proto
  `looked_up_at` field, previously unreachable through the idiomatic
  client). Identical for every item yielded by a single
  `LookupResourcesAsync`/`LookupSubjectsAsync` call. Additive to those
  records; existing field access is unaffected.


- **2026-08-15**: `LookupResourcesAsync`/`LookupSubjectsAsync` now yield native records instead of bare `string`s: `IAsyncEnumerable<LookupResource>` and `IAsyncEnumerable<LookupSubject>` respectively, mirroring `spicedb-go`'s `client/lookup_types.go`. Each result carries `Permissionship` (`HasPermission`/`ConditionalPermission`/`Unspecified`) and, for conditional results, `PartialCaveat.MissingRequiredContext`. Critically, `LookupSubject.ExcludedSubjects` now surfaces the subjects excluded from a wildcard `"*"` match — previously this information was silently dropped, so code that treated a wildcard subject ID as a blanket grant risked **over-granting access** to subjects the server had explicitly excluded. Deprecated proto fallback fields (`subject_object_id`/`permissionship`/`partial_caveat_info`/`excluded_subject_ids`) are still handled transparently for older servers.

  Before:
  ```csharp
  await foreach (var subjectID in client.LookupSubjectsAsync(consistency, "document", "1", "view", "user"))
  {
      grantedSubjectIDs.Add(subjectID); // wildcard "*" treated as blanket grant — unsafe!
  }
  ```
  After:
  ```csharp
  await foreach (var result in client.LookupSubjectsAsync(consistency, "document", "1", "view", "user"))
  {
      if (result.Subject.Permissionship != Permissionship.HasPermission)
          continue; // skip conditional results until caveat context is supplied

      if (result.Subject.SubjectID == "*")
      {
          // Wildcard grant — MUST honor ExcludedSubjects to avoid over-granting.
          grantedSubjectIDs.Add("*");
          excludedSubjectIDs.UnionWith(result.ExcludedSubjects.Select(s => s.SubjectID));
      }
      else
      {
          grantedSubjectIDs.Add(result.Subject.SubjectID);
      }
  }
  ```

  `LookupResourcesAsync` follows the same shape change (`LookupResource.ResourceID`/`.Permissionship`/`.PartialCaveat` in place of the old bare `string`):
  ```csharp
  await foreach (var result in client.LookupResourcesAsync(consistency, "document", "view", "user", "alice"))
  {
      if (result.Permissionship == Permissionship.HasPermission)
          accessibleResourceIDs.Add(result.ResourceID);
  }
  ```

- **2026-08-14**: `ExpandResult.TreeRoot` (the leaked proto `PermissionRelationshipTree`) is replaced with `ExpandResult.Tree`, a native `PermissionTree` record family (`ObjectRef`, `SubjectRef`, `IntermediateNode`, `LeafNode`, `TreeOperation`), mirroring `spicedb-go`'s native expand tree. No protobuf types are exposed from `ExpandPermissionTreeAsync` anymore.

  Before:
  ```csharp
  var result = await client.ExpandPermissionTreeAsync(consistency, "document", "1", "view");
  var root = result.TreeRoot; // PermissionRelationshipTree (proto)
  ```
  After:
  ```csharp
  var result = await client.ExpandPermissionTreeAsync(consistency, "document", "1", "view");
  PermissionTree tree = result.Tree; // native record
  ```

### Fixed

- **2026-08-19**: **`mage integrationTest` now asserts which example projects executed.** It ran
  `dotnet test SpiceDB.Client.Examples.sln` and reported the exit code. `dotnet test` over a
  solution *prints* "No test is available in ..." for an assembly with no tests and still exits 0,
  so commenting out the single `[Fact]` in `RelationshipCounters` -- which leaves the file, the
  `.csproj` and the `.sln` entry all in place, and therefore passes `CheckExamples` -- was silently
  green. That is the residual instance of root DESIGN.md's "RULE: An example must be executed by CI
  and must be able to fail", clause 1, in the client the rule's own narrative names, on the project
  whose single test is its whole assembly. The run is now TRX-logged into a directory cleared
  first, and every example assembly must have executed at least one test. Verified with that exact
  probe: `these example projects executed no test: RelationshipCounters (dotnet reported 29 executed
  tests across 12 of 13 example assemblies)`. A `NotExecuted` outcome does not count either, so a
  `[Fact(Skip = "...")]` fails the same way -- verified separately.

- **2026-08-19**: **`CallDeadlines` is a second narrow-schema project with no clear-first.** Like
  `RawEscapeHatch`, it writes a `document` definition with only `viewer`, and SpiceDB refuses a
  `WriteSchema` that drops a relation while a relationship still exists under it. It passed only
  because the solution happens to list it after `BulkOperations`, which writes no `editor`;
  reordering the solution would have failed it exactly as `RawEscapeHatch` did. It now calls
  `SpiceDBTestServer.ClearDocumentRelationshipsAsync` before writing its schema.

- **2026-08-19**: **The examples solution is now checked against the directory, not trusted.**
  All twelve examples once sat outside every solution file, so `dotnet build`/`dotnet test` never
  saw them -- for the repo's entire history. `SpiceDB.Client.Examples.sln` fixed that instance,
  but a solution is a hand-maintained snapshot and nothing compared it to disk: `grep -r
  "Examples.sln"` returned two hits, neither of them a check, so example #14 reintroduced the
  original defect by default. New `mage checkExamples` (also run by `mage test` and at the top of
  `mage integrationTest`, so it needs no server) diffs `examples/*/*.csproj` against the
  solution's project list in both directions and fails on divergence, and additionally fails if a
  listed project has no `Build.0` configuration -- a project can be listed and still excluded
  from every build. All three branches were verified by breaking them deliberately. Root
  DESIGN.md, "RULE: An example must be executed by CI and must be able to fail", clause 1.

- **2026-08-19**: **`mage lint` now covers the examples solution.** It ran `dotnet format` against
  `SpiceDB.Client.sln` alone, which contains only the library and its unit tests, so **no example
  was ever linted**. Verified by introducing a whitespace error in an example: green before,
  `error WHITESPACE` and exit 2 after.

- **2026-08-19**: **The example suite was nondeterministic, and is now serialized.** `dotnet test
  SpiceDB.Client.Examples.sln` runs the thirteen projects concurrently, and all thirteen share one
  SpiceDB and each writes a whole schema. One project's `WriteSchemaAsync` therefore lands between
  another's schema write and its relationship write, and the second fails with
  `relation/permission 'editor' not found under definition 'document'`. Two consecutive runs
  failed on four different examples -- `LookupSubjects` and `CheckPermission` on the first,
  `CallDeadlines` (all three tests) and `LookupResources` on the second -- which is what a green
  CI run on this suite has always been a coin flip away from. The examples run with
  `-maxcpucount:1` now. Serialized, the suite is deterministic: two consecutive runs, 30 example
  tests across 13 projects, zero failures.

- **2026-08-19**: **`RawEscapeHatch` could not run after any example that writes an `editor`
  relationship.** Its schema is deliberately narrower than the shared one, and SpiceDB refuses a
  `WriteSchema` that drops a relation while a relationship still exists under it -- so once the
  suite was serialized, it failed every time with `cannot delete relation 'editor' in object
  definition 'document'`. Under the old concurrent run this was invisible, since whether it failed
  depended on which project got there first. It now clears `document` relationships before writing
  its schema, via a shared helper.

- **2026-08-19**: **Examples now read `SPICEDB_ENDPOINT` and `SPICEDB_TOKEN`**, defaulting to
  `localhost:50051` and `somerandomkeyhere`. Both were hardcoded in thirty places, so the suite
  could not run on a host whose 50051 was already taken. New `examples/SpiceDBTestServer.cs`,
  linked into every example project by new `examples/Directory.Build.props`, holds both in one
  place. `docker-compose.test.yml` takes its published port from `SPICEDB_TEST_PORT` and its key
  from `SPICEDB_TEST_TOKEN` (same defaults), and `mage integrationTest` derives the port from
  `SPICEDB_ENDPOINT`.

- **2026-08-19** (documentation only): `examples/README.md` said to run `dotnet test` in a
  directory that contains no project or solution file, and claimed `mage test` "starts a SpiceDB
  container automatically". It does not, and never did. The README now names
  `mage integrationTest`, names the solution for a by-hand run, explains why `-maxcpucount:1` is
  required, and says what CI checks about example membership.

- **2026-08-19**: **A large bulk check is no longer sent as one oversized request.**
  `CheckPermissionsAsync`, `CheckPermissionAsync`, `CheckAnyAsync` and `CheckAllAsync` (and
  their `WithContext` variants) built a single `CheckBulkPermissions` request from however many
  relationships the caller passed. SpiceDB caps a request at `maxBulkCheckCount` -- 10,000, a
  hard-coded const in `internal/services/v1/bulkcheck.go` with no flag to raise or lower it --
  and rejects anything larger with `ERROR_REASON_TOO_MANY_CHECKS_IN_REQUEST`. Nothing in the
  proto enforced the cap either (`CheckBulkPermissionsRequest.items` carries only a per-item
  `required` rule, not a collection-size rule), so the failure surfaced only at runtime, on the
  largest inputs.

  Checks are now split into requests of at most 1,000 items -- the same batch size the import
  path already uses, and the value `spicedb-rust` (the one client that already chunked) picked
  -- and the responses are concatenated in input order, so `results[i]` still corresponds to the
  caller's i-th relationship across a chunk boundary. The response-length guard added earlier on
  this branch now runs per chunk. A caller passing fewer than 1,000 relationships still makes
  exactly one request.

  Three consequences of the split are worth stating outright, because they change contracts
  callers may already depend on:

  - **The malformed-pair message now names the caller's own index.** Its `check item N` prefix is
    computed from an absolute offset, not from the position within whichever request carried the
    failure. Without that, a failure at relationship 1,003 reported as `check item 3` — the same
    misattribution the response-length guard exists to prevent, relocated into the diagnostic.
    This client's *per-item error* path is unaffected: it routes the item's own status straight
    through the error mapper and never carried a `check item N` prefix to get wrong. Only
    `spicedb-go` and `spicedb-java` index that path.
  - **`checked_at` is per response, and a response is now one chunk.** Results from a single
    request still share one token; an input large enough to be split carries more than one across
    the returned collection. Root DESIGN.md's bulk-check invariant has been re-scoped to match.
  - **A per-call timeout, and the retry budget, bound each request rather than the whole call.**
    Worst-case wall time for `n` checks is `ceil(n / 1000) x timeout`. This is deliberate: one
    deadline spanning every chunk would make a large check fail purely for being large, and a
    retry budget shared across chunks would let one flaky chunk exhaust the allowance for the
    rest. The docs now say so.

  `DefaultCheckBatchSize` was already declared and never referenced; it is now what does the
  chunking, rather than a second constant being introduced beside it.

- **2026-08-19**: **A caller-supplied `GrpcChannel` is no longer disposed by the client that
  borrowed it.** `SpiceDBClient.CreateFromChannel(channel, token)` is the documented escape
  hatch for a channel you built yourself, but `DisposeAsync()` disposed it anyway
  (`DisposeAsync` -> `SpiceDBProtoClient.Dispose()` -> `_channel.Dispose()`), regardless of
  where the channel came from. The idiomatic .NET pattern is a DI-registered **singleton**
  `GrpcChannel` shared across the application, so the first scoped consumer to finish tore
  down a connection every other consumer was still using — surfacing elsewhere as
  `ObjectDisposedException: Grpc.Net.Client.GrpcChannel` from unrelated code.

  `SpiceDBProtoClient` now records whether it created the channel and disposes only what it
  created. A channel it built from an endpoint string (`CreatePlaintext`, `CreateSystemTls`)
  is still disposed exactly as before; a channel handed to `CreateFromChannel` is left open
  for its owner, who disposes it at application shutdown. Lending a channel does not transfer
  ownership. No API changed, and the security note on `CreateFromChannel` is unaffected: it
  documents that no loopback check happens on an already-configured channel, which is
  orthogonal to who disposes it.

- **2026-08-18**: **Security — a bypass in the guard that refuses to send credentials over
  plaintext to a non-loopback host was fixed.** `SpiceDBClient.CreatePlaintext(endpoint,
  token)` accepted `"127.0.0.1:443@evil.com"` as loopback and sent the bearer token to
  `evil.com` in cleartext, with no opt-in and nothing reported. The guard split the endpoint
  on its last colon and read the host as `127.0.0.1`; `GrpcChannel.ForAddress` parsed the
  same string as a URI, read `127.0.0.1:443` as *userinfo*, and connected to `evil.com`
  (`GrpcChannel.Target == "evil.com"`). `"[::1]:443@evil.com"` and `"[::1]:0@127.0.0.1:19999"`
  bypassed it the same way through the bracketed-IPv6 branch.

  The root cause was that the guard parsed the endpoint differently than the transport did,
  so the fix is not a tighter split: `IsLoopbackEndpoint` now hands the exact URI the client
  dials (`"http://" + endpoint`) to `System.Uri` — the same parser `GrpcChannel.ForAddress`
  uses — and applies the loopback test to `Uri.IdnHost`, the host the transport actually
  resolves. Guard and transport can no longer disagree. Endpoints containing `@`, `/`, `?`,
  `#`, or whitespace are additionally refused outright, since a legitimate SpiceDB target
  contains none of them. A bare IPv6 literal (`"::1"`) is bracketed — for the guard's parse
  **and** for the address the transport dials, both via one `TransportAuthority` helper, so the
  two cannot disagree — and keeps working, as do `localhost` and 127.0.0.0/8.

- **2026-08-18**: **Bare `::1` now actually connects.** It satisfied the guard but then threw
  `UriFormatException` out of `GrpcChannel.ForAddress`, because the guard bracketed the literal
  for its own parse while the constructor built the address from the raw endpoint
  (`"http://::1"` is not a legal URI). This is the same escaping-exception defect as the
  `Uri.IdnHost` one fixed earlier, one call further down, for an input the fixtures assert is
  supported. `"::1"`, `"[::1]"`, `"0:0:0:0:0:0:0:1"` and `"[::1]:50051"` all construct a client.

- **2026-08-18**: A null `endpoint` or `token` now throws `ArgumentNullException` rather than
  `NullReferenceException` from whichever string operation happened to run first.

- **2026-08-18**: **Breaking, and security-motivated: `unix:` endpoints are now refused instead
  of being treated as loopback.** `CreatePlaintext("unix:/var/run/spicedb.sock", token)`
  previously passed the guard on the grounds that "a unix socket never leaves the host's
  kernel" — but `Grpc.Net.Client` has no unix-domain-socket support reachable from an address
  string. `GrpcChannel.ForAddress("http://unix:/var/run/spicedb.sock")` reports
  `Target == "unix"`, so it resolved the **DNS name `unix`** and shipped the bearer token
  there in cleartext on port 80, while the guard reported "loopback". Nothing was ever
  connecting to a socket path, so no working configuration breaks.

  Such an endpoint now throws `InvalidOperationException` naming the problem, unconditionally
  — before the credential guard, and regardless of TLS or `allowInsecureRemoteCredentials`,
  since neither makes "resolve a hostname called `unix`" what the caller asked for. To use a
  unix socket, build a `GrpcChannel` on a `SocketsHttpHandler` with a UDS `ConnectCallback`
  and pass it to `CreateFromChannel`. The Go, Python and Ruby clients keep their `unix:`
  exemption; their transports genuinely dial the path.

- **2026-08-18**: `IsLoopbackEndpoint` could throw `System.UriFormatException` out of
  `CreatePlaintext`, which documents `InvalidOperationException`. `Uri.TryCreate` accepts
  hosts that `Uri.IdnHost` then refuses to IDN-map (`"‥localhost"`, `"loc‥alhost"`,
  `"127.0.0.1‥x"`). The guard now catches that and fails closed, as total as the string
  comparisons it replaced.

- **2026-08-18**: Call deadlines, per root `DESIGN.md` "RULE: A unary call must have a
  deadline". Previously the client had `CancellationToken` throughout (real caller-side
  cancellation — stops the client from waiting) but no server-enforced deadline: a cancelled
  token never told the server to stop working, and a SpiceDB instance that accepted a connection
  but never answered hung every caller that didn't cancel forever.
  - Every unary method gained an optional `TimeSpan? timeout = null`, applied via
    `CallOptions.Deadline` — **alongside**, not instead of, the pre-existing `CancellationToken`.
    Additive; existing call sites are unaffected. The six `params Relationship[]` check overloads
    (`CheckPermissionsAsync`, `CheckPermissionsWithContextAsync`, `CheckAnyAsync`,
    `CheckAnyWithContextAsync`, `CheckAllAsync`, `CheckAllWithContextAsync`) deliberately do
    **not** gain a `timeout` parameter — inserting one ahead of the
    `params` array would silently break an existing positional call site like
    `CheckPermissionsAsync(cs, "view", default, rel1, rel2)` (`rel1` would try to bind to the new
    parameter instead of the params array). They're still bounded by the client default; use the
    singular `CheckPermissionAsync` for a per-call override on checks.
  - `CreatePlaintext`/`CreateSystemTls`/`CreateFromChannel` all gained an optional `TimeSpan?
    defaultTimeout = null`, applied to any unary call that doesn't pass its own `timeout`. New
    public `SpiceDBClient.DefaultTimeout = TimeSpan.FromSeconds(30)` mirrors `authzed-node`'s
    `DEFAULT_DEADLINE_MS = 30_000` (its comment cites `grpc/grpc-node#541`). There is deliberately
    no way to construct a client whose unary calls have no bound at all.
  - Streaming methods (`ReadRelationshipsAsync`, `LookupResourcesAsync`, `LookupSubjectsAsync`,
    `UpdatesAsync`, `ExportRelationshipsAsync`) gained **no** `timeout` parameter and are **not**
    bound by `DefaultTimeout` — DESIGN.md's "Streaming calls MUST NOT inherit the unary default":
    these are long-lived by design (`UpdatesAsync` may legitimately run for the life of the
    process), and a 30s cutoff would end a legitimate stream, which is a worse defect than the
    one this change fixes.
  - **Fix round 1 correction:** `ImportRelationshipsAsync` also gained a `timeout` parameter, but
    — unlike the unary methods above — it is client-streaming, not unary, and is now explicitly
    **excluded** from `DefaultTimeout`: its duration scales with the size of the caller's
    dataset, not with server latency, so no fixed default is correct for it (root DESIGN.md,
    "RULE: A unary call must have a deadline", clause 3, amended to cover client-streaming and
    bidirectional RPCs, not only server-streaming). Omitting `timeout` now means unbounded there;
    passing it still bounds the call. Added a `DeadlineOrNull` helper (distinct from
    `EffectiveDeadline`, which still substitutes `DefaultTimeout`) that never substitutes a
    default. An earlier version of this fix incorrectly resolved `timeout` against
    `DefaultTimeout` for this call, which would have silently aborted large, legitimate
    multi-minute imports at 30 seconds.
  - `DeadlineExceededException` (added earlier, but never actually produced by this client since
    nothing enforced a server-side deadline) is now reachable: a timed-out call throws it, not a
    generic exception. `StatusCode.DeadlineExceeded` was already excluded from
    `ErrorMapper.TransientCodes`, so a timeout is never auto-retried.
  - New `SpiceDB.Client.Tests/DeadlineTests.cs`, against a real Kestrel-hosted gRPC server
    (`Grpc.AspNetCore.Server`, added as a test-only dependency) whose handlers deliberately
    stall: a unary call against a stub that never responds throws `DeadlineExceededException`
    well before the stall completes (not a hang), a per-call `timeout` overrides a much larger
    client default, a streaming call outlives a tiny unary default instead of inheriting it,
    bulk import is both unbounded by the default and still honors an explicit `timeout`, and a
    live call is actually cancelled mid-flight via `CancellationToken` (not just asserted against
    a Moq `It.IsAny<CancellationToken>()`, which proves plumbing, not propagation).
    Every call is wrapped in a watchdog (`Task.WhenAny` against a timeout) so a regression fails
    the test instead of hanging CI. A Moq-mocked service client (as used elsewhere in this suite)
    can't prove a deadline is actually enforced, since grpc's deadline machinery lives below the
    mock, inside `Grpc.Net.Client`'s HTTP/2 pipeline.
  - New `examples/CallDeadlines/`, run against a real SpiceDB rather than a mock: constructs a
    client via the documented `defaultTimeout` parameter, overrides it per-call, and confirms
    bulk import is unbounded by default. (See the CI entry below: when this was written that
    claim was aspirational, since no example project was in any solution.)
- **2026-08-18**: No example was built or run by CI. Every project under `examples/` is an xunit
  project, but none of the twelve was listed in `SpiceDB.Client.sln`, and both `mage test` and
  `mage integrationTest` ran `dotnet test SpiceDB.Client.sln` -- so `examples/` was never
  compiled, let alone executed, on any job. A signature change that broke an example would have
  gone green, and the "run against a real SpiceDB rather than a mock" claim on
  `examples/CallDeadlines/` above described an intent, not something that happened.

  Added `SpiceDB.Client.Examples.sln`, containing the client plus all twelve example projects.
  `mage test` now *builds* it (catching a broken example in the unit job, which has no SpiceDB to
  run against) and `mage integrationTest` *runs* it after starting SpiceDB, matching how every
  other client in this repo treats its examples. Two solutions rather than one because the
  examples require a live server and the unit job does not have one. All twelve compile as-is; no
  example was modified.

- **2026-08-18**: Retry safety, per root `DESIGN.md` "RULE: Automatic retry is for idempotent
  operations only". Three changes:
  - `RESOURCE_EXHAUSTED` is no longer retried. In SpiceDB it signals memory load-shed (retrying
    adds load to an already-overloaded server) or a deterministic `MaxDepthExceeded` (retrying can
    never succeed — it re-runs the most expensive class of check several times before surfacing
    the same error). Previously `ErrorMapper.TransientCodes`/`IsTransient` treated both
    `StatusCode.ResourceExhausted` and `ResourceExhaustedException` as transient.
  - Mutations (`WriteAsync`, `DeleteRelationshipsAsync`, `WriteSchemaAsync`, and the
    `ExperimentalRegisterRelationshipCounterAsync`/`ExperimentalUnregisterRelationshipCounterAsync`
    calls) are no longer retried on a transient error, even though the underlying gRPC code is
    retryable. A `WriteRelationships` carrying `OPERATION_CREATE` or preconditions is not
    idempotent: if it commits and the response is lost (a rolling restart, a proxy dropping the
    connection), a retry would surface `ALREADY_EXISTS`/`FAILED_PRECONDITION` for a write that in
    fact succeeded, and the caller would wrongly conclude it had failed. Reads still retry
    automatically. All five mutation call sites previously went through `RetryAsync`; they now go
    through a new `CallOnceAsync`, which converts the gRPC error without retrying.
  - Backoff is now full-jitter (`JitteredDelay`, `uniform(0, cap)`) instead of plain exponential
    doubling, applied everywhere a retry sleeps — the unary `RetryAsync` helper and all five
    streaming-establishment retry loops (`ReadRelationshipsAsync`, `LookupResourcesAsync`,
    `LookupSubjectsAsync`, `ExportRelationshipsAsync`, `UpdatesAsync`). Without jitter, every
    client in a fleet retries on the same schedule after a server restart, turning the recovery
    into a thundering herd.

  `ErrorsTests.cs`'s `IsTransient_ResourceExhausted_ReturnsTrue` and
  `IsTransient_TypedResourceExhaustedException_ReturnsTrue` are renamed to `...ReturnsFalse` and
  inverted, since the old assertions were exactly the defect this fixes. New coverage in
  `UnaryRetrySafetyTests.cs` (a mutation is attempted exactly once on a retryable error and on
  `RESOURCE_EXHAUSTED`; a read is retried; `RESOURCE_EXHAUSTED` is never retried on a read;
  backoff varies between calls).
- **2026-08-18**: **`UpdateFromProto` mapped any unrecognized watch operation — including
  `OPERATION_UNSPECIFIED` and any future wire value — to `UpdateOperation.Touch`.** A cache or
  index mirror consuming the watch stream would upsert a relationship on an update it could not
  actually interpret, which may in fact have been a delete. `UpdateOperation` gains a new
  `Unspecified = 0` value (purely additive — the existing members keep their explicit numeric
  values), and the mapper's `_ =>` arm now returns it instead of `Touch`, matching what
  `ToTreeOperation` ten lines above and both `permissionship` mappers in this file already do:
  server-supplied data the client does not recognise must degrade to a safe, non-permissive
  default, never a write. Root `DESIGN.md`, "RULE: A conversion that cannot preserve meaning must
  fail", clause 2.
- **2026-08-18**: `Filter.ToProto()` silently dropped `SubjectID`/`SubjectRelation` when
  `SubjectType` was not set, instead of raising. `OptionalSubjectId`/`OptionalRelation` were
  nested inside `if (!string.IsNullOrEmpty(SubjectType))`, so
  `new Filter("document").WithSubjectID("alice")` produced a proto `RelationshipFilter` with
  **no subject constraint at all**, while the `Filter` object itself still reported
  `SubjectID == "alice"` — a caller reading the object back would see the constraint they set;
  the server would not. `DeleteRelationshipsAsync` called with that filter deleted every
  relationship on every document, not just alice's. The wire's `SubjectFilter.subject_type` is a
  required field, so there is no way to express a subject ID/relation constraint without it,
  which makes silent widening the one unsafe resolution — `ToProto()` now throws
  `InvalidArgumentException` naming the field that was set without `SubjectType`, per root
  `DESIGN.md` "RULE: A conversion that cannot preserve meaning must fail", clause 1
  (caller-supplied data the client cannot represent MUST raise a typed error). Replaces
  `FilterTests.ToProto_WithoutSubjectType_DoesNotSetSubjectFilter`, which asserted the silent-drop
  behavior as correct, with tests asserting the throw for both `SubjectID` and `SubjectRelation`.
- **2026-08-18**: `CheckPermissionsCoreAsync` did not verify that
  `CheckBulkPermissions` returned as many pairs as were requested — the
  result array was sized off `resp.Pairs.Count` instead of the request's
  item count, and nothing compared the two. The proto guarantees pairs are
  returned in request order but says nothing about count, so a response
  with fewer pairs than items would silently produce an array shorter than
  `relationships` — every `results[i]` after the gap misaligned with
  `relationships[i]`, attributing one resource's answer to another. It now
  throws `SpiceDBException` naming both counts (`"CheckBulkPermissions
  returned N pair(s) for M request item(s)."`) when they differ, before
  mapping any pair, and also guards the malformed-oneof case — a
  `CheckBulkPermissionsPair` with neither `Item` nor `Error` set — the same
  way `spicedb-rust` already did, instead of dereferencing a `null` `Item`.
- **2026-08-18**: `Relationship.ToProto()` stringified every `CaveatContext`
  value (`Value.ForString(value?.ToString() ?? "")`), and `Relationship.FromProto`
  read every value back via `Value.StringValue` only — a round trip destroyed
  types in both directions. A caveat like `now < 100` stored against a
  stringified `"50"` fails to evaluate, and fails *silently*, as a conditional
  result rather than an error. Unlike a bad check-time context (which fails
  one call), a bad write-time context is **persisted**: every future check
  against that relationship mis-evaluates, and re-checking with correct
  context never repairs it — only rewriting the relationship does. Both
  directions now dispatch on the value's type/`kind` oneof via the same
  converters the check-time path already used correctly:
  `SpiceDBClient.ToProtoValue` (write) and the new
  `SpiceDBClient.FromProtoValue` (read, the inverse). The doc comment on
  `ToProtoValue` previously described the write path's stringification as a
  documented contrast ("unlike `Relationship.ToProto`'s write-time
  CaveatContext conversion, which stringifies every value") — that comment
  described the defect as if it were by design; it's removed now that both
  paths share one converter. No public API shape change — `ToProto`/`FromProto`
  signatures are unchanged, only the values they produce/consume.
- **2026-08-18**: `ToProtoValue`'s final fallback arm still silently stringified a value it could
  not otherwise represent (`_ => Value.ForString(value.ToString() ?? "")`) — a custom class
  instance, say — instead of raising. This fallback is shared by both the check path
  (`MergeCheckContext`) and the write path (`Relationship.ToProto`), and it was inherited
  unchanged by the write-time fix directly above rather than introduced by it. Caveat context is
  caller-supplied, so per root `DESIGN.md` "RULE: A conversion that cannot preserve meaning must
  fail", clause 1, an unrepresentable value must raise a typed error naming what could not be
  converted, not degrade to a guess (clause 2's server-data carve-out does not apply here). The
  fallback now throws `InvalidArgumentException` naming the unsupported type
  (`"unsupported caveat context value type: {value.GetType()}"`). A new `ToProtoValueForKey`
  wrapper, used at every per-key call site (`MergeCheckContext`'s two loops,
  `Relationship.ToProto`'s loop, and `ToProtoStructFrom` for nested dictionaries), catches that
  and re-raises with the offending key named (`"caveat context key \"K\": ..."`), matching
  `spicedb-ruby`'s message shape; a nested dictionary's error traces the full key path, since each
  enclosing call adds its own key in turn. No existing test depended on the old stringify-fallback
  behavior — the full suite passed unchanged before new tests for the throw were added.

- **2026-08-18**: `CheckAllAsync`/`CheckAllWithContextAsync` returned `true`
  for zero relationships — LINQ's `Enumerable.All` is vacuously `true` over
  an empty sequence. Root `DESIGN.md`'s "An aggregate over zero checks is not
  a grant" clause names the hazard: a gate like
  `CheckAllAsync(cs, "edit", ct, docs.Select(ToRel).ToArray())` was silently
  granted whenever the derived relationships array came up empty — a filter
  that matched nothing, an upstream returning `[]`. The pre-existing
  `relationships.Length == 0` early return inside the shared
  `CheckPermissionsCoreAsync` (which produces an empty result array, feeding
  the vacuous `All()`) is why neither method ever consulted the server for
  this case; both now guard the empty case explicitly before delegating to
  that shared core and return `false` directly. `CheckAnyAsync`/
  `CheckAnyWithContextAsync` are unchanged — already correctly `false` on
  empty via `Enumerable.Any`.

- **2026-08-16**: A per-item error from `CheckBulkPermissions` (surfaced via
  `CheckPermissionAsync`/`CheckPermissionsAsync`) now maps through
  `ErrorMapper.ToSpiceDBException` like every other RPC in this client,
  instead of discarding the `google.rpc.Status` error code and throwing the
  base `SpiceDBException`. A caller can now distinguish a per-item
  `PERMISSION_DENIED` (→ `PermissionDeniedException`) from any other
  per-item failure without string-matching the exception message. The fix
  synthesizes a `Grpc.Core.RpcException` from the pair's numeric
  `google.rpc.Status` code/message so it can be routed through the existing
  mapper switch unchanged.


- **2026-08-15**: `DefaultDeletePageSize` (the default `DeleteRelationshipsAsync` page size) is now 1,000, not 10,000. SpiceDB's default `--max-delete-relationships-limit` is 1,000, so a default (no explicit `limit`) `DeleteRelationshipsAsync` call against a stock server previously failed with `provided limit 10000 is greater than maximum allowed of 1000`. No API shape change — only the default value sent when `limit` isn't supplied.

- **2026-08-14**: Streaming/bulk methods (`ReadRelationshipsAsync`,
  `LookupResourcesAsync`, `LookupSubjectsAsync`, `ExportRelationshipsAsync`,
  `UpdatesAsync`, `ImportRelationshipsAsync`) now map `Grpc.Core.RpcException`
  raised while opening or iterating the underlying gRPC stream to the native
  `SpiceDBException` hierarchy via `ErrorMapper.ToSpiceDBException`, matching
  the mapping unary calls already got through `RetryAsync`. Previously a raw
  `RpcException` propagated to the `await foreach` consumer instead of e.g.
  `NotFoundException`. No API shape change — only the exception type thrown
  from these `IAsyncEnumerable<T>`/`Task` methods on stream failure.

- **2026-08-14**: Standardized the retryable (transient) status code set,
  aligning with the other SpiceDB clients. This entry set it to
  `{UNAVAILABLE, RESOURCE_EXHAUSTED, ABORTED}`; the shipped set is
  `{UNAVAILABLE, ABORTED}`, since the 2026-08-18 retry-safety entry above
  removed `RESOURCE_EXHAUSTED`. `DEADLINE_EXCEEDED` is no longer treated as transient and
  is no longer retried (the `Grpc.Core.StatusCode.DeadlineExceeded` →
  `DeadlineExceededException` mapping is unchanged; it just no longer counts
  as transient for `ErrorMapper.IsTransient`/`RetryAsync`). Added
  `AbortedException` with a `Grpc.Core.StatusCode.Aborted` mapping in
  `ErrorMapper.ToSpiceDBException`. Also reduced `MaxRetryAttempts` from `5`
  to `3` (4 total attempts instead of 6) to align retry attempts with the
  other clients.

## 0.1.0 (2026-03-18)

Initial release of the idiomatic C# SpiceDB client.

### Features

- **Security-obvious constructors**: `SpiceDBClient.CreatePlaintext()` / `SpiceDBClient.CreateSystemTls()` make TLS posture explicit
- **Full API coverage**: all non-deprecated SpiceDB APIs exposed through idiomatic C# types
  - Permission checks (`CheckPermission`, `CheckPermissions`, `CheckAny`, `CheckAll`) via BulkCheckPermissions
  - Relationship CRUD with `Transaction` builder and preconditions
  - Streaming reads via `IAsyncEnumerable<T>` with transparent cursor pagination
  - `LookupResources` and `LookupSubjects`
  - Schema read, write, reflect, diff, computable permissions, dependent relations
  - Bulk import/export
  - Watch for relationship changes
  - Experimental relationship counters
- **C# records**: `Relationship` and `Filter` are sealed records with `FromTriple`, `FromTuple`, `WithCaveat`, `WithExpiration`
- **Native `PermissionTree`**: `ExpandPermissionTreeAsync`/`ExpandResult` return a native `PermissionTree` record family (`ObjectRef`, `SubjectRef`, `IntermediateNode`, `LeafNode`, `TreeOperation`) instead of the proto `PermissionRelationshipTree`
- **Explicit consistency**: every read requires a `ConsistencyStrategy` (`Full`, `MinLatency`, `AtLeast`, `Snapshot`, `AtLeastOrFull`, `AtLeastOrMinLatency`)
- **Typed exceptions**: `SpiceDBException` hierarchy (`PermissionDeniedException`, `NotFoundException`, `AlreadyExistsException`, `InvalidArgumentException`)
- **Automatic retry**: exponential backoff for transient gRPC errors
- **`IAsyncDisposable`**: proper async resource cleanup
- **10 examples** covering all major API surfaces, doubling as xUnit integration tests
- **Targets .NET 8+**
