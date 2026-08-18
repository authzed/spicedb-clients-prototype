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
- `watch_changes/` — watching for relationship changes via the Watch API.
  This example streams indefinitely, so it's skipped by the integration test
  runner (`mage integrationTest`).
