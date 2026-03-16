# spicedb-go-proto — Design Manifest

This is the Go proto client for SpiceDB. It wraps buf-generated gRPC stubs
with minimal boilerplate for connection setup and authentication.

## Generated Files

Everything under `gen/` is produced by `buf generate` and MUST NOT be manually
modified. These files are regenerated on every `mage gen`.

## Additional Code (for Claude)

### Required Exports

Create a `client.go` file in the package root with:

1. **`Client` struct** — wraps all generated gRPC service clients:
   - `PermissionsServiceClient`
   - `SchemaServiceClient`
   - `WatchServiceClient`
   - `ExperimentalServiceClient`

2. **`NewClient(endpoint string, token string, opts ...Option) (*Client, error)`**
   — constructor that:
   - Creates a gRPC connection to the endpoint
   - Injects the bearer token via a per-RPC credential
   - Applies any additional options
   - Returns a Client wrapping all service stubs

3. **`Option` type** — `type Option func(*clientConfig)` for:
   - `WithInsecure()` — disable TLS (for testing)
   - `WithDialOptions(...grpc.DialOption)` — escape hatch for custom gRPC options

4. **Re-export of proto types** — a `types.go` file that re-exports key proto
   types needed by the idiomatic layer (ObjectReference, SubjectReference,
   Relationship, etc.) so the idiomatic client doesn't import `gen/` directly.

### Tests

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
