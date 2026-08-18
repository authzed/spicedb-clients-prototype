# spicedb-python — Idiomatic Python Client Design

## Inherits

Root DESIGN.md (`../DESIGN.md`) — read it for the overall vision and backwards
compatibility mandate. This document takes precedence for Python-specific
decisions.

## Language-Specific Goals

### Philosophy

Pythonic API that feels like a native library. Use modern Python features
(3.11+): type hints everywhere, dataclasses, async/await, exception hierarchy.

### Package Structure

- **`spicedb`** — main package
- **`spicedb.client`** — the async `Client` class and all operations
- **`spicedb.types`** — relationship types, filters, transactions (dataclasses)
- **`spicedb.consistency`** — consistency strategy constructors
- **`spicedb.errors`** — typed exception hierarchy

### Client Construction

```python
# For production (TLS)
client = SpiceDBClient("grpc.example.com:443", token="my-token")

# For testing (plaintext)
client = SpiceDBClient("localhost:50051", token="testtoken", insecure=True)

# Context manager
async with SpiceDBClient(...) as client:
    ...
```

### Consistency

Consistency is explicit, never defaulted:

```python
from spicedb.consistency import full, min_latency, at_least, snapshot

result = await client.check_permission(rel, consistency=full())
result = await client.check_permission(rel, consistency=at_least(revision))
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
```

Constructor helpers:
- `Relationship.from_triple("document:example", "viewer", "user:jimmy")`
- `Relationship.from_tuple("document:example#viewer", "user:jimmy")`

### Checks

```python
results = await client.check_permissions(consistency, *relationships)  # list[bool]
allowed = await client.check_permission(consistency, relationship)     # bool
any_allowed = await client.check_any(consistency, *relationships)      # bool
all_allowed = await client.check_all(consistency, *relationships)      # bool
```

All checks use BulkCheckPermissions under the hood.

### Streaming

Async iterators for streaming RPCs:

```python
async for rel in client.read_relationships(filter, consistency):
    ...

async for resource in client.lookup_resources(..., consistency):
    ...  # resource: LookupResource

async for subject in client.lookup_subjects(..., consistency):
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

@dataclass(frozen=True)
class PartialCaveatInfo:
    missing_required_context: list[str]

@dataclass(frozen=True)
class LookupResource:
    resource_id: str
    permissionship: Permissionship
    partial_caveat: PartialCaveatInfo | None = None  # non-None when Conditional

@dataclass(frozen=True)
class ResolvedSubject:
    subject_id: str
    permissionship: Permissionship
    partial_caveat: PartialCaveatInfo | None = None

@dataclass(frozen=True)
class LookupSubject:
    subject: ResolvedSubject
    excluded_subjects: list[ResolvedSubject]  # populated when subject.subject_id == "*"
```

`Permissionship.HAS_PERMISSION` is a full grant; `CONDITIONAL_PERMISSION`
means the match depends on caveat context that wasn't supplied
(`partial_caveat.missing_required_context` lists what's missing) — a
conditional result is NOT a full grant. When
`LookupSubject.subject.subject_id` is the wildcard `"*"`,
`LookupSubject.excluded_subjects` lists the subjects carved out of that
wildcard grant — callers MUST check it before treating `"*"` as "every
subject has access," or they risk over-granting to excluded subjects.

### Writes

Transaction builder:

```python
txn = Transaction()
txn.create(relationship)
txn.touch(relationship)
txn.delete(relationship)
txn.must_not_match(filter)  # precondition
revision = await client.write(txn)
```

### Deletions

`delete_relationships(filter, *, must_match=None, must_not_match=None, limit=None)`
reaches the proto's `optional_preconditions`/`optional_limit` fields, mirroring
`spicedb-go`'s `WithDeleteMustMatch`/`WithDeleteMustNotMatch`/`WithDeleteLimit`
(`spicedb-go/client/relationships.go`):

```python
revision = await client.delete_relationships(
    filter,
    must_match=[guard_filter],       # MUST_MATCH precondition(s)
    must_not_match=[other_filter],   # MUST_NOT_MATCH precondition(s)
    limit=1000,                      # optional_limit
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
```

Automatic retry with exponential backoff for transient errors.

### Type Hints

Full type hints on all public API. `py.typed` marker file for PEP 561.

## Public API Surface

See package sections above.

## Examples Manifest

| Directory | Demonstrates |
|-----------|-------------|
| `check_permission/` | Basic permission check |
| `write_relationships/` | Writing relationships with transaction builder |
| `read_relationships/` | Reading relationships with async iterator |
| `lookup_resources/` | Resource lookup |
| `lookup_subjects/` | Subject lookup |
| `watch_changes/` | Watching for changes |
| `schema_management/` | Schema read/write |
| `bulk_operations/` | Bulk checks and imports |

## Changelog

See [CHANGELOG.md](CHANGELOG.md) for release notes.

