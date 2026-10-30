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

An empty or whitespace-only preshared key (usually an unset environment
variable) throws `InvalidArgumentException` at construction.

`SpiceDB.Client` shares type names with two namespaces its callers often import:

- `Grpc.Core`: `CallOptions` and `WriteOptions`.
- `Authzed.Api.V1` (the protocol types, for example in code moving off `authzed-dotnet` or
  using the raw escape hatch): `Relationship`, `Consistency`, `ZedToken`, `Precondition`,
  `RelationshipUpdate`, `ResolvedSubject`, `PartialCaveatInfo`, `DebugInformation` and
  `CheckDebugTrace`.

A file that imports `SpiceDB.Client` and either of them fails with CS0104 wherever it writes
one of those names. Aliasing only one side does not help, because both namespace imports stay
in scope. Alias the bare name to the type you mean (a using-alias takes precedence over
namespace imports), and give the other type a prefixed alias. A file that imports all three
can copy this block whole; keep the lines for the namespaces it imports:

```csharp
using Authzed.Api.V1;
using Grpc.Core;
using SpiceDB.Client;

// Shared with Grpc.Core
using CallOptions = SpiceDB.Client.CallOptions;
using WriteOptions = SpiceDB.Client.WriteOptions;
using GrpcCallOptions = Grpc.Core.CallOptions;   // CallInvoker overrides
using GrpcWriteOptions = Grpc.Core.WriteOptions;

// Shared with Authzed.Api.V1
using Relationship = SpiceDB.Client.Relationship;
using Consistency = SpiceDB.Client.Consistency;
using ZedToken = SpiceDB.Client.ZedToken;
using Precondition = SpiceDB.Client.Precondition;
using RelationshipUpdate = SpiceDB.Client.RelationshipUpdate;
using ResolvedSubject = SpiceDB.Client.ResolvedSubject;
using PartialCaveatInfo = SpiceDB.Client.PartialCaveatInfo;
using DebugInformation = SpiceDB.Client.DebugInformation;
using CheckDebugTrace = SpiceDB.Client.CheckDebugTrace;
using ProtoRelationship = Authzed.Api.V1.Relationship;
using ProtoConsistency = Authzed.Api.V1.Consistency;
using ProtoZedToken = Authzed.Api.V1.ZedToken;
using ProtoPrecondition = Authzed.Api.V1.Precondition;
using ProtoRelationshipUpdate = Authzed.Api.V1.RelationshipUpdate;
using ProtoResolvedSubject = Authzed.Api.V1.ResolvedSubject;
using ProtoPartialCaveatInfo = Authzed.Api.V1.PartialCaveatInfo;
using ProtoDebugInformation = Authzed.Api.V1.DebugInformation;
using ProtoCheckDebugTrace = Authzed.Api.V1.CheckDebugTrace;
```

`NamespaceCollisionTests` finds these names by reflection and fails when a new one appears, and
`CallOptionsAliasTests` compiles a file with exactly this block.

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
  `Consistency.AtLeast(new ZedToken(stored))`. An empty or whitespace-only stored token
  throws `InvalidArgumentException`; `Consistency.AtLeastOrFull(...)` falls back instead.
  Likewise pass `null`, not an empty token, for no watch start revision.
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
  `results[i].Error` instead of throwing for the first one. A malformed item in the
  server's response (such as an unset permissionship) still fails the whole call.
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
    ids.Add(resource.ResourceId);
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
  `txn.IsIdempotent` reports whether a transaction qualifies, by the client's own rule.
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
runtime input. It is a `SpiceDBException`, not an `ArgumentException` or `FormatException`,
so `catch (ArgumentException)` does not catch it. Endpoints are `host:port`, `http://`,
`https://`, `dns:///host:port` or `unix:/path` (plaintext). The client connects lazily: call
`await client.ConnectAsync(new ConnectOptions { Timeout = TimeSpan.FromSeconds(5) }, ct)` at
startup to fail fast on a wrong endpoint or key. Reads, lookups and exports apply the client
default deadline to each page (time spent processing items does not count); pass a per-call
`Timeout` to bound the whole read. Tools with
their own policies configure the client once:

```csharp
await using var tool = SpiceDBClient.CreateSystemTls(endpoint, token, new ClientOptions
{
    Retries = Retries.Disabled,                // or RetryPolicy = new RetryPolicy { ..., OnRetry = e => log(e) }
    DefaultDeadline = DefaultDeadline.Disabled, // the caller's token and per-call Timeout still apply
    StreamRetries = StreamRetries.Disabled,     // never reopen any stream
});
// Watches are never reopened by the client-wide policy (a reopened watch would skip changes);
// resume from the last event's revision yourself, or opt one watch in to reopening before its
// first event:
await foreach (var e in tool.UpdatesAsync([], null, new WatchOptions { RetryPolicy = RetryPolicy.Default }, ct)) { /* ... */ }
// Or set one operation's policy for every call (per call > per operation > client-wide);
// a Write policy retries only idempotent writes, and deletes/imports reject a retrying one:
//   new ClientOptions { OperationRetryPolicies = new Dictionary<SpiceDBOperation, RetryPolicy> { [SpiceDBOperation.Updates] = RetryPolicy.None } }
var metadata = new ResponseMetadata();
await tool.ReadSchemaAsync(new SpiceDB.Client.CallOptions { Transport = new TransportOptions { ResponseMetadata = metadata } }, ct);
var trailers = metadata.Trailers; // headers and trailers of the last RPC
```

Protocol-shaped output (JSON, backups): `ProtoConvert.ToProto(relationship)` in
`SpiceDB.Client.Protocol`, then `Google.Protobuf.JsonFormatter`. Responses convert to the
server's exact wire shape, including `ProtoConvert.ToCheckBulkResponse(requests, results, options)`
for a whole bulk check with per-item error statuses. Use `RawProto()` only
when no native operation or option covers the need.

Native results serialize directly with `System.Text.Json`: `JsonSerializer.Serialize(result)`
writes enums as names (`"Permissionship":"HasPermission"`) and `ZedToken` as its string, so
tool output needs no hand-built DTO. A direct subject is one step from an object: an
`ObjectRef` converts implicitly to `SubjectRef`, and a relation of `...` (as user input and
tuples spell a direct subject) means direct in `SubjectRef.Named` and
`SubjectRelationFilter.Named`.

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
error to another party; it is never null (for local errors it equals `e.Message`). Native
errors are not `RpcException`s: catch `SpiceDBException`, never `RpcException`, around
client calls.

## Test

The in-memory double does not evaluate your schema. A permission such as
`view = reader + writer + parent->read` is granted on the double only by a
stored relationship whose relation is named `view`, or by a check resolver
you install; the double never derives it from `reader`, `writer` or `parent`.
Use the double for code that consumes check and lookup results (model the
permission with the resolver), and run SpiceDB itself (`spicedb serve-testing`
or a container) for tests of the permission logic in the schema. Projects
whose permissions are all computed (unions, arrows) get little from
stored-relation grants on the double, so model them with the resolver or test
against SpiceDB.

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
ISpiceDBClient full = fake; // or the client's full surface; operations the double cannot run fail as Unimplemented

// Checks over IDs generated at run time: a resolver, or grant everything.
fake.SetCheckResolver(check => check.Permission == "view" ? new CheckResult(Permissionship.HasPermission, [], default) : null);
fake.GrantAllChecks(); // same as fake.SetCheckResolver(InMemorySpiceDBClient.GrantAll)
// Lookups run over the stored relationships through the same check resolution.
await foreach (var org in fake.LookupResourcesAsync(Consistency.Full(), "organization", "admin", new ObjectRef("user", "alice"))) { }

// Failure paths without a fake server: fail one call, or fail at a defined point.
fake.FailNext(SpiceDBOperation.Check, InMemorySpiceDBClient.ServerError(Grpc.Core.StatusCode.Unavailable, "down"));
fake.SetFaultHook(f => f is { Stage: FaultStage.ImportBatch, Item: 2 }
    ? InMemorySpiceDBClient.ServerError(Grpc.Core.StatusCode.AlreadyExists, "duplicate") : null); // nothing is applied
// Assert on what was sent without a strict mock:
var read = fake.CallsOf(SpiceDBOperation.ReadRelationships)[0];
var cursor = read.OptionsAs<ReadOptions>()?.ResumeCursor; var filter = read.Argument<Filter>();
// Import-batch faults see the batch; CursorAfter(n) is the cursor the double puts on item n.
fake.SetFaultHook(f => f.Batch.Any(r => r.Resource.ObjectType == "banned")
    ? InMemorySpiceDBClient.ServerError(Grpc.Core.StatusCode.InvalidArgument, "bad batch") : null);
var resume = new ReadOptions { ResumeCursor = InMemorySpiceDBClient.CursorAfter(2) };
fake.SetReadSchemaRevision(SchemaRevision.Omitted); // schema reads report no revision
```

Inject `ISpiceDBRelationshipClient` (checks, bulk checks, writes, reads, bounded and
complete deletes, imports, exports) and `ISpiceDBSchemaClient` (read, write and reflect
schema) where code under test talks to SpiceDB; both `SpiceDBClient` and
`InMemorySpiceDBClient` implement them with identical signatures. `ISpiceDBClient` extends
both with the rest of the client (lookups, watch, introspection, experimental operations,
`RawProto`, disposal); the double implements it too, serving lookups and failing operations
it has no in-memory semantics for (watch, introspection, expansion, experimental operations)
with `StatusCode.Unimplemented` after recording them. The double applies
preconditions, CREATE conflicts, one-transaction imports and SpiceDB filter semantics
(`Filter.Matches`), honors cursors, limits and response-metadata sinks
(`SetResponseMetadata`), and throws the same exception types and messages as the real
client. A check resolves to a registered result or error, then the resolver (null declines),
then a stored relationship whose relation is the permission's name and whose subject matches
exactly or by wildcard (conditional if caveated), then `NoPermission`; it never evaluates a
schema. `LookupResourcesAsync` and `LookupSubjectsAsync` yield the known objects of the type
that this resolution grants. `ReflectSchemaAsync` returns what
`SetReflectSchemaResult` registered. Build expected errors with
`SpiceDBException.Create(code, message)` or `InMemorySpiceDBClient.ServerError(code, message, reason)`.
Every call is recorded (`Calls`, `CallsOf`), and fault hooks run before each call, import
batch, import commit and read, export or lookup item; a failed write or import applies nothing. For integration tests, run
`authzed/spicedb serve-testing` and assert both granted and denied cases.
