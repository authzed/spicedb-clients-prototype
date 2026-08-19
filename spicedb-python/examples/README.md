# spicedb-python Examples

Each subdirectory is a standalone, runnable example that also serves as an
integration test.

## Running

`mage integrationTest` is the one command that does everything: it starts the
SpiceDB container from `docker-compose.test.yml`, waits for it, runs every
example suite against that server, and tears the container down afterwards.
(`mage test` runs `tests/` and the example-wiring check -- it starts no
container and runs no example.)

```bash
mage integrationTest
```

To run one example by hand you need a SpiceDB of your own. `examples/conftest.py`
reads `SPICEDB_ENDPOINT` and `SPICEDB_TOKEN`, defaulting to `localhost:50051`
and `somerandomkeyhere` -- the endpoint and preshared key in
`docker-compose.test.yml` -- and every example that uses the shared server takes
both from there:

```bash
docker compose -f docker-compose.test.yml up -d
uv run pytest examples/check_permission -v

# or against any other SpiceDB
SPICEDB_ENDPOINT=spicedb.internal:50051 SPICEDB_TOKEN=hunter2 \
    uv run pytest examples/check_permission -v
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

Every example in the table below is executed by `mage integrationTest`, which
is what the `integration` job in `.github/workflows/python.yaml` runs, with two
exceptions: `watch_changes/` and `sync_watch_changes/` are open-ended streams
with no bounded consumer yet, so the runner skips them by name and prints each
skip. The runner names the remaining example directories on the pytest command
line rather than filtering with `-k`, and then reads pytest's JUnit report to
confirm every selected example actually contributed a test case -- a `-k`
filter that matches nothing exits 0, which is how a suite can report green over
nothing at all. See root `DESIGN.md`, "RULE: An example must be executed by CI
and must be able to fail".

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
