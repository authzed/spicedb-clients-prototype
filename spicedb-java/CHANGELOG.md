# Changelog

## Unreleased

### Breaking Changes

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

- **Streaming error mapping**: `readRelationships`, `lookupResources`, `lookupSubjects`, `exportRelationships`, and `updates` now map mid-stream gRPC errors (raised while iterating `serverStream.hasNext()`/`next()`) to the typed `SpiceDBException` hierarchy via `ErrorMapper`, instead of leaking a raw `io.grpc.StatusRuntimeException` to stream consumers
- **Cancelable streams**: `readRelationships`, `lookupResources`, `exportRelationships`, and `updates` now bind their underlying gRPC server-streaming call to a per-stream `io.grpc.Context.CancellableContext` and cancel it when the returned `Stream` is closed (e.g. via try-with-resources), so `close()` actually stops the server from producing further results instead of leaving the call open (`lookupSubjects` streams eagerly and has no open call to cancel)
- **Delete page size correction**: `DEFAULT_DELETE_PAGE_SIZE` is now 10,000 (matching Go client and DESIGN.md spec), not 1,000
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
