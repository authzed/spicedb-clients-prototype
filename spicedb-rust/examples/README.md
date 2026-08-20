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

The examples share one server, so each clears the `document` relationships
*before* writing its schema rather than after finishing with them. SpiceDB
refuses a `WriteSchema` that drops a relation while a relationship still exists
under it, so what a previous example left behind is the next example's problem
-- and a cleanup at exit does nothing when the example that should have run it
failed first, which turns one genuine failure into several spurious ones. The
delete is deliberately unchecked: on a fresh server there is no `document`
definition yet, which is not a failure.

## What runs in CI

Every example in the table below is executed by `mage integrationTest`, which
is what the `integration` job in `.github/workflows/rust.yaml` runs. Nothing is
skipped: `watch_changes` used to be, for being an open-ended stream with no
bounded consumer, and it now has one. The runner reconciles the examples on
disk against the list in `Magefile.go` in both directions, so an example that
is renamed out of the glob fails the job instead of quietly shrinking the run.
See root `DESIGN.md`, "RULE: An example must be executed by CI and must be able
to fail".

## Examples

| Example | Description |
|---------|-------------|
| `check_permission` | Basic permission check |
| `write_relationships` | Writing relationships with preconditions, and a guarded delete with `DeleteOptions` |
| `read_relationships` | Reading relationships with filters |
| `lookup_resources` | Finding resources a subject can access |
| `lookup_subjects` | Finding subjects with access to a resource |
| `watch_changes` | Watching for relationship changes with a bounded consumer: subscribe, write, consume until the expected update arrives, drop the stream, then resume on a fresh one |
| `schema_management` | Schema read/write |
| `bulk_operations` | Bulk checks, imports, and exports |
| `schema_reflection` | Schema reflection, computable permissions, diffs |
| `expand_permission_tree` | Expanding a permission tree and walking the native `PermissionTree` |
| `relationship_counters` | Experimental relationship counters |
| `raw_escape_hatch` | The `raw_proto()` escape hatch: driving the generated tonic client on this client's own connection to send `optional_transaction_metadata` (a proto field this crate does not wrap) and to call the single-check `CheckPermission` RPC |
| `call_deadlines` | Constructing a client with `default_timeout`, a per-call `_with_timeout` override, and confirming bulk import isn't bounded by the unary default |
| `error_mapping` | Recovering from `OUT_OF_RANGE` (stale ZedToken) and `UNAUTHENTICATED` without parsing a message |
| `insecure_opt_in` | Why `.plaintext()` is loopback-only, and the named opt-in a remote plaintext host requires |
| `retry_policy` | Which calls are retried for you and which are not, counted server-side |
| `unrepresentable_values` | A filter the wire cannot express fails loudly; unknown server enums degrade safely |
