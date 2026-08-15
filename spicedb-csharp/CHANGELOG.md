# Changelog

## Unreleased

### Added

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
  calls are unaffected (no preconditions, 10,000-item page size, partial
  deletions allowed, same as before). `mustMatch`/`mustNotMatch` add
  MUST_MATCH/MUST_NOT_MATCH preconditions (built the same way as
  `Transaction.MustMatch`/`MustNotMatch`, via the shared internal
  `Transaction.BuildPrecondition` helper) that guard the delete, rejecting it
  if unsatisfied; `limit` overrides the default 10,000-per-call page size.
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
