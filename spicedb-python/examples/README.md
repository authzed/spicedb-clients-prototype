# spicedb-python Examples

Each subdirectory is a standalone, runnable example that also serves as an
integration test.

## Running

```bash
export SPICEDB_ENDPOINT=localhost:50051
export SPICEDB_TOKEN=testtoken
```

Or use `mage test` which starts a SpiceDB container automatically.

## Examples

| Directory | Description |
|-----------|-------------|
| `check_permission/` | Basic permission check |
| `write_relationships/` | Writing relationships with the transaction builder |
| `read_relationships/` | Reading relationships with an async iterator |
| `delete_relationships/` | Deleting relationships, including precondition-guarded deletes |
| `lookup_resources/` | Resource lookup |
| `lookup_subjects/` | Subject lookup |
| `watch_changes/` | Watching for changes |
| `schema_management/` | Schema read/write/reflect/diff, plus computable_permissions/dependent_relations introspection |
| `bulk_operations/` | Bulk checks and bulk relationship import/export |
| `expand_permission_tree/` | Expanding a permission into its tree of subjects |
| `sync_check_permission/` | Basic permission check with `spicedb.sync` — build the client once at startup and reuse it, no event loop required |
| `sync_write_relationships/` | Writing relationships with the transaction builder, synchronously |
| `sync_read_relationships/` | Reading relationships with a plain `for` loop instead of `async for` |
| `sync_watch_changes/` | Watching for changes from a blocking generator |
