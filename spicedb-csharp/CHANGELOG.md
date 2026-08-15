# Changelog

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
