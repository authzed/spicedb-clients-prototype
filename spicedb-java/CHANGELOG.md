# Changelog

## Unreleased

### Fixes

- **Streaming error mapping**: `readRelationships`, `lookupResources`, `lookupSubjects`, `exportRelationships`, and `updates` now map mid-stream gRPC errors (raised while iterating `serverStream.hasNext()`/`next()`) to the typed `SpiceDBException` hierarchy via `ErrorMapper`, instead of leaking a raw `io.grpc.StatusRuntimeException` to stream consumers
- **Cancelable streams**: `readRelationships`, `lookupResources`, `exportRelationships`, and `updates` now bind their underlying gRPC server-streaming call to a per-stream `io.grpc.Context.CancellableContext` and cancel it when the returned `Stream` is closed (e.g. via try-with-resources), so `close()` actually stops the server from producing further results instead of leaving the call open (`lookupSubjects` streams eagerly and has no open call to cancel)
- **Delete page size correction**: `DEFAULT_DELETE_PAGE_SIZE` is now 10,000 (matching Go client and DESIGN.md spec), not 1,000
- **Escape-hatch documentation**: `ClientOption.apply()` and `create(...)` are now documented as advanced escape hatches for gRPC channel configuration. Removed unused `DEFAULT_CHECK_BATCH_SIZE` constant

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
