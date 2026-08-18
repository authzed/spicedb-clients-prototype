# spicedb-csharp — Idiomatic C# Client Design

## Inherits

Root DESIGN.md (`../DESIGN.md`) — read it for the overall vision and backwards
compatibility mandate. This document takes precedence for C#-specific decisions.

## Language-Specific Goals

### Philosophy: Pit of Success

The API should make the correct thing easy and the wrong thing hard. Users
should fall into correct usage patterns by default.

### Namespace & Project Structure

Single namespace: `SpiceDB.Client`

Projects:

- **SpiceDB.Client** — the `SpiceDBClient` class and all SpiceDB operations,
  plus relationship types, filters, transactions, consistency, and errors
- **SpiceDB.Client.Tests** — xUnit + FluentAssertions unit tests

Types (Relationship, Filter, Transaction, ConsistencyStrategy) are independent
of the client. Users can construct relationships and filters without creating a
client instance.

### Constructors

Security-obvious named constructors:

- `SpiceDBClient.CreatePlaintext(endpoint, presharedKey)` — for testing, makes
  insecure connection obvious
- `SpiceDBClient.CreateSystemTls(endpoint, presharedKey)` — for production
- `SpiceDBClient.CreateFromChannel(channel, presharedKey)` — escape hatch with
  existing GrpcChannel

The client implements `IAsyncDisposable`:

```csharp
await using var client = SpiceDBClient.CreatePlaintext("localhost:50051", "token");
```

### Consistency

ZedTokens are opaque `string` values, never proto types. Consistency is an
**explicit required parameter** on every read operation — never silently
defaulted.

Static factory methods on the `Consistency` class:

- `Consistency.Full()` — fully consistent, least performant
- `Consistency.MinLatency()` — SpiceDB's preferred revision, optimal performance
- `Consistency.AtLeast(revision)` — read-after-write
- `Consistency.Snapshot(revision)` — exact revision
- `Consistency.AtLeastOrFull(revision?)` — AtLeast if non-empty, else Full
- `Consistency.AtLeastOrMinLatency(revision?)` — AtLeast if non-empty, else MinLatency

All write operations return the revision as a `string`.

### Relationships

Sealed `Relationship` record (immutable, value equality):

```csharp
public sealed record Relationship
{
    public string ResourceType { get; init; }
    public string ResourceID { get; init; }
    public string ResourceRelation { get; init; }
    public string SubjectType { get; init; }
    public string SubjectID { get; init; }
    public string SubjectRelation { get; init; }
    public string? CaveatName { get; init; }
    public IReadOnlyDictionary<string, object>? CaveatContext { get; init; }
    public DateTimeOffset? Expiration { get; init; }
    public IReadOnlyDictionary<string, object>? CheckContext { get; init; }
}
```

`CaveatContext` (write-time, stored with the relationship's caveat) and
`CheckContext` (check-time-only, supplied fresh on every check call and
never written to SpiceDB) are deliberately separate fields — see "Caveat
context" under "Checks" below.

Static factory methods:

- `Relationship.FromTriple(resourceType, resourceID, resourceRelation, subjectType, subjectID, subjectRelation)` — validates fields, throws `ArgumentException`
- `Relationship.FromTuple(tupleString)` — parses `type:id#relation@type:id[#relation]`, throws `FormatException`
- `Relationship.FromProto(proto)` — converts from proto type

Immutable modifiers:

- `rel.WithCaveat(name, context?)` — returns copy with caveat (write-time
  `CaveatContext`)
- `rel.WithExpiration(dateTimeOffset)` — returns copy with expiration
- `rel.WithCheckContext(context?)` — returns copy carrying per-item
  check-time caveat context (`CheckContext`, distinct from `WithCaveat`'s
  write-time context — see "Caveat context" under "Checks")

Conversion:

- `rel.ToProto()` — converts to proto type
- `rel.ToFilter()` — creates a Filter matching this relationship
- `rel.ToString()` — returns tuple string representation

### Filter

Sealed `Filter` record (immutable, value equality):

```csharp
var filter = new Filter("document")
    .WithResourceID("doc1")
    .WithRelation("viewer")
    .WithSubjectType("user")
    .WithSubjectID("alice")
    .WithSubjectRelation("member");
```

Methods: `WithResourceID`, `WithResourceIDPrefix`, `WithRelation`,
`WithSubjectType`, `WithSubjectID`, `WithSubjectRelation`, `ToProto`

### Transaction

Mutable builder for batching relationship writes:

```csharp
var txn = new Transaction();
txn.Create(relationship);
txn.Touch(relationship);
txn.Delete(relationship);
txn.MustNotMatch(filter);   // precondition
txn.MustMatch(filter);      // precondition
var revision = await client.WriteAsync(txn);
```

Exposes `V1Updates` and `Preconditions` for advanced use cases.

### Checks

All checks use `BulkCheckPermissions` under the hood — there is no production
call site for the single-item `CheckPermission` RPC.

- `CheckPermissionAsync(consistency, permission, relationship, cancellationToken = default, context = null)` → `Task<CheckResult>`
- `CheckPermissionsAsync(consistency, permission, cancellationToken, params relationships)` → `Task<CheckResult[]>`
- `CheckPermissionsWithContextAsync(consistency, permission, context, cancellationToken, params relationships)` → `Task<CheckResult[]>`
- `CheckAnyAsync(consistency, permission, cancellationToken, params relationships)` → `Task<bool>`
- `CheckAnyWithContextAsync(consistency, permission, context, cancellationToken, params relationships)` → `Task<bool>`
- `CheckAllAsync(consistency, permission, cancellationToken, params relationships)` → `Task<bool>`
- `CheckAllWithContextAsync(consistency, permission, context, cancellationToken, params relationships)` → `Task<bool>`

`context` is an optional call-level default caveat context (an
`IReadOnlyDictionary<string, object>?`) — see "Caveat context" below for the
per-item form (`Relationship.WithCheckContext`) and the merge rule between
the two.

`CheckResult` carries the server's four-valued answer instead of collapsing
it to a `bool` (root DESIGN.md, "RULE: Only an unconditional grant is
true"):

```csharp
public sealed record CheckResult
{
    public Permissionship Permissionship { get; init; }
    public IReadOnlyList<string> MissingContext { get; init; } = [];
    public string CheckedAt { get; init; } = "";
    public bool HasPermission => Permissionship == Permissionship.HasPermission;
}
```

- `Permissionship` — `Unspecified`/`NoPermission`/`HasPermission`/`ConditionalPermission`
  (the same enum used by the lookup surface; see "Lookups" below). A
  `ConditionalPermission` result means the server found a matching
  relationship but could not evaluate its caveat because the required
  context was not supplied — it is NOT a grant.
- `MissingContext` — the caveat context keys the server needed and did not
  receive. Empty unless `Permissionship` is `ConditionalPermission`.
- `CheckedAt` — the ZedToken revision this check was evaluated at. Thread it
  into `Consistency.AtLeast` for read-your-writes.
- `HasPermission` — true ONLY for `HasPermission`, a single equality
  comparison. Prefer this over comparing `Permissionship` directly.

**No `operator bool`/`operator true`/`false` is defined on `CheckResult`.**
This is a deliberate choice, not an oversight: `if (result)` is a compile
error by design, forcing callers through `HasPermission` (or an explicit
`Permissionship` comparison) to get a boolean answer. C# records don't
implicitly participate in boolean contexts, so omitting these operators is
the safe default — it preserves the compile error rather than introducing a
truthy conversion that could disagree with `HasPermission` for a
`ConditionalPermission` result. C# is one of the languages that is "safe by
construction" per root DESIGN.md clause 5 (`if (result)` does not compile).

`CheckAnyAsync`/`CheckAllAsync` count only `HasPermission` results — a
`ConditionalPermission` never contributes to a `true`.

`CheckBulkPermissionsResponse.checked_at` is response-level, not per-item —
the bulk path propagates that one token onto every `CheckResult.CheckedAt`
in the batch.

A per-item `CheckBulkPermissions` error (`google.rpc.Status`, carried as the
`error` arm of the pair's oneof) is routed through the same `ErrorMapper`
switch used by every other RPC — synthesized into an `RpcException` from its
numeric code/message — so callers get the specific typed exception (e.g.
`PermissionDeniedException`) instead of the base `SpiceDBException`.

#### Caveat context

A `ConditionalPermission` result means the server needed caveat context
(named values like `now` that a caveat expression evaluates against) and
didn't get it. `MissingContext` tells the caller what was needed;
supplying it is what turns a conditional result into a real grant. Two
forms, matching the wire: `CheckBulkPermissionsRequestItem.context` (proto
field 4) is per-item — `CheckBulkPermissionsRequest` itself has no context
field — so a call-level default is fanned out onto every item at
request-build time.

- **Call-level** — a default applied to every relationship in the call.
  `CheckPermissionsWithContextAsync`/`CheckAnyWithContextAsync`/
  `CheckAllWithContextAsync` are new methods (not overloads) that add a
  required `context` parameter ahead of the existing `cancellationToken`;
  `CheckPermissionAsync` (the single-relationship form, which has no
  `params` array in the way) instead gets a new **trailing** optional
  `context = null` parameter on the existing method.
- **Per-item** — `Relationship.CheckContext` (set via
  `Relationship.WithCheckContext(context)`) carries context for that one
  relationship's check. It flows through even the plain, unchanged
  `CheckPermissionsAsync`/`CheckAnyAsync`/`CheckAllAsync` — no `WithContext`
  call is needed just to use per-item context alone.

**Merge rule (key-level, item wins):** each item's context is the
call-level dictionary with the item's own entries overwriting matching
keys; call-level keys the item doesn't mention are retained. A call-level
`{now: 42, region: "us"}` plus a per-item `{region: "eu"}` sends
`{now: 42, region: "eu"}` for that item; a sibling item with no per-item
context still gets `{now: 42, region: "us"}` unchanged. This is
deliberately NOT wholesale replacement — an item supplying one key must
never silently drop every other call-level key. When neither call-level
nor per-item context is supplied, no `context` field is set on that item's
wire representation (`null`, never an empty `Struct`).

`Relationship.CheckContext` is check-time-only and is a **distinct**
field from `Relationship.CaveatContext` (write-time, sent by `WriteAsync`
as part of a stored relationship's caveat). `Relationship.ToProto()` never
reads `CheckContext` — only `WriteAsync`'s caveat context is ever written to
SpiceDB. Conflating the two would leak check-time-only data into a stored
relationship the next time a `Relationship` round-trips through
`WriteAsync`.

```csharp
var callLevel = new Dictionary<string, object> { ["now"] = 42, ["region"] = "us" };
var relA = Relationship.FromTriple("document", "doc1", "view", "user", "alice");
var relB = Relationship.FromTriple("document", "doc2", "view", "user", "bob")
    .WithCheckContext(new Dictionary<string, object> { ["region"] = "eu" });

// relA -> {now: 42, region: "us"} (inherits the call-level default)
// relB -> {now: 42, region: "eu"} (region overridden, now retained)
var results = await client.CheckPermissionsWithContextAsync(
    consistency, "view", callLevel, default, relA, relB);
```

Why not an overload on the existing `CheckPermissionsAsync`/`CheckAnyAsync`/
`CheckAllAsync` signatures? Each ends in `params Relationship[]`, and C#
disallows any parameter after `params` — a `context` parameter would have to
land *before* it, which risks colliding with the pre-existing
`CancellationToken cancellationToken = default` slot for calls that already
pass `default` positionally. New method names sidestep that risk entirely
(and match how `spicedb-go`/`spicedb-rust` solved the same shape problem with
`*WithContext` methods) rather than leaning on subtle overload-resolution
betterness rules. `CheckPermissionAsync` has no such constraint — nothing
follows its `CancellationToken` parameter — so it's widened in place with a
trailing optional parameter instead of getting a fifth check method.

### Streaming & Transparent Cursor Pagination

`IAsyncEnumerable<T>` for all streaming RPCs. **Cursors are fully internal** —
the caller sees a single async enumerable, and the client transparently
re-fetches pages using the `AfterResultCursor` from each response.

| Method | Default page size | Notes |
|--------|------------------|-------|
| `ReadRelationshipsAsync` | 512 | cursor-based auto-pagination |
| `LookupResourcesAsync` | 512 | cursor-based auto-pagination |
| `LookupSubjectsAsync` | — | no cursor support in SpiceDB yet; single streaming call |
| `ExportRelationshipsAsync` | 512 | cursor-based auto-pagination |
| `DeleteRelationshipsAsync` | 1,000 | auto-repeats until all matched rels deleted; matches SpiceDB's default `--max-delete-relationships-limit` |
| `ImportRelationshipsAsync` | 1,000 | batches into client-streaming sends |
| `UpdatesAsync` | — | server-streaming, no pagination needed |

Async enumerables:

- `ReadRelationshipsAsync(consistency, filter)` → `IAsyncEnumerable<Relationship>`
- `LookupResourcesAsync(consistency, resourceType, permission, subjectType, subjectID)` → `IAsyncEnumerable<LookupResource>`
- `LookupSubjectsAsync(consistency, resourceType, resourceID, permission, subjectType)` → `IAsyncEnumerable<LookupSubject>`
- `ExportRelationshipsAsync(consistency, filter?)` → `IAsyncEnumerable<Relationship>`
- `UpdatesAsync(objectTypes?, startRevision?, includeCheckpoints?)` → `IAsyncEnumerable<WatchEvent>`

### Lookups

`LookupResourcesAsync`/`LookupSubjectsAsync` yield native records instead of
bare strings — the proto `LookupPermissionship`/`PartialCaveatInfo`/
`ResolvedSubject` types are never exposed. Mirrors `spicedb-go`'s
`client/lookup_types.go`.

```csharp
public enum Permissionship { Unspecified, HasPermission, ConditionalPermission, NoPermission }
public sealed record PartialCaveatInfo { MissingRequiredContext }
public sealed record LookupResource { ResourceID, Permissionship, PartialCaveat, LookedUpAt }
public sealed record ResolvedSubject { SubjectID, Permissionship, PartialCaveat }
public sealed record LookupSubject { Subject, ExcludedSubjects, LookedUpAt }
```

- `LookupResourcesAsync(consistency, resourceType, permission, subjectType, subjectID)` → `IAsyncEnumerable<LookupResource>`
- `LookupSubjectsAsync(consistency, resourceType, resourceID, permission, subjectType)` → `IAsyncEnumerable<LookupSubject>`

`Permissionship` MUST be checked before treating a result as a full grant —
`ConditionalPermission` results depend on caveat context (`PartialCaveat`)
that the server did not fully evaluate. `Permissionship` now also serves the
check surface (`CheckResult`, above) with a fourth value, `NoPermission`,
which — deliberately — lookups never yield: a subject/resource pair lacking
the permission is simply absent from a lookup stream rather than yielded
with that permissionship.

`LookedUpAt` (on both `LookupResource` and `LookupSubject`) is the ZedToken
revision the result was computed at — identical for every item yielded by a
single call, since it's a property of the call, not of the individual
result. It maps the proto `looked_up_at` field that was previously
unreachable through the idiomatic client.

**Wildcard exclusions**: when `LookupSubject.Subject.SubjectID` is the
wildcard `"*"`, the server has granted the permission to every subject of
`subjectType` EXCEPT those listed in `LookupSubject.ExcludedSubjects`.
Callers MUST check `ExcludedSubjects` before treating a wildcard match as a
blanket grant — ignoring it risks granting access to subjects the server
explicitly excluded. The deprecated proto fallback fields
(`subject_object_id`/`permissionship`/`partial_caveat_info`/
`excluded_subject_ids`) are handled transparently for servers that don't yet
populate the non-deprecated `subject`/`excluded_subjects` fields.

### Writes

- `WriteAsync(transaction)` → `Task<string>` (revision)

### Deletions

- `DeleteRelationshipsAsync(filter, mustMatch?, mustNotMatch?, limit?)` → `Task<string>` (revision)

Automatically pages through large result sets using a limit of 1,000 per RPC
call (override with `limit`; matches SpiceDB's default
`--max-delete-relationships-limit`, so the default works against a stock
server). Repeats until the server reports all matching relationships are
deleted.

Optional parameters reach the proto fields that were previously unreachable —
`optional_preconditions` and `optional_limit`:

```csharp
var revision = await client.DeleteRelationshipsAsync(
    filter,
    mustMatch: [guardFilter],       // MUST_MATCH precondition
    mustNotMatch: [otherFilter],    // MUST_NOT_MATCH precondition
    limit: 500);                    // override the 1,000 default page size
```

`mustMatch`/`mustNotMatch` build a `Precondition` from a `Filter` via the
same internal helper `Transaction.MustMatch`/`MustNotMatch` use
(`Transaction.BuildPrecondition`); the server rejects the whole call
(deleting nothing for that call) if a precondition isn't satisfied.
Preconditions are a per-request proto field, so when a delete spans multiple
pages, they're re-evaluated by the server on every page — there's no "check
once, apply to every page" semantics. A delete that starts successfully can
still fail partway through if the guarded state changes between pages, after
earlier pages were already deleted. For a single-shot, all-or-nothing guarded
delete, pair a precondition with `limit` set high enough to cover every
matching relationship in one call. No optional parameters given means
unchanged default behavior: no preconditions, 1,000-item page size, partial
deletions allowed (so auto-paging keeps working).

### Schema

- `ReadSchemaAsync()` → `Task<(string Schema, string Revision)>`
- `WriteSchemaAsync(schema)` → `Task<string>` (revision)
- `ReflectSchemaAsync(consistency)` → `Task<ReflectSchemaResult>`
- `ComputablePermissionsAsync(consistency, definitionName, relationName)` → `Task<(IReadOnlyList<RelationReference>, string)>`
- `DependentRelationsAsync(consistency, definitionName, permissionName)` → `Task<(IReadOnlyList<RelationReference>, string)>`
- `DiffSchemaAsync(consistency, comparisonSchema)` → `Task<(IReadOnlyList<SchemaDiff>, string)>`

### Expand

- `ExpandPermissionTreeAsync(consistency, resourceType, resourceID, permission)` → `Task<ExpandResult>`

`ExpandResult.Tree` is a native `PermissionTree` record — the proto
`PermissionRelationshipTree` is never exposed. Exactly one of
`Intermediate`/`Leaf` is non-null on each node, mapped recursively from the
proto `tree_type` oneof.

### Bulk Import / Export

- `ImportRelationshipsAsync(IAsyncEnumerable<Relationship>)` → `Task<ulong>` (numLoaded)
- `ExportRelationshipsAsync(consistency, filter?)` → `IAsyncEnumerable<Relationship>`

### Watch

- `UpdatesAsync(objectTypes?, startRevision?, includeCheckpoints?)` → `IAsyncEnumerable<WatchEvent>`
  — `includeCheckpoints` (default `false`) requests `WATCH_KIND_INCLUDE_CHECKPOINTS`
  (recommended behind a proxy that aborts idle connections, since a checkpoint keeps the
  stream alive with no changes to report)

`WatchEvent { IReadOnlyList<RelationshipUpdate> Updates, string ChangesThrough, bool
IsCheckpoint }` is one event per `WatchResponse`. `ChangesThrough` is always populated --
proto: "This token can be used in a subsequent WatchRequest to resume watching from this
point" -- pass it as `startRevision` to resume after a dropped stream instead of restarting
from the original `startRevision` (reprocessing, possibly past the GC window) or from head
(silently losing every change in the gap). `IsCheckpoint` is true for a checkpoint event,
which carries no `Updates`.

### Experimental — Relationship Counters

All experimental methods are marked with XML doc `<b>Experimental:</b>` notes.

- `ExperimentalRegisterRelationshipCounterAsync(name, filter)` → `Task`
- `ExperimentalCountRelationshipsAsync(name)` → `Task<(CountResult?, bool StillCalculating)>`
- `ExperimentalUnregisterRelationshipCounterAsync(name)` → `Task`

### Error Handling

Exception hierarchy rooted at `SpiceDBException`:

- `PermissionDeniedException`
- `NotFoundException`
- `AlreadyExistsException`
- `InvalidArgumentException`
- `FailedPreconditionException`
- `UnavailableException`
- `CancelledException`
- `ResourceExhaustedException`
- `DeadlineExceededException`
- `AbortedException`

`ErrorMapper` static class:

- `ToSpiceDBException(RpcException)` — maps gRPC status codes to typed exceptions
- `IsTransient(Exception)` — returns true for UNAVAILABLE, RESOURCE_EXHAUSTED, ABORTED

### Auto-Retry

Automatic retry with exponential backoff for transient gRPC errors (UNAVAILABLE,
RESOURCE_EXHAUSTED, ABORTED). Max 3 retries (4 attempts total) with 100ms
initial backoff, doubling each retry.

### Deadlines

Every unary method (`CheckPermissionAsync`, `WriteAsync`, `ReadSchemaAsync`,
etc.) takes an optional `TimeSpan? timeout = null`, applied via
`CallOptions.Deadline` — **alongside**, not instead of, the pre-existing
`CancellationToken`: a cancelled token stops the client from waiting, but
only a server-enforced deadline tells the server itself to stop working.
`CreatePlaintext`/`CreateSystemTls`/`CreateFromChannel` all take an optional
`TimeSpan? defaultTimeout = null`, applied to any unary call that doesn't
pass its own `timeout` — both default to `SpiceDBClient.DefaultTimeout`
(30s), mirroring `authzed-node`'s `DEFAULT_DEADLINE_MS = 30_000` (its
comment cites `grpc/grpc-node#541`). See root DESIGN.md, "RULE: A unary
call must have a deadline".

The six `params Relationship[]` check overloads (`CheckPermissionsAsync`,
`CheckPermissionsWithContextAsync`, `CheckAnyAsync`, `CheckAnyWithContextAsync`,
`CheckAllAsync`, `CheckAllWithContextAsync`) deliberately do **not** take a
`timeout` parameter: inserting one ahead of
the `params` array would silently break any existing positional call site
passing relationships right after `cancellationToken` (e.g.
`CheckPermissionsAsync(cs, "view", default, rel1, rel2)` — `rel1` would try
to bind to the new parameter instead of the params array). They are still
bounded by the client's `DefaultTimeout`; use the singular
`CheckPermissionAsync` for a per-call override on checks.

```csharp
await using var client = SpiceDBClient.CreatePlaintext(endpoint, token, defaultTimeout: TimeSpan.FromSeconds(5));
var result = await client.CheckPermissionAsync(Consistency.Full(), "view", rel);                                    // bound by the 5s default
var result2 = await client.CheckPermissionAsync(Consistency.Full(), "view", rel, timeout: TimeSpan.FromSeconds(1)); // overrides it
```

Server-streaming methods (`ReadRelationshipsAsync`, `LookupResourcesAsync`,
`LookupSubjectsAsync`, `UpdatesAsync`, `ExportRelationshipsAsync`) take no
`timeout` parameter and are NOT bound by `DefaultTimeout` — they are
long-lived by design (`UpdatesAsync` may run for the life of the process),
and applying the unary default to them would make the stream itself the
outage.

`ImportRelationshipsAsync` is client-streaming, not server-streaming, but
the same exclusion applies for the mirror-image reason: its duration scales
with the size of the caller's dataset, not with server latency, so no fixed
default is correct for it either. Unlike the server-streaming methods
above, it DOES take a `timeout` parameter — omitting it means unbounded
there, not "use `DefaultTimeout`"; pass it explicitly to bound a bulk
import. (Its `cancellationToken` still works as caller-side cancellation
regardless.)

Note for callers reasoning about worst-case latency: `timeout` is a
per-*attempt* budget, applied fresh on each retry, so a call that retries
can take up to `timeout × (retries + 1)` plus backoff, and an auto-paging
call (e.g. `DeleteRelationshipsAsync`) applies the same `timeout` fresh to
each page.

### Supporting Types

```csharp
public sealed record SchemaDefinition { Name, Comment, Relations, Permissions }
public sealed record SchemaRelation { Name, Comment, ParentDefinitionName }
public sealed record SchemaPermission { Name, Comment, ParentDefinitionName }
public sealed record SchemaCaveat { Name, Comment, Expression, Parameters }
public sealed record SchemaCaveatParameter { Name, Type, ParentCaveatName }
public sealed record ReflectSchemaResult { Definitions, Caveats, Revision }
public sealed record RelationReference { DefinitionName, RelationName, IsPermission }
public sealed record SchemaDiff { Kind, DefinitionName, RelationName, PermissionName, CaveatName }
public sealed record ExpandResult { Tree, Revision }
public sealed record CountResult { RelationshipCount, Revision }
public sealed record RelationshipUpdate { Operation, Relationship }
public enum UpdateOperation { Create = 1, Touch = 2, Delete = 3 }
public sealed record PermissionTree { ExpandedObject, ExpandedRelation, Intermediate, Leaf }
public sealed record ObjectRef { ObjectType, ObjectID }
public sealed record SubjectRef { SubjectType, SubjectID, OptionalRelation }
public sealed record IntermediateNode { Operation, Children }
public sealed record LeafNode { Subjects }
public enum TreeOperation { Unspecified, Union, Intersection, Exclusion }
public enum Permissionship { Unspecified, HasPermission, ConditionalPermission, NoPermission }
public sealed record PartialCaveatInfo { MissingRequiredContext }
public sealed record LookupResource { ResourceID, Permissionship, PartialCaveat, LookedUpAt }
public sealed record ResolvedSubject { SubjectID, Permissionship, PartialCaveat }
public sealed record LookupSubject { Subject, ExcludedSubjects, LookedUpAt }
public sealed record CheckResult { Permissionship, MissingContext, CheckedAt, HasPermission }
```

### Escape Hatches

- `ConsistencyStrategy.V1Consistency` — exposes underlying proto type
- `Transaction.V1Updates` / `Transaction.Preconditions` — exposes underlying proto updates
- `SpiceDBClient.CreateFromChannel(channel, key)` — use existing GrpcChannel

## Changelog

See [CHANGELOG.md](CHANGELOG.md) for release notes.
