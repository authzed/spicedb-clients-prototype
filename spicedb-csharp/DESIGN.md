# spicedb-csharp — Idiomatic C# Client Design

## Inherits

Root DESIGN.md (`../DESIGN.md`) — read it for the overall vision and backwards
compatibility mandate. This document takes precedence for C#-specific decisions.

## Language-Specific Goals

### Philosophy: Pit of Success

The API should make the correct thing easy and the wrong thing hard. Users
should fall into correct usage patterns by default.

### Namespace & Project Structure

Single namespace: `SpiceDB.Client`

Projects:

- **SpiceDB.Client** — the `SpiceDBClient` class and all SpiceDB operations,
  plus relationship types, filters, transactions, consistency, and errors
- **SpiceDB.Client.Tests** — xUnit + FluentAssertions unit tests

Types (Relationship, Filter, Transaction, ConsistencyStrategy) are independent
of the client. Users can construct relationships and filters without creating a
client instance.

### Constructors

Security-obvious named constructors:

- `SpiceDBClient.CreatePlaintext(endpoint, presharedKey)` — for testing, makes
  insecure connection obvious
- `SpiceDBClient.CreateSystemTls(endpoint, presharedKey)` — for production
- `SpiceDBClient.CreateFromChannel(channel, presharedKey)` — escape hatch with
  existing GrpcChannel

The client implements `IAsyncDisposable`:

```csharp
await using var client = SpiceDBClient.CreatePlaintext("localhost:50051", "token");
```

### Consistency

ZedTokens are opaque `string` values, never proto types. Consistency is an
**explicit required parameter** on every read operation — never silently
defaulted.

Static factory methods on the `Consistency` class:

- `Consistency.Full()` — fully consistent, least performant
- `Consistency.MinLatency()` — SpiceDB's preferred revision, optimal performance
- `Consistency.AtLeast(revision)` — read-after-write
- `Consistency.Snapshot(revision)` — exact revision
- `Consistency.AtLeastOrFull(revision?)` — AtLeast if non-empty, else Full
- `Consistency.AtLeastOrMinLatency(revision?)` — AtLeast if non-empty, else MinLatency

All write operations return the revision as a `string`.

### Relationships

Sealed `Relationship` record (immutable, value equality):

```csharp
public sealed record Relationship
{
    public string ResourceType { get; init; }
    public string ResourceID { get; init; }
    public string ResourceRelation { get; init; }
    public string SubjectType { get; init; }
    public string SubjectID { get; init; }
    public string SubjectRelation { get; init; }
    public string? CaveatName { get; init; }
    public IReadOnlyDictionary<string, object>? CaveatContext { get; init; }
    public DateTimeOffset? Expiration { get; init; }
}
```

Static factory methods:

- `Relationship.FromTriple(resourceType, resourceID, resourceRelation, subjectType, subjectID, subjectRelation)` — validates fields, throws `ArgumentException`
- `Relationship.FromTuple(tupleString)` — parses `type:id#relation@type:id[#relation]`, throws `FormatException`
- `Relationship.FromProto(proto)` — converts from proto type

Immutable modifiers:

- `rel.WithCaveat(name, context?)` — returns copy with caveat
- `rel.WithExpiration(dateTimeOffset)` — returns copy with expiration

Conversion:

- `rel.ToProto()` — converts to proto type
- `rel.ToFilter()` — creates a Filter matching this relationship
- `rel.ToString()` — returns tuple string representation

### Filter

Sealed `Filter` record (immutable, value equality):

```csharp
var filter = new Filter("document")
    .WithResourceID("doc1")
    .WithRelation("viewer")
    .WithSubjectType("user")
    .WithSubjectID("alice")
    .WithSubjectRelation("member");
```

Methods: `WithResourceID`, `WithResourceIDPrefix`, `WithRelation`,
`WithSubjectType`, `WithSubjectID`, `WithSubjectRelation`, `ToProto`

### Transaction

Mutable builder for batching relationship writes:

```csharp
var txn = new Transaction();
txn.Create(relationship);
txn.Touch(relationship);
txn.Delete(relationship);
txn.MustNotMatch(filter);   // precondition
txn.MustMatch(filter);      // precondition
var revision = await client.WriteAsync(txn);
```

Exposes `V1Updates` and `Preconditions` for advanced use cases.

### Checks

All checks use `BulkCheckPermissions` under the hood:

- `CheckPermissionAsync(consistency, permission, relationship)` → `Task<bool>`
- `CheckPermissionsAsync(consistency, permission, cancellationToken, params relationships)` → `Task<bool[]>`
- `CheckAnyAsync(consistency, permission, cancellationToken, params relationships)` → `Task<bool>`
- `CheckAllAsync(consistency, permission, cancellationToken, params relationships)` → `Task<bool>`

### Streaming & Transparent Cursor Pagination

`IAsyncEnumerable<T>` for all streaming RPCs. **Cursors are fully internal** —
the caller sees a single async enumerable, and the client transparently
re-fetches pages using the `AfterResultCursor` from each response.

| Method | Default page size | Notes |
|--------|------------------|-------|
| `ReadRelationshipsAsync` | 512 | cursor-based auto-pagination |
| `LookupResourcesAsync` | 512 | cursor-based auto-pagination |
| `LookupSubjectsAsync` | — | no cursor support in SpiceDB yet; single streaming call |
| `ExportRelationshipsAsync` | 512 | cursor-based auto-pagination |
| `DeleteRelationshipsAsync` | 10,000 | auto-repeats until all matched rels deleted |
| `ImportRelationshipsAsync` | 1,000 | batches into client-streaming sends |
| `UpdatesAsync` | — | server-streaming, no pagination needed |

Async enumerables:

- `ReadRelationshipsAsync(consistency, filter)` → `IAsyncEnumerable<Relationship>`
- `LookupResourcesAsync(consistency, resourceType, permission, subjectType, subjectID)` → `IAsyncEnumerable<string>`
- `LookupSubjectsAsync(consistency, resourceType, resourceID, permission, subjectType)` → `IAsyncEnumerable<string>`
- `ExportRelationshipsAsync(consistency, filter?)` → `IAsyncEnumerable<Relationship>`
- `UpdatesAsync(objectTypes?, startRevision?)` → `IAsyncEnumerable<RelationshipUpdate>`

### Writes

- `WriteAsync(transaction)` → `Task<string>` (revision)

### Deletions

- `DeleteRelationshipsAsync(filter)` → `Task<string>` (revision)

Automatically pages through large result sets using a limit of 10,000 per RPC
call. Repeats until the server reports all matching relationships are deleted.

### Schema

- `ReadSchemaAsync()` → `Task<(string Schema, string Revision)>`
- `WriteSchemaAsync(schema)` → `Task<string>` (revision)
- `ReflectSchemaAsync(consistency)` → `Task<ReflectSchemaResult>`
- `ComputablePermissionsAsync(consistency, definitionName, relationName)` → `Task<(IReadOnlyList<RelationReference>, string)>`
- `DependentRelationsAsync(consistency, definitionName, permissionName)` → `Task<(IReadOnlyList<RelationReference>, string)>`
- `DiffSchemaAsync(consistency, comparisonSchema)` → `Task<(IReadOnlyList<SchemaDiff>, string)>`

### Expand

- `ExpandPermissionTreeAsync(consistency, resourceType, resourceID, permission)` → `Task<ExpandResult>`

`ExpandResult.Tree` is a native `PermissionTree` record — the proto
`PermissionRelationshipTree` is never exposed. Exactly one of
`Intermediate`/`Leaf` is non-null on each node, mapped recursively from the
proto `tree_type` oneof.

### Bulk Import / Export

- `ImportRelationshipsAsync(IAsyncEnumerable<Relationship>)` → `Task<ulong>` (numLoaded)
- `ExportRelationshipsAsync(consistency, filter?)` → `IAsyncEnumerable<Relationship>`

### Watch

- `UpdatesAsync(objectTypes?, startRevision?)` → `IAsyncEnumerable<RelationshipUpdate>`

### Experimental — Relationship Counters

All experimental methods are marked with XML doc `<b>Experimental:</b>` notes.

- `ExperimentalRegisterRelationshipCounterAsync(name, filter)` → `Task`
- `ExperimentalCountRelationshipsAsync(name)` → `Task<(CountResult?, bool StillCalculating)>`
- `ExperimentalUnregisterRelationshipCounterAsync(name)` → `Task`

### Error Handling

Exception hierarchy rooted at `SpiceDBException`:

- `PermissionDeniedException`
- `NotFoundException`
- `AlreadyExistsException`
- `InvalidArgumentException`
- `FailedPreconditionException`
- `UnavailableException`
- `CancelledException`
- `ResourceExhaustedException`
- `DeadlineExceededException`

`ErrorMapper` static class:

- `ToSpiceDBException(RpcException)` — maps gRPC status codes to typed exceptions
- `IsTransient(Exception)` — returns true for UNAVAILABLE, DEADLINE_EXCEEDED, RESOURCE_EXHAUSTED

### Auto-Retry

Automatic retry with exponential backoff for transient gRPC errors (UNAVAILABLE,
DEADLINE_EXCEEDED, RESOURCE_EXHAUSTED). Max 5 attempts with 100ms initial
backoff, doubling each retry.

### Supporting Types

```csharp
public sealed record SchemaDefinition { Name, Comment, Relations, Permissions }
public sealed record SchemaRelation { Name, Comment, ParentDefinitionName }
public sealed record SchemaPermission { Name, Comment, ParentDefinitionName }
public sealed record SchemaCaveat { Name, Comment, Expression, Parameters }
public sealed record SchemaCaveatParameter { Name, Type, ParentCaveatName }
public sealed record ReflectSchemaResult { Definitions, Caveats, Revision }
public sealed record RelationReference { DefinitionName, RelationName, IsPermission }
public sealed record SchemaDiff { Kind, DefinitionName, RelationName, PermissionName, CaveatName }
public sealed record ExpandResult { Tree, Revision }
public sealed record CountResult { RelationshipCount, Revision }
public sealed record RelationshipUpdate { Operation, Relationship }
public enum UpdateOperation { Create = 1, Touch = 2, Delete = 3 }
public sealed record PermissionTree { ExpandedObject, ExpandedRelation, Intermediate, Leaf }
public sealed record ObjectRef { ObjectType, ObjectID }
public sealed record SubjectRef { SubjectType, SubjectID, OptionalRelation }
public sealed record IntermediateNode { Operation, Children }
public sealed record LeafNode { Subjects }
public enum TreeOperation { Unspecified, Union, Intersection, Exclusion }
```

### Escape Hatches

- `ConsistencyStrategy.V1Consistency` — exposes underlying proto type
- `Transaction.V1Updates` / `Transaction.Preconditions` — exposes underlying proto updates
- `SpiceDBClient.CreateFromChannel(channel, key)` — use existing GrpcChannel

## Changelog

See [CHANGELOG.md](CHANGELOG.md) for release notes.
