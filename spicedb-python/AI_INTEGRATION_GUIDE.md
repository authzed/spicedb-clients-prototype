# spicedb-python: integration guide for AI coding agents

Concrete Python for the rules in the repository-wide
[AI_INTEGRATION_GUIDE.md](../AI_INTEGRATION_GUIDE.md). Read that first for
*why*; this file is *how*. Modules:

| Module | Import | Role |
|---|---|---|
| typed (generated) | your package, e.g. `myapp.authz.permissions` + `myapp.authz.sync` / `myapp.authz.aio` | schema-checked API; `TypedClient` forwards every native method |
| `spicedb` | `import spicedb` / `from spicedb import ...` | values, filters, `Transaction`, consistency, `ZedToken`, options, results |
| `spicedb.sync` / `spicedb.aio` | `from spicedb.sync import SpiceDBClient` | native client, blocking or asyncio (same names and arguments) |
| `spicedb.errors` | `from spicedb.errors import FailedPreconditionError` | typed errors (`SpiceDBError` is a `grpc.RpcError`) |
| `spicedb.testing` | `from spicedb.testing import InMemoryClient` | in-memory test doubles (`InMemoryClient`, `AsyncInMemoryClient`; `Call`, `Fault`, `FaultStage`, `SchemaRevision`, `CheckResolver`, `grant_all`, `cursor_after`) |
| `spicedb.protocols` | `from spicedb.protocols import ClientProtocol` | full client surface (`ClientProtocol`, `AsyncClientProtocol`) satisfied by the clients and the doubles |
| `spicedb.protoconv` | `from spicedb import protoconv` | protocol conversions (tools only) |

Do **not** install the legacy `authzed` package (authzed-py) beside this
client unless it was rewritten by `authzed-py-bridge` (see
[MIGRATION.md](MIGRATION.md)): both ship `authzed/api/v1/*_pb2.py`. Do not
import `authzed.api.v1` modules to build requests.

## Generate the typed client (application code)

```bash
spicedb-gen --schema myapp/authz/schema.zed --lang python --out myapp/authz
# optional: --python.module=authzperms renames permissions.py (sync.py/aio.py follow)
```

This writes `permissions.py` (types), `sync.py` and `aio.py` (one `TypedClient`
per flavor) into a package directory; they import each other relatively.
Commit the output; regenerate whenever the schema changes; never edit it. The
output is `ruff format`-clean. The examples below use this schema:

```zed
use expiration

definition user {}

definition organization {
    relation admin: user
    relation member: user
    relation creator: user:*
    permission can_admin = admin
    permission view = admin + member
}

definition invitation {
    relation grant: organization#can_admin
    relation inviter: user with expiration
    permission accept = grant
}
```

Naming: `Organization(id)` builds a ref. Permission `p` → property `p`.
Relation `r` → writer `r(subject)`, key `r_key(subject)` (only where `r` admits
caveated or expiring subjects; elsewhere the writer identifies the tuple), check property
`r_relation`, filter `r_filter()`. A userset on a permission → property
`p_subject` (e.g. `can_admin_subject`). `user:*` → `UserWildcard()`. Static
refs for lookups are `ORGANIZATION_VIEW`; names are
`OrganizationPermissions.VIEW` and `ORGANIZATION_PERMISSION_NAMES`. Collisions
fail generation; nothing is renamed silently. Full rules:
[spicedb-gen/DESIGN.md](../spicedb-gen/DESIGN.md), "Python naming rules".

## Connect once

```python
from myapp.authz.sync import TypedClient

tc = TypedClient.connect("spicedb.example.com:443", os.environ["SPICEDB_TOKEN"])  # TLS
```

Create one client per process and `close()` it (or use `with`). Plaintext is
`transport_security=TransportSecurity.PLAINTEXT`, for loopback and unix
sockets; a non-loopback plaintext host additionally needs
`remote_credentials=RemoteCredentials.ALLOW`. Custom trust and mTLS:
`ca_cert=`, `client_cert=`, `client_key=` (PEM bytes). For an existing
`grpc.Channel` (in-memory, test server) use
`TypedClient.from_channel(channel)` (or `SpiceDBClient.from_channel`): the
channel brings its own credentials and `close()` leaves it open. asyncio code
uses `myapp.authz.aio.TypedClient` the same way with `await`:

```python
async with AsyncTypedClient.connect("spicedb.example.com:443", os.environ["SPICEDB_TOKEN"]) as tc:
    result = await tc.check(min_latency(), Organization("acme").view, User(user_id))
    async with tc.lookup_resources(
        min_latency(),
        authz.ORGANIZATION_VIEW,
        User(user_id),
        permissionship_filter=LookupPermissionshipFilter.HAS_PERMISSION_ONLY,
    ) as stream:
        async for res in stream:
            ids.append(res.resource_id)
```

## Check (fail closed)

```python
try:
    result = tc.check(min_latency(), Organization(org_id).view, User(user_id))
except SpiceDBError:
    raise Forbidden()
if not result.has_permission:  # CONDITIONAL and errors both deny
    raise Forbidden(str(Organization(org_id).view))
```

- Need to observe a write? Store `str(written.revision)` and check with
  `at_least(ZedToken(stored))`.
- Reporting what was denied: `str(perm)` is `organization:acme#view`; `perm.resource_id`, `perm.name`.
- Several at once (native `check_bulk`, results in input order):

```python
results = tc.check_bulk(
    authz.checks_for(User(user_id), Organization("a").view, Organization("b").view),
    consistency=min_latency(),
)
# results[i] matches input i; only results[i].has_permission grants.
```

- Permission names chosen at runtime (e.g. per API method):

```python
perm = Organization(org_id).permission_by_name(name)  # raises NotInSchemaError
allowed = tc.check_dynamic(min_latency(), perm, User(user_id)).has_permission
```

  At startup, verify every configured name is in
  `authz.ORGANIZATION_PERMISSION_NAMES`.

- Type and permission names both chosen at runtime (a policy layer, an admin
  tool), still validated against the schema before any call:

```python
resource = authz.definition_by_name(type_name)(resource_id)  # raises NotInSchemaError
allowed = tc.check_dynamic(min_latency(), resource.permission_by_name(perm_name), User(user_id))
ref = authz.permission_ref_by_name(type_name, perm_name)      # resource-less, for lookups
with tc.lookup_resources_by_name(min_latency(), type_name, perm_name, User(user_id)) as stream:
    ids = [r.resource_id for r in stream]
```

  `authz.DefinitionNames.ORGANIZATION` and `authz.DEFINITION_NAMES` name the
  definitions; `authz.SCHEMA_TEXT` is the schema the module was generated
  from, so `tc.write_schema(authz.SCHEMA_TEXT)` at startup needs no `.zed`
  file at runtime.

## List what a subject can access

```python
with tc.lookup_resources(
    min_latency(),
    authz.ORGANIZATION_VIEW,
    User(user_id),
    permissionship_filter=LookupPermissionshipFilter.HAS_PERMISSION_ONLY,
) as stream:
    for res in stream:
        ids.append(res.resource_id)
```

Omit the filter only if you handle `Permissionship.CONDITIONAL` results
explicitly. Always close streams (`with`), so the RPC is released.

## Write atomically

```python
org = Organization(org_id)
txn = (
    TypedTransaction()
    .must_match(authz.with_subject(org.admin_filter(), User(other_admin)))  # invariant: another admin remains
    .delete(org.admin(User(user_id)))
    .touch(org.member(User(user_id)))
)
try:
    written = tc.write_txn(txn)  # construction errors raise here, before any RPC
except FailedPreconditionError:
    raise LastAdmin()
```

- `touch` upserts; `create` raises `AlreadyExistsError` if present.
- Delete without inventing an expiration: `TypedTransaction().delete(Invitation(inv_id).inviter_key(User(u)))`.
- Delete what you just read: `txn.delete(TypedRelationship.from_relationship(item.relationship))`.
- **Do not retry a failed write blindly**; it may have committed. The client
  never replays mutations unless one call opts in, which only a touch-only
  write without preconditions and `write_schema` may do:
  `c.write(txn, call_options=CallOptions(retry_policy=RetryPolicy()))`.
- `with_subject(f, User(u))` pins the subject relation (direct only, never
  `team:eng#member`); `with_subject_any_relation` matches any.
- `txn.updates` / `txn.preconditions` show what a `TypedTransaction` will send.

## Read relationships

```python
with tc.read_relationships(min_latency(), Organization(org_id).admin_filter()) as stream:
    for item in stream:
        admins.append(item.relationship.subject_id)
        cursor = item.after_result_cursor  # item.read_at is the response revision
```

To resume, call again with `cursor=cursor` and the **same filter,
consistency and `page_size`**. `lookup_resources` items carry
`after_result_cursor` too; `export_relationships` items carry a cursor and no
revision. `paging=Paging.SINGLE_REQUEST` (reads and resource lookups) sends
exactly one request, as the raw API does.

## Schema-agnostic code (native tier)

```python
r = Relationship.from_tuple("document:readme#viewer@user:alice")  # raises InvalidArgumentError
written = c.write(Transaction().touch(r))
result = c.check(
    PermissionCheck(
        resource=ObjectRef("document", "readme"),
        permission="view",
        subject=SubjectRef(ObjectRef("user", "alice")),
    ),
    consistency=at_least(written.revision),
)
```

`c` is a `spicedb.sync.SpiceDBClient(endpoint, token=...)`. Tuple syntax
covers `[caveat:{...}]` and `[expiration:...]`; `str(relationship)` formats
it. Tools with their own retry or deadline policy:

```python
c = SpiceDBClient(
    endpoint,
    token=token,
    retry_policy=Retries.DISABLED,  # or RetryPolicy(max_retries=5, on_retry=print)
    default_timeout=DefaultDeadline.DISABLED,
    stream_retries=StreamRetries.DISABLED,
    # Most specific wins: per call, then per operation, then client-wide.
    operation_retry_policies={Operation.WATCH: Retries.DISABLED},
)
md = ResponseMetadata()
c.read_schema(call_options=CallOptions(response_metadata=md))
print(md.trailers)
```

Every operation accepts `call_options=CallOptions(metadata=..., wait_for_ready=...,
compression=..., response_metadata=..., retry_policy=...)`; `retry_policy`
overrides the client's for that call, including stream establishment
(`c.watch(call_options=CallOptions(retry_policy=Retries.DISABLED))`).
`operation_retry_policies` does the same for every call of one operation; an
`Operation.WRITE` policy retries only writes where `txn.is_idempotent()`
(touch-only, no preconditions), and deletes and imports refuse one with
retries when the client is built.
Protocol-shaped output: `spicedb.protoconv` (`relationship_to_proto`,
`check_result_to_proto`, `check_bulk_to_proto`, ...) with
`google.protobuf.json_format`.

## Errors

```python
if isinstance(err, FailedPreconditionError):
    pass  # precondition failed
elif isinstance(err, OutOfRangeError):
    pass  # stale ZedToken: drop it, re-read fully consistent
elif isinstance(err, UnavailableError):
    pass  # transient; reads were already retried
if code_of(err) == grpc.StatusCode.FAILED_PRECONDITION:  # also raw grpc.RpcError
    pass
if isinstance(err, SpiceDBError):
    print(err.server_message)  # what the server said, verbatim
```

Native errors are `grpc.RpcError`s: existing `except grpc.RpcError` handlers
and `err.code()` / `err.details()` keep working, including for local
validation, local deadlines and errors you construct. Forward
`err.server_message`, not `str(err)` of a wrapper, when relaying an error.

## Test

```python
fake = InMemoryClient([Relationship.from_tuple("organization:acme#admin@user:alice")])
tc = TypedClient(fake)  # a generated TypedClient wraps the double
# A stored relationship grants the check of its own relation name...
assert tc.check(full(), Organization("acme").admin_relation, User("alice")).has_permission
# ...but the double evaluates no schema: permissions need a result or a resolver.
fake.set_check_result(
    ObjectRef("organization", "acme"),
    "view",
    SubjectRef(ObjectRef("user", "alice")),
    Permissionship.HAS_PERMISSION,
)
fake.set_check_resolver(lambda c: True if c.subject.object.object_id == "root" else None)
fake.set_check_resolver(grant_all)  # or grant everything
err = SpiceDBError.from_code(grpc.StatusCode.FAILED_PRECONDITION, "precondition failed")
assert isinstance(err, FailedPreconditionError)
```

A check resolves to the result registered for that exact question, else to
the resolver's answer (None defers), else to the stored relationships (a
relationship whose relation is the permission's name and whose subject
matches exactly, or a wildcard for direct subjects; caveated ones are
conditional, expired ones absent), else to no permission. `lookup_resources`
and `lookup_subjects` enumerate the objects the double knows (from its
relationships) and return those that check resolution grants, honoring
`permissionship_filter`, `limit`, `cursor` and `wildcards`.

`InMemoryClient` (and `AsyncInMemoryClient`) implement `write`, `check`,
`check_bulk`, `lookup_resources`, `lookup_subjects`, `read_relationships`, `delete_relationships`,
`delete_all_relationships_non_atomic`, `import_relationships`,
`export_relationships`, `read_schema`, `write_schema` and `reflect_schema`
(`set_reflect_schema_result`) with the real clients' signatures, SpiceDB's
precondition and create-conflict semantics, and errors shaped like the real
client's. `set_response_metadata` fills `CallOptions(response_metadata=...)`.
Calls are recorded and faults injected, so failure paths need no fake server:

```python
fake.fail_next(Operation.CHECK, SpiceDBError.from_code(grpc.StatusCode.UNAVAILABLE, "down"))
fake.set_fault_hook(  # fail import batch 2 (0-based); nothing is applied
    lambda f: SpiceDBError.from_code(grpc.StatusCode.ALREADY_EXISTS, "duplicate")
    if f.stage is FaultStage.IMPORT_BATCH and f.item == 2
    else None
)
last_read = fake.calls_of(Operation.READ_RELATIONSHIPS)[-1]
print(last_read.request, last_read.options["cursor"])
```

`FaultStage` is `CALL`, `IMPORT_BATCH` (`f.batch` holds that batch's
relationships), `IMPORT_COMMIT` or `ITEM` (before each read, export or lookup item).
`cursor_after(n)` builds the double's resume cursor after `n` items, and
`fake.set_read_schema_revision(SchemaRevision.OMITTED)` makes `read_schema`
report no revision. The doubles implement every client method; the ones
without in-memory semantics (expand, watch, ...) are recorded and
raise `UNIMPLEMENTED`, so script them with the fault hook.

Type your code against `spicedb.protocols.ClientProtocol` (or
`AsyncClientProtocol`), which both the real client and the double satisfy, or
against just the methods you use:

```python
def grant(c: ClientProtocol, org_id: str, user_id: str) -> None:
    txn = TypedTransaction().touch(Organization(org_id).member(User(user_id))).build()
    c.write(txn)
```

For integration tests, run `authzed/spicedb serve-testing` and assert both
granted and denied cases.
