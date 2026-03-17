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
| `WatchChanges/` | Watching for changes |
| `SchemaManagement/` | Schema read/write |
| `BulkOperations/` | Bulk checks and batch writes |
| `SchemaReflection/` | Schema reflection, computable permissions, diffs |
| `RelationshipCounters/` | Relationship counter registration and counting |
