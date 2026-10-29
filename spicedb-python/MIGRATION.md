# Migrating from authzed-py to spicedb-python

This guide covers moving from [authzed-py](https://github.com/authzed/authzed-py)
(the `authzed` package on PyPI: `authzed.api.v1.Client`, `SyncClient`,
`AsyncClient`, `InsecureClient`, `grpcutil`) to `spicedb-python`. Each section
lists a behavior change you will observe, followed by a before/after. If your
application owns its schema, also consider the generated typed client
(`spicedb-gen --lang python`); see [AI_INTEGRATION_GUIDE.md](AI_INTEGRATION_GUIDE.md).

## Installing both during the migration

authzed-py and `spicedb-python-proto` (which spicedb-python depends on) both
install `authzed/api/v1/*_pb2.py`, `buf/validate`, `validate` and
`protoc_gen_openapiv2` into `site-packages`. Installed side by side they
overwrite each other's files: whichever installs last wins, a process can mix
modules generated from different API revisions (the descriptor pool rejects
the second, different `authzed/api/v1/core.proto`, or a message silently lacks
a field), and uninstalling either one deletes modules the other needs.

To run both, install an authzed-py build rewritten by the bridge that
`spicedb-python-proto` ships:

```bash
authzed-py-bridge --wheel authzed-1.25.0-py3-none-any.whl --out dist/   # or --sdist / --root <checkout>
pip install spicedb-python dist/authzed-1.25.0-py3-none-any.whl
```

The bridged authzed-py keeps its client classes and `grpcutil`, drops its own
generated modules, and requires `spicedb-python-proto`, so `authzed.api.v1.CheckPermissionRequest`
and spicedb-python's requests are the same class from one descriptor pool.
Values pass between the two without conversion.

## Dependency floors

spicedb-python needs `grpcio>=1.73.0` (authzed-py: `~=1.63`) and
`protobuf>=7.36.2` (authzed-py: `>=5.26`). grpcio 1.73 is the first release
whose transport rejects an authority-bearing `unix:` target, which the
plaintext-credential guard relies on; protobuf 7.36.2 is the bindings'
generated-code version. Both are inside authzed-py's ranges. Test tooling is
only in the `dev` extra.

## SpiceDB version

SpiceDB 1.30 and later is supported natively. Data older servers sent only in
trailers (for example check debug traces) is reachable through
`call_options=CallOptions(response_metadata=ResponseMetadata())`.

## Constructors and transport security

Pick the flavor explicitly; there is no auto-detection from a running event
loop. The token is an argument, not a credentials object. TLS is the default.

```python
# authzed-py
from authzed.api.v1 import Client, SyncClient, AsyncClient, InsecureClient
from grpcutil import bearer_token_credentials
client = Client("spicedb.example.com:443", bearer_token_credentials("key"))
client = InsecureClient("spicedb:50051", "key")

# spicedb-python
from spicedb import TransportSecurity, RemoteCredentials
from spicedb.sync import SpiceDBClient          # or: from spicedb.aio import SpiceDBClient
client = SpiceDBClient("spicedb.example.com:443", token="key")
client = SpiceDBClient("spicedb:50051", token="key",
                       transport_security=TransportSecurity.PLAINTEXT,
                       remote_credentials=RemoteCredentials.ALLOW)
```

- `InsecureClient` sent the token in plaintext to any host. Now plaintext to a
  non-loopback host (anything but `localhost`, `127.0.0.0/8`, `::1` or a unix
  socket) is refused unless you pass `remote_credentials=RemoteCredentials.ALLOW`.
- `bearer_token_credentials(token, certChain)` → `ca_cert=certChain`; mTLS uses
  `client_cert=` and `client_key=`.
- Close the client (`close()`, `with`, `async with`); it owns its channel.
- Code that built its own channel and called `init_stubs(channel)`:

```python
client = SpiceDBClient.from_channel(channel)   # channel keeps its own credentials; close() leaves it open
```

## Requests and results are native values

You no longer build protobuf messages. Consistency is a required argument.

```python
# authzed-py
resp = client.CheckPermission(CheckPermissionRequest(
    resource=ObjectReference(object_type="document", object_id="readme"),
    permission="view",
    subject=SubjectReference(object=ObjectReference(object_type="user", object_id="alice")),
    consistency=Consistency(fully_consistent=True),
))
allowed = resp.permissionship == CheckPermissionResponse.PERMISSIONSHIP_HAS_PERMISSION

# spicedb-python
from spicedb import ObjectRef, PermissionCheck, SubjectRef, full
result = client.check(
    PermissionCheck(ObjectRef("document", "readme"), "view", SubjectRef(ObjectRef("user", "alice"))),
    consistency=full(),
)
allowed = result.has_permission   # only an unconditional grant; CONDITIONAL never grants
```

- Tokens are `ZedToken` values: `at_least(ZedToken(saved))`; persist with
  `str(token)`. `resp.checked_at.token` → `result.checked_at`.
- Relationship fields are attributes with short paths:
  `rel.subject.object.object_id` → `rel.subject_id`, also `resource_id`,
  `resource_type`, `subject_type`, `subject_relation`.
- `Relationship.from_tuple("document:readme#viewer@user:alice[expiration:2030-01-01T00:00:00Z]")`
  parses SpiceDB's full tuple syntax; `str(relationship)` formats it.

## Writes

```python
# authzed-py
resp = client.WriteRelationships(WriteRelationshipsRequest(
    updates=[RelationshipUpdate(operation=RelationshipUpdate.OPERATION_TOUCH, relationship=rel)],
    optional_preconditions=[Precondition(operation=Precondition.OPERATION_MUST_MATCH, filter=f)],
))
token = resp.written_at

# spicedb-python
from spicedb import Transaction
result = client.write(Transaction().must_match(filter).touch(relationship))
token = result.revision
```

Writes, deletes and imports are never retried: a failed write may have
committed, so do not replay it blindly.

## Bulk checks

`CheckBulkPermissions` → `check_bulk(checks, consistency=...)`. Requests are
split into chunks of 1,000; results come back in input order, each with its
chunk's `checked_at`. The first per-item error raises (with its
`input_index`); to get every item's result with errors attached, as the raw
response did:

```python
results = client.check_bulk(checks, consistency=full(), item_errors=BulkItemErrors.RETURN)
for r in results:
    if r.error is not None: ...   # an errored item never grants
```

## Deletes

The default changed. authzed-py's `DeleteRelationships` without
`optional_allow_partial_deletions` was all-or-nothing with the server's default
limit. spicedb-python's default is a bounded partial delete that needs a limit:

```python
# authzed-py
client.DeleteRelationships(DeleteRelationshipsRequest(relationship_filter=f))

# spicedb-python: same all-or-nothing behavior
client.delete_relationships(filter, partial=DeletePartial.DISALLOWED)
# bounded partial delete (resume from result.cursor)
client.delete_relationships(filter, limit=1000)
# delete everything matching, batch by batch (not atomic)
client.delete_all_relationships_non_atomic(filter, on_progress=print)
```

## Streams, paging and cursors

Reads, lookups and exports page automatically and return closeable streams.
Close them (`with`), so the RPC is released even if you stop early.

```python
# authzed-py
for resp in client.ReadRelationships(ReadRelationshipsRequest(relationship_filter=f, consistency=c)):
    print(resp.relationship, resp.after_result_cursor, resp.read_at)

# spicedb-python
with client.read_relationships(filter, consistency=full()) as stream:
    for item in stream:
        print(item.relationship, item.after_result_cursor, item.read_at)
```

To resume, pass `cursor=` with the same filter, consistency and `page_size`.
A raw `ReadRelationships`/`LookupResources` call without a limit is one
unbounded request; this client pages by default. To reproduce the raw call
(and its final cursor), pass `paging=Paging.SINGLE_REQUEST`: exactly one
request, limited only by `limit=` where the method has one.
`lookup_resources` takes `permissionship_filter=LookupPermissionshipFilter.HAS_PERMISSION_ONLY`
to drop conditional results. `ImportBulkRelationships` (a request iterator) →
`import_relationships(iterable, batch_size=1000)`: one call is one
transaction, and a server abort raises the server's status (for example
`AlreadyExistsError`), not a stream error.

## Deadlines and retries

authzed-py had no default deadline and no retries.

- Every unary call (including bulk chunks and auto-fetched pages) has a
  30-second budget; per call `timeout=`, client-wide `default_timeout=`, or
  `default_timeout=DefaultDeadline.DISABLED` for none. Streams have no default
  lifetime.
- Reads retry `UNAVAILABLE`/`ABORTED` up to three times with backoff, and
  stream establishment retries before the first item. If you have your own
  policy: `retry_policy=Retries.DISABLED` (or a `RetryPolicy(...)`) and
  `stream_retries=StreamRetries.DISABLED`.
- Override the policy for one call with
  `call_options=CallOptions(retry_policy=...)`: for example
  `Retries.DISABLED` on one `watch` so a supervised watch is never reopened,
  or a `RetryPolicy` on a touch-only `write` (no preconditions) or a
  `write_schema` to retry those idempotent writes. Every other mutation
  raises `InvalidArgumentError` for the opt-in before sending anything.
- Set the policy for every call of one operation when building the client:
  `operation_retry_policies={Operation.WATCH: RetryPolicy(...)}` (reopen
  watches before their first event), `{Operation.READ_SCHEMA:
  Retries.DISABLED}`, or `{Operation.WRITE: RetryPolicy(...)}`, which retries
  only writes for which `Transaction.is_idempotent()` holds. Deletes,
  imports and counter registration reject a policy with retries when the
  client is built.
- Writes are never replayed otherwise. An empty `Transaction` is sent like
  authzed-py's empty `WriteRelationshipsRequest`: SpiceDB evaluates its
  preconditions and returns the current revision.

## Consistency on schema reads

Every schema read that takes a consistency requires one explicitly
(`reflect_schema`, `computable_permissions`, `dependent_relations`,
`diff_schema`). Where your raw requests left consistency unset, pass
`consistency=min_latency()`, which is what SpiceDB applies when it is absent.
`read_schema` has no consistency parameter because `ReadSchemaRequest` has none.

## Errors

Errors are typed `SpiceDBError` subclasses, which are still `grpc.RpcError`s.

```python
# authzed-py
try:
    client.WriteRelationships(req)
except grpc.RpcError as e:
    if e.code() == grpc.StatusCode.FAILED_PRECONDITION: ...

# spicedb-python: both of these work
try:
    client.write(txn)
except FailedPreconditionError as e:
    forward(e.server_message)          # the server's text, verbatim
except grpc.RpcError as e:
    if code_of(e) == grpc.StatusCode.UNAVAILABLE: ...
```

Local validation raises `InvalidArgumentError` before any RPC. `code_of`
also classifies raw `grpc.RpcError`, `asyncio.CancelledError` and
`TimeoutError`. Tests construct errors with
`SpiceDBError.from_code(grpc.StatusCode.FAILED_PRECONDITION, "...")`.

## Metadata and call options

authzed-py methods took gRPC keyword arguments (`metadata=`, `timeout=`,
`with_call`). spicedb-python operations take `timeout=` and
`call_options=CallOptions(metadata=..., wait_for_ready=..., compression=...,
response_metadata=ResponseMetadata())`; the last captures response headers and
trailers. `client.raw_grpc()` remains for protocol calls this client does not
cover; it reuses the authenticated channel. Protocol-shaped data for printing
or persistence: `spicedb.protoconv`.

## Tests

Replace mocks of authzed-py stubs with `spicedb.testing.InMemoryClient` (or
`AsyncInMemoryClient`): writes with preconditions and create conflicts,
filtered reads and deletes, and registered check results, with the real
client's method signatures.
