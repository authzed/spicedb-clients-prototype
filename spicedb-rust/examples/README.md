# spicedb-rust Examples

Each file is a standalone, runnable example that also serves as an integration
test.

## Running

`mage integrationTest` is the one command that does everything: it starts the
SpiceDB container from `docker-compose.test.yml`, waits for it, runs the
`#[ignore]`d live tests, and then runs every example against that server. It
tears the container down afterwards. (`mage test` runs the unit suite, clippy
and the example-wiring check -- it starts no container and runs no example.)

```bash
mage integrationTest
```

To run one example by hand you need a SpiceDB of your own. Every example reads
`SPICEDB_ENDPOINT` and `SPICEDB_TOKEN`, defaulting to `localhost:50051` and
`testtoken` -- the endpoint and preshared key in `docker-compose.test.yml`:

```bash
docker compose -f docker-compose.test.yml up -d
cargo run --example check_permission

# or against any other SpiceDB
SPICEDB_ENDPOINT=spicedb.internal:50051 SPICEDB_TOKEN=hunter2 \
    cargo run --example check_permission
```

If port 50051 is taken, `SPICEDB_TEST_PORT` chooses the port the compose file
publishes on, and `mage integrationTest` derives it from `SPICEDB_ENDPOINT`:

```bash
SPICEDB_ENDPOINT=localhost:50071 mage integrationTest
```

The examples share one server and run in name order, so each deletes the
`document` relationships it wrote before exiting. Skipping that cleanup makes a
*later* example fail: SpiceDB refuses a `WriteSchema` that drops a relation
while a relationship still exists under it.

## What runs in CI

Every example in the table below is executed by `mage integrationTest`, which
is what the `integration` job in `.github/workflows/rust.yaml` runs, with one
exception: `watch_changes` is an open-ended stream with no bounded consumer
yet, so the runner skips it by name and prints the skip. The runner also
asserts how many examples it executed, so an example that is renamed out of the
glob fails the job instead of quietly shrinking the run. See root `DESIGN.md`,
"RULE: An example must be executed by CI and must be able to fail".

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
| `expand_permission_tree` | Expanding a permission tree and walking the native `PermissionTree` |
| `relationship_counters` | Experimental relationship counters |
| `raw_escape_hatch` | The `raw_proto()` escape hatch: driving the generated tonic client on this client's own connection to send `optional_transaction_metadata` (a proto field this crate does not wrap) and to call the single-check `CheckPermission` RPC |
| `call_deadlines` | Constructing a client with `default_timeout`, a per-call `_with_timeout` override, and confirming bulk import isn't bounded by the unary default |
