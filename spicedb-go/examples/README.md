# spicedb-go Examples

Each subdirectory is a standalone, runnable example that also serves as an
integration test.

## Running

Examples require a running SpiceDB instance. Set environment variables:

```bash
export SPICEDB_ENDPOINT=localhost:50051
export SPICEDB_TOKEN=testtoken
```

Or use `mage test` which starts a SpiceDB container automatically.

## Examples

| Directory | Description |
|-----------|-------------|
| `check_permission/` | Basic permission check |
| `write_relationships/` | Writing relationships |
| `read_relationships/` | Reading relationships with iterators |
| `delete_relationships/` | Deleting relationships, including precondition-guarded deletes |
| `lookup_resources/` | Resource lookup |
| `lookup_subjects/` | Subject lookup |
| `watch_changes/` | Watching for changes |
| `schema_management/` | Schema read/write |
| `bulk_operations/` | Bulk checks and imports |
