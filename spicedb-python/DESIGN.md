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

async for resource_id in client.lookup_resources(..., consistency):
    ...
```

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

