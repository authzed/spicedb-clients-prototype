# spicedb-csharp Examples

Each subdirectory is a standalone xUnit test project that also serves as an
integration test.

## Running

Examples require a running SpiceDB instance. Start one with:

```bash
docker run --rm -p 50051:50051 authzed/spicedb serve-testing
```

Then run the tests:

```bash
dotnet test
```

Or use `mage test` which starts a SpiceDB container automatically.

## Examples

| Directory | Description |
|-----------|-------------|
| `CheckPermission/` | Basic permission check |
| `WriteRelationships/` | Writing relationships with transactions |
| `ReadRelationships/` | Reading relationships with async enumerables |
| `LookupResources/` | Resource lookup |
| `LookupSubjects/` | Subject lookup |
| `CallDeadlines/` | The `defaultTimeout` construction parameter, a per-call `timeout` override, and confirming bulk import isn't bounded by the unary default |
| `WatchChanges/` | Watching for changes |
| `SchemaManagement/` | Schema read/write |
| `BulkOperations/` | Bulk checks, batch writes, and bulk relationship import/export |
| `SchemaReflection/` | Schema reflection, computable permissions, diffs |
| `RelationshipCounters/` | Relationship counter registration and counting |
| `ExpandPermissionTree/` | Expanding a permission into its native `PermissionTree` of subjects |
| `RawEscapeHatch/` | The `RawProto()` escape hatch: driving the generated service client on this client's own connection to send `OptionalTransactionMetadata` (a proto field this client does not wrap) and to call the single-check `CheckPermission` RPC |
