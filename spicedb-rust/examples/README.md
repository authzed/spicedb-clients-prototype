# spicedb-rust Examples

Each file is a standalone, runnable example that also serves as an integration
test.

## Running

Examples require a running SpiceDB instance. Set environment variables:

```bash
export SPICEDB_ENDPOINT=localhost:50051
export SPICEDB_TOKEN=testtoken
```

Or use `mage test` which starts a SpiceDB container automatically.

Run a specific example:

```bash
cargo run --example check_permission
```

## Examples

| Example | Description |
|---------|-------------|
| `check_permission` | Basic permission check |
| `write_relationships` | Writing relationships with preconditions, and a guarded delete with `DeleteOptions` |
| `read_relationships` | Reading relationships with filters |
| `lookup_resources` | Finding resources a subject can access |
| `lookup_subjects` | Finding subjects with access to a resource |
| `watch_changes` | Watching for relationship changes |
| `schema_management` | Schema read/write |
| `bulk_operations` | Bulk checks, imports, and exports |
| `schema_reflection` | Schema reflection, computable permissions, diffs |
| `relationship_counters` | Experimental relationship counters |
