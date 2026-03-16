# spicedb-go — Idiomatic Go Client Design

## Inherits

Root DESIGN.md (`../DESIGN.md`) — read it for the overall vision and backwards
compatibility mandate. This document takes precedence for Go-specific decisions.

## Language-Specific Goals

### Philosophy: Pit of Success

The API should make the correct thing easy and the wrong thing hard. Users
should fall into correct usage patterns by default.

### Package Structure

Four packages, mirroring gochugaru's proven design:

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
- `CheckIter(ctx, cs, iter) iter.Seq2[bool, error]`

### Streaming

Go 1.23+ `iter.Seq2` for all streaming RPCs:
- `ReadRelationships(...)` → `iter.Seq2[*rel.Relationship, error]`
- `LookupResources(...)` → `iter.Seq2[string, error]`
- `LookupSubjects(...)` → `iter.Seq2[string, error]`
- `Updates(...)` → `iter.Seq2[rel.Update, error]`

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

### Testing

Use `github.com/stretchr/testify/require` for all assertions in tests and
examples.

### Error Handling

- Standard Go `(result, error)` returns
- Sentinel errors: `ErrInvalidResource`, `ErrInvalidRelation`, etc.
- `Must*` variants that panic (for tests/initialization)
- Automatic retry with exponential backoff for transient gRPC errors

### Performance

- S2 compression by default
- BulkCheck for all check operations (even single)
- Streaming with configurable page sizes

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
| `bulk_operations/` | Bulk checks and imports |

## Changelog

<!-- Claude appends here when making changes, with date + what changed -->
