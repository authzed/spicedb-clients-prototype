# spicedb-java Examples

Each test class is a standalone, runnable example that also serves as an
integration test.

## Running

`mage integrationTest` is the one command that does everything: it starts the
SpiceDB container from `docker-compose.test.yml`, waits for it, runs
`gradle test` (which covers `:lib:test` and `:examples:test`), and tears the
container down afterwards. (`mage test` runs `:lib:test` and the example-wiring
check -- it starts no container and runs no example.)

```bash
mage integrationTest
```

To run the examples by hand you need a SpiceDB of your own. There is **no
`gradlew` wrapper in this project** -- use the `gradle` on your PATH.
`SpiceDBIntegrationTest` reads `SPICEDB_ENDPOINT` and `SPICEDB_TOKEN`,
defaulting to `localhost:50051` and `somerandomkeyhere` -- the endpoint and
preshared key in `docker-compose.test.yml`:

```bash
# from spicedb-java/
docker compose -f docker-compose.test.yml up -d
gradle :examples:test

# one example
gradle :examples:test --tests '*CheckPermissionTest'

# or against any other SpiceDB
SPICEDB_ENDPOINT=spicedb.internal:50051 SPICEDB_TOKEN=hunter2 \
    gradle :examples:test
```

If port 50051 is taken, `SPICEDB_TEST_PORT` chooses the port the compose file
publishes on, and `mage integrationTest` derives it from `SPICEDB_ENDPOINT`:

```bash
SPICEDB_ENDPOINT=localhost:50071 mage integrationTest
```

## What runs in CI

Every example in the table below is executed by `mage integrationTest`, which
is what the `integration` job in `.github/workflows/java.yaml` runs -- there are
no skips here. The runner deletes `examples/build/test-results/test` before
invoking Gradle, so `:examples:test` cannot be reported as up to date without
running, and then reads the JUnit reports it produced to confirm every example
class on disk contributed at least one test case. `mage checkExamples` (also run
by `mage test`) asserts the expected number of example classes are present, so
an example that is renamed out of the glob fails instead of quietly shrinking
the run. See root `DESIGN.md`, "RULE: An example must be executed by CI and must
be able to fail".

All examples share one SpiceDB and run sequentially (`maxParallelForks = 1` in
`examples/build.gradle.kts`). An example whose schema is narrower than
`SpiceDBIntegrationTest.SCHEMA` must call
`SpiceDBIntegrationTest.clearDocumentRelationships` before writing it --
SpiceDB refuses a `WriteSchema` that drops a relation while a relationship
still exists under it, and JUnit's class order is not something to rely on.

## Examples

| Test Class | Description |
|---|---|
| `CheckPermissionTest` | Basic permission check with `checkPermission`, returning `CheckResult` |
| `ConditionalCheckTest` | `CONDITIONAL_PERMISSION` against a live caveated relationship whose context was never supplied — `hasPermission()` must be false |
| `WriteRelationshipsTest` | Writing relationships with `Transaction` builder |
| `ReadRelationshipsTest` | Reading relationships with cursor-based auto-pagination |
| `LookupResourcesTest` | Finding resources a subject can access; the `withDebug` overload reaching `LookupResourcesRequest.with_debug` on the wire |
| `LookupSubjectsTest` | Finding subjects with access to a resource |
| `WatchChangesTest` | Watching for relationship changes via the watch API |
| `SchemaManagementTest` | Schema read/write operations |
| `BulkOperationsTest` | Bulk permission checks with `checkPermissions`, `checkAll`, `checkAny`, plus bulk `importRelationships`/`exportRelationships` |
| `SchemaReflectionTest` | Schema reflection, computable permissions, dependent relations, schema diff |
| `RelationshipCountersTest` | Experimental relationship counter registration and reading |
| `ExpandPermissionTreeTest` | Expanding a permission with `expandPermissionTree` and walking the native `PermissionTree` (intermediate/leaf nodes, subjects) |
| `RawEscapeHatchTest` | The `rawChannel()` escape hatch: building a generated stub on this client's own channel to send `optionalTransactionMetadata` (a proto field this client does not wrap) and to call the single-check `CheckPermission` RPC |
| `CallDeadlinesTest` | The `Duration defaultTimeout` construction overload, a per-call `timeout` override, and confirming bulk import isn't bounded by the unary default |
| `ErrorMappingTest` | Recovering from `OUT_OF_RANGE` (stale ZedToken) and `UNAUTHENTICATED` without parsing a message |
| `InsecureOptInTest` | Why `createPlaintext` is loopback-only, and the named opt-in a remote plaintext host requires |
| `RetryPolicyTest` | Which calls are retried for you and which are not, counted server-side |
| `UnrepresentableValuesTest` | Caller data that cannot convert fails loudly, naming the key; unknown server enums degrade safely |
