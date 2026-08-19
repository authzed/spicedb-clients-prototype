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

2. **Constructor: `SpiceDBProtoClient(string endpoint, string token, bool insecure = false,
   bool allowInsecureRemoteCredentials = false)`** — creates a `GrpcChannel` with:
   - TLS or insecure credentials based on the `insecure` parameter
   - Bearer token injection via `CallCredentials` (secure) or interceptor (insecure)
   - All four service client properties populated
   - A refusal, before any channel/credential/handler exists, when `insecure` is combined
     with a non-loopback `endpoint` and `allowInsecureRemoteCredentials` is false — root
     DESIGN.md, "RULE: Credentials over insecure transport require an explicit opt-in".
     `unix:` targets are refused outright (this transport would dial the DNS name `unix`).

3. **Constructor: `SpiceDBProtoClient(GrpcChannel channel, string token)`** — the escape
   hatch for a caller-built channel (behind `SpiceDBClient.CreateFromChannel`). Attaches
   the bearer token via an interceptor and performs no loopback check: the channel already
   exists, fully configured, so there is no endpoint or insecure flag left to guard.

4. **`bool _ownsChannel` field** — true only when this client created the channel itself
   (constructor 2), false when one was handed to it (constructor 3).

5. **`IDisposable`** — disposes the underlying `GrpcChannel` **only when `_ownsChannel`**.
   A caller-supplied channel is left open: it belongs to whoever built it, and the
   idiomatic .NET pattern is one DI-registered singleton `GrpcChannel` shared across the
   application, so disposing it here broke every other consumer. Lending a channel does
   not transfer ownership.

### Tests

Create a `ClientTest.cs` with xUnit tests:

1. **Constructor test** — verify constructor creates a client with all service
   stubs populated (non-null) using an insecure connection
2. **Secure constructor test** — verify constructor works with secure defaults
3. **Dispose test** — verify `Dispose()` does not throw
4. **Ownership tests** — verify `Dispose()` leaves a caller-supplied channel usable (build
   a stub on it afterwards), **and** that it still disposes a channel this client created
   (a call through it afterwards throws `ObjectDisposedException`). Both directions are
   required: without the second, "never dispose anything" passes the first.

### Deprecation Handling

Any methods or fields marked deprecated in the proto definitions must carry
`[Obsolete("...")]` attributes in the client wrapper.

### Invariants

- No business logic — only plumbing (connection, auth)
- Generated files under `gen/` are never modified
- Project targets .NET 8
