# spicedb-ruby Examples

Each subdirectory is a standalone RSpec spec file that also serves as an
integration test.

## Running

Examples require a running SpiceDB instance. Set environment variables:

```bash
export SPICEDB_ENDPOINT=localhost:50051
export SPICEDB_TOKEN=testtoken
```

Then run all examples:

```bash
cd examples
bundle exec rspec
```

Or run a single example:

```bash
bundle exec rspec check_permission/check_permission_spec.rb
```

## Examples

| Directory | Description |
|-----------|-------------|
| `check_permission/` | Basic permission check with `check_permission` |
| `write_relationships/` | Writing relationships with transaction builder |
| `delete_relationships/` | Deleting relationships, including guarded deletes with `must_match:`/`must_not_match:` and `limit:` |
| `read_relationships/` | Reading relationships with enumerator |
| `lookup_resources/` | Finding resources a subject can access |
| `lookup_subjects/` | Finding subjects with access to a resource |
| `watch_changes/` | Watching for relationship changes |
| `schema_management/` | Schema read/write operations |
| `bulk_operations/` | Bulk checks, check_all, check_any, and import |
| `call_deadlines/` | Constructing a client with `default_timeout:`, a per-call `timeout:` override, and confirming bulk import isn't bounded by the unary default |
| `schema_reflection/` | Schema reflection, computable permissions, diffs |
| `relationship_counters/` | Registering and reading relationship counters |
| `expand_permission_tree/` | Expanding a permission into its native `PermissionTree` (intermediate/leaf nodes, subjects) |
