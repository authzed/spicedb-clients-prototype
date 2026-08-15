# Changelog

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
