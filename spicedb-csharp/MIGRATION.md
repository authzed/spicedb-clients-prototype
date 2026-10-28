# Migrating from Authzed.Net

`Authzed.Net` is the official legacy .NET package: generated gRPC stubs in
`Authzed.Api.V1`, with no native types, defaults or error mapping. `SpiceDB.Client` is
the idiomatic client built on `SpiceDB.Proto`, the canonical generated binding. This
guide lists every behavior change a migrating application will see.

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

- Endpoints are `host:port`, without a scheme. The preshared key is a positional
  argument; the client adds the bearer header.
- Plaintext with credentials to a non-loopback host is refused unless you opt in:

  ```csharp
  // Before: any http:// address with an auth interceptor
  // After
  SpiceDBClient.CreatePlaintext("spicedb.internal:50051", token,
      new ClientOptions { RemotePlaintextCredentials = RemotePlaintextCredentials.Allow });
  ```

- Keep an existing channel or invoker (DI singleton, test server, interceptors): use
  `CreateFromChannel(channel, token)`, or `CreateFromCallInvoker(invoker)` when the invoker
  already carries credentials. The client never disposes either.

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
- Singular checks call `CheckPermission`. Bulk checks (`CheckBulkAsync`) split into chunks
  of 1,000, validate every echoed item, and throw for the first failed item unless
  `CheckOptions.ItemErrors = BulkItemErrors.Returned`.
- `ZedToken` is a native value: `new ZedToken(text)`, `.Value` to persist it.

## Defaults: deadlines and retries

Authzed.Net applied no deadline and no retries.

- Unary calls get a 30-second total deadline (attempts, backoff, chunks and pages).
  Restore the old behavior with
  `new ClientOptions { DefaultDeadline = DefaultDeadline.Disabled }`, or set
  `ClientOptions.DefaultTimeout` / a per-operation `Timeout`.
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
  that call, stream establishment included: `new WatchOptions { RetryPolicy = RetryPolicy.None }`
  never reopens one supervised watch while everything else keeps retrying, and a policy on a
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

- The server's `RpcException` is the `InnerException`; `Status`, `Trailers` and
  `ToRpcException()` expose it (with `ErrorInfo` details), and `Reason`/`ReasonMetadata`
  surface SpiceDB's `ErrorInfo` directly.
- `ServerMessage` is the server's message verbatim, for forwarding errors to others.
- Caller cancellation is an `OperationCanceledException` carrying your token, not an
  `RpcException(Cancelled)`. `SpiceDBException.CodeOf(ex)` classifies native, raw
  `RpcException` and cancellation exceptions alike.
- Local validation failures are `InvalidArgumentException` before any RPC.
- Test doubles: `SpiceDBException.Create(StatusCode.NotFound, "...")`, or use
  `SpiceDB.Client.Testing.InMemorySpiceDBClient` behind `ISpiceDBRelationshipClient` and
  `ISpiceDBSchemaClient`.

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

`SpiceDB.Client` supports SpiceDB 1.30 and later natively, and requires
`Grpc.Net.Client` 2.80.0 and `Google.Protobuf` 3.34.1 or later, the same floors as
Authzed.Net 1.6.0.
