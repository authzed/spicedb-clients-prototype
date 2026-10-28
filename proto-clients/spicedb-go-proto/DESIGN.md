# spicedb-go-proto — Design Manifest

- The [root design](../../DESIGN.md) defines shared semantics, RPC coverage, security, and verification.

This is the Go proto client for SpiceDB. It wraps buf-generated gRPC stubs
with minimal boilerplate for connection setup and authentication.

## Generated Files

Everything under `gen/` is produced by `buf generate` and MUST NOT be manually
modified, with one exception listed below. These files are regenerated on
every `mage gen`.

## Canonical binding

This module is the single Go binding for `authzed.api.v1` and
`authzed.api.materialize.v0` (root DESIGN.md, "Be the single copy of the
protocol bindings in any process"). It must never depend on authzed-go's
generated packages, and it must generate everything authzed-go generates on
these message types so that authzed-go can alias it:

- `buf.gen.yaml` runs protoc-gen-go, grpc-go, vtprotobuf
  (`marshal+unmarshal+size+clone+equal`), protoc-gen-validate and
  grpc-gateway. Packages are named `v1` and `v0`; managed mode rewrites only
  `go_package`, so embedded descriptors keep every other upstream file option.
- `gen/authzed/api/v1/00_handwritten_validation.go` is hand-maintained (buf
  never writes it). It carries authzed-go's `HandwrittenValidate` methods,
  which must live in the same package as the types.
- `cmd/authzed-go-bridge` rewrites an authzed-go checkout so its
  `proto/authzed/api/{v1,materialize/v0}` packages become type aliases,
  constants, variables and forwarding functions over this module. Its test
  links a generated bridge beside the canonical packages and must not panic.
  Run it in authzed-go whenever this module's exported surface changes.

## Additional Code (for Claude)

### Required Exports

Create a `client.go` file in the package root with:

1. **`Client` struct** — wraps all generated gRPC service clients:
   - `PermissionsServiceClient`
   - `SchemaServiceClient`
   - `WatchServiceClient`
   - `ExperimentalServiceClient`
   - `RoaringLookupResourcesServiceClient` from `authzed.api.materialize.v0`

2. **`NewClient(endpoint string, token string, opts ...Option) (*Client, error)`**
   — constructor that:
   - Creates a gRPC connection to the endpoint
   - Injects the bearer token via a per-RPC credential
   - Applies any additional options
   - Returns a Client wrapping all service stubs

3. **`Option` type** — `type Option func(*clientConfig)` for:
   - `WithInsecure()` — disable TLS (for testing)
   - `WithDialOptions(...grpc.DialOption)` — escape hatch for custom gRPC options
   - `WithInsecureAllowRemoteHost()` — distinct explicit credential opt-in for
     a non-loopback plaintext target

4. **Re-export of proto types** — a `types.go` file that re-exports key proto
   types needed by the idiomatic layer (ObjectReference, SubjectReference,
   Relationship, etc.) so the idiomatic client doesn't import `gen/` directly.

### Tests

Use `github.com/stretchr/testify/require` for all assertions.

Create a `client_test.go` with:

1. **Constructor test** — verify `NewClient` creates a client with all service
   stubs populated (use a mock gRPC server or just verify the struct fields are
   non-nil after construction with `WithInsecure()`)
2. **Options test** — verify `WithInsecure()` and `WithDialOptions()` are applied

### Deprecation Handling

Any methods or fields marked deprecated in the proto definitions must carry
`// Deprecated:` comments in the re-exported types and client wrapper methods.

### Invariants

- No business logic — only plumbing (connection, auth, re-export)
- All proto types re-exported as-is, no transformation
- Generated files under `gen/` are never modified

## Canonical idiomatic redesign boundary

The approved breaking idiomatic redesign is specified in
`../../spicedb-go/DESIGN.md`. This tier retains raw generated request/response
values; native PermissionCheck, opaque strategies/filters/transactions, and
ZedToken live above it. Generated files are regenerated, never hand patched.
Update service/type exports when the protocol changes, including the materialize
roaring lookup wrapper, so every supported RPC remains reachable. Do not create
legacy idiomatic aliases in this tier.

System TLS delegates to grpc-go's default trust store. The plaintext credential
guard must inspect the same target/resolver semantics as the dialer, including
resolver authority. Refuse remote plaintext without the separate opt-in and
return a typed sentinel the idiomatic constructor can map to InvalidArgument.
Custom dial credentials must not silently bypass the credential guard. Preserve
real-handshake and parser-bypass regression coverage.

Read-only retry policy may cover transient Unavailable/Aborted with finite
attempts and jitter, never ResourceExhausted or mutations. The idiomatic layer
owns the complete operation deadline (30s unary default); this tier honors its
context across attempts without resetting it. Do not combine independent retry
loops. Raw calls have no idiomatic default deadline or error mapping; callers
manage their contexts. Cancellation/close releases transports, and Close is
idempotent. The authenticated RawProto accessor reuses this client; it is not a
new connection path.

The secondary `Connection() grpc.ClientConnInterface` accessor borrows the existing
authenticated connection to construct additional generated service clients. It
returns nil for hand-built stub-only clients, opens nothing, and transfers no
ownership. `Close()` still owns transport teardown. Tests verify reuse and lifecycle.
