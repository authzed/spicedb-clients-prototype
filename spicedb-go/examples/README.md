# spicedb-go Examples

Each subdirectory is a standalone, runnable example that also serves as an
integration test.

## Running

`mage integrationTest` is the one command that does everything: it starts the
SpiceDB container from `docker-compose.test.yml`, waits for it, runs every
example against that server, and tears the container down afterwards.
(`mage test` builds the examples and runs the unit suite and the example-wiring
check -- it starts no container and runs no example.)

```bash
mage integrationTest
```

To run one example by hand you need a SpiceDB of your own. Every example reads
`SPICEDB_ENDPOINT` and `SPICEDB_TOKEN`, defaulting to `localhost:50051` and
`somerandomkeyhere` -- the endpoint and preshared key in
`docker-compose.test.yml`:

```bash
docker compose -f docker-compose.test.yml up -d
go run ./examples/check_permission

# or against any other SpiceDB
SPICEDB_ENDPOINT=spicedb.internal:50051 SPICEDB_TOKEN=hunter2 \
    go run ./examples/check_permission
```

If port 50051 is taken, `SPICEDB_TEST_PORT` chooses the port the compose file
publishes on, and `mage integrationTest` derives it from `SPICEDB_ENDPOINT`:

```bash
SPICEDB_ENDPOINT=localhost:50071 mage integrationTest
```

## What runs in CI

Every example in the table below is executed by `mage integrationTest`, which
is what the `integration` job in `.github/workflows/go.yaml` runs. Nothing is
skipped: `watch_changes` used to be, for being an open-ended stream with no
bounded consumer, and it now has one. The runner reconciles the examples on
disk against the list in `Magefile.go` in both directions, so an example that
is renamed out of the glob fails the job instead of quietly shrinking the run.
See root `DESIGN.md`, "RULE: An example must be executed by CI and must be able
to fail".

## Examples

| Directory | Description |
|-----------|-------------|
| `check_permission/` | Basic permission check |
| `write_relationships/` | Writing relationships |
| `read_relationships/` | Reading relationships with iterators |
| `delete_relationships/` | Deleting relationships, including precondition-guarded deletes |
| `lookup_resources/` | Resource lookup |
| `lookup_subjects/` | Subject lookup |
| `watch_changes/` | Watching for changes with a bounded consumer that cancels the stream explicitly |
| `call_deadlines/` | Bounding calls with a `context.Context` deadline, including a call against a server that never answers |
| `error_mapping/` | Recovering from `OUT_OF_RANGE` (stale ZedToken) and `UNAUTHENTICATED` without parsing a message |
| `insecure_opt_in/` | Why plaintext is loopback-only, and the named opt-in a remote plaintext host requires |
| `retry_policy/` | Which calls are retried for you and which are not, counted server-side |
| `unrepresentable_values/` | Caller data that cannot convert fails loudly; unknown server enums degrade safely |
| `schema_management/` | Schema read/write |
| `bulk_operations/` | Bulk checks, batch writes, and bulk import/export |
| `expand_permission_tree/` | Expanding a permission into its tree of subjects |
| `schema_reflection/` | Schema reflection, computable permissions, dependent relations, diff |
| `relationship_counters/` | Registering, reading, and unregistering relationship counters |
| `raw_escape_hatch/` | The `RawProto()` escape hatch: driving the generated service client on this client's own connection to send `OptionalTransactionMetadata` (a proto field this package does not wrap) and to call the single-check `CheckPermission` RPC |
