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
- **`spicedb.raw`** — the escape hatch's `RawGrpc` container; secondary API,
  see "Escape hatch" below

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

Per root DESIGN.md, "RULE: Credentials over insecure transport require an
explicit opt-in": `insecure=True` only permits plaintext to a loopback
endpoint (`localhost`, `127.0.0.0/8`, `::1`, or a `unix:` socket target) — the
local-development case that is the entire reason it exists. Anything else
needs `allow_insecure_remote_credentials=True` passed explicitly, or the
constructor raises `InvalidArgumentError` before any channel is created.

### Custom TLS trust material

```python
# A SpiceDB behind a private or corporate CA
client = SpiceDBClient(
    "spicedb.internal:443",
    token="my-token",
    ca_cert=pathlib.Path("/etc/ssl/certs/internal-ca.pem").read_bytes(),
)

# ...and where the server requires mutual TLS
client = SpiceDBClient(
    "spicedb.internal:443",
    token="my-token",
    ca_cert=ca_pem,
    client_cert=cert_pem,
    client_key=key_pem,
)
```

All three are PEM **bytes**, not paths: certificates commonly arrive from a
mounted secret or a config store rather than the local filesystem, and reading
a file is the caller's one-liner either way.

Why these exist. Root DESIGN.md, "RULE: A system-TLS constructor must reach a
real server", requires the default secure path to delegate to the ecosystem's
default trust source — for grpc-python that is `grpc.ssl_channel_credentials()`
with no arguments — and names the hazard that leaves visible: grpc's C-core
compiles in its own `roots.pem`, so a CA an operator installed in the host's
trust store is **not** honoured by the default. That rule permits delegating
to the bundled set precisely *because* a caller can supply their own material
instead; `ca_cert` is what makes that true here.

`ca_cert` **replaces** grpc's bundled roots for that client rather than adding
to them (grpc's own behavior, and generally what a deployment pinning a private
CA wants). Leaving all three unset produces credentials byte-identical to a
bare `grpc.ssl_channel_credentials()`, so the default remains pure delegation —
this library never selects a root set of its own, which clause 1 of that rule
prohibits.

Two combinations are refused in the constructor, before any channel exists:

- **`insecure=True` with any of the three.** A plaintext connection performs no
  handshake, so grpc would silently ignore the material and put the bearer
  token on the wire in cleartext behind a call site that reads as though TLS
  were configured — the failure root DESIGN.md, "RULE: Credentials over
  insecure transport require an explicit opt-in", exists to prevent. Supplying
  trust material is never a second, quieter route to an insecure transport, and
  never a construction path that skips that rule's guard. It raises rather than
  silently turning TLS on, since an implicit upgrade is just as surprising.
- **`client_cert` without `client_key`, or vice versa.** Neither half is usable
  alone; grpc's C-core would reject the pair later, from a layer with no idea
  which argument was wrong.

Testing this needs `openssl` on `PATH`: `tests/test_custom_tls.py` and
`examples/custom_tls/` generate a throwaway CA and sign a server certificate with
it, then complete a real handshake against a real gRPC TLS server. They fail
rather than skip without it, deliberately — a skipped handshake test reads as
coverage while proving nothing, which is the failure mode root DESIGN.md, "RULE:
A system-TLS constructor must reach a real server", clause 3 warns about one
layer up.

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

### Escape hatch: raw gRPC access

`raw_grpc()` on both flavors returns a `spicedb.raw.RawGrpc` — the live channel
and the bearer-token metadata this client is already using:

```python
from authzed.api.v1 import permission_service_pb2 as psp
from authzed.api.v1 import permission_service_pb2_grpc as psg

raw = client.raw_grpc()                # sync
raw = await client.raw_grpc()          # aio -- the channel binds to the running loop
stub = psg.PermissionsServiceStub(raw.channel)
resp = stub.CheckPermission(psp.CheckPermissionRequest(...), metadata=raw.metadata)
```

Clearly-marked **secondary** API, which is what root DESIGN.md's "What NOT To
Do" permits: channels, stubs and metadata stay out of the primary surface, and
"escape hatches for advanced use are acceptable as clearly marked secondary
API". It exists so a request the idiomatic surface cannot express — an RPC or
proto field not wrapped here, such as
`WriteRelationshipsRequest.optional_transaction_metadata` — has a workaround
short of forking the client.

Four properties, all deliberate:

- **`metadata` is not optional.** This client authenticates per call rather
  than through channel credentials or an interceptor (see `spicedb._auth`), so
  a stub built from `raw.channel` alone sends no token and SpiceDB answers
  `UNAUTHENTICATED`.
- **A raw call is a raw call.** No `spicedb.errors` mapping, no retry, and no
  `default_timeout` — pass `timeout=` yourself. That is the cost of the hatch,
  and the reason the idiomatic methods remain the default.
- **The channel stays owned by the client.** `close()`/`__exit__` closes it;
  closing it yourself breaks every later call on that client.
- **It is an accessor, never a constructor.** It takes no endpoint, token, or
  transport setting, and `spicedb.raw` defines no client. Channel construction
  stays on the single guarded path in `__init__`, so the hatch cannot become a
  way around root DESIGN.md, "RULE: Credentials over insecure transport require
  an explicit opt-in".

No stability promise beyond what `grpcio` and the generated `authzed.api.v1`
stubs give: they are those packages' objects, and this client will not shim
over a change in either.

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

#### Stream lifecycle: abandoning a stream releases it

Root `DESIGN.md`, "RULE: Abandoning a stream must release it": taking the
first N results and stopping is the natural idiom, and it must tell the
server to stop. Every streaming method holds the gRPC call object and cancels
it in a `finally`:

```python
call = None
try:
    call = self._permissions.ReadRelationships(request, metadata=self._metadata)
    for resp in call:          # `async for` in spicedb.aio
        ...
        yield ...
finally:
    if call is not None:
        call.cancel()
```

Opening the call is the first statement *inside* the `try`, not above it,
and the cancel is guarded with `if call is not None`. If call construction
itself raises -- before it returns anything to assign -- `call` must already
be bound to something, or the `finally` that runs on the way out raises
`UnboundLocalError` and masks the real exception. Assigning `None` first and
guarding the cancel makes "the call was never opened" a no-op instead of a
crash.

The `finally` is the load-bearing part. Both surfaces are generators, so
closing the generator throws `GeneratorExit` in at the suspended `yield` —
which runs the `finally` and cancels the call. Without it, closing the
generator would unwind the loop and leave the RPC entirely alone, and SpiceDB
would hold a dispatch open per abandoned stream for the life of the channel.
Cancelling an already-finished call is a no-op, so the same `finally` covers
normal exhaustion and mid-stream errors without a special case.

How the generator gets closed differs between the two surfaces, and this is
worth knowing:

- **`spicedb.sync`** returns a plain generator. `break` drops the last
  reference, CPython closes it immediately, and the cancel happens
  synchronously before the next statement. (Even before the `finally`
  existed, the call object's own finalizer usually got there eventually —
  via the same refcounting. A reference cycle involving the generator
  defers both to the same gc pass: an uncollected generator does not run
  its `finally` any more than an uncollected call object runs its
  finalizer, so the explicit cancel is not what makes that case
  deterministic — it isn't. What it actually buys is independence: release
  no longer depends on CPython's refcounting behavior being what tears the
  generator down, or on grpc's own `__del__` existing and doing the right
  thing — properties of the interpreter and the library, not of this
  client's contract.)
- **`spicedb.aio`** returns an async generator, whose `aclose()` is a
  coroutine and therefore cannot run at refcount-zero. A bare `break` still
  releases the stream — the event loop's async-generator finalization hook
  schedules `aclose()` — but on the loop's schedule, not yours.
  `contextlib.aclosing()` is the deterministic form and the one to reach for
  when the release must happen before the next line:

  ```python
  from contextlib import aclosing

  async with aclosing(client.watch()) as events:
      async for event in events:
          if done(event):
              break        # stream released here, not whenever the loop gets to it
  ```

This is deliberately *not* a per-call `timeout=` on streaming methods. A
deadline is the wrong bound for a long-lived stream — see "Deadlines" below;
cancellation is the right one, and it is what these methods offer.

`tests/test_stream_release.py` covers all five streams on both surfaces. It
asserts against a real in-process gRPC server whose handlers park until their
own RPC terminates, because a test that only checked the consuming loop
exited would pass whether or not anything was released.

### Watch

`watch()` yields one `WatchEvent` per `WatchResponse` from the server — not a
bare tuple, and not one yield per relationship update:

```python
@dataclass(frozen=True)
class Update:
    operation: UpdateOperation  # CREATE, TOUCH, DELETE, or UNSPECIFIED
    relationship: Relationship

@dataclass(frozen=True)
class WatchEvent:
    updates: list[Update]
    changes_through: str  # resume token; pass as start_revision to resume after a dropped stream
    is_checkpoint: bool = False  # True carries no updates -- a fresh resume point only
```

`changes_through` is always populated — it is the proto's `changes_through`
ZedToken, "the point in time that the watch response is current through...
can be used in a subsequent WatchRequest to resume watching from this
point." Without it, a consumer whose stream dropped could only restart from
its original `start_revision` (reprocessing everything since, possibly past
the GC window) or from head (silently losing every change in the gap).

`watch(include_checkpoints=True)` requests `WATCH_KIND_INCLUDE_CHECKPOINTS`
in addition to relationship updates — recommended if this SpiceDB instance
is running behind a proxy that aborts idle connections, since a checkpoint
keeps the stream alive even when there are no changes. A checkpoint event
carries no `updates`, so `WatchEvent.is_checkpoint` lets a caller tell
"nothing changed, here is a fresh resume point" from "here are changes"
rather than silently treating a checkpoint as an empty update batch.

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

### Deadlines

Every unary method takes a keyword-only `timeout: float | None = None`
(seconds), passed straight through as the underlying stub call's `timeout=`.
`None` (the default on every call site) means "use the client's
`default_timeout`" -- there is deliberately no way to request an unbounded
unary call. `SpiceDBClient(..., default_timeout=30.0)` sets that client-wide
default; both flavors default it to 30 seconds, mirroring `authzed-node`'s
`DEFAULT_DEADLINE_MS = 30_000` (its comment cites `grpc/grpc-node#541`). See
root DESIGN.md, "RULE: A unary call must have a deadline" — without a finite
default, a SpiceDB instance that accepts a connection but never answers hangs
every caller that didn't opt in to a timeout forever, since the connection
looks fine at the transport level and nothing is ever produced to retry.

```python
client = SpiceDBClient("localhost:50051", token="t", insecure=True, default_timeout=5.0)
result = await client.check_permission(full(), rel)             # bound by the 5s default
result = await client.check_permission(full(), rel, timeout=1.0)  # overrides it for this call
```

Server-streaming calls (`read_relationships`, `lookup_resources`,
`lookup_subjects`, `watch`, `export_relationships`) do NOT take a `timeout`
and are NOT bound by `default_timeout` — they are long-lived by design (a
`watch` may run for the life of the process), and applying the unary default
to them would make the stream itself the outage.

`import_relationships` (`ImportBulkRelationships`) is client-streaming, not
server-streaming, but the same exclusion applies for the mirror-image reason:
its duration scales with the size of the caller's dataset, not with server
latency, so no fixed default is correct for it. Unlike the server-streaming
calls above, it DOES still take a `timeout` — passing `None` (the default)
means unbounded, not "use `default_timeout`"; pass an explicit `timeout` to
bound a bulk import.

Note for callers reasoning about worst-case latency: `timeout` is a
per-*attempt* budget, applied fresh on each retry, so a call that retries can
take up to `timeout × (retries + 1)` plus backoff, and an auto-paging call
(e.g. `delete_relationships`) applies the same `timeout` fresh to each page.

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

Automatic retry with jittered exponential backoff, for **reads only**, on
**`UNAVAILABLE` and `ABORTED`**.

`RESOURCE_EXHAUSTED` is deliberately NOT retryable. In SpiceDB it means
either memory load-shed — where retrying adds load to an already-overloaded
server — or a deterministic `MaxDepthExceeded`, which can never succeed and
whose retries re-run the most expensive class of check several times before
surfacing the same error. See root `DESIGN.md`, "RULE: Automatic retry is
for idempotent operations only".

**Mutations are never auto-retried.** `WriteRelationships` carrying
`OPERATION_CREATE`, or any request with preconditions, is not idempotent: if
it commits and the response is lost — a rolling restart, a proxy dropping the
connection — the retry returns `ALREADY_EXISTS`/`FAILED_PRECONDITION` and the
caller concludes a write failed that in fact succeeded. Writes, deletes,
schema writes, bulk import, and the counter registration calls therefore
never enter the retry loop: their errors are mapped to this client's typed
form and raised on the first attempt. A caller who wants a mutation retried
must decide that themselves, knowing their own idempotency.

**Timeout shape**: the per-call timeout is a per-*attempt* budget, applied
fresh to each retry rather than shrinking across them, so a call that
legitimately needs several retries is not made more likely to fail than one
that needs none. Worst-case latency for a timeout `t` is therefore
`t × (retries + 1)` plus backoff. This applies to calls that take a
`timeout` parameter at all -- the auto-paging streaming calls
(`read_relationships`, `export_relationships`) take none, per root
`DESIGN.md`, "Streaming calls MUST NOT inherit the unary default": they rely
on caller cancellation, not a per-page deadline, so there is no `t` to
re-apply per page. Root `DESIGN.md`, "On worst-case latency", covers why the
per-attempt shape differs from Go's; a caller needing a true end-to-end
bound on a timed call must impose it above this client.

This is identical on both flavors: `is_transient()` checks against
`grpc.RpcError`, the base type both `grpc`'s and `grpc.aio`'s error classes
satisfy. Narrowing it to `grpc.aio.AioRpcError` would silently disable retry
for `spicedb.sync`, whose channel raises `grpc._channel._InactiveRpcError`.

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
| `raw_escape_hatch/` | `raw_grpc()` — driving a generated stub directly for a proto field (`optional_transaction_metadata`) and an RPC (`CheckPermission`) the idiomatic API does not expose, on both flavors |

## Changelog

See [CHANGELOG.md](CHANGELOG.md) for release notes.

