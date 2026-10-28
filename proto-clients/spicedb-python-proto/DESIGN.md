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

## Canonical binding and the authzed-py bridge

Root DESIGN.md, "Be the single copy of the protocol bindings in any process".

- **Single protocol copy.** This distribution is the only copy of the
  `authzed/api/v1`, `authzed/api/materialize/v0`, `buf/validate`, `validate`
  and `protoc_gen_openapiv2` generated modules (`*_pb2.py`, `*_pb2.pyi`,
  `*_pb2_grpc.py`) in a process. `google/api` and `google/rpc` come from
  `googleapis-common-protos` and are not shipped. buf also emits `.pyi` stubs
  (`buf.build/protocolbuffers/pyi`), so a bridged authzed-py keeps typed
  message modules.
- **Coexistence layout.** Every shared package (`authzed`, `authzed.api`,
  `authzed.api.v1`, `authzed.api.materialize[.v0]`, `buf`, `validate`,
  `protoc_gen_openapiv2`) is a namespace package here: `gen/` holds no
  `__init__.py`, and the wheel ships none. authzed-py keeps owning
  `authzed/api/v1/__init__.py` and `authzed/api/materialize/v0/__init__.py`
  (its client classes) and `grpcutil`. The wheel also ships
  `authzed/py.typed` (from `pkgdata/`, outside `gen/`).
- **Bridge.** `spicedb_python_proto.bridge` (console script
  `authzed-py-bridge`) rewrites an authzed-py checkout (`--root`), sdist
  (`--sdist`) or wheel (`--wheel`): it deletes every module listed in
  `CANONICAL_MODULES` (plus `_grpc` and `.pyi` siblings) and `authzed/py.typed`,
  keeps authzed-py-only protos (`authzed/api/v0`, `authzed/api/v1alpha1`),
  prepends `__path__ = pkgutil.extend_path(...)` to each remaining
  `__init__.py` in a bridged package (so split `sys.path` layouts such as
  editable installs still resolve the canonical modules), adds a
  `spicedb-python-proto` requirement, and drops uv_build `module-name` entries
  whose directories are gone. The input is treated as data; nothing in it is
  imported or executed. A test keeps `CANONICAL_MODULES` equal to `gen/`.
- **Proof.** `tests/test_bridge.py` downloads the pinned authzed-py wheel
  (`AUTHZED_PY_VERSION`, default 1.25.0, sha256-checked against PyPI; or
  `AUTHZED_PY_WHEEL`), shows the unbridged wheel overlaps this one
  (`authzed/api/v1/core_pb2.py`), bridges it, and installs it with this wheel
  and spicedb-python into a fresh venv. `tests/bridge_coexistence_check.py`
  then asserts that every shared module is this distribution's file, that the
  default descriptor pool's `authzed/api/v1/core.proto` is `core_pb2.DESCRIPTOR`,
  that authzed-py's re-exported classes are the canonical classes, that the two
  distributions own disjoint files, and that authzed-py's `InsecureClient` and
  spicedb-python's client both work against an in-process server and, with
  `SPICEDB_ENDPOINT`, a live SpiceDB. Uninstalling authzed-py leaves the
  canonical modules importable. The test needs `uv` and network access (or a
  warm cache) and fails rather than skips without them.
- **Hand-maintained files in generated packages: none.** authzed-py's
  hand-written helpers (its client classes and `TokenAuthorization`
  interceptor in `__init__.py`, `grpcutil`) are not bound to message types by
  package placement, so they stay in authzed-py rather than moving here.
- **Dependency floors** (root "Document the path off the legacy libraries"):
  `protobuf>=7.36.2` (the gencode version; the runtime check rejects anything
  older), `grpcio>=1.73.0` (older transports accept authority-bearing `unix:`
  targets, which the loopback guard relies on the transport to reject),
  `googleapis-common-protos>=1.56.1`. `protovalidate` is an optional
  `validation` extra, as in authzed-py; test tooling is only in the `dev` extra.
