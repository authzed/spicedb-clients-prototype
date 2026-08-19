# spicedb-ruby Examples

Each subdirectory is a standalone RSpec spec file that also serves as an
integration test.

## Running

`mage integrationTest` is the one command that does everything: it starts the
SpiceDB container from `docker-compose.test.yml`, waits for it, runs every
example spec against that server, and tears the container down afterwards.
(`mage test` runs `spec/` and the example-wiring check -- it starts no
container and runs no example.)

```bash
mage integrationTest
```

To run the examples by hand you need a SpiceDB of your own.
`examples/spec_helper.rb` reads `SPICEDB_ENDPOINT` and `SPICEDB_TOKEN`,
defaulting to `localhost:50051` and `somerandomkeyhere` -- the endpoint and
preshared key in `docker-compose.test.yml`. Run rspec **from the gem root**,
naming the directory:

```bash
docker compose -f docker-compose.test.yml up -d
bundle exec rspec examples/

# one example
bundle exec rspec examples/check_permission

# or against any other SpiceDB
SPICEDB_ENDPOINT=spicedb.internal:50051 SPICEDB_TOKEN=hunter2 \
    bundle exec rspec examples/
```

Naming the path matters. `cd examples && bundle exec rspec` -- which this file
used to document -- loads **zero** spec files and exits **0**: with no `.rspec`
present, RSpec falls back to its default path `spec`, which does not exist
inside `examples/`, so it prints "No examples found" and reports success.

`custom_tls/` is the exception among the examples: it stands up its own
TLS-terminated server and ignores both variables, since a plaintext SpiceDB has
nothing to demonstrate about trust material.

If port 50051 is taken, `SPICEDB_TEST_PORT` chooses the port the compose file
publishes on, and `mage integrationTest` derives it from `SPICEDB_ENDPOINT`:

```bash
SPICEDB_ENDPOINT=localhost:50071 mage integrationTest
```

## What runs in CI

Every example in the table below is executed by `mage integrationTest`, which
is what the `integration` job in `.github/workflows/ruby.yaml` runs, with one
exception: `watch_changes/` is an open-ended stream with no bounded consumer
yet, so the runner skips it by name and prints the skip. The runner names the
remaining example directories on the rspec command line rather than filtering
with `--tag ~watch`, and then reads rspec's JSON report to confirm every
selected example actually contributed a spec. Two separate things were wrong
with the tag filter: it excluded `watch_changes_spec.rb` from every CI run the
repo has ever done -- the `:watch` tags were added later, by a commit that had
no reason to notice the flag -- and, being a filter, it would have exited 0
just the same had it matched nothing at all. Naming the directories fixes the
first; reading the report fixes the second. See root `DESIGN.md`, "RULE: An
example must be executed by CI and must be able to fail".

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
| `raw_escape_hatch/` | The `#proto_client` escape hatch: driving the generated stub on this client's own connection to send `optional_transaction_metadata` (a proto field this gem does not wrap) and to call the single-check `CheckPermission` RPC |
| `custom_tls/` | Reaching a SpiceDB behind a private CA with `new_custom_tls(ca_cert:)`, and mutual TLS with `client_cert:`/`client_key:`. Brings up its own TLS-terminated endpoint — the only example tagged `:no_spicedb`, since a plaintext server has nothing to demonstrate about trust material |
