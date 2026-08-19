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
| `bulk_operations/` | Bulk checks, batch writes, and bulk import/export |
| `expand_permission_tree/` | Expanding a permission into its tree of subjects |
| `schema_reflection/` | Schema reflection, computable permissions, dependent relations, diff |
| `relationship_counters/` | Registering, reading, and unregistering relationship counters |
| `raw_escape_hatch/` | The `RawProto()` escape hatch: driving the generated service client on this client's own connection to send `OptionalTransactionMetadata` (a proto field this package does not wrap) and to call the single-check `CheckPermission` RPC |
