# spicedb-csharp-proto — Design Manifest

- The [root design](../../DESIGN.md) defines shared semantics, RPC coverage, security, and verification.

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

1. **`SpiceDBProtoClient` class** — wraps all generated gRPC service clients, as eight
   public get-only properties. **The property names are part of the contract, not an
   implementation detail:** the idiomatic client's escape hatch, `SpiceDBClient.RawProto()`,
   hands this object to callers, so `client.RawProto().Permissions` is public API of
   `spicedb-csharp`. Renaming one here breaks that client's users, not just this project.
   - `Permissions` — `PermissionsService.PermissionsServiceClient`
   - `Schema` — `SchemaService.SchemaServiceClient`
   - `Watch` — `WatchService.WatchServiceClient`
   - `Experimental` — `ExperimentalService.ExperimentalServiceClient`
   - `Materialize` — `RoaringLookupResourcesService.RoaringLookupResourcesServiceClient`
     (`Authzed.Api.Materialize.V0`, a separate generated package from the other four)
   - `MaterializeRelationships` — `RelationshipsService.RelationshipsServiceClient`
   - `MaterializePermissionSets` — `WatchPermissionSetsService.WatchPermissionSetsServiceClient`
   - `MaterializeWatchPermissions` — `WatchPermissionsService.WatchPermissionsServiceClient`,
     marked `[Obsolete("Use MaterializePermissionSets instead.")]` for the upstream-deprecated service.
     All materialize accessors use the same authenticated call invoker and channel ownership.


2. **Constructor: `SpiceDBProtoClient(string endpoint, string token, bool insecure = false,
   bool allowInsecureRemoteCredentials = false)`** — creates a `GrpcChannel` with:
   - TLS or insecure credentials based on the `insecure` parameter
   - Bearer token injection via `CallCredentials` (secure) or interceptor (insecure)
   - All five service client properties populated
   - A refusal, before any channel/credential/handler exists, when `insecure` is combined
     with a non-loopback `endpoint` and `allowInsecureRemoteCredentials` is false — root
     DESIGN.md, "RULE: Credentials over insecure transport require an explicit opt-in".
     `unix:` targets are refused outright (this transport would dial the DNS name `unix`).
   - Endpoint forms `host:port`, `http://host:port` (needs `insecure: true`) and
     `https://host[:port]` (needs `insecure: false`). A plaintext endpoint without a port is
     rejected (no silent port 80); a TLS one uses 443. Parsed by
     `internal static string ParseEndpoint(string endpoint, bool insecure)`. Any other form
     throws `ArgumentException` naming the problem and `EndpointForms`, before the
     loopback guard, which then judges the parsed authority.

3. **Constructor: `SpiceDBProtoClient(GrpcChannel channel, string token)`** — the escape
   hatch for a caller-built channel (behind `SpiceDBClient.CreateFromChannel`). Attaches
   the bearer token via an interceptor and performs no loopback check: the channel already
   exists, fully configured, so there is no endpoint or insecure flag left to guard.

4. **`bool _ownsChannel` field** — true only when this client created the channel itself
   (constructor 2), false when one was handed to it (constructors 3 and 9). `_channel` is
   null for constructor 9.

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

7. **`TransitiveProtoStubs.cs`** — stub descriptors for transitive proto dependencies that
   the buf-generated descriptor registrations reference (`buf/validate/validate.proto`,
   `validate/validate.proto`, and the openapiv2 `annotations.proto`/`openapiv2.proto`).
   The SpiceDB protos use them only as custom options, so each stub is an otherwise empty
   `FileDescriptorProto` that **must carry the dependency's real file name and package**
   (and its own dependencies), built once and cached. The descriptor pool keys files by
   name: stubs built from empty bytes all had the name `""`, collided, and made every
   SpiceDB descriptor throw `TypeInitializationException`, which broke `ToString()`,
   `JsonFormatter` and `.Descriptor` for every message. It exists so the project builds
   without BSR NuGet authentication or packages that do not exist on nuget.org.
   Hand-written, but not to be edited except when buf's output starts or stops referencing
   such a dependency.

9. **Constructor: `SpiceDBProtoClient(CallInvoker callInvoker)`** — the existing-connection
   constructor behind `SpiceDBClient.CreateFromCallInvoker` (root DESIGN.md, "Keep protocol
   access secondary and ownership explicit"). Builds every service client on the caller's
   invoker, adds no credentials (the invoker carries its own), and owns nothing.

10. **`InternalsVisibleTo("SpiceDB.Proto.Tests")`** in `SpiceDB.Proto.csproj`, so the
   separate test project can reach the internal constructor and `IsLoopbackEndpoint`.

11. **`bridge/`** — the legacy-library bridge (root DESIGN.md, "Be the single copy of the
   protocol bindings in any process"). The official legacy package `Authzed.Net` defines
   the same generated type names in its own assembly. `bridge/generator` (a console app,
   run by `mage bridge`) reflects over this assembly and writes
   `bridge/Authzed.Net/TypeForwards.g.cs`: one `[assembly: TypeForwardedTo]` per exported
   top-level type in `Authzed.Api.V1` and `Authzed.Api.Materialize.V0` (nested types
   follow their declaring type). `bridge/Authzed.Net/Authzed.Net.csproj` compiles that file
   into an assembly named `Authzed.Net`, version 2.0.0, to be published as the next major
   of the legacy package, so legacy source and binaries bind to this tier's types. Never
   edit `TypeForwards.g.cs` by hand; regenerate after `buf generate`. Authzed.Net's
   `V0`/`V1Alpha1` namespaces are not generated upstream any more and are not bridged.

There are no hand-maintained files inside `gen/`: the C# legacy library has no helpers
bound to the generated types, so the root rule's legacy-helper exception lists nothing here.

8. **`IDisposable`** — disposes the underlying `GrpcChannel` **only when `_ownsChannel`**.
   A caller-supplied channel is left open: it belongs to whoever built it, and the
   idiomatic .NET pattern is one DI-registered singleton `GrpcChannel` shared across the
   application, so disposing it here broke every other consumer. Lending a channel does
   not transfer ownership.

### Tests

The tests live in `tests/SpiceDB.Proto.Tests.csproj`, never in `SpiceDB.Proto.csproj`, so
the library package depends on no test framework (root DESIGN.md, dependency floors).
`SpiceDB.Proto.csproj` excludes `tests/**` and `bridge/**` from compilation and declares
exact minimum package versions (no floating wildcards). `spicedb-csharp/SpiceDB.Client.sln`
includes the test project, so the C# unit job runs it; `mage test` runs it directly.

`DescriptorInitializationTest.cs` initialises the descriptor of every generated file and
calls `ToString()`, `JsonFormatter.Default.Format` and `.Descriptor` on every message in it,
does the same through an `Authzed.Net` type name, and runs `tests/DescriptorProbe` (a
console program built but not referenced by the test project) as a separate process, so a
type initialiser failure is caught in a fresh process too.

`AuthzedNetBridgeTest.cs` loads the bridge `Authzed.Net` and this assembly in one process
and proves: every exported generated type is forwarded here; assembly-qualified legacy
names (`Authzed.Api.V1.X, Authzed.Net`, as compiled legacy binaries record them) resolve to
this tier's types; every type the real `Authzed.Net` 1.6.0 package ships in the shared
namespaces (downloaded with `PackageDownload`, inspected with `MetadataLoadContext`, never
loaded) is forwarded; and the checked-in `TypeForwards.g.cs` matches the generator.

Two further xUnit test files are required.

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
- `SpiceDB.Proto.csproj` and the bridge target `net8.0;net9.0;net10.0` and build with
  `TreatWarningsAsErrors`; the test, probe and generator projects target `net10.0`
- Package references are exact floors (currently Grpc 2.80.0, Google.Protobuf 3.34.1,
  Google.Api.CommonProtos 2.17.0, matching the legacy Authzed.Net 1.6.0)

## Prototype reset transport contract (2026-10-26)

The root and idiomatic client DESIGN.md define the approved API reset. This tier
remains transport plumbing: generated messages and RPC signatures are unchanged.
Preserve all services including materialize, upstream deprecations, authentication,
TLS defaults/overrides, endpoint guards and borrowed-channel ownership.

Forward caller deadlines and cancellation without replacing them with a fresh
per-attempt timeout. The idiomatic tier owns the one total operation budget and
read-only retry policy. Transport cancellation must release streams/uploads even
when no next message has arrived. Preserve complete status details and trailers.
Native idiomatic cancellation semantics are implemented above this tier.

Singular and bulk check stubs preserve with_tracing and debug_trace fields,
including recursive debug data; no transport adapter may erase them. Test real
loopback transport behavior for cancellation/security in addition to generated
stub construction. Real TLS handshake tests remain gated and run in CI.
