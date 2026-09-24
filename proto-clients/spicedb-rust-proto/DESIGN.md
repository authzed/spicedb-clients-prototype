# spicedb-rust-proto — Design Manifest

This is the Rust proto client for SpiceDB. It wraps tonic-generated gRPC stubs
with minimal boilerplate for connection setup and authentication.

## Proto Files

The `proto/` directory contains proto files exported from `buf.build/authzed/api`
via `buf export`. These files are NOT checked into the repo and MUST NOT be
manually modified. To populate them:

```sh
buf export buf.build/authzed/api -o proto
```

After exporting, `cargo build` invokes `build.rs` which uses `tonic-build` to
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
protoc/tonic-build; the others use `buf generate`, which has the well-known types
built into buf's image.

## Build Steps

1. `buf export buf.build/authzed/api -o proto` — download proto definitions
2. `cargo build` — tonic-build generates Rust types from proto files
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
