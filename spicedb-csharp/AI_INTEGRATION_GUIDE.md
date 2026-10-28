# spicedb-csharp: integration guide for AI coding agents

Concrete C# for the rules in the repository-wide
[AI_INTEGRATION_GUIDE.md](../AI_INTEGRATION_GUIDE.md). Read that first for
*why*; this file is *how*. Namespaces:

| Namespace | Package | Role |
|---|---|---|
| `SpiceDB.Client` | `SpiceDB.Client` | native client, options, typed exceptions, results, `ObjectRef`/`SubjectRef`/`Relationship`/`Filter`/`Transaction`, `Consistency` and `ZedToken` |
| `SpiceDB.Client.Testing` | `SpiceDB.Client` | `InMemorySpiceDBClient` test double |
| `SpiceDB.Client.Protocol` | `SpiceDB.Client` | `ProtoConvert` protocol conversions (tools only) |
| `Authzed.Api.V1` | `SpiceDB.Proto` | generated protocol types, reached through `RawProto()` only |

There is no C# generator yet, so the native client is the tier for application
code too: keep resource types, relation and permission names in one place (constants)
and verify them at startup. Do **not** reference the legacy `Authzed.Net` 1.x package
alongside this client (duplicate types, CS0433); use the `Authzed.Net` 2.x bridge, which
forwards to `SpiceDB.Proto` (see [MIGRATION.md](MIGRATION.md)). Do not build requests from
`Authzed.Api.V1` types.

The examples below use this schema:

```zed
definition user {}

definition organization {
    relation admin: user
    relation member: user
    permission can_admin = admin
    permission view = admin + member
}
```

## Connect once

```csharp
await using var client = SpiceDBClient.CreateSystemTls("spicedb.example.com:443",
    Environment.GetEnvironmentVariable("SPICEDB_TOKEN")!);
```

Create one client per process (it is thread-safe) and dispose it at shutdown.
`SpiceDBClient.CreatePlaintext` is for local development; a non-loopback plaintext target
additionally needs
`new ClientOptions { RemotePlaintextCredentials = RemotePlaintextCredentials.Allow }`.
For an existing caller-owned transport (in-memory or test-server channel, interceptors),
use `SpiceDBClient.CreateFromCallInvoker(channel.CreateCallInvoker())` (the invoker
carries its own credentials) or `CreateFromChannel(channel, token)`; disposing the client
never disposes either.

## Check (fail closed)

```csharp
try
{
    var result = await client.CheckAsync(new PermissionCheck(
        new ObjectRef("organization", orgId), "view",
        SubjectRef.Direct(new ObjectRef("user", userId)), Consistency.MinLatency()),
        cancellationToken: ct);
    if (!result.HasPermission) throw new ForbiddenException(); // Conditional and denied both deny
}
catch (SpiceDBException) { throw new ForbiddenException(); } // errors deny too
```

- Need to observe a write? Store `written.Revision.Value` and check with
  `Consistency.AtLeast(new ZedToken(stored))`.
- Reporting what was denied: `$"{resource}#{permission}"` prints `organization:acme#view`
  (`ObjectRef.ToString()` is `type:id`).
- Several at once (results stay in input order; only `HasPermission` grants):

```csharp
var checks = orgIds.Select(id => new PermissionCheck(new ObjectRef("organization", id), "view",
    SubjectRef.Direct(new ObjectRef("user", userId)), Consistency.MinLatency())).ToArray();
var results = await client.CheckBulkAsync(checks, cancellationToken: ct);
var visible = orgIds.Where((_, i) => results[i].HasPermission).ToList();
```

  `new CheckOptions { ItemErrors = BulkItemErrors.Returned }` returns per-item errors on
  `results[i].Error` instead of throwing for the first one.
- Permission names chosen at runtime (e.g. per API method): verify every configured name
  at startup, since the native client cannot reject them at compile time:

```csharp
var schema = await client.ReflectSchemaAsync(Consistency.Full(), cancellationToken: ct);
var known = schema.Definitions.Single(d => d.Name == "organization").Permissions.Select(p => p.Name).ToHashSet();
var unknown = configuredPermissions.Where(p => !known.Contains(p)).ToList();
if (unknown.Count > 0) throw new InvalidOperationException("Unknown permissions: " + string.Join(", ", unknown));
```

## List what a subject can access

```csharp
var ids = new List<string>();
await foreach (var resource in client.LookupResourcesAsync(Consistency.MinLatency(), "organization", "view",
    SubjectRef.Direct(new ObjectRef("user", userId)),
    new LookupOptions { PermissionshipFilter = LookupPermissionshipFilter.HasPermissionOnly }, ct))
    ids.Add(resource.ResourceID);
```

Omit the filter only if you handle `Permissionship.ConditionalPermission` explicitly.

## Write atomically

```csharp
var txn = new Transaction();
txn.MustMatch(new Filter("organization").WithResourceId(orgId).WithRelation("admin")
    .WithSubjectType("user").WithSubjectId(otherAdmin)); // invariant: another admin remains
txn.Delete(new Relationship(new ObjectRef("organization", orgId), "admin", SubjectRef.Direct(new ObjectRef("user", userId))));
txn.Touch(new Relationship(new ObjectRef("organization", orgId), "member", SubjectRef.Direct(new ObjectRef("user", userId))));
try
{
    var written = await client.WriteAsync(txn, cancellationToken: ct); // invalid values throw while building, before any RPC
}
catch (FailedPreconditionException) { throw new LastAdminException(); }
```

- `Touch` upserts; `Create` fails with `AlreadyExistsException` if present.
- Delete by tuple identity: build the relationship without caveat or expiration
  (`new Relationship(resource, relation, subject)`); SpiceDB deletes by identity.
- Delete what you just read: `txn.Delete(item.Relationship)`.
- **Do not retry a failed write blindly**; it may have committed. The client never
  retries writes, deletes or imports on its own. A write of only touches without
  preconditions (and a schema write) is idempotent and may opt in per call with
  `new WriteOptions { RetryPolicy = ... }`; other mutations reject that option.
- An empty `Transaction` is sent: SpiceDB checks its preconditions and returns the current
  revision.

## Read relationships

```csharp
var admins = new List<string>();
string? cursor = null;
await foreach (var item in client.ReadRelationshipsAsync(Consistency.MinLatency(),
    new Filter("organization").WithResourceId(orgId).WithRelation("admin"),
    new ReadOptions { PageSize = 500 }, ct))
{
    admins.Add(item.Relationship.SubjectId);
    cursor = item.AfterResultCursor; // persist to resume after an interruption
}
```

Each item also carries `item.ReadAt`. To resume, pass
`new ReadOptions { PageSize = 500, ResumeCursor = cursor }` with the **same request,
consistency and page size**. `LookupResourcesAsync` and `ExportRelationshipsAsync` work
the same way. To send exactly one request like the raw API (limit = `Limit` or unbounded,
no follow-up page), set `Paging = Paging.SingleRequest` on `ReadOptions` or
`LookupOptions`.

## Schema-agnostic code (native tier)

```csharp
var relationship = Relationship.FromTuple("document:readme#viewer@user:alice");
var txn = new Transaction();
txn.Touch(relationship);
var written = await client.WriteAsync(txn, cancellationToken: ct);
var result = await client.CheckAsync(new PermissionCheck(relationship.Resource, "view",
    relationship.Subject, Consistency.AtLeast(written.Revision)), cancellationToken: ct);
```

Constructors and `FromTuple` validate and throw `InvalidArgumentException`; catch it for
runtime input. Tools with their own policies configure the client once:

```csharp
await using var tool = SpiceDBClient.CreateSystemTls(endpoint, token, new ClientOptions
{
    Retries = Retries.Disabled,                // or RetryPolicy = new RetryPolicy { ..., OnRetry = e => log(e) }
    DefaultDeadline = DefaultDeadline.Disabled, // the caller's token and per-call Timeout still apply
    StreamRetries = StreamRetries.Disabled,     // never reopen any stream
});
// Or keep client-wide retries and exempt one supervised watch:
await foreach (var e in tool.UpdatesAsync([], null, new WatchOptions { RetryPolicy = RetryPolicy.None }, ct)) { /* ... */ }
var metadata = new ResponseMetadata();
await tool.ReadSchemaAsync(new SpiceDB.Client.CallOptions { Transport = new TransportOptions { ResponseMetadata = metadata } }, ct);
var trailers = metadata.Trailers; // headers and trailers of the last RPC
```

Protocol-shaped output (JSON, backups): `ProtoConvert.ToProto(relationship)` in
`SpiceDB.Client.Protocol`, then `Google.Protobuf.JsonFormatter`. Responses convert to the
server's exact wire shape, including `ProtoConvert.ToCheckBulkResponse(requests, results, options)`
for a whole bulk check with per-item error statuses. Use `RawProto()` only
when no native operation or option covers the need.

## Errors

```csharp
try { await client.WriteAsync(txn, cancellationToken: ct); }
catch (FailedPreconditionException) { /* precondition failed */ }
catch (OutOfRangeException) { /* stale ZedToken: drop it, re-read with Consistency.Full() */ }
catch (UnavailableException) { /* transient; reads were already retried */ }
catch (SpiceDBException e) when (e.StatusCode == Grpc.Core.StatusCode.PermissionDenied) { /* status interop */ }
```

`SpiceDBException.StatusCode`, `Status` and `ToRpcException()` interoperate with code that
inspects gRPC statuses; `SpiceDBException.CodeOf(ex)` classifies any exception, including
raw `RpcException`s and cancellation. Caller cancellation stays an
`OperationCanceledException`. Forward `e.ServerMessage`, not `e.Message`, when relaying an
error to another party.

## Test

```csharp
var fake = new InMemorySpiceDBClient();
var seed = new Transaction();
seed.Touch(Relationship.FromTuple("organization:acme#admin@user:alice"));
await fake.WriteAsync(seed);
fake.SetCheckResult(new ObjectRef("organization", "acme"), "view",
    SubjectRef.Direct(new ObjectRef("user", "alice")), Permissionship.HasPermission);
fake.SetCheckError(new ObjectRef("organization", "acme"), "can_admin",
    SubjectRef.Direct(new ObjectRef("user", "alice")), SpiceDBException.Create(Grpc.Core.StatusCode.Unavailable, "down"));

ISpiceDBRelationshipClient authz = fake; // production code takes the same interface; SpiceDBClient implements it
```

Inject `ISpiceDBRelationshipClient` (checks, bulk checks, writes, reads, bounded and
complete deletes, imports, exports) and `ISpiceDBSchemaClient` (read, write and reflect
schema) where code under test talks to SpiceDB; both `SpiceDBClient` and
`InMemorySpiceDBClient` implement them with identical signatures. The double applies
preconditions, CREATE conflicts, one-transaction imports and SpiceDB filter semantics
(`Filter.Matches`), honors cursors, limits and response-metadata sinks
(`SetResponseMetadata`), and throws the same exception types and messages as the real
client; unregistered checks return `NoPermission`, and `ReflectSchemaAsync` returns what
`SetReflectSchemaResult` registered. Build expected errors with
`SpiceDBException.Create(code, message)`. For integration tests, run
`authzed/spicedb serve-testing` and assert both granted and denied cases.
