# Changelog

## Unreleased

### Added

- `spicedb.sync.SpiceDBClient` — a synchronous flavor of the client, exposing
  the same 22 methods as `spicedb.aio.SpiceDBClient` with identical names and
  signatures (`tests/test_parity.py` fails the build if the two surfaces
  drift). Intended for callers with no event loop — Django, Flask, scripts,
  batch jobs — that want to build one client at startup and reuse it for the
  life of the process.

  ```python
  from spicedb.sync import SpiceDBClient

  client = SpiceDBClient("localhost:50051", token="t", insecure=True)
  if client.check_permission(full(), rel).has_permission:
      ...
  for found in client.read_relationships(filter, full()):
      ...
  ```
- `EventLoopBindingError` — new exception, exported from `spicedb`. Raised by
  `spicedb.aio.SpiceDBClient` when the client is used from a different
  asyncio event loop than the one it bound its gRPC channel to on first use.
  The message points callers at `spicedb.sync.SpiceDBClient` as the fix for
  code that can't guarantee a single long-lived event loop.
- `FailedPreconditionError`, `UnavailableError`, and `CancelledError` are now
  exported from the package root (`from spicedb import ...`) and included in
  `spicedb.__all__`. These exception classes already existed and were already
  raised for their corresponding gRPC status codes; they were just missing
  from the public export surface, so `from spicedb import UnavailableError`
  previously failed even though `except spicedb.errors.UnavailableError`
  worked.

- `SpiceDBClient.delete_relationships()` now accepts optional `must_match`/
  `must_not_match` (each `list[Filter]`) and `limit` keyword args, mirroring
  `spicedb-go`'s `WithDeleteMustMatch`/`WithDeleteMustNotMatch`/
  `WithDeleteLimit` (`spicedb-go/client/relationships.go`). Previously the
  proto's `optional_preconditions`/`optional_limit` fields were silently left
  unset, so there was no way to do a precondition-guarded delete. Additive —
  existing `delete_relationships(filter)` call sites are unaffected.

  ```python
  # Only delete if an `owner` relationship still exists on the resource:
  revision = await client.delete_relationships(
      filter,
      must_match=[Filter(resource_type="document", resource_id="1", relation="owner")],
      limit=1000,
  )
  ```
- `SpiceDBClient.computable_permissions()` and `SpiceDBClient.dependent_relations()`
  — previously missing `SchemaService` RPCs (API-coverage gap). Both take
  `(consistency, definition_name, relation_name_or_permission_name)` and
  return `list[RelationReference]` (new native type: `definition_name`,
  `relation_name`, `is_permission`), mirroring `spicedb-go`'s
  `ComputablePermissions`/`DependentRelations`/`RelationReference`
  (`spicedb-go/client/schema.go`). These are stable `SchemaService` RPCs, not
  experimental.

  ```python
  permissions = await client.computable_permissions(full(), "document", "viewer")
  for p in permissions:  # p: RelationReference
      print(p.relation_name, p.is_permission)

  relations = await client.dependent_relations(full(), "document", "view")
  ```
- `DeadlineExceededError` and `ResourceExhaustedError` — two of the canonical
  nine SpiceDB error types, previously missing from this client (it mapped
  only seven of nine gRPC status codes, falling through to the generic
  `SpiceDBError` for `DEADLINE_EXCEEDED`/`RESOURCE_EXHAUSTED`). Both are
  exported from `spicedb` and included in `spicedb.__all__`.
  This entry also made `is_transient()`'s typed-instance fallback recognize
  `ResourceExhaustedError`; the 2026-08-18 retry-safety entry below reversed
  that, and the shipped `is_transient()` does not treat `RESOURCE_EXHAUSTED`
  as retryable in either form. The error *type* remains, and is still what a
  rate-limited call raises -- only its retryability changed.
- `LookupResource`/`LookupSubject` gained `looked_up_at: str` — the revision
  a `lookup_resources()`/`lookup_subjects()` result was computed at
  (`LookupResourcesResponse`/`LookupSubjectsResponse.looked_up_at`),
  identical for every item from a single call. Previously dropped entirely,
  making it impossible to pin a later call to at least as fresh as an
  earlier lookup. Additive — the new field defaults to `""` and existing
  positional/keyword construction of both dataclasses is unaffected.

  ```python
  async for resource in client.lookup_resources("document", "view", ("user:alice", ""), full()):
      ...
  # resource.looked_up_at can be threaded into at_least() for a later call
  ```

- `Relationship` gained a `check_context: dict[str, Any] | None = None` field
  (also accepted by `Relationship.from_triple()`/`from_tuple()`), letting a
  `Relationship` passed to `check_permission()`/`check_permissions()` supply
  or override caveat context for just that one check item. It merges with
  the pre-existing call-level `context=` keyword key-by-key — this item's
  keys win on conflict, call-level keys the item doesn't mention are
  retained (NOT wholesale replacement) — and an item with no `check_context`
  inherits `context` unchanged. Previously this client could only apply one
  context dict to every item in a bulk check, with no way to vary it
  per-relationship, making a `CheckResult.missing_context` on one item
  unactionable without either supplying that context for every other item
  too or issuing a second call. `check_context` is check-time-only and has
  no wire representation on `core_pb2.Relationship` — it is distinct from
  the pre-existing `caveat_context`, which is written into a relationship at
  write time (`optional_caveat`) and evaluated on every future check against
  it; the two must not be conflated. Fully additive: no existing
  `check_permission()`/`check_permissions()` call site changes, and
  `check_context` defaults to `None`, so no existing `Relationship`
  construction is affected. The merge lives in the shared
  `spicedb/_requests.py::check_bulk_request`, used identically by both
  `spicedb.aio.SpiceDBClient` and `spicedb.sync.SpiceDBClient`.

  ```python
  rel_a = Relationship.from_triple(
      "document:a", "conditional_view", "user:alice",
      check_context={"region": "eu"},  # overrides for this item only
  )
  rel_b = Relationship.from_triple("document:b", "conditional_view", "user:alice")

  results = await client.check_permissions(
      full(), rel_a, rel_b, context={"now": 42, "region": "us"}
  )
  # rel_a's item is checked with {"now": 42, "region": "eu"}
  # rel_b's item is checked with {"now": 42, "region": "us"} (call-level default, unmodified)
  ```

### Fixed

- **2026-08-18**: **Security hardening — the guard that refuses to send credentials over
  plaintext to a non-loopback host now fails closed on targets it cannot vouch for.** The
  equivalent guard in this repo's C#, Rust, TypeScript and Java clients had a bypass:
  `"127.0.0.1:443@evil.com"` was read as loopback by a last-colon split while their transports
  parsed the same string as a URI, took `127.0.0.1:443` for *userinfo*, and connected to
  `evil.com` — sending the bearer token there in cleartext. **Python was not exploitable
  through that class**: grpc's C-core resolves that target to `ipv4:127.0.0.1:443` and never
  contacts `evil.com`. But the guard was doing its own string split, and depending on C-core
  happening not to be fooled by one input is not a property worth relying on.

  Unlike the other clients, `is_loopback_endpoint` cannot be made to call the transport's own
  parser — the target goes to grpc's C-core, which parses it in C++ and exposes no
  Python-callable equivalent. So it now (1) refuses outright any endpoint containing `@`, `/`,
  `?`, `#`, or whitespace, the characters that can move the authority under URI parsing, and
  (2) splits what remains the way C-core's `SplitHostPort` does — a bracketed host must be
  followed by end-of-string or `":"` + a numeric port, and a single-colon `host:port` is split
  only when the port is numeric. That numeric-port check is the one whose removal opened the C#
  bypass, and it tests for **ASCII** digits: `str.isdigit()` is true for `"٤٤٣"`, `"４４３"` and
  `"²"`, none of which C-core parses as a port, so a predicate built on it would have split
  there and handed back the loopback host. `"127.0.0.1:443@evil.com"`, `"[::1]:443@evil.com"`,
  `"127.0.0.1:notaport"` and the non-ASCII-digit forms now
  require `allow_insecure_remote_credentials=True` instead of being accepted as loopback. Every
  ordinary local target keeps working with no opt-in: `localhost:50051`, `127.0.0.1:50051`,
  `[::1]:50051`, `::1`, and `unix:` targets — the last now matched
  case-insensitively, since a URI scheme is case-insensitive and C-core normalizes `UNIX:`
  and dials the socket just the same, so the previous case-sensitive check refused a target
  the transport treats as local.

- **2026-08-18**: Abandoning a stream did not release it. Every streaming method on both
  surfaces (`read_relationships`, `lookup_resources`, `lookup_subjects`, `watch`,
  `export_relationships`) iterated the gRPC call inline -- `async for resp in
  self._permissions.LookupSubjects(...)` -- with no `finally` anywhere, so closing the generator
  unwound the loop and left the RPC alone. SpiceDB kept a dispatch open per abandoned stream for
  the life of the channel. Each method now holds the call object and cancels it in a `finally`,
  which runs when `GeneratorExit` lands on the suspended `yield`, so `break` releases the stream
  on both surfaces. Root DESIGN.md, "RULE: Abandoning a stream must release it", clause 2.

  The impact differed by surface, and the fix matters for a different reason on each:

  - `spicedb.aio` genuinely leaked. An async generator's `aclose()` is a coroutine, so nothing
    ran at refcount-zero and the call was released only when the channel closed or the process
    exited. Now a bare `break` releases it (via the loop's async-generator finalizer), and
    `contextlib.aclosing()` releases it deterministically -- reach for `aclosing` when the
    release must happen before the next line.
  - `spicedb.sync` mostly got away with it: CPython closes a plain generator at refcount-zero
    and the call object's own finalizer usually cancelled from there. "Usually" is the problem,
    but not the one it looks like -- a reference cycle defers the generator's `finally` to a gc
    pass exactly as it defers the call object's finalizer, so the explicit cancel does not make
    that case deterministic. What it buys is independence from CPython's refcounting behavior
    and from grpc's own `__del__` doing the right thing, neither of which this client should
    depend on.

  New `tests/test_stream_release.py` covers all five streams on both surfaces against a real
  in-process gRPC server whose handlers park until their own RPC terminates. It asserts the
  server observed the stream end, not that the consuming loop exited -- the latter is true with
  or without the leak. Note for anyone writing new streaming tests: a stub that returns a bare
  generator no longer matches what the client sees, since the client calls `cancel()` on what
  the stub returns. `tests/test_client.py` and `tests/test_client_sync.py` now wrap their
  streaming stubs in `_FakeAioCall` / `_FakeSyncCall`, which model the real shape (iterator +
  RPC handle) and record whether the client cancelled.

- **2026-08-18**: `import_relationships` (sync and aio) required a materialized `list`, forcing a
  caller streaming in a large import from a generator or a DB cursor to hold every relationship
  in memory at once before calling this method at all. Both now accept `Iterable[Relationship]`
  -- a `list` still works unchanged, but a generator or any other iterable now works too, and is
  consumed lazily: `_requests.import_batches` pulls one batch (`IMPORT_BATCH_SIZE` relationships)
  at a time via `itertools.islice` rather than indexing/slicing the whole input up front (which
  also required `len()`, so a plain generator raised `TypeError` immediately).
- **2026-08-18**: Call deadlines, per root `DESIGN.md` "RULE: A unary call must have a
  deadline". Previously no method on either flavor accepted a timeout, and no client-level
  default existed, so a SpiceDB instance that accepted a connection but never answered hung
  every caller forever — the connection looks fine at the transport level, so no error is
  produced and there is nothing for retry logic to act on.
  - Every unary method (`check_permission`/`check_permissions`/`check_any`/`check_all`, `write`,
    `delete_relationships`, `read_schema`, `write_schema`,
    `expand_permission_tree`, `register_relationship_counter`/`count_relationships`/
    `unregister_relationship_counter`, `reflect_schema`, `diff_schema`,
    `computable_permissions`, `dependent_relations`) now takes a keyword-only
    `timeout: float | None = None` (seconds), passed straight through as the stub call's
    `timeout=`. Additive — omitting it is unaffected.
  - `SpiceDBClient.__init__` gained `default_timeout: float = 30.0`, applied to any unary call
    that doesn't pass its own `timeout`. 30s mirrors `authzed-node`'s
    `DEFAULT_DEADLINE_MS = 30_000` (its comment cites `grpc/grpc-node#541`). There is
    deliberately no way to construct a client whose unary calls have no bound at all.
  - Streaming calls (`read_relationships`, `lookup_resources`, `lookup_subjects`, `watch`,
    `export_relationships`) do **not** accept `timeout` and are **not** bound by
    `default_timeout` — DESIGN.md's "Streaming calls MUST NOT inherit the unary default":
    these are long-lived by design (a `watch` may legitimately run for the life of the
    process), and a 30s cutoff would end a legitimate stream, which is a worse defect than
    the one this change fixes.
  - **Fix round 1 correction**: `import_relationships` (`ImportBulkRelationships`) also takes a
    `timeout: float | None = None`, but — unlike the unary methods above — it is
    client-streaming, not unary, and is now explicitly **excluded** from `default_timeout`: its
    duration scales with the size of the caller's dataset, not with server latency, so no fixed
    default is correct for it (root DESIGN.md, "RULE: A unary call must have a deadline",
    clause 3, amended to cover client-streaming and bidirectional RPCs, not only
    server-streaming). Passing no `timeout` means unbounded; passing one still bounds the call.
    An earlier version of this fix incorrectly applied `default_timeout` to bulk imports, which
    would have silently aborted large, legitimate multi-minute loads at 30 seconds.
  - New `examples/call_deadlines/` demonstrates the `default_timeout` construction parameter,
    a per-call `timeout` override, and that bulk import is unbounded by default — run against a
    real SpiceDB, not a mock, so a regression in the documented construction path (e.g. the
    parameter silently disappearing) fails this test too, not just the unit tests below.
  - `DeadlineExceededError` (added earlier, see below, but never actually produced by this
    client since nothing enforced a deadline) is now reachable: a timed-out call raises it,
    not a generic `SpiceDBError`. `DEADLINE_EXCEEDED` is not in `_TRANSIENT_CODES`, so a
    timeout is never auto-retried.
  - New `tests/test_deadlines.py`, both flavors, against a real in-process gRPC server whose
    handlers deliberately stall: a unary call against a stub that never responds raises
    `DeadlineExceededError` well before the server's stall completes (not a hang), a per-call
    `timeout=` overrides a much larger client default, and a streaming call outlives a tiny
    unary default instead of inheriting it. Every test is wrapped in its own watchdog so a
    regression fails the suite instead of hanging CI.
- **2026-08-18**: Retry safety, per root `DESIGN.md` "RULE: Automatic retry is for idempotent
  operations only". Three changes, both `spicedb.sync` and `spicedb.aio`:
  - `RESOURCE_EXHAUSTED` is no longer retried. In SpiceDB it signals memory load-shed (retrying
    adds load to an already-overloaded server) or a deterministic `MaxDepthExceeded` (retrying can
    never succeed — it re-runs the most expensive class of check several times before surfacing
    the same error). Previously it was in `_TRANSIENT_CODES`/`is_transient`'s retryable set.
  - Mutations (`write`/`WriteRelationships`, `delete_relationships`/`DeleteRelationships`,
    `write_schema`/`WriteSchema`, `register_relationship_counter`/`unregister_relationship_counter`)
    are no longer retried on a transient error, even though the underlying gRPC code is retryable.
    A `WriteRelationships` containing `OPERATION_CREATE`, or any request carrying preconditions, is
    not idempotent: if it commits and the response is lost (a rolling restart, a proxy dropping the
    connection), a retry surfaced `ALREADY_EXISTS`/`FAILED_PRECONDITION` for a write that in fact
    succeeded, and the caller wrongly concluded it had failed. Reads still retry automatically.
    Both flavors previously routed every RPC, mutations included, through the same `_with_retry`;
    mutations now go through a new `_call_once` that converts the gRPC error without retrying.
  - Backoff is now full-jitter (`random.uniform(0, cap)`) instead of plain exponential doubling.
    Without jitter, every client in a fleet retries on the same schedule after a server restart,
    turning the recovery into a thundering herd.

  `tests/test_client_sync.py::test_transient_error_is_retried_then_succeeds` previously exercised
  `write()`/`WriteRelationships` to prove a transient error gets retried; it now exercises
  `read_schema()`/`ReadSchema` instead, since asserting a `WriteRelationships` retry is exactly the
  now-fixed defect. `tests/test_errors.py::test_is_transient_true_for_spicedb_resource_exhausted_error`
  (asserted `is_transient(ResourceExhaustedError(...))` is `True`) is renamed
  `test_is_transient_false_for_spicedb_resource_exhausted_error` and inverted to `False`.
- **2026-08-18**: `Filter._to_proto()` silently dropped `subject_id`/`subject_relation` when
  `subject_type` was not set, instead of raising. The `optional_subject_filter` was only built
  inside `if self.subject_type:`, so `Filter("document", subject_id="alice")` produced a proto
  `RelationshipFilter` with **no subject constraint at all**, while the `Filter` object itself
  still reported `subject_id == "alice"` — a caller reading the object back would see the
  constraint they set; the server would not. `client.delete_relationships(f)` called with that
  filter deleted every relationship on every document, not just alice's — a correct-looking
  user-offboarding call that wipes the whole system. The wire's `SubjectFilter.subject_type` is a
  required field, so there is no way to express a subject ID/relation constraint without it,
  which makes silent widening the one unsafe resolution — `_to_proto()` now raises
  `InvalidArgumentError` naming the field that was set without `subject_type`, per root
  `DESIGN.md` "RULE: A conversion that cannot preserve meaning must fail", clause 1
  (caller-supplied data the client cannot represent MUST raise a typed error). No pre-existing
  test asserted the silent-drop behavior, so none needed replacing.
- `_mapping.check_results` (shared by both `spicedb.sync` and `spicedb.aio`)
  did not verify that `BulkCheckPermissions` returned as many pairs as were
  requested — the result list was built by iterating `resp.pairs`, with
  nothing comparing its length to the number of items sent. The proto
  guarantees pairs are returned in request order but says nothing about
  count, so a response with fewer pairs than items would silently produce a
  list shorter than the input relationships — every `results[i]` after the
  gap misaligned with `relationships[i]`, attributing one resource's answer
  to another. `check_results` now takes the request's item count and raises
  `SpiceDBError` naming both counts (`"check_bulk_permissions returned N
  pair(s) for M request item(s)"`) when they differ, before mapping any
  pair. It also now guards the malformed-oneof case — a
  `CheckBulkPermissionsPair` whose `response` oneof is unset (neither `item`
  nor `error`) — the same way `spicedb-rust` already did, instead of
  silently falling through to `pair.item`'s zero-value default.
- `check_all`/`await check_all` (both `spicedb.sync` and `spicedb.aio`)
  returned `True` for zero relationships — Python's builtin `all()` is
  vacuously `True` over an empty iterable. Root `DESIGN.md`'s "An aggregate
  over zero checks is not a grant" names the hazard directly: a gate like
  `check_all(cs, "edit", *docs_to_rels(docs))` was silently granted whenever
  the derived relationship collection came up empty — a filter that matched
  nothing, an upstream returning `[]`. Both flavors now guard the empty case
  before the aggregate and return `False` without calling
  `BulkCheckPermissions`. `check_any` is unchanged — it was already correctly
  `False` on empty.
- The secure (TLS) path sent the `authorization` header twice on every
  call — once from a gRPC call-credentials layer, once from an interceptor —
  while the insecure path sent it once. A server that logged or rate-limited
  on that header would have seen it duplicated only for TLS connections.
  Neither client now uses a gRPC interceptor or composes call credentials;
  both attach the bearer token as per-call `metadata=` exactly once, on every
  call, on both the secure and insecure paths, for both flavors.
- `is_transient()` checked only `grpc.aio.AioRpcError`, so it always returned
  `False` for errors raised by a sync `grpc.Channel`
  (`grpc._channel._InactiveRpcError`, a `grpc.RpcError` but not an
  `AioRpcError`). A transient failure — a restart, a load balancer hiccup —
  would have been raised straight to the caller by `spicedb.sync.SpiceDBClient`
  with no retry at all. `is_transient()` now checks `grpc.RpcError`, the base
  type both `grpc`'s and `grpc.aio`'s error classes satisfy, so both flavors
  retry the same transient codes.
- `spicedb.aio.SpiceDBClient` opened its gRPC channel in `__init__`, binding
  it to whatever asyncio event loop was running at construction time —
  usually none. A client constructed at import time or module load (a common
  pattern, and the one `spicedb.sync.SpiceDBClient` is designed to support)
  would fail on its first real call, since `asyncio.run()` creates a new loop
  per invocation. The channel now opens lazily on first use and binds to
  whichever loop is running then; reusing the same client from a second event
  loop now raises the new `EventLoopBindingError` with a message that
  explains the constraint, instead of an opaque low-level gRPC failure.
- `read_relationships()`, `lookup_resources()`, `lookup_subjects()`,
  `watch()`, and `export_relationships()` now retry a transient gRPC error
  (as shipped, `UNAVAILABLE`/`ABORTED`; `RESOURCE_EXHAUSTED` was in that set
  when this landed and was removed by the 2026-08-18 retry-safety entry
  below) that occurs while
  ESTABLISHING the stream (or, for the paginated methods, a page), using the
  same exponential backoff as `_with_retry`. Previously these streaming RPCs
  re-raised immediately on any transient error, even when it happened before
  a single item had been produced — a gap DESIGN.md's "automatic retry on
  transient errors" doesn't carve streaming out of. The retry is guarded so
  it only ever fires when zero items have been yielded from the current
  stream/page; a transient error after any item has been yielded is never
  retried, so callers can never see a duplicated/replayed item.
- `Update._from_proto()` no longer raises a bare `KeyError` when the wire
  `RelationshipUpdate.operation` is `OPERATION_UNSPECIFIED` or any other
  unrecognized value — that killed a live `watch()` stream with a
  non-`SpiceDBError`. Unknown/unspecified operations now map to the new
  `UpdateOperation.UNSPECIFIED` enum member instead.
- `SpiceDBClient.check_permissions()` (and `check_permission`/`check_any`/
  `check_all`, which delegate to it) no longer fabricates a generic
  `SpiceDBError` (mapped from a synthetic `INTERNAL` gRPC error) when a
  `CheckBulkPermissions` response pair carries a per-item error. It now maps
  the real `google.rpc.Status` on the pair to the correct typed
  `SpiceDBError` subclass (e.g. `InvalidArgumentError`, `NotFoundError`) via
  the new `error_from_status_proto()` helper in `spicedb.errors`, preserving
  both the real status code and message.
- `Consistency` (the native type introduced for the `Consistency` proto
  removal, see below) is now exported from the package root — `from spicedb
  import Consistency` works for type annotations, matching the constructor
  functions which were already exported.

### Breaking

- **2026-08-18** (behavioral; new keyword-only parameter): per root DESIGN.md, "RULE: Credentials
  over insecure transport require an explicit opt-in" -- `SpiceDBClient(..., insecure=True)` (both
  `spicedb.sync` and `spicedb.aio`) now refuses to construct a client for a non-loopback endpoint
  (loopback means `localhost`, `127.0.0.0/8`, `::1`, or a `unix:` socket target). Previously an
  insecure connection would send its bearer token in cleartext to any host -- neither flavor uses a
  gRPC interceptor or composes call credentials; the token is passed as plain per-call metadata
  regardless of transport, so nothing ever checked where it was going. A new keyword-only
  parameter, `allow_insecure_remote_credentials=True`, opts in explicitly when a caller genuinely
  means to send credentials in cleartext to a remote host; it must be passed alongside
  `insecure=True`, since neither alone is sufficient for a non-loopback endpoint anymore.
  `insecure=True` against `localhost` is unaffected -- no code change needed for local development.
  Raised as `spicedb.errors.InvalidArgumentError`, before any channel is created.

- **2026-08-18** (behavioral; no signature change): the two entries below change what existing,
  unmodified call sites do. They are listed here because neither announces itself -- nothing
  fails to compile, and the difference only shows up under load or against a slow query.
  - **Unary calls are now bounded by a 30-second default** -- see "Call deadlines" in this
    release. A call that legitimately takes longer than 30 s (most plausibly a deep
    `expand_permission_tree` on a large graph, or a filtered delete sweeping many pages) now fails
    with a deadline error where it previously ran to completion. Raise it with `SpiceDBClient(...,
    default_timeout=)`, or pass `timeout=` on the individual call. There is deliberately no way to
    ask for no bound at all on a unary call.
  - **Mutations are no longer retried automatically** -- see "Retry safety" in this release.
    `write`, `delete_relationships`, `write_schema`, `import_relationships`, and the experimental
    counter register/unregister calls (both flavors) now surface a transient `UNAVAILABLE` to the
    caller on the first attempt rather than retrying. This is the correct default (replaying a
    non-idempotent write can report failure for a write that in fact committed), but a caller who
    was relying on the client to ride out a rolling restart must now retry themselves, knowing
    their own idempotency. Reads are unaffected. `RESOURCE_EXHAUSTED` is no longer retried either,
    on reads or mutations.

- **2026-08-18**: Watch resumability. `watch()` (both flavors) now yields a
  `WatchEvent` dataclass instead of a bare `(updates, revision)` tuple, and
  accepts a keyword-only `include_checkpoints: bool = False`. `WatchEvent`
  has no `__iter__`/`__getitem__`, so any existing caller unpacking the old
  tuple shape — `for updates, revision in client.watch():` — now raises
  `TypeError: cannot unpack non-iterable WatchEvent object` instead of
  silently misbehaving.
  - `WatchEvent.changes_through` was already surfaced (as the tuple's second
    element) — it is the proto's `changes_through` ZedToken, "the point in
    time that the watch response is current through... can be used in a
    subsequent WatchRequest to resume watching from this point." Moving it
    onto a named field (rather than a positional tuple slot) makes its
    resumability role explicit at the call site.
  - New: `WatchEvent.is_checkpoint`. Passing `include_checkpoints=True`
    requests `WATCH_KIND_INCLUDE_CHECKPOINTS` from the server (previously no
    caller could request this at all); a checkpoint event carries no
    `updates`, so `is_checkpoint` lets a consumer distinguish "nothing
    changed, here is a fresh resume point" from "here are changes" instead of
    silently treating a checkpoint as an empty update batch. Per the proto:
    recommended when running behind a proxy that aborts idle connections, so
    the stream stays alive during quiet periods.
  - Without a resume token, a consumer whose stream dropped could only
    restart from its original `start_revision` (reprocessing everything
    since, possibly past the GC window) or from head (silently losing every
    change in the gap). `WatchEvent.changes_through` is always populated and
    is safe to pass back as `start_revision`.
  - `examples/watch_changes/` and `examples/sync_watch_changes/` updated for
    the new `WatchEvent` shape and extended with a checkpoint-request
    example.

  Before:
  ```python
  async for updates, revision in client.watch(object_types=["document"]):
      for u in updates:
          ...
  ```
  After:
  ```python
  async for event in client.watch(object_types=["document"], include_checkpoints=True):
      if event.is_checkpoint:
          continue  # nothing changed; event.changes_through is a fresh resume point
      for u in event.updates:
          ...
      resume_token = event.changes_through  # pass as start_revision to resume later
  ```
- `SpiceDBClient.check_permission()` now returns a `CheckResult` instead of a
  bare `bool`, and `SpiceDBClient.check_permissions()` now returns
  `list[CheckResult]` instead of `list[bool]`.
  `CheckPermissionResponse.permissionship` is three-valued
  (`NO_PERMISSION`/`HAS_PERMISSION`/`CONDITIONAL_PERMISSION`), and the old
  bool-returning surface collapsed a `CONDITIONAL_PERMISSION` result — a
  caveated relationship whose context wasn't supplied at check time — to
  `False`, making it indistinguishable from a real denial. New
  `spicedb.CheckResult` (frozen dataclass in `spicedb/types.py`):
  `permissionship: Permissionship`, `missing_context: list[str]`
  (populated when conditional), `checked_at: str` (the check's revision —
  see below), and a `has_permission` property that is `True` **only** for
  `HAS_PERMISSION`. `Permissionship` gained a fourth member,
  `NO_PERMISSION`, appended after `CONDITIONAL_PERMISSION` so the
  pre-existing members keep their values; it appears only on `CheckResult`
  (lookups never yield it). `check_any()`/`check_all()` keep their `bool`
  signatures — they already only counted `HAS_PERMISSION` as granted before
  this change, so their behavior is unaffected; they're now built from
  `CheckResult.has_permission` instead of a raw permissionship comparison.
  `CheckResult.checked_at` exposes `CheckPermissionResponse.checked_at` (a
  ZedToken), previously unreachable through this client's public API at all
  — thread it into `at_least()` to make a later call observe this check
  (read-your-writes for checks; see `examples/read_your_writes/` and
  `examples/caveated_check/`). `CheckResult` also implements `__bool__`,
  mirroring `has_permission` — so `if result:` (the most natural migration
  path from the old bool-returning `check_permission()`) is safe and is
  `False` for a `CONDITIONAL_PERMISSION` result, not unconditionally `True`
  the way a plain object would otherwise be.

  Before:
  ```python
  allowed = await client.check_permission(full(), rel)  # bool
  if allowed:
      ...  # WRONG: True here could mean either a real grant or (on some
           # other clients) a conditional the caller silently treated as one
  ```
  After:
  ```python
  result = await client.check_permission(full(), rel)  # CheckResult
  if result:  # __bool__ mirrors has_permission -- False for CONDITIONAL_PERMISSION
      ...
  elif result.permissionship == Permissionship.CONDITIONAL_PERMISSION:
      print(f"needs context: {result.missing_context}")
  ```
- `spicedb.SpiceDBClient` no longer exists. Import `spicedb.aio.SpiceDBClient`
  (async — same behavior as the old top-level client) or the new
  `spicedb.sync.SpiceDBClient` (synchronous) instead. Everything else —
  `Relationship`, `Filter`, `Transaction`, the consistency constructors, the
  error hierarchy — is unaffected and still imports from `spicedb` directly.

  Before:
  ```python
  from spicedb import SpiceDBClient
  async with SpiceDBClient("localhost:50051", token="t", insecure=True) as client:
      ...
  ```
  After:
  ```python
  from spicedb.aio import SpiceDBClient
  async with SpiceDBClient("localhost:50051", token="t", insecure=True) as client:
      ...
  ```
- `spicedb-gen`'s generated typed client now emits three files —
  `permissions.py`, `sync.py`, and `aio.py` — instead of a single generated
  client module, mirroring the sync/async split above. Regenerate any
  checked-in generated output after upgrading `spicedb-gen`.

- `SpiceDBClient.lookup_resources()` and `SpiceDBClient.lookup_subjects()`
  now yield native `LookupResource`/`LookupSubject` result dataclasses
  instead of bare ID strings, so callers no longer have to blindly trust an
  ID string — they can see whether a match is a full grant or conditional on
  caveat context, and (critically) which subjects are excluded from a
  wildcard `"*"` match. Dropping `excluded_subjects` was a real over-grant
  risk: a caller that saw `"*"` and nothing else had no way to know some
  subjects were carved out of that grant. New types in `spicedb/types.py`:
  `Permissionship`, `PartialCaveatInfo`, `LookupResource`, `ResolvedSubject`,
  `LookupSubject` — mirrors `spicedb-go`'s reference design
  (`spicedb-go/client/lookup_types.go`).

  Before:
  ```python
  async for resource_id in client.lookup_resources("document", "view", ("user:alice", ""), full()):
      print(resource_id)

  async for subject_id in client.lookup_subjects(("document", "doc1"), "view", "user", full()):
      print(subject_id)  # "*" here silently meant "everyone", excluded subjects were dropped
  ```
  After:
  ```python
  async for resource in client.lookup_resources("document", "view", ("user:alice", ""), full()):
      if resource.permissionship != Permissionship.HAS_PERMISSION:
          continue  # conditional match; resource.partial_caveat lists what's missing
      print(resource.resource_id)

  async for subject in client.lookup_subjects(("document", "doc1"), "view", "user", full()):
      if subject.subject.subject_id == "*":
          excluded = {e.subject_id for e in subject.excluded_subjects}  # MUST check before granting to "everyone"
      print(subject.subject.subject_id)
  ```
- `SpiceDBClient.watch()` now yields `tuple[list[Update], str]` instead of
  `tuple[list[core_pb2.RelationshipUpdate], str]`. Added `Update` and
  `UpdateOperation` native types.

  Before:
  ```python
  async for updates, revision in client.watch():
      for u in updates:  # u: core_pb2.RelationshipUpdate (proto)
          print(u.operation, u.relationship)
  ```
  After:
  ```python
  async for updates, revision in client.watch():
      for u in updates:  # u: spicedb.Update (native)
          print(u.operation, u.relationship)  # operation: UpdateOperation
  ```
- `SpiceDBClient.expand_permission_tree()` now returns `tuple[PermissionTree, str]`
  instead of `tuple[core_pb2.PermissionRelationshipTree, str]`. Added native
  `PermissionTree`, `IntermediateNode`, `LeafNode`, `ObjectRef`, `SubjectRef`,
  and `TreeOperation` types (mirrors the shape in `spicedb-go`).

  Before:
  ```python
  tree, revision = await client.expand_permission_tree(("document", "1"), "view", full())
  # tree: core_pb2.PermissionRelationshipTree (proto)
  ```
  After:
  ```python
  tree, revision = await client.expand_permission_tree(("document", "1"), "view", full())
  # tree: spicedb.PermissionTree (native dataclass)
  for subject in tree.leaf.subjects:  # leaf: LeafNode | None
      print(subject.subject_id)
  ```
- `SpiceDBClient.reflect_schema()` now returns a native `ReflectSchemaResult`
  (`definitions: list[SchemaDefinition]`, `caveats: list[SchemaCaveat]`,
  `revision: str`) instead of the raw proto response. Also fixes a
  pre-existing bug where the request built an invalid filter message
  (`ExpSchemaFilter`, which does not exist) instead of
  `ReflectionSchemaFilter`, which made every `reflect_schema()` call raise
  `AttributeError`.

  Before:
  ```python
  resp = await client.reflect_schema(full())
  for d in resp.definitions:  # resp: proto ReflectSchemaResponse
      print(d.name)
  ```
  After:
  ```python
  result = await client.reflect_schema(full())
  for d in result.definitions:  # result: native ReflectSchemaResult
      print(d.name)  # d: SchemaDefinition
  ```
- `Consistency` is now an opaque native frozen dataclass instead of a
  `permission_service_pb2.Consistency` proto alias. `full()`, `min_latency()`,
  `at_least()`, `snapshot()`, `at_least_or_full()`, and
  `at_least_or_min_latency()` all still return `Consistency` and every
  `consistency=...` call site is unaffected — the proto is now hidden behind
  a private `_to_proto()` accessor used internally by `SpiceDBClient`. Code
  that reached into the proto's oneof fields directly (e.g.
  `full().fully_consistent`) will break; construct via the helper functions
  and let the client handle the rest.

  Before:
  ```python
  cs = full()
  if cs.fully_consistent:  # direct proto oneof field access
      ...
  ```
  After:
  ```python
  cs = full()
  # no direct field access; construct via full()/at_least()/etc. and pass
  # `cs` straight to client calls — internals handle the rest
  ```
- `SpiceDBClient.diff_schema()` now returns `list[SchemaDiff]` instead of the
  raw proto response. Added native `ReflectSchemaResult`, `SchemaDefinition`,
  `SchemaRelation`, `SchemaPermission`, `SchemaCaveat`,
  `SchemaCaveatParameter`, and `SchemaDiff` types (mirrors the shape in
  `spicedb-go/client/schema.go`).

  Before:
  ```python
  resp = await client.diff_schema(full(), new_schema)
  for d in resp.diffs:  # resp: proto DiffSchemaResponse
      print(d.definition_added.name)
  ```
  After:
  ```python
  diffs = await client.diff_schema(full(), new_schema)
  for d in diffs:  # diffs: list[SchemaDiff] (native)
      print(d.kind, d.definition_name)
  ```

## 0.1.0 (2026-03-16)

Initial release of the idiomatic Python SpiceDB client.
