# spicedb-ruby — Idiomatic Ruby Client Design

## Inherits

Root DESIGN.md (`../DESIGN.md`) — read it for the overall vision and backwards
compatibility mandate. This document takes precedence for Ruby-specific decisions.

## Language-Specific Goals

### Philosophy: Pit of Success

The API should make the correct thing easy and the wrong thing hard. Users
should fall into correct usage patterns by default. The API should feel like
hand-written, idiomatic Ruby — not a thin wrapper around gRPC.

### Gem & Module

- **Gem name**: `spicedb`
- **Top-level module**: `SpiceDB`
- **Minimum Ruby**: 3.2 (for `Data.define`)

### Module Structure

Six modules under `SpiceDB`:

- **`SpiceDB::Client`** — the client class and all SpiceDB operations
- **`SpiceDB::Consistency`** — module with factory methods for consistency modes
- **`SpiceDB::Relationship`** — `Data.define` value type for relationships
- **`SpiceDB::Filter`** — `Data.define` value type for relationship filters
- **`SpiceDB::Transaction`** — builder for batching relationship writes
- **`SpiceDB::Errors`** — exception hierarchy and gRPC error mapping

Types (`Relationship`, `Filter`, `Consistency`) are independent of `Client`.
Users can construct relationships and filters without requiring the client.

### Constructors

Security-obvious named constructors:

- `SpiceDB::Client.new_plaintext(endpoint, token)` — for testing, makes
  insecure connection obvious
- `SpiceDB::Client.new_system_tls(endpoint, token)` — for production
- `SpiceDB::Client.new_custom_tls(endpoint, token, ca_cert:, client_cert:,
  client_key:)` — for a SpiceDB fronted by a private CA, and for mutual TLS
- Block form: `SpiceDB::Client.new_plaintext(...) { |client| ... }` — yields
  client and ensures cleanup

All three live in `SpiceDB::Connecting`, which `Client` **extends**. That is
purely an extraction — `SpiceDB::Client.new_plaintext(...)` and friends are
unchanged for callers — done for the same reason `SpiceDB::CaveatContext`,
`SpiceDB::Retrying` and `SpiceDB::WatchMapping` were: `Client`'s
`Metrics/ClassLength` ceiling exists to stop that class growing without bound,
and the answer to hitting it is to move a coherent concern out, never to raise
the ceiling.

Per root DESIGN.md, "RULE: Credentials over insecure transport require an
explicit opt-in": `new_plaintext` only permits plaintext to a loopback
endpoint (`localhost`, `127.0.0.0/8`, `::1`, or a `unix:` socket target) — the
local-development case that is the entire reason it exists. Anything else
needs `allow_insecure_remote_credentials: true` passed explicitly, or
`new_plaintext` refuses to construct the client at all, before any connection
is created.

#### Custom TLS trust material

```ruby
# A SpiceDB behind a private or corporate CA
SpiceDB::Client.new_custom_tls('spicedb.internal:443', token,
                               ca_cert: File.read('/etc/ssl/certs/internal-ca.pem')) do |client|
  ...
end

# ...and where the server requires mutual TLS
SpiceDB::Client.new_custom_tls('spicedb.internal:443', token,
                               ca_cert: ca_pem, client_cert: cert_pem, client_key: key_pem)
```

All three are PEM **strings**, not paths: certificates commonly arrive from a
mounted secret or a config store rather than the local filesystem, and reading
a file is the caller's one-liner either way.

Why this constructor exists. Root DESIGN.md, "RULE: A system-TLS constructor
must reach a real server", requires `new_system_tls` to delegate to the
ecosystem's default trust source — for grpc-ruby, `GRPC::Core::ChannelCredentials.new`
with no arguments — and names the hazard that leaves visible: gRPC's C-core
compiles in its own `roots.pem`, so a CA an operator installed in the host's
trust store is **not** honoured by `new_system_tls`. That rule permits
delegating to the bundled set precisely *because* a caller can supply their own
material instead; `new_custom_tls` is what makes that true.

`ca_cert` **replaces** gRPC's built-in roots for that client rather than adding
to them (the C-core's own behavior, and generally what a deployment pinning a
private CA wants). The material reaches the C-core as
`GRPC::Core::ChannelCredentials.new(ca_cert, client_key, client_cert)` — note
the key comes *before* the certificate chain there, the reverse of how a caller
names them. On the `new_system_tls` path all three are `nil`, which is the same
call the zero-argument form makes, so that path is still pure delegation: this
library never selects a root set of its own, which clause 1 of that rule
prohibits.

Three refusals, all raised before any channel or credential is created:

- **All three arguments nil.** That is `new_system_tls`, and a constructor named
  for custom trust material that silently used the compiled-in roots instead
  would be a quiet way to believe a private CA was configured when it was not.
- **Trust material on the plaintext path.** There is deliberately no plaintext
  constructor that accepts it — `new_plaintext` takes none — and the private
  `Client.new` refuses `insecure: true` combined with any of the three rather
  than discarding it. A plaintext channel performs no handshake, so the material
  would be ignored and the bearer token would go out in cleartext behind a call
  site that reads as though TLS were configured: the failure root DESIGN.md,
  "RULE: Credentials over insecure transport require an explicit opt-in", exists
  to prevent. Supplying trust material is never a second, quieter route to an
  insecure transport, and never a construction path that skips that rule's
  guard — which still runs first, and whose message is what a caller sees. It
  raises rather than silently turning TLS on, since an implicit upgrade is just
  as surprising.
- **`client_cert` without `client_key`, or vice versa.** Neither half is usable
  alone; the C-core rejects the pair later, from a layer with no idea which
  argument was wrong.

### Consistency

ZedTokens are opaque `String` values, never proto types. Consistency is an
**explicit required parameter** on every read operation — never silently
defaulted.

Module methods in `SpiceDB::Consistency`:
- `full` — fully consistent, least performant
- `min_latency` — SpiceDB's preferred revision, optimal performance
- `at_least(revision)` — read-after-write
- `snapshot(revision)` — exact revision
- `at_least_or_full(revision)` — AtLeast if revision present, Full otherwise
- `at_least_or_min_latency(revision)` — AtLeast if revision present, MinLatency otherwise

All write operations return a revision string.

### Relationships

Immutable `Data.define` value type:

```ruby
SpiceDB::Relationship = Data.define(
  :resource_type, :resource_id, :resource_relation,
  :subject_type, :subject_id, :subject_relation,
  :caveat_name, :caveat_context, :expiration,
  :check_context
)
```

Constructors:
- `SpiceDB::Relationship.new(...)` — standard Data.define constructor
- `SpiceDB::Relationship.from_triple(...)` — from resource/subject triples
- `SpiceDB::Relationship.from_tuple(string)` — parse tuple string format

Immutable modifiers (return new instances):
- `r.with_caveat(name, context)` — returns new relationship with caveat
- `r.with_expiration(time)` — returns new relationship with expiration
- `r.with_check_context(context)` — returns new relationship with check-time
  caveat context (see Checks below) — distinct from `with_caveat`'s
  write-time context
- `r.to_filter` — returns a Filter matching this relationship's resource

`check_context` is check-time-only caveat context for the check surface — a
**different concept** from `caveat_context`, which is write-time context
embedded in `optional_caveat` and persisted to SpiceDB. `check_context` has
no wire representation on the write path at all; it is read exclusively by
`check_permission`/`check_permissions` (see Checks below). Conflating the
two would leak check-time-only context into a write, silently altering a
stored relationship's evaluated caveat forever — they are kept independently
settable (`with_caveat` and `with_check_context` never touch each other's
field) for exactly that reason.

**Caveat context types.** Caveat context crosses the wire as
`google.protobuf.Struct`, whose values are a `kind` oneof. Conversion dispatches on
that oneof in both directions. `relationship_from_proto` dispatches on `kind`, so
context already stored in SpiceDB reads back with its types intact.
`relationship_to_proto` dispatches too, via `SpiceDB::CaveatContext#caveat_context_to_struct`
— the write path used to stringify every value (`Value.new(string_value: v.to_s)`), a
defect it shared with the Go, C# and Java clients before the cross-client write-path fix.
`with_caveat("x", { "n" => 42 })` now round-trips `"n"` as the `Float` `42.0` (see below
for why a `Float`, not the original `Integer`), not the String `"42"`.

The check-time and write-time codecs — `check_context_to_struct`/`check_context_value`
(check surface) and `caveat_context_to_struct`/`caveat_context_value_from_proto`/
`struct_to_caveat_context` (write surface) — live in `SpiceDB::CaveatContext`
(`lib/spicedb/caveat_context.rb`), a module `Client` includes rather than a set of
private methods defined directly on it. Both write-side entry points
(`check_context_to_struct` for check-time, `caveat_context_to_struct` for write-time)
dispatch every value through the same `check_context_value`, so a value that cannot be
represented (any Ruby type outside `nil`/`true`/`false`/`Numeric`/`String`/`Hash`/`Array`)
raises `SpiceDB::InvalidArgumentError` naming the offending key on either surface, rather
than being silently discarded or stringified — the value came from the caller, who can
see the error and fix their input.

Reading a non-string value via `Value#string_value` returns `""`, silently destroying
stored context rather than raising — which is why the `kind` dispatch is required for
correctness and not merely tidiness. `Struct#fields` is a `Google::Protobuf::Map`,
**not** a `Hash` — Hash-only methods such as `transform_values` raise `NoMethodError`
on it directly. `Map#to_h` is not a safe
substitute either: for message-valued maps it recursively converts each `Value` via the
generic protobuf-to-hash conversion rather than leaving it as a `Value` to dispatch on.
`Map` includes `Enumerable`, so conversion iterates its raw entries directly (e.g. via
`each_with_object`) instead of going through `Hash` or `to_h` at all.

A numeric caveat context value reads back as a `Float` (`42` becomes `42.0`), because
`google.protobuf.Value.number_value` is a `double`. This applies to any value that
reached the wire *as a number* — written by another client, by `zed`, or by this client's
own `with_caveat`/`check_context`, both of which send a Ruby `Integer` through as
`number_value`. The `Float` widening is inherent to the proto and consistent across all
seven clients; it is not worked around.

### Checks

All checks use `BulkCheckPermissions` under the hood:
- `check_permission(consistency, permission, relationship, context: nil)` → `CheckResult`
- `check_permissions(consistency, permission, *relationships, context: nil)` → `Array<CheckResult>`
- `check_any(consistency, permission, *relationships, context: nil)` → `Boolean`
- `check_all(consistency, permission, *relationships, context: nil)` → `Boolean`

`check_permission`/`check_permissions` return `SpiceDB::CheckResult`, not a
bare `Boolean` — `CheckPermissionResponse#permissionship` is four-valued
(`UNSPECIFIED`/`NO_PERMISSION`/`HAS_PERMISSION`/`CONDITIONAL_PERMISSION`), and
a caveated relationship whose context wasn't supplied at check time comes
back `CONDITIONAL_PERMISSION` — the server saying "I need more information,"
which collapsing to a Boolean would silently turn into either a grant or a
denial.

```ruby
SpiceDB::CheckResult = Data.define(:permissionship, :missing_context, :checked_at) do
  def has_permission?
    permissionship == :has_permission
  end
end
```

- `permissionship` — a Symbol: `:unspecified`, `:no_permission`,
  `:has_permission`, or `:conditional_permission` (the same native symbol
  set the lookup surfaces use, below — checks are simply the one surface
  that can affirmatively produce `:no_permission`)
- `missing_context` — `Array<String>` of caveat parameter names SpiceDB
  couldn't evaluate because context wasn't supplied; `[]` unless
  `permissionship` is `:conditional_permission`
- `checked_at` — the ZedToken (`String`) the check was evaluated against
- `has_permission?` — `true` ONLY when `permissionship` is
  `:has_permission`

**Callers MUST call `result.has_permission?` — never test the result
itself.** Ruby has no `__bool__`-style hook: every `CheckResult` is truthy in
a bare `if result` regardless of `permissionship`, so `if result` is
unconditionally true even for a `:conditional_permission` result. This is
the one mitigation available in Ruby for the truthiness hazard that a
Boolean-returning check API creates when it's replaced by an object — unlike
Python (`__bool__`), Ruby cannot make the object itself refuse to be truthy.

`check_any`/`check_all` remain plain `Boolean` — they count ONLY
`:has_permission` results. A `:conditional_permission` result does NOT
count as a grant for either (deliberately fail-closed): `check_any` is
`results.any?(&:has_permission?)`, and `check_all` is
`results.all?(&:has_permission?)` **guarded by an explicit
`return false if relationships.empty?`**.

That guard is not redundant. `Array#all?` is vacuously `true` on an empty
array, so a bare `results.all?(&:has_permission?)` granted whenever the
relationship list came up empty — a filter that matched nothing, an upstream
returning `nil`, a defensive empty array — with no server round trip and no
way for the caller to tell a real grant from an empty one. `check_any` needs
no such guard: `Array#any?` is already `false` on empty. Root `DESIGN.md`,
"RULE: Only an unconditional grant is true": an aggregate over zero checks is
not a grant.

#### Supplying caveat context

`:conditional_permission` alone is not actionable — the caller also needs a
way to supply the caveat context named in `missing_context` and get back an
actual grant/denial. All four check methods accept an optional `context:`
keyword, and `SpiceDB::Relationship` carries a matching `check_context`
field, so a caller can supply context two ways:

- **Call-level** — `context:` on `check_permission`/`check_permissions`/
  `check_any`/`check_all` is a default applied to every relationship in the
  call.
- **Per-item** — `relationship.with_check_context({...})` (or
  `Relationship.new(..., check_context: {...})`) overrides `context:` for
  that one relationship's check.

```ruby
# Call-level: applies to every relationship checked in this call.
client.check_permissions(consistency, "view", rel1, rel2, context: { now: 42 })

# Per-item: overrides the call-level default for just this relationship.
rel = SpiceDB::Relationship.from_triple("doc", "1", "viewer", "user", "alice")
                            .with_check_context({ now: 42 })
client.check_permission(consistency, "view", rel)
```

All checks go through `BulkCheckPermissions` under the hood, whose wire
format (`CheckBulkPermissionsRequestItem#context`, proto field 4) attaches
context **per item** — `CheckBulkPermissionsRequest` itself has no context
field. So `context:` is fanned out onto every item at request-build time,
and each item's own `check_context` (if any) is then merged on top:

**Merge rule: key-level, item wins.** `call_level.merge(item_level)` — an
item's own keys override the call-level default on conflict, but call-level
keys the item doesn't mention are **retained**, not dropped. An item with no
`check_context` inherits `context:` unchanged. This is deliberately NOT a
wholesale replacement: if a single per-item key silently discarded every
other call-level key, a caveat would fail for context the caller believed it
had already supplied, landing right back in the confusing
`CONDITIONAL_PERMISSION` state this feature exists to make legible.

```
call-level:  { now: 42, region: "us" }
item-level:  { region: "eu" }
sent for that item: { now: 42, region: "eu" }
```

If neither call-level nor per-item context is supplied for an item, no
`context` field is set on the wire at all (nil, not an empty `Struct`).

This is purely additive — `context:` defaults to `nil` on every check
method and `check_context` defaults to `nil` on `Relationship`, so no
existing call site changes.

### Lookups

`lookup_resources`/`lookup_subjects` yield native result objects — never
bare ID strings — so callers can't accidentally treat a caveated or
wildcard-excluded result as a full grant. Mirrors spicedb-go's
`client/lookup_types.go`.

```ruby
SpiceDB::PartialCaveatInfo = Data.define(:missing_required_context)
SpiceDB::LookupResource    = Data.define(:resource_id, :permissionship, :partial_caveat, :looked_up_at)
SpiceDB::ResolvedSubject   = Data.define(:subject_id, :permissionship, :partial_caveat)
SpiceDB::LookupSubject     = Data.define(:subject, :excluded_subjects, :looked_up_at)
```

`permissionship` is a Symbol: `:unspecified`, `:has_permission`, or
`:conditional_permission` (lookups never produce `:no_permission` — see
Checks above for the fourth value). `partial_caveat` is `nil` unless
`permissionship` is `:conditional_permission`, in which case it carries the
`missing_required_context` that must be supplied to fully evaluate the
grant. `looked_up_at` is the ZedToken (`String`) the lookup was evaluated
against — pass it to a later `Consistency.at_least` for read-your-writes
against this result.

- `lookup_resources(...)` → `Enumerator<SpiceDB::LookupResource>`
- `lookup_subjects(...)` → `Enumerator<SpiceDB::LookupSubject>`, where
  `LookupSubject#subject` is a `ResolvedSubject` and
  `LookupSubject#excluded_subjects` is an `Array<ResolvedSubject>`

Callers MUST check `permissionship` before treating a `LookupResource` as a
full grant. For `lookup_subjects`, when `subject.subject_id` is the
wildcard `"*"`, callers MUST check `excluded_subjects` before treating the
wildcard as a blanket grant — the server grants the permission to every
subject of the requested type EXCEPT those listed there.

### Streaming & Transparent Cursor Pagination

Ruby `Enumerator` for all streaming RPCs. **Cursors are fully internal** —
the caller sees a single Enumerator, and the client transparently re-fetches
pages using the cursor from each response. Default page sizes use sensible
defaults:

| Method | Default page size | Notes |
|--------|------------------|-------|
| `read_relationships` | 512 | cursor-based auto-pagination |
| `lookup_resources` | 512 | cursor-based auto-pagination |
| `lookup_subjects` | — | single streaming call |
| `export_relationships` | 512 | cursor-based auto-pagination |
| `delete_relationships` | 1,000 | auto-repeats until all deleted; matches SpiceDB's default `--max-delete-relationships-limit` |
| `import_relationships` | 1,000 | batches into streaming sends |
| `updates` | — | server-streaming, no pagination |

Enumerators support `.lazy` for memory-efficient processing of large result sets.

#### Stream lifecycle: abandoning an Enumerator releases it

Root `DESIGN.md`, "RULE: Abandoning a stream must release it", requires that
stopping early actually tells the server to stop. This client satisfies it
with no explicit cancel anywhere, because Ruby's iteration protocol and
grpc-ruby together already do it — clause 3's "where a language's iteration
protocol closes a generator by calling `return()` on `break`, that mechanism
only releases the stream if the transport underneath honors it". grpc-ruby
honors it; Connect-ES, the counterexample the rule cites, does not.

The chain, for `for`/`each`/`first`/`take` alike:

1. None of these ever create a Fiber. Internal iteration runs the
   generator block on the caller's own fiber — `Fiber.current` measured
   inside the block is `Fiber.current` measured outside it — so `break`
   (or `first`'s early stop) unwinding out of `Enumerator#each` is an
   ordinary unwind on a single call stack, not a jump across a fiber
   boundary. (External iteration via `#next` is the one case that *does*
   run the block on a separate Fiber — see the gap noted below.)
2. That unwind reaches `GRPC::ActiveCall#each_remote_read_then_finish`,
   whose `ensure` calls `set_input_stream_done`.
3. That reaches `maybe_finish_and_close_call_locked`, which calls
   `@call.close` — tearing down the core call synchronously, on the calling
   thread, not on a GC pass.

`spec/client_stream_release_spec.rb` pins all of this against a real
`GRPC::RpcServer` whose handlers stream until the send itself fails. It
asserts the *server* saw the stream end, because a spec that only checked
the consuming loop exited would pass identically with or without a leak.
That spec exists because the absence of a `cancel` call reads, from the
outside, exactly like the defect the rule describes — a grep for "cancel"
under `lib/` finds only `SpiceDB::CancelledError`.

`read_relationships` and `lookup_resources` are a slightly different case:
each drains a whole page into an Array before yielding anything, so by the
time the caller sees result one, that page's stream is already finished and
there is nothing to strand. What they must not do is keep *fetching* — an
abandoned auto-pager that opens the next page is its own leak — and the same
spec covers that by handing back exactly a full page and asserting no second
call arrives.

**Known gap: external iteration via `#next` leaks permanently.** Every
method here returns a public `Enumerator`, and nothing stops a caller from
driving one with `.next` instead of `each`/`for`. `Enumerator#next` is
implemented with a real Fiber, unlike the internal-iteration chain above —
each call resumes it up to the next `yielder <<`. Abandoning that (calling
`.next` a few times, then dropping the last reference without exhausting or
explicitly closing the Enumerator) does not release the stream: measured
directly, ten full `GC.start(full_mark: true, immediate_sweep: true)` passes
after dropping the reference, and `each_remote_read_then_finish`'s `ensure`
still had not run. Ruby does not run `ensure` for a garbage-collected Fiber.
This is not "eventually, on some later GC pass" the way the sync-Python
finalizer race is — it does not happen at all, on any pass. A caller who
starts external iteration and walks away leaks the gRPC call and the
server-side dispatch for the life of the connection.

A cheap guard, if this is ever worth closing: `ObjectSpace.define_finalizer`
on the returned Enumerator, closing over the raw `GRPC::ActiveCall` (never
`self`, or the Enumerator can't be collected) and calling `@call.cancel` in
the finalizer proc. Finalizer procs are a GC-level hook, not a Fiber
`ensure`, so they run on collection regardless of which Fiber the object's
work happened on — that's why this would close the gap the internal-
iteration chain above cannot. Not implemented; recorded here as the shape a
fix would take, not a recommendation to add one without weighing the same
kind of transport risk raised below.

**Do not add an explicit cancel here without reading this.** The only way to
get a cancellable handle out of grpc-ruby is `return_op: true`, and that path
freezes the call's metadata (`merge_metadata_to_send`) *before* the
interceptor chain runs. On an insecure connection, where this client carries
its bearer token in a `GRPC::ClientInterceptor`, every stream opened that way
goes out unauthenticated — and every double-based spec in the suite stays
green while it happens, because a double has no transport to notice. Adding
cancellation would mean plumbing the token explicitly out of
`SpiceDBProto::Client` alongside it: real risk, taken on to fix something
that measurably is not broken.

### Writes

Transaction builder pattern:

```ruby
txn = SpiceDB::Transaction.new
txn.create(relationship)
txn.touch(relationship)
txn.delete(relationship)
txn.must_not_match(filter)  # precondition
txn.must_match(filter)      # precondition
revision = client.write(txn)
```

### Deletions

`delete_relationships` automatically pages through large result sets using a
limit of 1,000 per RPC call (override with `limit:`; matches SpiceDB's
default `--max-delete-relationships-limit`, so the default works against a
stock server). It repeats until the server reports all matching
relationships are deleted. Returns the final revision.

Optional `must_match:`/`must_not_match:` keyword args add preconditions that
guard the delete, mirroring `Transaction#must_match`/`#must_not_match`:

```ruby
client.delete_relationships(filter, must_match: [guard_filter])
client.delete_relationships(filter, must_not_match: [guard_filter], limit: 500)
```

Preconditions are a per-request proto field, so a delete spanning multiple
auto-paged calls re-evaluates them on every page rather than checking once
for the whole operation — pair a precondition with a `limit:` large enough
to cover every match in one call for all-or-nothing semantics.

### Error Handling

Exception hierarchy under `SpiceDB::Error`:
- `SpiceDB::PermissionDeniedError`
- `SpiceDB::NotFoundError`
- `SpiceDB::AlreadyExistsError`
- `SpiceDB::InvalidArgumentError`
- `SpiceDB::FailedPreconditionError`
- `SpiceDB::UnavailableError`
- `SpiceDB::CancelledError`
- `SpiceDB::DeadlineExceededError`
- `SpiceDB::ResourceExhaustedError`

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
`t × (retries + 1)` plus backoff, and an auto-paging call spends a fresh `t`
per page. Root `DESIGN.md`, "On worst-case latency", covers why this differs
from Go's; a caller needing a true end-to-end bound must impose it above this
client.

#### Retrying a stream: establishment only

A server stream cannot be resumed, so retrying one means re-running the
block that feeds the caller's `Enumerator` — `yielder <<` side effects and
all. That is safe in exactly one window: before anything has been produced.
`SpiceDB::Retrying#with_establishment_retry` is that window, and every
streaming method that retries goes through it:

```ruby
count = with_establishment_retry do |progress|
  call_export_relationships(...) do |rel, new_cursor|
    yielder << rel
    progress.call        # <- what tells the guard something was delivered
    cursor = new_cursor
  end
end
```

`should_retry_establishment?` decides only whether the error is transient
and whether budget remains; the zero-produced guard lives in
`with_establishment_retry`, which is why a block that forgets `progress.call`
turns a duplicate-suppressing guard into a duplicate-producing one.

`lookup_subjects` is the method this shape most needs stating for, because
it has been wrong in both directions. It once wrapped the whole call in
`with_retry`, so a mid-stream failure replayed results the caller had
already seen — `LookupSubjects`' cursor is marked unimplemented in the
proto, so unlike `lookup_resources`/`export_relationships` there is no
resume point at all. The correction removed retry entirely, which
over-corrected: a transient failure while *opening* the stream has delivered
nothing to replay, and dropping retry there made this the only client of the
seven that failed such a caller outright. The zero-produced guard covers
both — retry the open, never the middle.

`updates` is the deliberate exception: it retries neither, since a watch
stream's whole purpose is to run indefinitely and a caller that wants one
re-established should decide that itself, with a resume token
(`WatchEvent#changes_through`) the client has already handed it.

### Deadlines

Every unary method takes a `timeout:` keyword (seconds), passed straight
through as grpc-ruby's `deadline:` call option (an absolute `Time`, computed
fresh — `Time.now + timeout` — on each individual RPC attempt, so a retried
call gets a full new window per attempt rather than a shrinking one).
`Client.new`/`.new_plaintext`/`.new_system_tls` all take a `default_timeout:`
(seconds, default 30) applied whenever a call omits its own `timeout:` —
mirroring `authzed-node`'s `DEFAULT_DEADLINE_MS = 30_000` (its comment cites
`grpc/grpc-node#541`). See root DESIGN.md, "RULE: A unary call must have a
deadline" — without a finite default, a SpiceDB instance that accepts a
connection but never answers hangs every caller that didn't opt in to a
timeout forever, since the connection looks fine at the transport level and
nothing is ever produced to retry.

```ruby
client = SpiceDB::Client.new_plaintext('localhost:50051', 'token', default_timeout: 5)
result = client.check_permission(cs, 'view', rel)             # bound by the 5s default
result = client.check_permission(cs, 'view', rel, timeout: 1) # overrides it for this call
```

Server-streaming calls (`read_relationships`, `lookup_resources`,
`lookup_subjects`, `updates`, `export_relationships`) do NOT take a
`timeout:` and are NOT bound by `default_timeout` — they are long-lived by
design (`updates` may run for the life of the process), and applying the
unary default to them would make the stream itself the outage.

`import_relationships` (`import_bulk_relationships`) is client-streaming, not
server-streaming, but the same exclusion applies for the mirror-image reason:
its duration scales with the size of the caller's dataset, not with server
latency, so no fixed default is correct for it either. Unlike the
server-streaming calls above, it DOES still take a `timeout:` — `nil` (the
default) means unbounded there, not "use `default_timeout`"; pass an
explicit `timeout:` to bound a bulk import.

Note for callers reasoning about worst-case latency: `timeout:` is a
per-*attempt* budget, applied fresh on each retry, so a call that retries can
take up to `timeout × (retries + 1)` plus backoff, and an auto-paging call
(e.g. `delete_relationships`) applies the same `timeout:` fresh to each page.

### Complete Method List

`timeout:` (seconds, default `nil` meaning "use the client's `default_timeout`")
appears on every unary method below — see "Deadlines" above. It is omitted
from `import_relationships` and `export_relationships`/`read_relationships`/
`lookup_resources`/`lookup_subjects`/`updates` in the sense that those calls
are NOT bound by `default_timeout`; `import_relationships` still accepts
`timeout:` (default `nil` there means unbounded, not "use the default").

**Checks:**
- `check_permission(consistency, permission, relationship, context: nil, timeout: nil)` → `CheckResult`
- `check_permissions(consistency, permission, *relationships, context: nil, timeout: nil)` → `Array<CheckResult>`
- `check_any(consistency, permission, *relationships, context: nil, timeout: nil)` → `Boolean`
- `check_all(consistency, permission, *relationships, context: nil, timeout: nil)` → `Boolean`

**Relationships:**
- `write(transaction, timeout: nil)` → `String` (revision)
- `read_relationships(consistency, filter)` → `Enumerator<Relationship>`
- `delete_relationships(filter, must_match: [], must_not_match: [], limit: nil, timeout: nil)` → `String` (revision)

**Lookups:**
- `lookup_resources(consistency, resource_type, permission, subject_type, subject_id)` → `Enumerator<LookupResource>`
- `lookup_subjects(consistency, resource_type, resource_id, permission, subject_type)` → `Enumerator<LookupSubject>`

**Schema:**
- `read_schema(timeout: nil)` → `[String, String]` (schema, revision)
- `write_schema(schema, timeout: nil)` → `String` (revision)
- `reflect_schema(consistency, timeout: nil)` → `ReflectSchemaResult`
- `computable_permissions(consistency, definition_name, relation_name, timeout: nil)` → `[Array<RelationReference>, String]`
- `dependent_relations(consistency, definition_name, permission_name, timeout: nil)` → `[Array<RelationReference>, String]`
- `diff_schema(consistency, comparison_schema, timeout: nil)` → `[Array<SchemaDiff>, String]`

**Expand:**
- `expand_permission_tree(consistency, resource_type, resource_id, permission, timeout: nil)` → `ExpandResult`

**Bulk:**
- `import_relationships(enum, timeout: nil)` → `Integer` (num_loaded) — client-streaming;
  NOT bound by `default_timeout` (`timeout: nil` here means unbounded, not "use the default")
- `export_relationships(consistency, filter = nil)` → `Enumerator<Relationship>`

**Watch:**
- `updates(object_types, start_revision: nil, include_checkpoints: false)` → `Enumerator<WatchEvent>`
  — `include_checkpoints` requests `WATCH_KIND_INCLUDE_CHECKPOINTS` (recommended behind a
  proxy that aborts idle connections, since a checkpoint keeps the stream alive with no
  changes to report)

`WatchEvent = Data.define(:updates, :changes_through, :is_checkpoint)` is one event per
`WatchResponse`. `changes_through` is always populated -- proto: "This token can be used in a
subsequent WatchRequest to resume watching from this point" -- pass it as `start_revision` to
resume after a dropped stream instead of restarting from the original `start_revision`
(reprocessing, possibly past the GC window) or from head (silently losing every change in the
gap). `is_checkpoint` is true for a checkpoint event, which carries no `updates`.

**Experimental:**
- `experimental_register_relationship_counter(name, filter, timeout: nil)` → `nil`
- `experimental_count_relationships(name, timeout: nil)` → `CountResult`
- `experimental_unregister_relationship_counter(name, timeout: nil)` → `nil`

### Escape Hatches

`client.proto_client` returns the underlying `SpiceDBProto::Client` — the
`permissions`/`schema`/`watch`/`experimental` stubs this gem makes its own calls through:

```ruby
response = client.proto_client.permissions.check_permission(request)
```

Clearly-marked **secondary** API, which is what root DESIGN.md's "What NOT To Do" permits:
channels, stubs and metadata stay out of the primary surface, and "escape hatches for
advanced use are acceptable as clearly marked secondary API". It exists so a request the
idiomatic surface cannot express — an RPC or proto field not wrapped here, such as
`WriteRelationshipsRequest#optional_transaction_metadata`, or the single-check
`CheckPermission` RPC that `#check_permission` deliberately routes around — has a
workaround short of forking the gem.

Four properties, all deliberate:

- **The bearer token comes free.** The stubs carry it already (composed call credentials on
  the secure path, a `BearerTokenInterceptor` on the plaintext one), so a raw call is
  authenticated exactly as an idiomatic one is.
- **A raw call is a raw call.** No `SpiceDB::Error` mapping (you rescue `GRPC::BadStatus`),
  no retry, and no `default_timeout` — pass `deadline:` yourself.
- **The connection belongs to the client.** `#close` releases it; closing it from here
  breaks every later call.
- **It is an accessor, never a constructor.** An `attr_reader` takes no arguments, so
  channel construction stays on the single guarded path in the constructor and this cannot
  become a way around root DESIGN.md, "RULE: Credentials over insecure transport require an
  explicit opt-in". `spec/client_raw_escape_hatch_spec.rb` pins that (arity zero) alongside
  a real-server test of the hatch itself.

No stability promise beyond grpc-ruby's and the generated code's.

## Public API Surface

See module sections above for the complete API manifest.

## Examples Manifest

| Directory | Demonstrates |
|-----------|-------------|
| `check_permission/` | Basic permission check with `check_permission` |
| `write_relationships/` | Writing relationships with the transaction builder |
| `delete_relationships/` | Deleting relationships, including guarded deletes with `must_match:`/`must_not_match:` and `limit:` |
| `read_relationships/` | Reading relationships with an enumerator |
| `lookup_resources/` | Finding resources a subject can access |
| `lookup_subjects/` | Finding subjects with access to a resource |
| `watch_changes/` | Watching for relationship changes with a bounded consumer: subscribe from a known revision, write, consume until that exact update arrives, then `break` |
| `schema_management/` | Schema read/write operations |
| `bulk_operations/` | Bulk checks, `check_all`, `check_any`, and import |
| `call_deadlines/` | Constructing a client with `default_timeout:`, a per-call `timeout:` override, confirming bulk import isn't bounded by the unary default, and proving both deadlines bite against a listener that accepts the connection and never answers |
| `schema_reflection/` | Schema reflection, computable permissions, diffs |
| `relationship_counters/` | Registering and reading relationship counters, polling to a terminal state and asserting an exact count |
| `expand_permission_tree/` | Expanding a permission into its native `PermissionTree` (intermediate/leaf nodes, subjects) |
| `raw_escape_hatch/` | `#proto_client` — driving the generated stub directly for a proto field (`optional_transaction_metadata`) and an RPC (`CheckPermission`) the idiomatic API does not expose |
| `custom_tls/` | Reaching a SpiceDB behind a private CA with `new_custom_tls(ca_cert:)`, and mutual TLS with `client_cert:`/`client_key:`. Brings up its own TLS-terminated endpoint — the only example tagged `:no_spicedb` |

## Changelog

See [CHANGELOG.md](CHANGELOG.md) for release notes.
