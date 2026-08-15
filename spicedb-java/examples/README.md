# spicedb-java Examples

Each test class is a standalone, runnable example that also serves as an
integration test.

## Running

Examples require a running SpiceDB instance. Start one locally:

```bash
docker run --rm -p 50051:50051 authzed/spicedb serve-testing
```

Then run the examples:

```bash
cd spicedb-java
./gradlew :examples:test
```

Or use `mage test` which starts a SpiceDB container automatically.

## Examples

| Test Class | Description |
|---|---|
| `CheckPermissionTest` | Basic permission check with `checkPermission` |
| `WriteRelationshipsTest` | Writing relationships with `Transaction` builder |
| `ReadRelationshipsTest` | Reading relationships with cursor-based auto-pagination |
| `LookupResourcesTest` | Finding resources a subject can access |
| `LookupSubjectsTest` | Finding subjects with access to a resource |
| `WatchChangesTest` | Watching for relationship changes via the watch API |
| `SchemaManagementTest` | Schema read/write operations |
| `BulkOperationsTest` | Bulk permission checks with `checkPermissions`, `checkAll`, `checkAny`, plus bulk `importRelationships`/`exportRelationships` |
| `SchemaReflectionTest` | Schema reflection, computable permissions, dependent relations, schema diff |
| `RelationshipCountersTest` | Experimental relationship counter registration and reading |
| `ExpandPermissionTreeTest` | Expanding a permission with `expandPermissionTree` and walking the native `PermissionTree` (intermediate/leaf nodes, subjects) |
