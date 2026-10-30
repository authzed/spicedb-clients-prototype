# Migrating from Authzed.Net

`Authzed.Net` is the official legacy .NET package: generated gRPC stubs in
`Authzed.Api.V1`, with no native types, defaults or error mapping. `SpiceDB.Client` is
the idiomatic client built on `SpiceDB.Proto`, the canonical generated binding. This
guide lists every behavior change a migrating application will see.

## Requirements

| Requirement | Floor |
| --- | --- |
| .NET | `net8.0`, `net9.0` or `net10.0` (each package targets all three) |
| SpiceDB server | 1.30 or later (older servers' trailer-only data is reachable through `RawProto()`) |
| `Grpc.Net.Client`, `Grpc.Core.Api`, `Grpc.StatusProto` | 2.80.0 |
| `Google.Protobuf` | 3.34.1 |
| `Google.Api.CommonProtos` | 2.17.0 |
| Legacy code in the same process | `Authzed.Net` 2.0.0 (the bridge); 1.x cannot be referenced alongside |

The dependency floors are the same as `Authzed.Net` 1.6.0's, so adopting the client raises
nothing a migrating application already has. NuGet versions are minimums: newer releases
of these packages work. The packages are not on NuGet yet; see the [README](README.md) for
consuming them from a local feed.

## Migrate incrementally: both in one process

The generated types (`Authzed.Api.V1.*`, `Authzed.Api.Materialize.V0.*`) exist once, in
`SpiceDB.Proto`. The `Authzed.Net` 2.x bridge contains no types of its own; it
type-forwards those names to `SpiceDB.Proto`. With the bridge, legacy code (including
already-compiled libraries built against Authzed.Net 1.x) and `SpiceDB.Client` share the
same message types, so values pass between them without conversion. Authzed.Net 1.x and
`SpiceDB.Client` cannot be referenced together: the duplicate type names fail to compile
(CS0433). Authzed.Net's `Authzed.Api.V0` and `V1Alpha1` namespaces are not carried over.

Mid-migration, reach the stubs on the native client's authenticated connection instead
of building a second channel:

```csharp
var raw = client.RawProto().Permissions;          // PermissionsService.PermissionsServiceClient
var native = ProtoConvert.FromProto(rawResponse);  // SpiceDB.Client.Protocol
```

## Construction and transport security

```csharp
// Before
var credentials = CallCredentials.FromInterceptor((_, metadata) =>
{ metadata.Add("Authorization", $"Bearer {token}"); return Task.CompletedTask; });
var channel = GrpcChannel.ForAddress("https://spicedb.example.com:443",
    new GrpcChannelOptions { Credentials = ChannelCredentials.Create(new SslCredentials(), credentials) });
var permissions = new PermissionsService.PermissionsServiceClient(channel);

// After
await using var client = SpiceDBClient.CreateSystemTls("spicedb.example.com:443", token);
```

- Endpoints are `host:port`, `http://host:port` (plaintext, port required) or
  `https://host[:port]` (TLS, port defaults to 443). A plaintext endpoint always needs a
  port (`localhost` alone is rejected; write `localhost:50051`); a port-less TLS endpoint
  uses 443. A URL's scheme must match the
  factory: `CreatePlaintext` rejects `https://` and `CreateSystemTls` rejects `http://`.
  `dns:///host:port` (client-side round-robin across the resolved addresses) and
  `unix:/path/to/socket` (plaintext) gRPC targets are accepted too. Paths, `user@` and other
  schemes are rejected at construction. The preshared key is a
  positional argument; the client adds the bearer header.
- Channels connect lazily, as `GrpcChannel` does. To fail at startup on a wrong or
  unreachable endpoint, call `await client.ConnectAsync(new ConnectOptions { Timeout =
  TimeSpan.FromSeconds(5) }, ct)`.
- Plaintext with credentials to a non-loopback host is refused unless you opt in:

  ```csharp
  // Before: any http:// address with an auth interceptor
  // After (also for a Docker Compose service name such as "spicedb:50051")
  SpiceDBClient.CreatePlaintext("spicedb.internal:50051", token,
      new ClientOptions { RemotePlaintextCredentials = RemotePlaintextCredentials.Allow });
  ```

- Keep an existing channel or invoker (DI singleton, test server, interceptors): use
  `CreateFromChannel(channel, token)`, or `CreateFromCallInvoker(invoker)` when the invoker
  already carries credentials. The client never disposes either.

An empty or whitespace-only preshared key (usually an unset environment
variable) throws `InvalidArgumentException` at construction.

`SpiceDB.Client.CallOptions` and `Grpc.Core.CallOptions` share a name, so a file that imports
both namespaces (for example one with a custom `CallInvoker` passed to
`CreateFromCallInvoker`) fails with CS0104 wherever it writes `CallOptions`. Aliasing only
this library's type does not help, because both namespace imports stay in scope. Alias the
bare name to the type you mean there (a using-alias takes precedence over namespace
imports), and give the other type its own alias if the file needs it too:

```csharp
using Grpc.Core;
using SpiceDB.Client;
using CallOptions = Grpc.Core.CallOptions;             // CallInvoker overrides
using SpiceDBCallOptions = SpiceDB.Client.CallOptions; // per-call options for this client
```

## Requests and consistency

```csharp
// Before
var response = await permissions.CheckPermissionAsync(new CheckPermissionRequest
{
    Resource = new ObjectReference { ObjectType = "document", ObjectId = "readme" },
    Permission = "view",
    Subject = new SubjectReference { Object = new ObjectReference { ObjectType = "user", ObjectId = "alice" } },
    Consistency = new Consistency { FullyConsistent = true },
});
var allowed = response.Permissionship == CheckPermissionResponse.Types.Permissionship.HasPermission;

// After
var result = await client.CheckAsync(new PermissionCheck(
    new ObjectRef("document", "readme"), "view",
    SubjectRef.Direct(new ObjectRef("user", "alice")), SpiceDB.Client.Consistency.Full()));
var allowed = result.HasPermission; // only an unconditional grant
```

- Consistency is a required argument on every read; there is no implicit default. That
  includes the schema reads (`ReflectSchemaAsync`, `ComputablePermissionsAsync`,
  `DependentRelationsAsync`, `DiffSchemaAsync`): where your raw requests left consistency
  unset, pass `Consistency.MinLatency()`, which is what SpiceDB applies when it is absent.
- `Consistency.AtLeast(token)` and `Snapshot(token)` throw `InvalidArgumentException` for an
  empty or whitespace-only token rather than sending it; use `AtLeastOrFull` or
  `AtLeastOrMinLatency` when a stored token may be missing. Watch start revisions and
  materialize at-revisions take `ZedToken?`: pass null, not an empty token, to omit one.
- Singular checks call `CheckPermission`. Bulk checks (`CheckBulkAsync`) split into chunks
  of 1,000, validate every echoed item, and throw for the first failed item unless
  `CheckOptions.ItemErrors = BulkItemErrors.Returned`. A malformed item in the server's
  response (such as an unset permissionship) still fails the whole call.
- `ZedToken` is a native value: `new ZedToken(text)`, `.Value` to persist it.
- A direct subject is one step from an object: `ObjectRef` converts implicitly to
  `SubjectRef`, or call `SubjectRef.Direct(obj)`. A subject relation of `...` (as tuples,
  the protocol and user input spell a direct subject) means direct everywhere:
  `SubjectRef.Named(obj, "...")` and `SubjectRelationFilter.Named("...")` return the direct
  forms, so input can be passed through without a hand mapping.
- Native results serialize with `System.Text.Json` as canonical values: enums as names
  (`"Permissionship":"HasPermission"`) and `ZedToken` as its string. For the protocol
  messages themselves, `Google.Protobuf.JsonFormatter` and `ToString()` work as before.

## Renamed members

Code that read the generated response messages reads native results instead. The members
that changed name or shape:

| Legacy (`Authzed.Api.V1`) | `SpiceDB.Client` |
| --- | --- |
| `ReadSchemaResponse.SchemaText` | `SchemaResult.Schema` |
| `ReadSchemaResponse.ReadAt.Token` | `SchemaResult.Revision.Value` |
| `WriteSchemaResponse.WrittenAt.Token` | the returned `ZedToken`'s `.Value` |
| `CheckPermissionResponse.Permissionship == ...Types.Permissionship.HasPermission` | `CheckResult.HasPermission` (or `.Permissionship == Permissionship.HasPermission`) |
| `CheckPermissionResponse.CheckedAt.Token` | `CheckResult.CheckedAt.Value` |
| `CheckPermissionResponse.PartialCaveatInfo.MissingRequiredContext` | `CheckResult.MissingContext` |
| `CheckPermissionResponse.OptionalExpiresAt` | `CheckResult.Expiration` |
| `CheckBulkPermissionsResponse.Pairs[i].Item` / `.Error` | `CheckBulkAsync(...)[i]` / `CheckResult.Error` (with `ItemErrors = Returned`) |
| `WriteRelationshipsResponse.WrittenAt.Token` | `WriteResult.Revision.Value` |
| `DeleteRelationshipsResponse.DeletedAt` | `DeleteResult.Revision` |
| `DeleteRelationshipsResponse.RelationshipsDeletedCount` | `DeleteResult.DeletedCount` |
| `DeleteRelationshipsResponse.DeletionProgress` | `DeleteResult.Completed` |
| `ReadRelationshipsResponse.Relationship` / `.ReadAt` / `.AfterResultCursor.Token` | `ReadRelationship.Relationship` / `.ReadAt` / `.AfterResultCursor` |
| `Relationship.Resource.ObjectType` / `.ObjectId` | `Relationship.ResourceType` / `.ResourceId` |
| `Relationship.Subject.Object.ObjectId` / `.Subject.OptionalRelation` | `Relationship.SubjectId` / `.SubjectRelation` (null when direct) |
| `Relationship.OptionalCaveat.CaveatName` / `.Context` | `Relationship.CaveatName` / `.CaveatContext` |
| `Relationship.OptionalExpiresAt` | `Relationship.Expiration` |
| `LookupResourcesResponse.ResourceObjectId` | `LookupResource.ResourceId` |
| `LookupResourcesResponse.LookedUpAt.Token` / `.AfterResultCursor.Token` | `LookupResource.LookedUpAt.Value` / `.AfterResultCursor` |
| `LookupResourcesResponse.PartialCaveatInfo` | `LookupResource.PartialCaveat` |
| `LookupSubjectsResponse.Subject.SubjectObjectId` | `LookupSubject.Subject.SubjectId` |
| `LookupSubjectsResponse.ExcludedSubjects[i].SubjectObjectId` | `LookupSubject.ExcludedSubjects[i].SubjectId` |
| `ExpandPermissionTreeResponse.TreeRoot` / `.ExpandedAt` | `ExpandResult.Tree` / `.Revision` |
| `WatchResponse.Updates` / `.ChangesThrough` | `WatchEvent.Updates` / `.ChangesThrough` |
| `ImportBulkRelationshipsResponse.NumLoaded` | the returned `ulong` |
| `ExperimentalCountRelationshipsResponse.ReadCounterValue.RelationshipCount` | `CountResult.RelationshipCount` |

Earlier builds of this client named the lookup identifiers `LookupResource.ResourceID` and
`ResolvedSubject.SubjectID`. Those names remain as `[Obsolete]` aliases of `ResourceId` and
`SubjectId`.

## Defaults: deadlines and retries

Authzed.Net applied no deadline and no retries.

- Unary calls get a 30-second total deadline (attempts, backoff, chunks and pages).
  Restore the old behavior with
  `new ClientOptions { DefaultDeadline = DefaultDeadline.Disabled }`, or set
  `ClientOptions.DefaultTimeout` / a per-operation `Timeout`.
- Relationship reads, lookups and exports have no total default, but each page request gets
  the default deadline, so a stalled server fails the read rather than hanging it. Time your
  loop spends processing items does not count against it. Watch has
  no default. `DefaultDeadline.Disabled` removes both defaults.
- Idempotent reads, and stream establishment before any item, retry Unavailable/Aborted
  up to three times with jittered backoff. Writes, deletes and imports never retry.
  If you already retry, avoid stacking policies:

  ```csharp
  new ClientOptions { Retries = Retries.Disabled }
  // or keep the client's retries but log them / add codes
  new ClientOptions { RetryPolicy = new RetryPolicy { RetryableCodes = [StatusCode.Unavailable, StatusCode.ResourceExhausted], OnRetry = e => log(e) } }
  // and never reopen any stream
  new ClientOptions { StreamRetries = StreamRetries.Disabled }
  ```

  Every options type also takes a per-call `RetryPolicy` that overrides the client's for
  that call, stream establishment included. Watches are never reopened by the client-wide
  policy (a reopened watch without a start revision would skip changes); only
  `new WatchOptions { RetryPolicy = ... }` or a per-operation policy opts one in, before its
  first event. A policy on a
  touch-only `WriteAsync` (no preconditions) or on `WriteSchemaAsync` retries those
  idempotent writes. Other mutations reject the opt-in.

  To apply one policy to every call of an operation, set it once on the client:
  `new ClientOptions { OperationRetryPolicies = new Dictionary<SpiceDBOperation, RetryPolicy> { [SpiceDBOperation.Updates] = RetryPolicy.None } }`.
  A per-call policy still wins. A `Write` policy retries only writes for which
  `txn.IsIdempotent` is true; deletes, imports and counter registration reject a retrying
  policy when the client is built.

## Errors

```csharp
// Before
catch (RpcException e) when (e.StatusCode == StatusCode.FailedPrecondition) { ... }

// After: typed exceptions...
catch (FailedPreconditionException e) { ... }
// ...that keep status inspection working
catch (SpiceDBException e) when (e.StatusCode == StatusCode.FailedPrecondition) { ... }
```

**`SpiceDBException` is not an `RpcException`, so an existing `catch (RpcException)` still
compiles but no longer matches anything the client throws.** Search for every
`catch (RpcException` and `when (... is RpcException` around client calls and convert it.
The exception does not derive from `RpcException` because `RpcException` has no
constructor that takes an inner exception: a subclass could not keep the server's
`RpcException` as `InnerException`, which is where the server's status, trailers and
`ErrorInfo` reason are preserved, and it would be re-caught by the client's own
`catch (RpcException)` mapping. The mapping:

| Legacy (`RpcException e`) | `SpiceDB.Client` (`SpiceDBException e`) |
| --- | --- |
| `catch (RpcException e)` | `catch (SpiceDBException e)` |
| `e.StatusCode` | `e.StatusCode` (same `Grpc.Core.StatusCode`, also for local errors) |
| `e.Status` | `e.Status` (the server's own status for server errors) |
| `e.Status.Detail` | `e.ServerMessage` (never null; equals `e.Message` for local errors) |
| `e.Trailers`, `e.GetRpcStatus()` | `e.Trailers`, `e.ToRpcException().GetRpcStatus()`, or `e.Reason` / `e.ReasonMetadata` |
| `when (e.StatusCode == StatusCode.X)` | the typed subclass (`NotFoundException`, ...) or the same `when` on `SpiceDBException` |
| code that must keep an `RpcException` | `e.ToRpcException()`, or `e.InnerException as RpcException` for server errors |
| one handler for both tiers mid-migration | `catch (Exception e) when (SpiceDBException.CodeOf(e) == StatusCode.X)` |

- The server's `RpcException` is the `InnerException`; `Status`, `Trailers` and
  `ToRpcException()` expose it (with `ErrorInfo` details), and `Reason`/`ReasonMetadata`
  surface SpiceDB's `ErrorInfo` directly.
- `ServerMessage` is the server's message verbatim, for forwarding errors to others. It is
  never null: for local validation, deadlines and caller-built errors it equals `Message`,
  so `e.ServerMessage ?? e.Message` fallbacks can be dropped.
- Caller cancellation is an `OperationCanceledException` carrying your token, not an
  `RpcException(Cancelled)`. `SpiceDBException.CodeOf(ex)` classifies native, raw
  `RpcException` and cancellation exceptions alike.
- Local validation failures are `InvalidArgumentException` before any RPC, including
  `Relationship.FromTuple` parse errors. It is a `SpiceDBException` (code `InvalidArgument`,
  `ServerMessage` equal to `Message`, no inner `RpcException`), not an `ArgumentException` or
  `FormatException`, because a .NET exception has one base class and native errors keep one
  shape. Code that caught `ArgumentException` or `FormatException` around parsing catches
  `InvalidArgumentException` instead. A server `INVALID_ARGUMENT` is also an
  `InvalidArgumentException`; a local one has no `InnerException`.
- A response the client cannot interpret (for example a relationship without a subject, or
  a check, bulk-check item or lookup result whose permissionship is unset) fails with
  an `Internal`-status `SpiceDBException` rather than yielding empty fields or
  `Permissionship.Unspecified`. Its message reads `<operation>: client detected malformed
  response from server: <detail>.`, so logs do not mistake it for an `Internal` status the
  server sent. Code that branched on `Permissionship.Unspecified` from a check or lookup
  only sees it now for a permissionship value newer than the client.
- Test doubles: `SpiceDBException.Create(StatusCode.NotFound, "...")`, or use
  `SpiceDB.Client.Testing.InMemorySpiceDBClient` behind `ISpiceDBClient` (or
  `ISpiceDBRelationshipClient` and `ISpiceDBSchemaClient`). It answers checks from
  registrations, a resolver (`SetCheckResolver`, `GrantAllChecks()`) and its stored
  relationships, and serves `LookupResourcesAsync` and `LookupSubjectsAsync`. It never
  answers `Permissionship.Unspecified`: registering it, or returning it from a resolver,
  fails with `InvalidArgumentException`.

## Relationships and reads

```csharp
// Before
await foreach (var response in permissions.ReadRelationships(request).ResponseStream.ReadAllAsync())
    Console.WriteLine(response.Relationship.Subject.Object.ObjectId);

// After: pages, cursors and the read-at revision are handled for you
await foreach (var item in client.ReadRelationshipsAsync(SpiceDB.Client.Consistency.Full(), new Filter("document")))
    Console.WriteLine(item.Relationship.SubjectId);
```

- Relationship fields are immutable properties with short paths (`ResourceId`,
  `SubjectId`, ...). `Relationship.FromTuple("document:1#viewer@user:a[caveat:{...}]")`
  and `ToString()` use SpiceDB's tuple syntax.
- To resume, pass an item's `AfterResultCursor` as `ReadOptions.ResumeCursor` with the
  same request and page size.
- Raw `ReadRelationships`/`LookupResources` calls without a limit are one unbounded
  request; this client pages by default. To reproduce the raw call (and its final cursor),
  set `Paging = Paging.SingleRequest` on `ReadOptions` or `LookupOptions`.
- An empty transaction is sent like the raw call: SpiceDB checks its preconditions and
  returns the current revision.
- Writes take a `Transaction` and return `WriteResult`; read `.Revision`.
- `DeleteRelationshipsAsync` is one bounded request; `DeleteAllRelationshipsNonAtomicAsync`
  deletes in batches and is not atomic as a whole.
- Imports are one stream and one transaction per call (`ImportOptions.BatchSize` sets
  relationships per message) and report the server's status when it aborts.

## Response metadata

Authzed.Net callers read headers and trailers from the call object. Use the transport
escape hatch instead (also the way to reach data pre-1.30 servers send in trailers):

```csharp
var sink = new ResponseMetadata();
await client.ReadSchemaAsync(new CallOptions { Transport = new TransportOptions { ResponseMetadata = sink } });
var version = sink.Headers?.GetValue("x-example-header");
```

## Minimum versions

See [Requirements](#requirements) at the top of this guide.
