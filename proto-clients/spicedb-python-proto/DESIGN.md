# spicedb-python-proto — Design Manifest

This is the Python proto client for SpiceDB. It wraps buf-generated gRPC stubs
with minimal boilerplate for connection setup and authentication.

## Generated Files

Everything under `gen/` is produced by `buf generate` and MUST NOT be manually
modified.

## Additional Code (for Claude)

### Required Exports

Create a `client.py` in the package root with:

1. **`Client` class** — wraps all generated gRPC service stubs:
   - `permissions` — PermissionsServiceStub
   - `schema` — SchemaServiceStub
   - `watch` — WatchServiceStub
   - `experimental` — ExperimentalServiceStub

2. **`Client(endpoint: str, token: str, *, insecure: bool = False)` constructor**
   — creates the gRPC channel with bearer token metadata injection.

3. **`Client.close()` method and context manager support** (`__aenter__`,
   `__aexit__`) for proper channel lifecycle.

4. **Re-export of proto types** — an `__init__.py` that re-exports key proto
   types from `gen/` so the idiomatic client doesn't import `gen/` directly.

### Tests

Create `tests/test_client.py` with:

1. **Constructor test** — verify Client creates all service stubs
2. **Context manager test** — verify `async with Client(...) as c:` works

### Deprecation Handling

Any methods marked deprecated in proto definitions must carry deprecation
annotations via `warnings.warn(..., DeprecationWarning)` in the wrapper.

### Invariants

- No business logic — only plumbing
- All proto types re-exported as-is
- Generated files under `gen/` are never modified
