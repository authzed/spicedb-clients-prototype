# spicedb-csharp Examples

Each subdirectory is a standalone xUnit test project that also serves as an
integration test.

## Running

`mage integrationTest` is the one command that does everything: it starts the
SpiceDB container from `docker-compose.test.yml`, waits for it, runs the unit
suite and then every example project against that server, and tears the
container down afterwards. (`mage test` builds both solutions, runs the unit
suite, and checks the example wiring -- it starts no container and runs no
example.)

```bash
mage integrationTest
```

To run the examples by hand you need a SpiceDB of your own, and you must name
the examples solution: this directory contains no project or solution file, so
a bare `dotnet test` here does nothing useful. Every example reads
`SPICEDB_ENDPOINT` and `SPICEDB_TOKEN` (via `SpiceDBTestServer.cs`, linked into
each project by `Directory.Build.props`), defaulting to `localhost:50051` and
`somerandomkeyhere` -- the endpoint and preshared key in
`docker-compose.test.yml`:

```bash
# from spicedb-csharp/, not from examples/
docker compose -f docker-compose.test.yml up -d
dotnet test SpiceDB.Client.Examples.sln -maxcpucount:1

# one example
dotnet test examples/CheckPermission/CheckPermission.csproj

# or against any other SpiceDB
SPICEDB_ENDPOINT=spicedb.internal:50051 SPICEDB_TOKEN=hunter2 \
    dotnet test SpiceDB.Client.Examples.sln -maxcpucount:1
```

`-maxcpucount:1` is not optional. All thirteen projects share one SpiceDB and
each writes a whole schema, so running them concurrently lets one project's
`WriteSchemaAsync` land between another's schema write and its relationship
write -- which fails, nondeterministically, on a different example each run.

If port 50051 is taken, `SPICEDB_TEST_PORT` chooses the port the compose file
publishes on, and `mage integrationTest` derives it from `SPICEDB_ENDPOINT`:

```bash
SPICEDB_ENDPOINT=localhost:50071 mage integrationTest
```

## What runs in CI

Every example in the table below is executed by `mage integrationTest`, which
is what the `integration` job in `.github/workflows/csharp.yaml` runs -- there
are no skips here. Neither membership nor execution is taken on trust: `mage checkExamples` (also
run by `mage test`) diffs `examples/*/*.csproj` against the project list in
`SpiceDB.Client.Examples.sln` in both directions and fails on divergence, and
checks that every listed project has build configurations; and the run itself is
TRX-logged into a directory cleared beforehand, so `mage integrationTest` can
assert that every example assembly executed at least one test. `dotnet test`
over a solution *prints* "No test is available in ..." for an assembly with no
tests and still exits 0, so an example whose tests are all commented out or
`[Fact(Skip = "...")]` passes every membership check while running nothing. All twelve examples
once sat outside every solution file, so nothing built or ran them for the
repo's entire history; adding the solution fixed the instance, and this check
is what stops example #14 from reintroducing it. `mage lint` covers both
solutions for the same reason -- it used to name only `SpiceDB.Client.sln`, so
no example was ever linted. See root `DESIGN.md`, "RULE: An example must be
executed by CI and must be able to fail".

## Examples

| Directory | Description |
|-----------|-------------|
| `CheckPermission/` | Basic permission check |
| `WriteRelationships/` | Writing relationships with transactions |
| `ReadRelationships/` | Reading relationships with async enumerables |
| `LookupResources/` | Resource lookup |
| `LookupSubjects/` | Subject lookup |
| `CallDeadlines/` | The `defaultTimeout` construction parameter, a per-call `timeout` override, and confirming bulk import isn't bounded by the unary default |
| `WatchChanges/` | Watching for changes |
| `SchemaManagement/` | Schema read/write |
| `BulkOperations/` | Bulk checks, batch writes, and bulk relationship import/export |
| `SchemaReflection/` | Schema reflection, computable permissions, diffs |
| `RelationshipCounters/` | Relationship counter registration and counting |
| `ExpandPermissionTree/` | Expanding a permission into its native `PermissionTree` of subjects |
| `RawEscapeHatch/` | The `RawProto()` escape hatch: driving the generated service client on this client's own connection to send `OptionalTransactionMetadata` (a proto field this client does not wrap) and to call the single-check `CheckPermission` RPC |
