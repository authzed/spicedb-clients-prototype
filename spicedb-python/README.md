# spicedb-python

The idiomatic Python client for [SpiceDB](https://authzed.com/spicedb): native,
immutable values instead of protobuf messages, matching blocking
(`spicedb.sync`) and asyncio (`spicedb.aio`) clients, typed errors, explicit
consistency, and an in-memory test double. Pair it with `spicedb-gen` for a
schema-checked typed client.

## Requirements

Python 3.11 or later, `grpcio>=1.73.0`, `protobuf>=7.36.2` and
`googleapis-common-protos>=1.56.1`, and SpiceDB 1.30 or later. The client
depends on `spicedb-python-proto`, the generated protocol bindings from this
repository.

## Install

Once published:

```bash
pip install spicedb-python          # or: uv add spicedb-python
```

Before publication, install both packages from this repository, the bindings
first (the client names `spicedb-python-proto` as a dependency without a
source):

```bash
REPO=git+https://github.com/authzed/spicedb-clients-prototype
pip install "spicedb-python-proto @ $REPO#subdirectory=proto-clients/spicedb-python-proto" \
            "spicedb-python @ $REPO#subdirectory=spicedb-python"
```

or from a checkout:

```bash
pip install ./proto-clients/spicedb-python-proto ./spicedb-python
# uv: uv add --editable ./proto-clients/spicedb-python-proto ./spicedb-python
```

Pin a commit (`$REPO@<sha>#subdirectory=...`) for reproducible builds. If the
legacy `authzed` package must stay installed during a migration, read
[MIGRATION.md](MIGRATION.md#installing-both-during-the-migration) first: the
two install the same protobuf modules.

## First call

```python
from spicedb import ObjectRef, PermissionCheck, Relationship, SubjectRef, Transaction, at_least
from spicedb.sync import SpiceDBClient

with SpiceDBClient("spicedb.example.com:443", token="my-preshared-key") as client:  # TLS by default
    client.write_schema("""
        definition user {}
        definition document {
            relation viewer: user
            permission view = viewer
        }
    """)
    written = client.write(
        Transaction().touch(Relationship.from_tuple("document:readme#viewer@user:alice"))
    )
    result = client.check(
        PermissionCheck(ObjectRef("document", "readme"), "view", SubjectRef(ObjectRef("user", "alice"))),
        consistency=at_least(written.revision),
    )
    print(result.has_permission)  # True; a CONDITIONAL result never counts as a grant
```

For a local `spicedb serve-testing` on `localhost:50051`, pass
`transport_security=TransportSecurity.PLAINTEXT`. The endpoint is
`host:port`, `http://host:port` or `https://host:port`. Plaintext endpoints
must name their port (`localhost:50051`, not `localhost`); TLS endpoints
without one use 443. grpc's `dns:///host:port` (client-side load balancing
over every resolved address) and `unix:` socket targets are also accepted. A
URL's scheme must agree with `transport_security`, and any other form fails in
the constructor with `LocalValidationError` (a `ValueError`) naming the problem.

The client connects lazily, on its first call. To fail at startup instead,
call `client.ready(timeout=5.0)` (`await client.ready(...)` for `aio`):
it raises `UnavailableError` naming the endpoint and the cause, or
`DeadlineExceededError` if the connection is still in progress.

From another Docker Compose service, SpiceDB is a remote host (for example
`spicedb:50051`), so plaintext there takes two explicit choices:

```python
import os
from spicedb import RemoteCredentials, TransportSecurity
from spicedb.sync import SpiceDBClient

client = SpiceDBClient(
    "spicedb:50051",  # or "http://spicedb:50051"
    token=os.environ["SPICEDB_TOKEN"],
    transport_security=TransportSecurity.PLAINTEXT,
    remote_credentials=RemoteCredentials.ALLOW,  # bearer token in cleartext
)
```

The asyncio client,
`spicedb.aio.SpiceDBClient`, takes the same arguments; await its calls and
iterate its streams with `async for`.

In tests, `spicedb.testing.InMemoryClient` (and `AsyncInMemoryClient`) stands
in for the client: it stores relationships, resolves checks and lookups from
them (or from results and a resolver you register), records every call and
injects faults.

The double does not evaluate your schema: a computed permission such as
`view = reader + writer + parent->read` is granted only by a stored
relationship named `view` or by a check resolver you install. Projects whose
permissions are all computed get little from stored-relationship grants;
model them with the resolver, or test the permission logic against SpiceDB
itself (`spicedb serve-testing` or a container).

## Typed client

Generate a schema-checked client from your `.zed` file with `spicedb-gen`
(build it from [`spicedb-gen/`](../spicedb-gen/)):

```bash
spicedb-gen --schema schema.zed --lang python --out myapp/authz
```

The [AI integration guide](AI_INTEGRATION_GUIDE.md) shows it end to end.

## Further reading

- [DESIGN.md](DESIGN.md): the Python client's design and guarantees, under
  the repository-wide [DESIGN.md](../DESIGN.md).
- [MIGRATION.md](MIGRATION.md): moving from authzed-py, with requirements and
  a table of renamed request and response members.
- [AI_INTEGRATION_GUIDE.md](AI_INTEGRATION_GUIDE.md): typed and native usage,
  checks, lookups, writes, errors and tests.
- [CHANGELOG.md](CHANGELOG.md) and [examples/](examples/).
