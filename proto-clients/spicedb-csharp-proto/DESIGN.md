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

1. **`SpiceDBProtoClient` class** — wraps all generated gRPC service clients, as four
   public get-only properties. **The property names are part of the contract, not an
   implementation detail:** the idiomatic client's escape hatch, `SpiceDBClient.RawProto()`,
   hands this object to callers, so `client.RawProto().Permissions` is public API of
   `spicedb-csharp`. Renaming one here breaks that client's users, not just this project.
   - `Permissions` — `PermissionsService.PermissionsServiceClient`
   - `Schema` — `SchemaService.SchemaServiceClient`
   - `Watch` — `WatchService.WatchServiceClient`
   - `Experimental` — `ExperimentalService.ExperimentalServiceClient`

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

5. **Internal constructor: `SpiceDBProtoClient(string endpoint, string token, bool insecure,
   bool allowInsecureRemoteCredentials, HttpMessageHandler? httpHandler)`** (`Client.cs:94`)
   — the shared implementation behind constructor 2, which only delegates to it. Every
   guard, the channel construction, the credentials and the four stubs live here. It is
   `internal` for one reason: it lets the test assembly substitute an
   `HttpMessageHandler` and capture the exact outgoing `authorization` header, so
   `InsecureHostGuardTest` can prove a rejected combination never reaches a handler at
   all rather than merely that an exception was thrown. Not public API.

6. **`internal static bool IsLoopbackEndpoint(string endpoint)`** (`Client.cs:326`) and its
   helper `TransportAuthority` — the loopback decision behind the guard in constructor 2.
   It must derive its host from `System.Uri`, the same parser `GrpcChannel.ForAddress`
   dials with, and must refuse any endpoint containing `@`, `/`, `?`, `#`, or whitespace.
   See root DESIGN.md, "RULE: Credentials over insecure transport require an explicit
   opt-in", for why a hand-rolled split is prohibited here. `internal` so the guard tests
   can drive it directly.

7. **`TransitiveProtoStubs.cs`** — stub types for transitive proto dependencies that the
   buf-generated descriptor registrations reference (`Buf.Validate`,
   `Grpc.Gateway.ProtocGenOpenapiv2.Options`, and similar). Metadata only, never called at
   runtime. It exists so the project builds without BSR NuGet authentication or packages
   that do not exist on nuget.org. Hand-written, but not to be edited except when buf's
   output starts or stops referencing such a dependency.

8. **`IDisposable`** — disposes the underlying `GrpcChannel` **only when `_ownsChannel`**.
   A caller-supplied channel is left open: it belongs to whoever built it, and the
   idiomatic .NET pattern is one DI-registered singleton `GrpcChannel` shared across the
   application, so disposing it here broke every other consumer. Lending a channel does
   not transfer ownership.

### Tests

Two xUnit test files, both required.

`ClientTest.cs`:

1. **Constructor test** — verify constructor creates a client with all service
   stubs populated (non-null) using an insecure connection
2. **Secure constructor test** — verify constructor works with secure defaults
3. **Dispose test** — verify `Dispose()` does not throw
4. **Ownership tests** — verify `Dispose()` leaves a caller-supplied channel usable (build
   a stub on it afterwards), **and** that it still disposes a channel this client created
   (a call through it afterwards throws `ObjectDisposedException`). Both directions are
   required: without the second, "never dispose anything" passes the first.

`InsecureHostGuardTest.cs` — the guard in constructor 2 is a security control, and a
manifest that specifies the control without specifying its tests is how the control gets
dropped. It must keep covering, at minimum:

1. **`IsLoopbackEndpoint` truth table** — `localhost`, 127.0.0.0/8, `::1` (bare,
   bracketed, expanded, with and without a port) are loopback; everything else is not.
2. **Endpoints that dial off-host are refused** — `127.0.0.1:443@evil.com` and the
   bracketed-IPv6 variants, which a last-colon split reads as loopback while `System.Uri`
   reads the leading part as *userinfo* and connects to `evil.com`.
3. **A rejected combination never reaches the wire** — driven through the internal
   `HttpMessageHandler` seam, asserting the handler observed no request at all, not merely
   that a constructor threw.
4. **Bare IPv6 loopback constructs a real client**, rather than passing the guard and then
   throwing `UriFormatException` out of `GrpcChannel.ForAddress`.
5. **`unix:` targets are refused outright**, before either `insecure` or
   `allowInsecureRemoteCredentials` is consulted — this transport would resolve the DNS
   name `unix`.
6. **Null arguments throw `ArgumentNullException`.**

### Deprecation Handling

Any methods or fields marked deprecated in the proto definitions must carry
`[Obsolete("...")]` attributes in the client wrapper.

### Invariants

- No business logic — only plumbing (connection, auth)
- Generated files under `gen/` are never modified
- Project targets `net10.0` (`SpiceDB.Proto.csproj`)
