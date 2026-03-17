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

2. **`SpiceDBProtoClient::new(endpoint, token, insecure)`** — async constructor
   that:
   - Creates a tonic Channel to the endpoint
   - Configures TLS (or plaintext if insecure)
   - Injects the bearer token via a tonic Interceptor
   - Returns a client wrapping all service stubs

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
