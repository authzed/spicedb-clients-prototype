# spicedb-typescript Examples

Each subdirectory is a standalone, runnable TypeScript example that also serves
as an integration test.

## Running

```bash
export SPICEDB_ENDPOINT=localhost:50051
export SPICEDB_TOKEN=testtoken
```

Or use `mage test` which starts a SpiceDB container automatically.

## Examples

- `check_permission/` — `checkPermission`, `checkPermissions`, `checkAny`, `checkAll`.
- `bulk_operations/` — bulk permission checks, `importBulkRelationships`, and
  `exportBulkRelationships`.
- `expand_permission_tree/` — `expandPermissionTree` and walking the native
  `PermissionTree` (intermediate nodes with a `TreeOperation`, leaf nodes with
  concrete subjects).
- `lookup_resources/` — `lookupResources`, including reading `permissionship`.
- `lookup_subjects/` — `lookupSubjects`, including the wildcard/excluded-
  subjects case.
- `read_relationships/` — reading relationships via async iterator, with
  filters.
- `write_relationships/` — the `Transaction` builder: create, touch, delete,
  and preconditions.
- `schema_management/` — reading and writing the SpiceDB schema.
- `call_deadlines/` — `defaultTimeoutMs` on `createSpiceDBClient`, a per-call
  `timeoutMs` override, and confirming bulk import isn't bounded by the
  unary default.
- `raw_escape_hatch/` — the `raw()` escape hatch: driving the generated
  Connect client directly to send `optionalTransactionMetadata` (a proto field
  this client does not wrap) and to call the single-check `CheckPermission`
  RPC, then handing the same connection back to the idiomatic API.
- `custom_tls/` — reaching a SpiceDB behind a private CA with `tls.caCert`,
  and mutual TLS with `tls.clientCert`/`tls.clientKey`. Brings up its own
  TLS-terminated endpoint — the only example that does not use the shared
  SpiceDB at `localhost:50051`, since a plaintext server has nothing to say
  about trust material.
- `watch_changes/` — watching for relationship changes via the Watch API.
  This example streams indefinitely, so it's skipped by the integration test
  runner (`mage integrationTest`).
