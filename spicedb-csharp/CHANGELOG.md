# Changelog

## Unreleased

### Added

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
  (`{UNAVAILABLE, RESOURCE_EXHAUSTED, ABORTED}`), reusing the same backoff and
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

- **2026-08-14**: Standardized the retryable (transient) status code set to
  `{UNAVAILABLE, RESOURCE_EXHAUSTED, ABORTED}`, aligning with the other
  SpiceDB clients. `DEADLINE_EXCEEDED` is no longer treated as transient and
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
