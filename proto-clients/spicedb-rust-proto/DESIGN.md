# spicedb-rust-proto — Design Manifest

- The [root design](../../DESIGN.md) defines shared semantics, RPC coverage, security, and verification.

This is the Rust proto client for SpiceDB. It wraps tonic-generated gRPC stubs
with minimal boilerplate for connection setup and authentication.

## Proto Files

The `proto/` directory contains proto files exported from `buf.build/authzed/api`
via `buf export`. These files are NOT checked into the repo and MUST NOT be
manually modified. To populate them:

```sh
buf export buf.build/authzed/api -o proto
```

After exporting, `cargo build` invokes `build.rs` which uses `tonic-prost-build` to
generate Rust types from the proto files. The generated code lives in the
`target/` directory (managed by Cargo) and is included via `tonic::include_proto!`.

### Well-known types and the protoc toolchain

`buf export` writes the authzed protos and their non-well-known dependencies
(`google/api`, `google/rpc`, `buf/validate`, ...) into `proto/`, but it does
**not** emit the `google/protobuf` well-known types (`Timestamp`, `Struct`,
`Duration`, `descriptor.proto`, ...): buf treats those as built into its image,
so they never land on disk. protoc still has to parse them to resolve
`import "google/protobuf/..."` — prost maps them to `prost-types` instead of
generating code, but the descriptors must still resolve — so the well-known-type
`.proto` files have to come from the compiler, not from `proto/`.

**RULE: do not rely on a system protoc to supply the well-known types.** `build.rs`
pins BOTH the compiler and its bundled well-known types via the
`protoc-bin-vendored` build-dependency: it sets `PROTOC` to the vendored binary
and adds `protoc_bin_vendored::include_path()` to the include dirs. This keeps the
build hermetic and independent of whatever protoc the host happens to have.

The reason this is a rule and not a convenience: CI installs protoc via
`apt install protobuf-compiler`, which ships only the binary — the well-known-type
`.proto` files live in the separate `libprotobuf-dev` package, and that protoc
(libprotoc 3.21) is old enough that it does not embed them either. When the runner
image stopped providing those files, every regen that touched a proto file failed
to build with `google/protobuf/descriptor.proto: File not found`, while builds on
machines that happened to have the files kept working. The vendored toolchain
removes that ambient dependency; the `apt` protoc install in `rust.yaml` is now
redundant. This client is the only one affected because it compiles protos with
protoc/tonic-prost-build; the others use `buf generate`, which has the well-known types
built into buf's image.

## Build Steps

1. `buf export buf.build/authzed/api -o proto` — download proto definitions
2. `cargo build` — tonic-prost-build generates Rust types from proto files
3. `cargo test` — run tests

## Additional Code (for Claude)

### Required Exports

The `src/client.rs` file provides:

1. **`SpiceDBProtoClient` struct** — wraps all generated gRPC service clients:
   - `permissions` — PermissionsServiceClient
   - `schema` — SchemaServiceClient
   - `watch` — WatchServiceClient
   - `experimental` — ExperimentalServiceClient
   - `materialize` — RoaringLookupResourcesServiceClient (`authzed.api.materialize.v0`;
     the package also defines `RelationshipsService`/`WatchPermissionsService`/
     `WatchPermissionsSetsService`, which this crate does not wrap — those exist for
     Materialize's own internal sync, not for a gRPC caller)

2. **`SpiceDBProtoClient::new(endpoint, token, insecure)`** — async constructor
   that:
   - Creates a tonic Channel to the endpoint
   - Configures TLS (or plaintext if insecure)
   - Injects the bearer token via a tonic Interceptor
   - Returns a client wrapping all service stubs

   Per root DESIGN.md, "RULE: Credentials over insecure transport require an
   explicit opt-in": `insecure` alone only permits a plaintext connection to
   a loopback endpoint (`localhost`, `127.0.0.0/8`, or `::1`). A
   `unix:` target is NOT loopback here and is refused outright: tonic dials a
   URI, so it would resolve the DNS name `unix` rather than a socket path.
   `SpiceDBProtoClient::new_with_options(endpoint, token, insecure,
   allow_insecure_remote_credentials)` is the opt-in entry point
   for a non-loopback endpoint; `new` delegates to it with `false`. Returns
   `SpiceDBProtoClientError` (not a bare `tonic::transport::Error`), with an
   `InsecureRemoteHostNotAllowed(String)` variant for a rejected combination
   and a `Transport(tonic::transport::Error)` variant for everything tonic
   itself can fail on.

   Endpoint forms (root DESIGN.md, "Accept the endpoint forms deployments use,
   or reject them at construction"): `host:port`, `http://host[:port]` and
   `https://host[:port]` (scheme case-insensitive, one trailing `/` allowed).
   A URL's scheme must agree with `insecure` (`http` with `true`, `https` with
   `false`); the scheme is never prefixed a second time. Any other scheme, a
   disagreeing one, userinfo, a path, a query, a non-numeric port or an
   unparseable authority fails with `InvalidEndpoint(String)`, whose message
   names the accepted forms. The loopback guard runs first on plaintext and
   judges the URL's authority, so `http://127.0.0.1:50051` is loopback, while
   authority-shifting endpoints still fail as `InsecureRemoteHostNotAllowed`.
   `is_loopback_endpoint` accepts URLs too; `endpoint_scheme(endpoint)`
   returns the `EndpointScheme` of a URL endpoint so the idiomatic builder can
   let it select the transport.

   `SpiceDBProtoClient::new_with_connect_mode(endpoint, token, insecure,
   allow_insecure_remote_credentials, ConnectMode)` chooses when the channel
   is dialed: `TransportDefault` (what `new_with_options` uses: TLS eager,
   plaintext lazy), `Lazy` or `Eager`. `Eager` lets a plaintext client fail at
   construction with `Transport` when the server is unreachable (root
   DESIGN.md: lazy clients offer a startup readiness check).

3. **`SpiceDBProtoClient::from_channel(channel, token: Option<&str>)`** —
   synchronous constructor over an existing tonic `Channel` (in-memory
   `connect_with_connector` channels, buffered listeners, embedded servers).
   Nothing is dialed and no endpoint/TLS guard applies: the caller chose the
   transport. `Channel` is a cheap shared handle, so the connection stays
   caller-owned; dropping the client never closes the caller's clone. `Some`
   sends `Bearer <token>` (malformed tokens return `InvalidToken`); `None`
   sends no `authorization` header. `BearerTokenInterceptor` therefore holds an
   optional token. Root DESIGN.md: "Every client has a constructor from an
   existing connection or channel".

The `src/lib.rs` file provides:
- Proto module declarations via `tonic::include_proto!`
- Re-export of `SpiceDBProtoClient`

### Tests

Create `tests/client_test.rs` with:

1. **Constructor test** — verify `SpiceDBProtoClient::new` creates a client
   (use insecure mode, verify construction doesn't panic)
2. **Token format test** — verify bearer token is formatted correctly

### Deprecation Handling

Any methods or fields marked deprecated in the proto definitions must carry
`#[deprecated]` attributes in the generated or wrapper code.

### Invariants

- No business logic — only plumbing (connection, auth, re-export)
- All proto types re-exported as-is, no transformation
- Proto files under `proto/` are never modified (they come from buf export)

## Single protocol copy and legacy bridge

Root DESIGN.md, "Be the single copy of the protocol bindings in any process",
is **not applicable** to Rust, and this tier ships no bridge generator:

- prost has no process-global descriptor registry. Generated messages are
  plain Rust types namespaced by crate, so a second, independently generated
  copy of `authzed.api.*` (for example another crate's tonic-build output)
  links into the same binary without a load-time conflict or misbinding. The
  two copies are simply distinct types; values cross between them by
  `prost::Message` encode/decode.
- There is no official `authzed-*` Rust library to alias or bridge, so there
  is no legacy helper set to carry and no hand-maintained code inside the
  generated modules. Generated code lives only in `OUT_DIR` and is never
  edited.

## Dependency floor

Root DESIGN.md, "Library modules declare the lowest dependency versions they
actually need". `tonic` is declared with `default-features = false` and only
`codegen`, `channel`, `tls-ring` and `tls-native-roots`, so consumers do not
inherit tonic's server stack or router. The generated stubs use the prost codec
from `tonic-prost`. tokio and tonic's `server` and `router` features are
dev-dependencies for the tests. tonic 0.14 / tonic-prost 0.14 / prost 0.14 /
tonic-prost-build 0.14 are one generation, the current one, and move together:
an application on tonic 0.14 then links one tonic, prost, tower and hyper stack,
and `tonic::Status`, `Channel` and `Code` values cross between it and these
crates. Build tooling (`tonic-prost-build`, `glob`,
`protoc-bin-vendored`) is a build-dependency only and the Magefile is a Go
module outside the crate.

## Canonical idiomatic redesign boundary

The approved breaking native API is specified in
`../../spicedb-rust/DESIGN.md`. This tier continues to expose generated tonic
requests/results. Native PermissionCheck, request options, ZedToken, exact
filters and tree enums belong in the idiomatic crate; do not duplicate them here
or retain removed idiomatic aliases. Keep exported/generated service coverage
current and propagate protocol deprecations.

SpiceDBProtoClient and its generated handles support cheap Clone sharing the
channel. No application-wide mutex may serialize unrelated calls. A retained
clone owns its channel independently; dropping another client does not invalidate
it. `raw_proto()` uses the already authenticated channel and is not a new
construction or credential-bypass route.

System TLS calls tonic native-root configuration and is verified with a real
handshake. Preserve the current absence of custom-CA/client-identity builders;
do not claim mTLS configuration support. Guard and dialer must parse the same
endpoint with tonic/http URI semantics. Refuse unsupported unix targets and
remote plaintext credentials lacking the separately named opt-in. Malformed
header tokens return typed errors rather than panicking; idiomatic construction
maps those errors to its native InvalidArgument category.

Do not add independent automatic retry or default timeout middleware: the
idiomatic layer owns read-only retries and one complete-operation deadline,
including chunks/pages/backoff. Its remaining budget propagates through
Request::set_timeout. Raw tonic users manage deadlines and errors themselves.
Dropping a future/stream must cancel the corresponding transport work; avoid
detached tasks that outlive their owner.
