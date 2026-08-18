# spicedb-go — Idiomatic Go Client Design

## Inherits

Root DESIGN.md (`../DESIGN.md`) — read it for the overall vision and backwards
compatibility mandate. This document takes precedence for Go-specific decisions.

## Language-Specific Goals

### Philosophy: Pit of Success

The API should make the correct thing easy and the wrong thing hard. Users
should fall into correct usage patterns by default.

### Package Structure

Four packages:

- **`spicedb`** (root) — doc-only package, godoc entry point
- **`client`** — the `Client` struct and all SpiceDB operations
- **`consistency`** — strategy constructors for consistency modes
- **`rel`** — relationship types, filters, transactions, parsing

Types (`rel`, `consistency`) are independent of `client`. Users can construct
relationships and filters without importing the client.

### Constructors

Security-obvious named constructors:

- `client.NewPlaintext(endpoint, presharedKey string) (*Client, error)` — for
  testing, makes insecure connection obvious
- `client.NewSystemTLS(endpoint, presharedKey string) (*Client, error)` — for
  production
- `client.NewWithOpts(endpoint string, opts ...Option) (*Client, error)` —
  escape hatch with functional options

Per root DESIGN.md, "RULE: Credentials over insecure transport require an
explicit opt-in": `WithInsecure()` (and therefore `NewPlaintext`) only permits
plaintext to a loopback endpoint (`localhost`, `127.0.0.0/8`, `::1`, or a
`unix:` socket target) — the local-development case that is the entire reason
`WithInsecure` exists. Anything else needs the separately-named
`WithInsecureAllowRemoteHost()` option passed alongside `WithInsecure()`, or
`NewWithOpts` refuses to construct the client at all, before any connection
is created.

### Consistency

ZedTokens are opaque `string` values, never proto types. Consistency is an
**explicit required parameter** on every read operation — never silently
defaulted.

Named constructors in the `consistency` package:
- `Full()` — fully consistent, least performant
- `MinLatency()` — SpiceDB's preferred revision, optimal performance
- `AtLeast(revision string)` — read-after-write
- `Snapshot(revision string)` — exact revision

All write operations return `(revision string, error)`.

### Relationships

Flat `rel.Relationship` struct (not nested protos):

```go
type Relationship struct {
    ResourceType, ResourceID, ResourceRelation string
    SubjectType, SubjectID, SubjectRelation    string
    CaveatName    string
    CaveatContext map[string]any
    Expiration    *time.Time
    CheckContext  map[string]any
}
```

`rel.Interface` trait for user-defined domain types:

```go
type Interface interface { Relationship() Relationship }
```

Constructors: `FromTriple()`, `MustFromTriple()`, `FromTuple()`, `FromObjects()`

Immutable modifiers: `r.WithCaveat()`, `r.WithExpiration()`, `r.WithCheckContext()`, `r.Filter()`

`CaveatContext` (via `WithCaveat`) and `CheckContext` (via `WithCheckContext`) are
both `map[string]any`, but serve different moments: `CaveatContext` is stored
with the relationship as part of a write, supplying values for the caveat
baked into that specific tuple. `CheckContext` is never sent on a write — it's
read only when the `Relationship` is passed to a check call, supplying
per-item values for evaluating whatever caveat the permission check
encounters. See "Checks" below for how it combines with a call-level default.

### Checks

All checks use `BulkCheckPermissions` under the hood:
- `Check(ctx, cs, permission, rs ...rel.Relationship) ([]CheckResult, error)`
- `CheckOne(ctx, cs, permission, r rel.Relationship) (CheckResult, error)`
- `CheckAny(ctx, cs, permission, rs ...rel.Relationship) (bool, error)` — true
  only if at least one result is `HasPermission()`; a Conditional result does
  not count
- `CheckAll(ctx, cs, permission, rs ...rel.Relationship) (bool, error)` — true
  only if every result is `HasPermission()`; a Conditional result does not
  count
- `CheckIter(ctx, cs, permission, iter) iter.Seq2[CheckResult, error]` —
  auto-batches in chunks of 1000

Each has a `*WithContext` counterpart taking an extra `checkContext
map[string]any` parameter (positioned right after `permission`, before the
relationships) for supplying a call-level caveat context:
`CheckWithContext`, `CheckOneWithContext`, `CheckAnyWithContext`,
`CheckAllWithContext`, `CheckIterWithContext`. The relationships parameter
stays variadic on every method, including the `*WithContext` variants —
`checkContext` is an ordinary (non-variadic) parameter, so it doesn't
conflict with Go's one-variadic-parameter-last rule the way a trailing
`opts ...CheckOption` would have. The non-context method is a thin
delegation to its `*WithContext` counterpart with a `nil` checkContext — one
implementation, two entry points — so existing call sites
(`client.Check(ctx, cs, "view", r1, r2)`) are completely unaffected: no
brackets, no options, same signature as before this feature existed. This
was a deliberate call-site-readability tradeoff — an earlier version of this
feature added `opts ...CheckOption` to the existing methods, which forced
`rs ...rel.Relationship` to become `rs []rel.Relationship` on `Check`/
`CheckAny`/`CheckAll` to make room for the trailing variadic slot,
degrading every caller (including the majority who never touch caveat
context) for the benefit of the few who do. Parallel `*WithContext` methods
avoid that entirely, at the cost of five more exported names.

#### Check-time caveat context

The proto only ever attaches caveat context to an individual check item
(`CheckPermissionRequest.context`, `CheckBulkPermissionsRequestItem.context`)
— there is no request-level context field, so a call-level default has to be
fanned out onto every item at request-build time. Two forms are supported:

- **Call-level default**, via the `checkContext map[string]any` parameter on
  a `*WithContext` method — applied to every relationship in the call.
- **Per-item**, via `rel.Relationship.WithCheckContext(map[string]any)` —
  overrides the default for that one relationship. Works with both the
  plain and `*WithContext` methods.

They merge **key by key, item wins**: each item's context is built as
`{...call-level, ...item-level}`. Item keys win on conflict; call-level keys
absent from the item are retained (wholesale replacement would silently drop
shared keys and push the caveat back into `ConditionalPermission`). An item
with no per-item context inherits the call-level context unchanged; if
neither is supplied, no `context` field is set on the wire.

```go
result, err := c.CheckOneWithContext(ctx, cs, "conditional_view",
    map[string]any{"now": time.Now().Unix()},
    r, // has no per-item context, so it gets the call-level default above
)

// Per-item overrides a shared default for one relationship in a bulk call:
results, err := c.CheckWithContext(ctx, cs, "view",
    map[string]any{"now": N, "region": "us"},
    r1,                                                   // gets {"now": N, "region": "us"}
    r2.WithCheckContext(map[string]any{"region": "eu"}),  // gets {"now": N, "region": "eu"}
)

// No caveat context involved: call sites are unchanged from before this
// feature existed.
results, err := c.Check(ctx, cs, "view", r1, r2)
```

`CheckResult` (see `client/check_types.go`) carries the server's
three-valued answer, not a collapsed bool:

```go
type CheckResult struct {
    Permissionship Permissionship // HasPermission, NoPermission, or ConditionalPermission
    MissingContext []string       // caveat context keys the server needed but didn't get
    CheckedAt      string         // revision this check was evaluated at
}

func (r CheckResult) HasPermission() bool // true only for PermissionshipHasPermission
```

A Conditional result is **NOT a grant**: it means the server found a
caveated relationship but was not given the context needed to evaluate the
caveat, so it could not determine `HasPermission`/`NoPermission` either way.
`HasPermission()` returns `false` for a Conditional result — treating it as
`true` would authorize access on a condition nobody evaluated.
`MissingContext` names what was needed (e.g. `["now"]`), and `CheckedAt` is
the revision the check was evaluated at — thread it into
`consistency.AtLeast` so a subsequent read observes everything this check
observed (read-your-writes for checks).

`CheckAny`/`CheckAll` stay boolean since they already collapse a slice of
results into one decision, but they count **only** `HasPermission()` results
as granted — a Conditional result is treated as not-granted, same as
`NoPermission`, to stay fail-closed.

### Streaming & Transparent Cursor Pagination

Go 1.23+ `iter.Seq2` for all streaming RPCs. **Cursors are fully internal** —
the caller sees a single iterator, and the client transparently re-fetches
pages using the `AfterResultCursor` from each response. Default page sizes
use sensible defaults:

| Method | Default page size | Notes |
|--------|------------------|-------|
| `ReadRelationships` | 512 | cursor-based auto-pagination |
| `LookupResources` | 512 | cursor-based auto-pagination |
| `LookupSubjects` | — | no cursor support in SpiceDB yet; single streaming call |
| `ExportRelationships` | 512 | cursor-based auto-pagination |
| `DeleteRelationships` | 1,000 | auto-repeats until all matched rels deleted; override via `WithDeleteLimit`; matches SpiceDB's default `--max-delete-relationships-limit` |
| `CheckIter` | 1,000 | batches input rels into bulk check calls |
| `ImportRelationships` | 1,000 | batches into client-streaming sends |
| `Updates` | — | server-streaming, no pagination needed |

Iterators:
- `ReadRelationships(...)` → `iter.Seq2[rel.Relationship, error]`
- `LookupResources(...)` → `iter.Seq2[client.LookupResource, error]`
- `LookupSubjects(...)` → `iter.Seq2[client.LookupSubject, error]`
- `ExportRelationships(...)` → `iter.Seq2[rel.Relationship, error]`
- `Updates(...)` → `iter.Seq2[client.WatchEvent, error]`

`LookupResource` and `LookupSubject` (see `client/lookup_types.go`) are native
result structs, not bare ID strings — they carry the data a caller needs to
avoid silently over-granting access:

```go
type LookupResource struct {
    ResourceID     string
    Permissionship Permissionship     // HasPermission vs ConditionalPermission
    PartialCaveat  *PartialCaveatInfo // non-nil when Conditional
    LookedUpAt     string             // revision this result was computed at
}

type ResolvedSubject struct {
    SubjectID      string
    Permissionship Permissionship
    PartialCaveat  *PartialCaveatInfo
}

type LookupSubject struct {
    Subject          ResolvedSubject
    ExcludedSubjects []ResolvedSubject // populated when Subject.SubjectID == "*"
    LookedUpAt       string            // revision this result was computed at
}
```

`LookedUpAt` is identical for every item yielded by a single
`LookupResources`/`LookupSubjects` call — it's the revision of the call as a
whole (`looked_up_at` on each streamed response), not a per-item value.
`Permissionship` on a lookup result is never `PermissionshipNoPermission`: a
resource/subject lacking the permission is simply absent from the stream
rather than yielded with that value. `NoPermission` only appears on
`CheckResult`, from the check surface, where the server is answering a
question about one specific pair and "no" is itself the answer.

`WatchEvent` (see `client/watch.go`) is one event per `WatchResponse`,
carrying the resume token and checkpoint flag the raw proto does not surface
on its own:

```go
type WatchEvent struct {
    Updates        []rel.Update
    ChangesThrough string // resume token; pass as startRevision to resume after a dropped stream
    IsCheckpoint   bool   // true for a checkpoint event, which carries no Updates
}
```

`ChangesThrough` is always populated. Without it, a consumer whose stream
dies can only restart from its original `startRevision` (reprocessing
everything since, possibly past the GC window) or from head (silently
losing every change in the gap). `WithIncludeCheckpoints()` (a `WatchOption`
to `Updates`) requests periodic checkpoint events — recommended behind a
proxy that aborts idle connections — and `IsCheckpoint` lets a caller tell
"nothing changed, here is a fresh resume point" from "here are changes".

`Permissionship` is `PermissionshipHasPermission` for a full grant, or
`PermissionshipConditionalPermission` when the match depends on caveat
context that wasn't supplied (`PartialCaveat.MissingRequiredContext` lists
what's missing). A conditional result is NOT a full grant. When
`LookupSubject.Subject.SubjectID` is the wildcard `"*"`,
`LookupSubject.ExcludedSubjects` lists the subjects carved out of that
wildcard grant — callers MUST check it before treating `"*"` as "every
subject has access," or they risk over-granting to excluded subjects.

#### Stream lifecycle: abandoning an iterator releases it

Root `DESIGN.md`, "RULE: Abandoning a stream must release it", requires that
stopping early actually tells the server to stop. Go's range-over-func makes
stopping early the natural idiom — `break` out of the loop and the iterator's
`yield` returns `false` — so the client must make that idiom safe rather than
document its way around it.

Every streaming iterator derives its own cancellable context from the one
the caller passed and cancels it on the way out (`CheckIter`/
`CheckIterWithContext` are excluded: they batch input relationships into
`BulkCheckPermissions` calls and never open a stream, so there is nothing
to release):

```go
return func(yield func(WatchEvent, error) bool) {
    ctx, cancel := context.WithCancel(ctx)
    defer cancel()
    // ... open the stream on the derived ctx ...
}
```

The `defer` covers every exit path — consumer `break`, mid-stream error, and
normal exhaustion alike — which is why cancellation is wired to the iterator's
return rather than to any single one of them. Without it, grpc-go's
`ClientConn.NewStream` contract applies: unless the context is cancelled,
`Close` is called, or `RecvMsg` drains to a non-nil error, "a goroutine and a
context will be leaked", and SpiceDB holds a dispatch open per abandoned
stream for the life of the connection. The leak is invisible from the caller's
side of a `break`, which is what makes it worth closing in the library rather
than in every call site.

This is additive to the caller's own control, not a replacement for it: the
caller's context still governs the call, and cancelling it still cancels the
stream. It only adds a release the caller could not otherwise express, because
the caller's context typically outlives the loop.

`client/stream_release_test.go` holds the tests for this. They assert on a
*server-side* signal — a stub handler parked on its own `stream.Context()
.Done()` — because a test asserting only that the range loop exited passes
whether or not the stream was released.

`ImportRelationships` is the one client-streaming call and does not fit the
shape above — it consumes an `iter.Seq`, it does not return one — but it
opens a `grpc.ClientStream` the same way and can abandon it the same way: a
relationship whose caveat context fails to convert, or a `Send` that fails
mid-batch, returns early and would otherwise leave that stream unreleased on
the caller's own (typically long-lived) context. It uses the identical
fix — a `context.WithCancel` derived at the top of the function and
deferred once — so it, not just the five iterators above, releases on every
exit path. Six streaming calls total, all covered.

#### `Close()`: releasing the connection

`Client.Close() error` releases the underlying gRPC connection. It is
idempotent and safe to call concurrently with itself (the proto tier guards
with a `CompareAndSwap`, since `grpc.ClientConn.Close` is not documented safe
to call twice), and it is a no-op on a `Client` that never opened a connection
— a zero value, or one assembled by hand from stubs in a test.

`Close()` and per-stream cancellation solve different problems and neither
substitutes for the other. `Close()` is connection-scoped: it tears down the
one connection every call on this `Client` shares, so it belongs at process or
component shutdown (`defer c.Close()` after construction, as every example
does). Abandoning a single iterator must not require tearing down the
connection the rest of the program is still using — that is what the derived
per-stream context above is for.

### Writes

Transaction builder pattern:

```go
var txn rel.Txn
if err := txn.Create(relationship); err != nil {
    return err
}
if err := txn.Touch(relationship); err != nil {
    return err
}
if err := txn.Delete(relationship); err != nil {
    return err
}
if err := txn.MustNotMatch(filter); err != nil { // precondition
    return err
}
revision, err := client.Write(ctx, txn)
```

Every builder method returns `error` and adds nothing to the transaction when
it does — `Create`/`Touch`/`Delete` if the relationship's `CaveatContext`
cannot be converted to protobuf (`rel.ErrInvalidCaveatContext`),
`MustMatch`/`MustNotMatch` if the filter cannot (`rel.ErrInvalidFilter`).
These are not decorative: discarding them writes a relationship with its
caveat name attached and its context silently missing, which mis-evaluates
every future check against that relationship and is only repaired by
rewriting it. Go permits dropping a return value in statement position, so
nothing warns you — check them.

`Write`, `DeleteRelationships`, and `WriteSchema` all return the revision the
mutation occurred at. `ImportRelationships` (bulk import) is the one
exception: it returns `(numLoaded uint64, error)` with no revision, because
`ImportBulkRelationshipsResponse` carries no `ZedToken` field at all — the
proto itself gives the client nothing to expose there, not a client-side
gap.

### Deletions

`DeleteRelationships` automatically pages through large result sets using a
limit of 1,000 per RPC call (matches SpiceDB's default
`--max-delete-relationships-limit`, so the default works against a stock
server). It repeats until the server reports all matching relationships are
deleted. Returns the final revision.

Optional functional options (`DeleteOption`) reach the proto fields that were
previously unreachable — `optional_preconditions` and `optional_limit`:

```go
revision, err := client.DeleteRelationships(ctx, filter,
    client.WithDeleteMustMatch(guardFilter),    // MUST_MATCH precondition
    client.WithDeleteMustNotMatch(otherFilter),  // MUST_NOT_MATCH precondition
    client.WithDeleteLimit(500),                  // override the 1,000 default page size
)
```

`WithDeleteMustMatch`/`WithDeleteMustNotMatch` build a `*v1.Precondition` from
a `rel.Filter` (same pattern as `Txn.MustMatch`/`Txn.MustNotMatch`); the
server rejects the whole call (deleting nothing for that call) if a
precondition isn't satisfied. Preconditions are a per-request proto field, so
when a delete spans multiple pages, they're re-evaluated by the server on
every page — there's no "check once, apply to every page" semantics. A delete
that starts successfully can still fail partway through if the guarded state
changes between pages, after earlier pages were already deleted. For a
single-shot, all-or-nothing guarded delete, pair a precondition with
`WithDeleteLimit` set high enough to cover every matching relationship in one
call. No options given means unchanged default behavior: no preconditions,
1,000-item page size, partial deletions allowed (so auto-paging keeps
working).

### Testing

Use `github.com/stretchr/testify/require` for all assertions in tests and
examples.

### Error Handling

- Standard Go `(result, error)` returns
- Sentinel errors in `rel` package:
  - `ErrInvalidResource` — resource type, ID, or relation is empty
  - `ErrInvalidRelation` — relation string is empty
  - `ErrInvalidSubject` — subject type or ID is empty
  - `ErrInvalidFilter` — a `Filter`'s `SubjectID`/`SubjectRelation` is set
    without `SubjectType`; `Filter.ToProto` returns this instead of silently
    building a filter with no subject constraint at all
  - `ErrInvalidCaveatContext` — a `Relationship`'s `CaveatContext` holds a
    value protobuf cannot represent; `Relationship.ToProto`,
    `Txn.Create`/`Touch`/`Delete`, and the check surface all return this
    instead of writing the relationship with its caveat name attached and its
    context silently missing. The wrapping error **names the offending key**:
    `structpb.NewStruct` reports only the value's Go type, so
    `rel.CaveatContextToStruct` converts per key and identifies the entry —
    the same thing the C#, Java and Ruby clients report for this failure.
    `rel.CaveatContextToStruct` is the single converter for both surfaces
    (write-time `CaveatContext` and check-time `CheckContext`), so the two
    can never drift apart

  Both sentinels live in `rel`, which is deliberately client-independent — it
  cannot import `client` without an import cycle. The `client` package wraps
  them as a `*client.Error` with `CodeInvalidArgument` at its own API
  boundaries (`ImportRelationships`, `ReadRelationships`,
  `DeleteRelationships`, and the check surface), so `errors.Is` works against
  both `rel`'s sentinel and `client.ErrInvalidArgument` for any error that
  passed through the client. An error returned directly by a `rel` builder
  (e.g. `Txn.Create`) carries the `rel` sentinel only.
- `Must*` variants that panic (for tests/initialization)
- Automatic retry with jittered exponential backoff for transient gRPC
  errors — see "Retry and timeout semantics" below

#### Retry and timeout semantics

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
caller concludes a write failed that in fact succeeded. This client expresses
that in the service config rather than in a call-once wrapper: one
SERVICE-level `methodConfig` entry carries the `retryPolicy` for all four
services, and a second METHOD-level entry naming the seven mutation RPCs
carries none. grpc-go's `getMethodConfig` prefers an exact
`/service/method` match over a `/service/` wildcard, so those seven get no
retry while every other RPC on the same service does. A caller who wants a
mutation retried must decide that themselves, knowing their own idempotency.

**Timeout shape**: unlike the other six clients, this one has no per-call
timeout of its own — the caller's `context.Context` is the bound, and retry is
grpc-go's service-config `retryPolicy`, which reuses that same context across
every attempt. A context deadline is a point in time, not a duration, so it
bounds the whole operation: attempts, backoff between them, and auto-pagination
all draw down one budget, and a retry that would start after the deadline fails
immediately instead. `context.WithTimeout(ctx, 30*time.Second)` therefore means
at most 30 s here, where the same nominal 30 s in the hand-rolled clients can
reach ~120 s plus backoff. Root `DESIGN.md`, "On worst-case latency".

### Performance

- BulkCheck for all check operations (even single)
- Transparent cursor-based pagination with sensible default page sizes
- Batched deletions (1,000-item limit, matching SpiceDB's default `--max-delete-relationships-limit`) to avoid server-side timeouts

### Escape Hatches

Proto fields are semi-exposed on builder types (`Txn.V1Updates`,
`Filter.V1Filter`, `Strategy.V1Consistency`) for advanced use cases.

## Public API Surface

See package sections above for the complete API manifest.

## Examples Manifest

| Directory | Demonstrates |
|-----------|-------------|
| `check_permission/` | Basic permission check with CheckOne, plus a caveated check with no context to show a Conditional CheckResult |
| `write_relationships/` | Writing relationships with transaction builder |
| `read_relationships/` | Reading relationships with iterator |
| `lookup_resources/` | Finding resources a subject can access |
| `lookup_subjects/` | Finding subjects with access to a resource |
| `watch_changes/` | Watching for relationship changes |
| `schema_management/` | Reading and writing schema |
| `bulk_operations/` | Bulk checks, batch writes, and bulk import/export |
| `schema_reflection/` | Schema reflection, computable permissions, dependent relations, diff |
| `relationship_counters/` | Registering, reading, and unregistering relationship counters |
| `expand_permission_tree/` | Expanding a permission into its tree of subjects with ExpandPermissionTree |

## Changelog

See [CHANGELOG.md](CHANGELOG.md) for release notes.
