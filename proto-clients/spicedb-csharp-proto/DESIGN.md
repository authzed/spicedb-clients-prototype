# spicedb-csharp-proto — Design Manifest

This is the C# proto client for SpiceDB. It wraps buf-generated gRPC stubs
with minimal boilerplate for connection setup and authentication.

## Generated Files

Everything under `gen/` is produced by `buf generate` and MUST NOT be manually
modified. These files are regenerated on every `mage gen`.

**Important:** `buf generate` must be run before the project will build. The
`Client.cs` file references types from the `Authzed.Api.V1` namespace that are
generated into the `gen/` directory.

## Additional Code (for Claude)

### Required Exports

Create a `Client.cs` file in the package root with:

1. **`SpiceDBProtoClient` class** — wraps all generated gRPC service clients:
   - `PermissionsService.PermissionsServiceClient`
   - `SchemaService.SchemaServiceClient`
   - `WatchService.WatchServiceClient`
   - `ExperimentalService.ExperimentalServiceClient`

2. **Constructor: `SpiceDBProtoClient(string endpoint, string token, bool insecure = false)`**
   — creates a `GrpcChannel` with:
   - TLS or insecure credentials based on the `insecure` parameter
   - Bearer token injection via `CallCredentials` (secure) or interceptor (insecure)
   - All four service client properties populated

3. **`IDisposable`** — disposes the underlying `GrpcChannel`

### Tests

Create a `ClientTest.cs` with xUnit tests:

1. **Constructor test** — verify constructor creates a client with all service
   stubs populated (non-null) using an insecure connection
2. **Secure constructor test** — verify constructor works with secure defaults
3. **Dispose test** — verify `Dispose()` does not throw

### Deprecation Handling

Any methods or fields marked deprecated in the proto definitions must carry
`[Obsolete("...")]` attributes in the client wrapper.

### Invariants

- No business logic — only plumbing (connection, auth)
- Generated files under `gen/` are never modified
- Project targets .NET 8
