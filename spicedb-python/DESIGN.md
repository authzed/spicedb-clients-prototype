# spicedb-python — Idiomatic Python Client Design

## Inherits

Root DESIGN.md (`../DESIGN.md`) — read it for the overall vision and backwards
compatibility mandate. This document takes precedence for Python-specific
decisions.

## Language-Specific Goals

### Philosophy

Pythonic API that feels like a native library. Use modern Python features
(3.11+): type hints everywhere, dataclasses, async/await, exception hierarchy.

### Sync and Async

Both concurrency models are first-class and neither owns the bare namespace:

```python
from spicedb.sync import SpiceDBClient   # synchronous
from spicedb.aio import SpiceDBClient    # asynchronous
```

There is deliberately no `spicedb.SpiceDBClient`. Everything else — relationship
types, filters, transactions, consistency constructors, the error hierarchy — is
flavor-free and imported from `spicedb` directly.

The two clients expose identical method names and signatures; only
`async`/`await` and `Iterator` vs `AsyncIterator` differ.
`tests/test_parity.py` enforces this.

Request building and response mapping live in `spicedb/_requests.py` and
`spicedb/_mapping.py`, shared by both flavors so neither can drift on
proto handling.

Neither client uses a gRPC interceptor. Both attach the bearer token as
per-call metadata, because `grpc` and `grpc.aio` differ in interceptor
registration semantics in ways that silently change which calls get
authenticated.

Sync callers should build one client at startup and reuse it. Async callers
must not share a client across event loops — doing so raises
`EventLoopBindingError`, which points at `spicedb.sync`.

### Package Structure

- **`spicedb`** — main package; relationship types, filters, transactions,
  consistency constructors, and the error hierarchy all live at this level
- **`spicedb.aio`** — the asynchronous `SpiceDBClient`
- **`spicedb.sync`** — the synchronous `SpiceDBClient`
- **`spicedb.types`** — relationship types, filters, transactions (dataclasses)
- **`spicedb.consistency`** — consistency strategy constructors
- **`spicedb.errors`** — typed exception hierarchy

There is no `spicedb.client` module anymore, and no `spicedb.SpiceDBClient`
alias — import `spicedb.aio.SpiceDBClient` or `spicedb.sync.SpiceDBClient`
explicitly. See "Sync and Async" above.

### Client Construction

```python
from spicedb.aio import SpiceDBClient    # or: from spicedb.sync import SpiceDBClient

# For production (TLS)
client = SpiceDBClient("grpc.example.com:443", token="my-token")

# For testing (plaintext)
client = SpiceDBClient("localhost:50051", token="testtoken", insecure=True)
```

Context manager — `async with` on `spicedb.aio`, `with` on `spicedb.sync`:

```python
async with SpiceDBClient(...) as client:  # aio
    ...

with SpiceDBClient(...) as client:  # sync
    ...
```

`spicedb.sync.SpiceDBClient` needs no event loop, so a sync caller typically
skips the context manager and builds one client at process startup instead,
reusing it for the process lifetime:

```python
client = SpiceDBClient("localhost:50051", token="testtoken", insecure=True)
# reused by every caller for the life of the process; nothing to await
```

### Consistency

Consistency is explicit, never defaulted:

```python
from spicedb import full, min_latency, at_least, snapshot

result = await client.check_permission(full(), rel)
result = await client.check_permission(at_least(revision), rel)
```

All write operations return a `revision: str`.

### Types

Dataclass-based types (not proto messages):

```python
@dataclass(frozen=True)
class Relationship:
    resource_type: str
    resource_id: str
    resource_relation: str
    subject_type: str
    subject_id: str
    subject_relation: str = ""
    caveat_name: str | None = None
    caveat_context: dict[str, Any] | None = None
    expiration: datetime | None = None
    check_context: dict[str, Any] | None = None
```

Constructor helpers:
- `Relationship.from_triple("document:example", "viewer", "user:jimmy")`
- `Relationship.from_tuple("document:example#viewer", "user:jimmy")`

`check_context` is a distinct concept from `caveat_context` and the two must
not be conflated: `caveat_context` is written to the server as part of the
relationship (`_to_proto()` embeds it in `optional_caveat`) and is evaluated
whenever anything checks against that stored relationship in the future.
`check_context` has no wire representation on `core_pb2.Relationship` at
all — it only matters when this `Relationship` is passed to
`check_permission()`/`check_permissions()` as the item being checked, where
it supplies per-item caveat context for that one check. See "Checks" below.

### Checks

```python
# aio
results = await client.check_permissions(consistency, *relationships)  # list[CheckResult]
result = await client.check_permission(consistency, relationship)      # CheckResult
any_allowed = await client.check_any(consistency, *relationships)      # bool
all_allowed = await client.check_all(consistency, *relationships)      # bool

# sync — identical signatures, no `await`
results = client.check_permissions(consistency, *relationships)
result = client.check_permission(consistency, relationship)
any_allowed = client.check_any(consistency, *relationships)
all_allowed = client.check_all(consistency, *relationships)
```

All checks use BulkCheckPermissions under the hood.

`check_permission()`/`check_permissions()` return `CheckResult`, not a bare
bool — `CheckPermissionResponse.permissionship` is three-valued
(`NO_PERMISSION`/`HAS_PERMISSION`/`CONDITIONAL_PERMISSION`), and collapsing a
`CONDITIONAL_PERMISSION` result (a caveated relationship whose context wasn't
supplied) to `False` makes it indistinguishable from a real denial:

```python
@dataclass(frozen=True)
class CheckResult:
    permissionship: Permissionship
    missing_context: list[str]  # populated when permissionship is CONDITIONAL_PERMISSION
    checked_at: str             # revision this check was evaluated at

    @property
    def has_permission(self) -> bool:
        """True ONLY for HAS_PERMISSION."""
        ...

    def __bool__(self) -> bool:
        """Mirrors has_permission, so `if result:` is safe."""
        ...

result = await client.check_permission(full(), rel)
if result.permissionship == Permissionship.CONDITIONAL_PERMISSION:
    print(f"needs context: {result.missing_context}")  # e.g. ["now"]
elif result:  # or result.has_permission -- __bool__ mirrors it
    ...  # a full grant
```

`CheckResult.__bool__` exists because `if result:` is the single most natural
migration path from the old bool-returning `check_permission()`, and a plain
object (with no `__bool__`) is unconditionally truthy — `if result:` would
otherwise silently grant on a `CONDITIONAL_PERMISSION`, reintroducing via the
most obvious code the exact fail-open this change removes from
`.has_permission`. `bool(result)` and `result.has_permission` always agree.

`Permissionship` gained a fourth member, `NO_PERMISSION`, appended after
`CONDITIONAL_PERMISSION` (not inserted alongside `UNSPECIFIED`) so the
pre-existing members keep their values. The same enum serves both the check
surface (`CheckResult`) and the lookup surface (`LookupResource`,
`ResolvedSubject`) — lookups never yield `NO_PERMISSION`, since a
subject/resource pair lacking the permission is simply absent from a lookup
stream rather than yielded with that permissionship.

`check_any()`/`check_all()` stay boolean, but count **only**
`CheckResult.has_permission` results as granted — a `CONDITIONAL_PERMISSION`
result never makes either `True`. This is deliberately fail-closed: an
unevaluated caveat must not silently pass an "any"/"all" check.

`CheckResult.checked_at` exposes `CheckPermissionResponse.checked_at` (a
ZedToken), which no earlier version of this client surfaced at all — thread
it into `at_least()` to make a later call observe this check and everything
it observed (read-your-writes for checks; see `examples/read_your_writes/`).
`CheckBulkPermissionsResponse.checked_at` is response-level, not per-item, so
`check_permissions()` propagates that one token onto every `CheckResult` it
returns.

#### Caveat context: call-level default and per-item override

`check_permission()`/`check_permissions()` take a call-level `context=`
keyword — a default fanned out onto every item, since the wire has no
call-level context field (`CheckBulkPermissionsRequest` carries only
`consistency`/`items`/`with_tracing`; only
`CheckBulkPermissionsRequestItem.context`, proto field 4, exists per-item).
A `Relationship` passed in as an item can also carry its own
`check_context`, which overrides `context` for that one item:

```python
rel_a = Relationship.from_triple(
    "document:a", "conditional_view", "user:alice",
    check_context={"region": "eu"},   # overrides for this item only
)
rel_b = Relationship.from_triple("document:b", "conditional_view", "user:alice")

results = await client.check_permissions(
    full(), rel_a, rel_b, context={"now": 42, "region": "us"}
)
# item for rel_a is checked with {"now": 42, "region": "eu"}
# item for rel_b is checked with {"now": 42, "region": "us"} (unmodified default)
```

The merge is **key-level, item wins** — `{**context, **rel.check_context}`
— not wholesale replacement. An item's `check_context` only overrides the
keys it actually names; call-level keys it doesn't mention are retained. If
an item's context replaced the call-level context outright, supplying one
key would silently drop every other call-level key the caveat still needed,
landing the caller right back in the confusing `CONDITIONAL_PERMISSION`
state this whole mechanism exists to make legible. If neither call-level nor
per-item context is supplied for an item, no `context` field is set on that
item's wire request at all (not an empty `Struct`).

This is purely additive: no existing `check_permission()`/`check_permissions()`
call site changes, and `Relationship.check_context` defaults to `None`, so
every existing `Relationship` construction is unaffected. The merge itself
lives in the flavor-free `spicedb/_requests.py::check_bulk_request` (and its
private `_merge_check_context` helper), shared by both `spicedb.aio` and
`spicedb.sync`.

### Streaming

`spicedb.aio` yields `AsyncIterator`s; `spicedb.sync` yields plain
`Iterator`s. Same method names, same automatic pagination — only `for` vs
`async for` differs:

```python
# aio
async for rel in client.read_relationships(filter, consistency):
    ...

async for resource in client.lookup_resources(..., consistency):
    ...  # resource: LookupResource

async for subject in client.lookup_subjects(..., consistency):
    ...  # subject: LookupSubject

# sync
for rel in client.read_relationships(filter, consistency):
    ...

for resource in client.lookup_resources(..., consistency):
    ...  # resource: LookupResource

for subject in client.lookup_subjects(..., consistency):
    ...  # subject: LookupSubject
```

`lookup_resources()`/`lookup_subjects()` yield native result dataclasses, not
bare ID strings — they carry the data a caller needs to avoid silently
over-granting access (mirrors `spicedb-go`'s `client/lookup_types.go`, the
reference design for this pattern):

```python
class Permissionship(Enum):
    UNSPECIFIED = 0
    HAS_PERMISSION = 1
    CONDITIONAL_PERMISSION = 2
    NO_PERMISSION = 3  # check surface only -- lookups never yield this

@dataclass(frozen=True)
class PartialCaveatInfo:
    missing_required_context: list[str]

@dataclass(frozen=True)
class LookupResource:
    resource_id: str
    permissionship: Permissionship
    partial_caveat: PartialCaveatInfo | None = None  # non-None when Conditional
    looked_up_at: str = ""  # revision this result was computed at

@dataclass(frozen=True)
class ResolvedSubject:
    subject_id: str
    permissionship: Permissionship
    partial_caveat: PartialCaveatInfo | None = None

@dataclass(frozen=True)
class LookupSubject:
    subject: ResolvedSubject
    excluded_subjects: list[ResolvedSubject]  # populated when subject.subject_id == "*"
    looked_up_at: str = ""  # revision this result was computed at
```

`Permissionship.HAS_PERMISSION` is a full grant; `CONDITIONAL_PERMISSION`
means the match depends on caveat context that wasn't supplied
(`partial_caveat.missing_required_context` lists what's missing) — a
conditional result is NOT a full grant. When
`LookupSubject.subject.subject_id` is the wildcard `"*"`,
`LookupSubject.excluded_subjects` lists the subjects carved out of that
wildcard grant — callers MUST check it before treating `"*"` as "every
subject has access," or they risk over-granting to excluded subjects.

`looked_up_at` is identical for every item yielded by a single
`lookup_resources()`/`lookup_subjects()` call — it's a property of the call,
not of the individual result — and, like `CheckResult.checked_at`, can be
threaded into `at_least()` to make a later call observe this lookup
(read-your-writes for lookups).

### Writes

Transaction builder — identical on both flavors except the final call:

```python
txn = Transaction()
txn.create(relationship)
txn.touch(relationship)
txn.delete(relationship)
txn.must_not_match(filter)  # precondition

revision = await client.write(txn)  # aio
revision = client.write(txn)        # sync
```

`write()`, `delete_relationships()`, and `write_schema()` all already
returned a revision string before this document's "CheckResult" work landed;
auditing the write surface for that work found nothing to add there.
`import_relationships()` is the one write RPC that does NOT return a
revision — `ImportBulkRelationshipsResponse` carries no `ZedToken` field in
the proto at all, so there is nothing to expose.

### Deletions

`delete_relationships(filter, *, must_match=None, must_not_match=None, limit=None)`
reaches the proto's `optional_preconditions`/`optional_limit` fields, mirroring
`spicedb-go`'s `WithDeleteMustMatch`/`WithDeleteMustNotMatch`/`WithDeleteLimit`
(`spicedb-go/client/relationships.go`):

```python
# aio
revision = await client.delete_relationships(
    filter,
    must_match=[guard_filter],       # MUST_MATCH precondition(s)
    must_not_match=[other_filter],   # MUST_NOT_MATCH precondition(s)
    limit=1000,                      # optional_limit
)

# sync — identical signature, no `await`
revision = client.delete_relationships(
    filter,
    must_match=[guard_filter],
    must_not_match=[other_filter],
    limit=1000,
)
```

`must_match`/`must_not_match` build `Precondition` protos the same way
`Transaction.must_match`/`must_not_match` do; the server rejects the whole
call (deleting nothing) if a precondition isn't satisfied. No options given
means the request is unchanged from before: no preconditions, no limit.

Unlike `spicedb-go`'s `DeleteRelationships`, this client does not yet
auto-page a delete across multiple RPCs when the match set exceeds a single
server-side page — that gap is pre-existing and out of scope for this
addition. Supplying `limit` bounds a single call to deleting at most that
many relationships; when more relationships match than `limit`, the server
requires `optional_allow_partial_deletions` to permit that (otherwise it
rejects the call outright), so this client sets it automatically whenever
`limit` is given. Callers that need to delete more than `limit` matches must
call again with the same filter to continue.

### Testing

Use `pytest` with `pytest-asyncio` for all tests. Examples should also be
runnable as pytest tests.

### Error Handling

Exception hierarchy:
```python
class SpiceDBError(Exception): ...
class PermissionDeniedError(SpiceDBError): ...
class NotFoundError(SpiceDBError): ...
class AlreadyExistsError(SpiceDBError): ...
class InvalidArgumentError(SpiceDBError): ...
class FailedPreconditionError(SpiceDBError): ...
class UnavailableError(SpiceDBError): ...
class CancelledError(SpiceDBError): ...
class EventLoopBindingError(SpiceDBError): ...
```

All nine are exported from `spicedb` directly — no flavor prefix.

`EventLoopBindingError` doesn't come from a gRPC status; it's raised by
`spicedb.aio.SpiceDBClient` when a client already bound to one asyncio event
loop is reused from a different one. It can't happen on
`spicedb.sync.SpiceDBClient`, which has no event loop to bind to.

Automatic retry with exponential backoff for transient errors
(`UNAVAILABLE`/`RESOURCE_EXHAUSTED`/`ABORTED`) on both flavors —
`is_transient()` checks against `grpc.RpcError`, the base type both `grpc`'s
and `grpc.aio`'s error classes satisfy.

### Type Hints

Full type hints on all public API. `py.typed` marker file for PEP 561.

## Public API Surface

See package sections above.

## Examples Manifest

| Directory | Demonstrates |
|-----------|-------------|
| `check_permission/` | Basic permission check |
| `caveated_check/` | Checking a caveated relationship with no context supplied (CONDITIONAL_PERMISSION), and resolving the same conditional to a grant by supplying the missing context via `Relationship.check_context` |
| `read_your_writes/` | Using `CheckResult.checked_at`/`LookupResource.looked_up_at` with `at_least()` to make a later call observe an earlier write |
| `write_relationships/` | Writing relationships with transaction builder |
| `read_relationships/` | Reading relationships with async iterator |
| `delete_relationships/` | Deleting relationships, including precondition-guarded deletes |
| `lookup_resources/` | Resource lookup |
| `lookup_subjects/` | Subject lookup |
| `watch_changes/` | Watching for changes |
| `schema_management/` | Schema read/write |
| `bulk_operations/` | Bulk checks and imports |
| `expand_permission_tree/` | Expanding a permission into its tree of subjects |
| `sync_check_permission/` | Basic permission check with `spicedb.sync` — one client built at startup, reused, no event loop |
| `sync_write_relationships/` | Writing relationships with the transaction builder, synchronously |
| `sync_read_relationships/` | Reading relationships with a plain `for` loop instead of `async for` |
| `sync_watch_changes/` | Watching for changes from a blocking generator |

## Changelog

See [CHANGELOG.md](CHANGELOG.md) for release notes.

