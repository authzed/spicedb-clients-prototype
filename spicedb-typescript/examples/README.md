# spicedb-typescript Examples

Each subdirectory is a standalone, runnable TypeScript example that also serves
as an integration test.

## Running

`mage integrationTest` is the one command that does everything: it starts the
SpiceDB container from `docker-compose.test.yml`, waits for it, runs every
example against that server, and tears the container down afterwards.
(`mage test` builds and runs the unit suite and the example-wiring check -- it
starts no container and runs no example.)

```bash
mage integrationTest
```

To run one example by hand you need a SpiceDB of your own. Every example that
uses the shared server reads `SPICEDB_ENDPOINT` and `SPICEDB_TOKEN`, defaulting
to `localhost:50051` and `testtoken` -- the endpoint and preshared key in
`docker-compose.test.yml`:

```bash
docker compose -f docker-compose.test.yml up -d
npx tsx examples/check_permission/index.ts

# or against any other SpiceDB
SPICEDB_ENDPOINT=spicedb.internal:50051 SPICEDB_TOKEN=hunter2 \
    npx tsx examples/check_permission/index.ts
```

`custom_tls/` is the exception: it stands up its own TLS-terminated server and
ignores both variables, since a plaintext SpiceDB has nothing to demonstrate
about trust material.

If port 50051 is taken, `SPICEDB_TEST_PORT` chooses the port the compose file
publishes on, and `mage integrationTest` derives it from `SPICEDB_ENDPOINT`:

```bash
SPICEDB_ENDPOINT=localhost:50071 mage integrationTest
```

## What runs in CI

Every example listed below is executed by `mage integrationTest`, which is what
the `integration` job in `.github/workflows/typescript.yaml` runs, with one
exception: `watch_changes` is an open-ended stream with no bounded consumer
yet, so the runner skips it by name and prints the skip. The runner also
asserts how many examples it executed, so an example that is renamed out of the
glob fails the job instead of quietly shrinking the run. See root `DESIGN.md`,
"RULE: An example must be executed by CI and must be able to fail".

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
