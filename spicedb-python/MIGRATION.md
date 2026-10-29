# Migrating from authzed-py to spicedb-python

This guide covers moving from [authzed-py](https://github.com/authzed/authzed-py)
(the `authzed` package on PyPI: `authzed.api.v1.Client`, `SyncClient`,
`AsyncClient`, `InsecureClient`, `grpcutil`) to `spicedb-python`. Each section
lists a behavior change you will observe, followed by a before/after. If your
application owns its schema, also consider the generated typed client
(`spicedb-gen --lang python`); see [AI_INTEGRATION_GUIDE.md](AI_INTEGRATION_GUIDE.md).

## Requirements

| Requirement | spicedb-python | authzed-py |
| --- | --- | --- |
| Python | 3.11 or later | |
| `grpcio` | `>=1.73.0` | `~=1.63` |
| `protobuf` | `>=5.26.1` (5.x, 6.x and 7.x tested) | `>=5.26` |
| `googleapis-common-protos` | `>=1.56.1` | |
| SpiceDB | 1.30 or later | |

- grpcio 1.73 is the first release whose transport rejects an
  authority-bearing `unix:` target, which the plaintext-credential guard
  relies on. The bindings are generated as protobuf 5.26.1 code, so an
  application on protobuf 5.x, 6.x or 7.x adopts the client without changing
  its protobuf version; the suites pass at 5.26.1, 5.29.5, 6.33.2 and 7.36.2.
  On Python 3.14, protobuf 5.26.1 fails to import (that runtime predates
  Python 3.14); 5.29.5 and later work there.
  Both floors are inside authzed-py's ranges, so one environment can hold both
  clients (see below). Test tooling is only in the `dev` extra.
- SpiceDB 1.30 and later is supported natively. Data older servers sent only
  in trailers (for example check debug traces) is reachable through
  `call_options=CallOptions(response_metadata=ResponseMetadata())`.
- Installation, including from a checkout before publication:
  [README.md](README.md).

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
- The endpoint is `host:port`, `http://host:port` or `https://host[:port]`,
  plus `dns:///host:port` and `unix:` socket targets. A plaintext endpoint
  must name its port (`localhost:50051`; a bare `localhost` used to dial
  443); a TLS endpoint without one uses 443. The scheme must agree with
  `transport_security`. Other gRPC target URIs (`dns://8.8.8.8/host:port`,
  `dns:host:port`, `ipv4:...`) and URLs with a path, query or userinfo are
  rejected at construction with `InvalidArgumentError`; authzed-py passed
  them to grpc unchecked.
- authzed-py had no readiness check either; a client that should fail at
  startup calls `client.ready(timeout=...)` (`await` it on `aio`).
- `bearer_token_credentials(token, certChain)` → `ca_cert=certChain`; mTLS uses
  `client_cert=` and `client_key=`.
- Close the client (`close()`, `with`, `async with`); it owns its channel.
- Code that built its own channel and called `init_stubs(channel)`:

```python
client = SpiceDBClient.from_channel(channel)   # channel keeps its own credentials; close() leaves it open
```

- One `spicedb.aio.SpiceDBClient` per event loop. An asyncio client binds to
  the loop it is first used on, like the `grpc.aio` channel under it; using it
  from another loop raises `EventLoopBindingError`. An application whose web
  server and background worker each run their own loop builds a client in
  each (for example in each loop's startup or lifespan hook) instead of
  sharing one built at import time. Genuinely synchronous code uses
  `spicedb.sync.SpiceDBClient`, which any thread may share.

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

## Renamed request and response members

Requests are keyword arguments and native values, and responses are frozen
dataclasses. Where a protobuf member has a different name or shape:

| authzed-py (protobuf) | spicedb-python |
| --- | --- |
| `ObjectReference(object_type=, object_id=)` | `ObjectRef(object_type, object_id)` |
| `SubjectReference(object=, optional_relation=)` | `SubjectRef(object, relation)` (`None` or `"..."` is direct) |
| `Relationship.subject.object.object_id` | `Relationship.subject_id` (also `resource_id`, `resource_type`, `subject_type`, `subject_relation`) |
| `Relationship.optional_caveat`, `optional_expires_at` | `Relationship.caveat`, `expiration` (`datetime`) |
| `ZedToken.token` | `str(token)` on a `ZedToken` |
| `Consistency(fully_consistent=True)` / `(minimize_latency=True)` / `(at_least_as_fresh=t)` / `(at_exact_snapshot=t)` | `full()` / `min_latency()` / `at_least(t)` / `snapshot(t)` |
| `CheckPermissionRequest.with_tracing` | `tracing=Tracing.ENABLED` |
| `CheckPermissionResponse.permissionship == PERMISSIONSHIP_HAS_PERMISSION` | `CheckResult.has_permission` (or `permissionship is Permissionship.HAS_PERMISSION`) |
| `CheckPermissionResponse.partial_caveat_info.missing_required_context` | `CheckResult.missing_context` |
| `CheckPermissionResponse.checked_at` | `CheckResult.checked_at` |
| `CheckBulkPermissionsResponse.pairs[i].item` / `.error` | `check_bulk(...)[i]` / `.error` with `item_errors=BulkItemErrors.RETURN` |
| `WriteRelationshipsRequest.updates`, `optional_preconditions` | `Transaction().touch/create/delete(...)`, `.must_match/.must_not_match(filter)` |
| `WriteRelationshipsResponse.written_at` | `WriteResult.revision` |
| `RelationshipFilter(resource_type=, optional_resource_id=, optional_resource_id_prefix=, optional_relation=, optional_subject_filter=)` | `Filter(resource_type, resource_id=, resource_id_prefix=, relation=, subject=)` |
| `SubjectFilter(subject_type=, optional_subject_id=, optional_relation=)` | `SubjectFilter(subject_type, subject_id=, relation=SubjectRelationFilter.any()/.direct()/.named(r))` |
| `DeleteRelationshipsRequest.optional_limit`, `optional_allow_partial_deletions` | `delete_relationships(limit=, partial=DeletePartial.ALLOWED/DISALLOWED)` |
| `DeleteRelationshipsResponse.deleted_at`, `relationships_deleted_count`, `deletion_progress` | `DeleteResult.revision`, `deleted_count`, `progress` |
| `ReadRelationshipsRequest.optional_limit`, `optional_cursor` | `read_relationships(page_size=, cursor=)` (cursor is a `str`) |
| `ReadRelationshipsResponse.after_result_cursor.token` | `ReadRelationship.after_result_cursor` |
| `LookupResourcesRequest.resource_object_type`, `optional_limit`, `optional_cursor` | `lookup_resources(resource_type=, page_size=/limit=, cursor=)` |
| `LookupResourcesResponse.resource_object_id`, `partial_caveat_info` | `LookupResource.resource_id`, `partial_caveat` |
| `LookupSubjectsRequest.subject_object_type`, `optional_subject_relation`, `optional_concrete_limit`, `wildcard_option` | `lookup_subjects(subject_type=, subject_relation=, page_size=/limit=, wildcards=)` |
| `LookupSubjectsResponse.subject.subject_object_id` (or deprecated `subject_object_id`) | `LookupSubject.subject.subject_id` |
| `ExpandPermissionTreeResponse.tree_root`, `expanded_at` | `ExpandResult.tree`, `revision` |
| `ReadSchemaResponse.schema_text`, `read_at` | `SchemaResult.schema`, `revision` |
| `WriteSchemaResponse.written_at` | the returned `ZedToken` |
| `WatchRequest.optional_object_types`, `optional_relationship_filters`, `optional_start_cursor` | `watch(object_types, filters=, start_revision=)` |
| `WatchResponse.changes_through`, `optional_transaction_metadata` | `WatchEvent.changes_through`, `transaction_metadata` |
| `ExportBulkRelationshipsResponse.relationships`, `after_result_cursor` | one `ExportedRelationship(relationship, after_result_cursor)` per relationship |

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
  `default_timeout=DefaultDeadline.DISABLED` for none. Finite paged reads
  (relationship reads, lookups, exports, permission-set lookups) apply the
  default to each page's wait on the server (to each wait for the next
  message on an unpaged read), so a stalled server fails the read; time your
  code spends between items never counts. Watch has no default lifetime.
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

Local validation raises `LocalValidationError` before any RPC. It is both an
`InvalidArgumentError` (code `INVALID_ARGUMENT`, a `grpc.RpcError`) and a
`ValueError`, so input handling written as `except ValueError` keeps working:

```python
try:
    rel = Relationship.from_triple(resource, relation, subject)   # e.g. "document", "viewer", "user:1"
except ValueError:          # also matches `except InvalidArgumentError`
    raise BadRequest()
```

An `INVALID_ARGUMENT` from the server stays a plain `InvalidArgumentError`
(not a `ValueError`) with `server_message` set. `ObjectRef` applies SpiceDB's
tuple rules (a lowercase type, optionally `prefix/type`, and a nonempty ID of
`a-z A-Z 0-9 / _ | - = +` or `*`), so `ObjectRef("", "")` raises instead of
reaching the server. `ObjectRef.parse("document:readme")` and
`SubjectRef.parse("team:eng#member")` read the string forms. `code_of`
also classifies raw `grpc.RpcError`, `asyncio.CancelledError` and
`TimeoutError`. Tests construct errors with
`SpiceDBError.from_code(grpc.StatusCode.FAILED_PRECONDITION, "...")`.

A response the client cannot interpret (a missing relationship, object, ID or
revision, an expand node with neither children nor subjects, a cursor that
does not advance) raises `InternalError` (code `INTERNAL`, not retried by
default). authzed-py passed such responses through with empty values; this
client never skips them or fills them in.

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
filtered reads and deletes, checks resolved from registered results, a
resolver callback (`set_check_resolver(grant_all)` grants everything) or the
stored relationships, and lookups over the objects it knows, with the real
client's method signatures. A generated `TypedClient` wraps it:
`TypedClient(InMemoryClient())`.
