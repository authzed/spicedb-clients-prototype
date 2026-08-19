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
| `caveated_check/` | Checking a caveated relationship with no context supplied (CONDITIONAL_PERMISSION), and resolving the same conditional to a grant by supplying the missing context via `Relationship.check_context` |
| `read_your_writes/` | Using `CheckResult.checked_at`/`LookupResource.looked_up_at` with `at_least()` to make a later call observe an earlier write |
| `write_relationships/` | Writing relationships with the transaction builder |
| `read_relationships/` | Reading relationships with an async iterator |
| `delete_relationships/` | Deleting relationships, including precondition-guarded deletes |
| `lookup_resources/` | Resource lookup |
| `lookup_subjects/` | Subject lookup |
| `watch_changes/` | Watching for changes |
| `schema_management/` | Schema read/write/reflect/diff, plus computable_permissions/dependent_relations introspection |
| `bulk_operations/` | Bulk checks and bulk relationship import/export |
| `expand_permission_tree/` | Expanding a permission into its tree of subjects |
| `call_deadlines/` | Constructing a client with `default_timeout`, a per-call `timeout` override, and confirming bulk import isn't bounded by the unary default |
| `sync_check_permission/` | Basic permission check with `spicedb.sync` — build the client once at startup and reuse it, no event loop required |
| `sync_write_relationships/` | Writing relationships with the transaction builder, synchronously |
| `sync_read_relationships/` | Reading relationships with a plain `for` loop instead of `async for` |
| `sync_watch_changes/` | Watching for changes from a blocking generator |
| `raw_escape_hatch/` | The `raw_grpc()` escape hatch: building a generated stub on the client's own channel to send `optional_transaction_metadata` (a proto field this client does not wrap) and to call the single-check `CheckPermission` RPC, from both the async and sync flavors |
| `custom_tls/` | Reaching a SpiceDB behind a private CA with `ca_cert`, and mutual TLS with `client_cert`/`client_key`. Brings its own TLS endpoint — the only example that does not use the shared SpiceDB at `localhost:50051`, since a plaintext server has nothing to say about trust material |
