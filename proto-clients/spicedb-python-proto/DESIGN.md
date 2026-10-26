# spicedb-python-proto — Design Manifest

- The [root design](../../DESIGN.md) defines shared semantics, RPC coverage, security, and verification.

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

Use `pytest` with `pytest-asyncio` for async tests.

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

## 2026-10-26 transport contract

The approved idiomatic reset does not rename generated protocol APIs or move
business logic into this layer. Expose the materialize
RoaringLookupResourcesServiceStub as `materialize` alongside the four v1 stubs,
and re-export its generated messages. No nondeprecated wrapped RPC is omitted.

The constructor also accepts `allow_insecure_remote_credentials=False` and
custom `ca_cert`, `client_cert`, `client_key` PEM bytes. Delegate default trust
to gRPC, reject incomplete certificate/key pairs or TLS material with plaintext,
and refuse remote plaintext credentials without the separate named opt-in.
The loopback guard must fail closed on ambiguous endpoint syntax before channel
creation; a raw accessor cannot create an unguarded connection.

Plumbing preserves bearer authentication, caller metadata, remaining timeout,
and cancellation on every RPC shape. Stream wrappers retain cancel/close
capabilities; no iterator/deprecation wrapper may hide transport ownership.
The idiomatic layer owns total operation budgets, retries, chunking, and native
error mapping; proto calls must not add retries or reset deadlines underneath it.
Verify real authenticated calls and cancellation, not only stub construction.
