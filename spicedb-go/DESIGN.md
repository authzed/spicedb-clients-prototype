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
}
```

`rel.Interface` trait for user-defined domain types:

```go
type Interface interface { Relationship() Relationship }
```

Constructors: `FromTriple()`, `MustFromTriple()`, `FromTuple()`, `FromObjects()`

Immutable modifiers: `r.WithCaveat()`, `r.WithExpiration()`, `r.Filter()`

### Checks

All checks use `BulkCheckPermissions` under the hood:
- `Check(ctx, cs, rs...) ([]bool, error)`
- `CheckOne(ctx, cs, r) (bool, error)`
- `CheckAny(ctx, cs, rs...) (bool, error)`
- `CheckAll(ctx, cs, rs...) (bool, error)`
- `CheckIter(ctx, cs, iter) iter.Seq2[bool, error]` — auto-batches in chunks
  of 1000

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
| `DeleteRelationships` | 10,000 | auto-repeats until all matched rels deleted; override via `WithDeleteLimit` |
| `CheckIter` | 1,000 | batches input rels into bulk check calls |
| `ImportRelationships` | 1,000 | batches into client-streaming sends |
| `Updates` | — | server-streaming, no pagination needed |

Iterators:
- `ReadRelationships(...)` → `iter.Seq2[rel.Relationship, error]`
- `LookupResources(...)` → `iter.Seq2[client.LookupResource, error]`
- `LookupSubjects(...)` → `iter.Seq2[client.LookupSubject, error]`
- `ExportRelationships(...)` → `iter.Seq2[rel.Relationship, error]`
- `Updates(...)` → `iter.Seq2[rel.Update, error]`

`LookupResource` and `LookupSubject` (see `client/lookup_types.go`) are native
result structs, not bare ID strings — they carry the data a caller needs to
avoid silently over-granting access:

```go
type LookupResource struct {
    ResourceID     string
    Permissionship Permissionship     // HasPermission vs ConditionalPermission
    PartialCaveat  *PartialCaveatInfo // non-nil when Conditional
}

type ResolvedSubject struct {
    SubjectID      string
    Permissionship Permissionship
    PartialCaveat  *PartialCaveatInfo
}

type LookupSubject struct {
    Subject          ResolvedSubject
    ExcludedSubjects []ResolvedSubject // populated when Subject.SubjectID == "*"
}
```

`Permissionship` is `PermissionshipHasPermission` for a full grant, or
`PermissionshipConditionalPermission` when the match depends on caveat
context that wasn't supplied (`PartialCaveat.MissingRequiredContext` lists
what's missing). A conditional result is NOT a full grant. When
`LookupSubject.Subject.SubjectID` is the wildcard `"*"`,
`LookupSubject.ExcludedSubjects` lists the subjects carved out of that
wildcard grant — callers MUST check it before treating `"*"` as "every
subject has access," or they risk over-granting to excluded subjects.

### Writes

Transaction builder pattern:

```go
var txn rel.Txn
txn.Create(relationship)
txn.Touch(relationship)
txn.Delete(relationship)
txn.MustNotMatch(filter) // precondition
revision, err := client.Write(ctx, txn)
```

### Deletions

`DeleteRelationships` automatically pages through large result sets using a
limit of 10,000 per RPC call. It repeats until the server reports all matching
relationships are deleted. Returns the final revision.

Optional functional options (`DeleteOption`) reach the proto fields that were
previously unreachable — `optional_preconditions` and `optional_limit`:

```go
revision, err := client.DeleteRelationships(ctx, filter,
    client.WithDeleteMustMatch(guardFilter),    // MUST_MATCH precondition
    client.WithDeleteMustNotMatch(otherFilter),  // MUST_NOT_MATCH precondition
    client.WithDeleteLimit(1000),                // override the 10,000 default page size
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
10,000-item page size, partial deletions allowed (so auto-paging keeps
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
- `Must*` variants that panic (for tests/initialization)
- Automatic retry with exponential backoff for transient gRPC errors

### Performance

- BulkCheck for all check operations (even single)
- Transparent cursor-based pagination with sensible default page sizes
- Batched deletions (10,000-item limit) to avoid server-side timeouts

### Escape Hatches

Proto fields are semi-exposed on builder types (`Txn.V1Updates`,
`Filter.V1Filter`, `Strategy.V1Consistency`) for advanced use cases.

## Public API Surface

See package sections above for the complete API manifest.

## Examples Manifest

| Directory | Demonstrates |
|-----------|-------------|
| `check_permission/` | Basic permission check with CheckOne |
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
