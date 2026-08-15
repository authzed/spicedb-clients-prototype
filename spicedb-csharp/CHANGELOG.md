# Changelog

## Unreleased

### Breaking Changes

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
