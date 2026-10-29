# Migrating from `@authzed/authzed-node`

## Requirements

| Requirement | Floor | Notes |
|---|---|---|
| Node.js | 20.19 | The package is ESM; 20.19 is the first Node 20 release where CommonJS code can `require()` ESM without a flag. |
| SpiceDB | 1.30 | Older servers' trailer-only data is reachable through `onTrailer`. |
| `@bufbuild/protobuf` | ^2.11.0 | Protobuf runtime, matching the code generator the protocol stubs were built with. Installed as a dependency. |
| `@connectrpc/connect` | ^2.0.0 | RPC runtime. Installed as a dependency. |
| `@connectrpc/connect-node` | ^2.0.0 | Node HTTP/2 transport, through `@spicedb/proto`. |
| `@grpc/grpc-js` | not used | authzed-node's transport. Remove it once nothing else imports it; it can stay installed during the migration. |
| `@types/node` | >=20.19.0 | Optional peer, for TypeScript projects. |
| TypeScript | built and tested with 7.x | The package is ESM with `.d.ts` types; resolve it with `moduleResolution: "bundler"`, as this repository does. |

Before the package is published, install it from tarballs built from this
repository; see [README.md](README.md#before-publication).

`@spicedb/client` replaces `@authzed/authzed-node` for Node.js applications.
Both can be installed in the same process (and the same bundle) while you move
call sites over: their protobuf runtimes keep no global registry, so there is
no registration conflict (see `proto-clients/spicedb-typescript-proto`'s
`legacy-coexistence.test.ts`). Move one call at a time.

If your application owns its schema, generate a typed client with
`spicedb-gen --lang typescript` instead of writing native requests; see
[AI_INTEGRATION_GUIDE.md](AI_INTEGRATION_GUIDE.md). The snippets below use the
native (schema-agnostic) client.

## Behavior changes at a glance

| Area | authzed-node | @spicedb/client |
|---|---|---|
| Requests | protobuf messages (`v1.CheckPermissionRequest.create({...})`) | native objects (`{ resource, permission, subject, consistency }`) |
| Consistency | optional; omitted means server default (minimize latency) | **required** on every read: `full()`, `minLatency()`, `atLeast(token)`, `snapshot(token)` |
| Tokens | `ZedToken` messages / strings | `ZedToken` class; persist with `token.toString()`, restore with `new ZedToken(text)` |
| Deadline | 30 s on every RPC, streams included (a built-in `deadlineInterceptor`), unless the call sets its own | 30 s per unary operation (attempts, backoff and chunks share it); finite paged reads (`readRelationships`, lookups, exports) get 30 s per page request, so a stalled server fails the read; watch and imports have no default; a call's `timeoutMs` bounds the whole call; `defaultDeadline: DefaultDeadline.Disabled` turns the default off |
| Retries | none | idempotent reads retry Unavailable/Aborted up to 3 times (full jitter from 100 ms); never mutations. `retries: Retries.Disabled` or a `retryPolicy` changes it |
| Streams | `promises.*` collect a stream into an array | `AsyncIterableIterator`, paged automatically; break to cancel |
| Check result | `permissionship` enum compared by hand | `CheckResult.hasPermission()` is true only for an unconditional grant |
| Errors | grpc-js `ServiceError` with numeric `.code` | `SpiceDBError` subclasses with `.code` (Connect `Code`), `.serverMessage`, `.reason`; a local abort is `CancelledError` (`Code.Canceled`, `name` `"AbortError"`), not a `DOMException`; a response the client cannot interpret is `Code.Internal`, never retried by default |
| Plaintext | `ClientSecurity.INSECURE_PLAINTEXT_CREDENTIALS` | `TransportSecurity.Plaintext`; non-loopback also needs `RemoteCredentials.AllowPlaintext` |
| Transport | `@grpc/grpc-js` | Connect over Node HTTP/2 (`@connectrpc/connect-node`) |
| Minimum SpiceDB | any | 1.30 (older trailer-only data is reachable through `onTrailer`) |

## Constructing a client

Before:

```ts
import { v1 } from "@authzed/authzed-node";
const client = v1.NewClient(token, "spicedb.example.com:443", v1.ClientSecurity.SECURE);
const local = v1.NewClient(token, "localhost:50051", v1.ClientSecurity.INSECURE_LOCALHOST_ALLOWED);
const custom = v1.NewClientWithCustomCert(token, "spicedb.internal:443", caBuffer);
```

After (note the argument order: endpoint, then token):

```ts
import { createSpiceDBClient, TransportSecurity, RemoteCredentials } from "@spicedb/client";
const client = createSpiceDBClient("spicedb.example.com:443", token);
const local = createSpiceDBClient("localhost:50051", token, { transportSecurity: TransportSecurity.Plaintext });
const custom = createSpiceDBClient("spicedb.internal:443", token, { tls: { caCert: caBuffer } });
// Remote plaintext is an explicit, named opt-in:
const remote = createSpiceDBClient("spicedb.internal:50051", token, {
    transportSecurity: TransportSecurity.Plaintext,
    remoteCredentials: RemoteCredentials.AllowPlaintext,
});
```

The endpoint may also be a URL: `http://host:port` selects plaintext and
`https://host[:port]` selects TLS (port 443 by default) when
`transportSecurity` is omitted, and must agree with it when given. A URL with
a path, query, fragment or userinfo, a plaintext endpoint without a port
(`localhost` or `http://localhost`; write `localhost:50051`), a
non-numeric port and any other scheme are refused at construction with an
`InvalidArgumentError` naming the problem; `@authzed/authzed-node` accepted
only `host:port`.

Like authzed-node, the client connects lazily. To fail at startup on a wrong
or unreachable endpoint, `await client.ready({ timeoutMs })`: it rejects with
`UnavailableError`, `DeadlineExceededError` or `UnauthenticatedError`.
grpc-js targets such as `dns:///spicedb:50051` or `unix:/path` are rejected at
construction (Connect over Node HTTP/2 supports neither); use `spicedb:50051`
or an `http://`/`https://` URL, and put a load balancer or DNS round-robin in
front of SpiceDB instead of client-side balancing.

`NewClientWithChannelCredentials` has no direct equivalent; build a Connect
transport yourself and use `SpiceDBClient.fromTransport(transport)`. The client
never closes a transport you supply.

A custom `deadlineInterceptor(ms)` is **not** the same as `defaultTimeoutMs: ms`.
The interceptor put `ms` on every RPC, streams included. `defaultTimeoutMs`
is the total budget of a unary operation (its attempts, backoff and bulk
chunks share it) and the budget of each page request of a finite paged read
(`readRelationships`, `lookupResources`, `lookupSubjects`, exports: from
opening the page to its last item, not counting your loop body), and it never
applies to `watch` or imports. Set `defaultTimeoutMs: ms` for the per-request
part, and pass `timeoutMs` on a call that needs a total bound, such as a
long-lived stream that relied on the old cut-off.

## Checks

Before:

```ts
const resp = await client.promises.checkPermission(v1.CheckPermissionRequest.create({
    resource: v1.ObjectReference.create({ objectType: "document", objectId: "readme" }),
    permission: "view",
    subject: v1.SubjectReference.create({ object: v1.ObjectReference.create({ objectType: "user", objectId: "alice" }) }),
}));
const allowed = resp.permissionship === v1.CheckPermissionResponse_Permissionship.HAS_PERMISSION;
```

After:

```ts
const result = await client.check({
    resource: { objectType: "document", objectId: "readme" },
    permission: "view",
    subject: { object: { objectType: "user", objectId: "alice" } },
    consistency: minLatency(),
});
const allowed = result.hasPermission(); // conditional and unknown results never grant
```

Bulk checks take an array and return results in input order:
`client.checkBulk(requests, options)`. Every request must use the same
consistency. A per-item error fails the call by default (with the item's
`inputIndex`); `{ itemErrors: BulkItemErrors.Returned }` returns it on
`CheckResult.error` instead.

## Writes

Before:

```ts
await client.promises.writeRelationships(v1.WriteRelationshipsRequest.create({
    updates: [v1.RelationshipUpdate.create({
        operation: v1.RelationshipUpdate_Operation.TOUCH,
        relationship: v1.Relationship.create({ resource, relation: "viewer", subject }),
    })],
}));
```

After:

```ts
const { revision } = await client.write(new Transaction()
    .mustNotMatch({ resourceType: "document", resourceId: "readme", relation: "owner" })
    .touch(relationship("document:readme", "viewer", "user:alice")));
```

`write` returns `WriteResult`; take `revision` explicitly. An empty transaction
is sent as authzed-node sends it (SpiceDB checks its preconditions and returns
the current revision). Writes are not retried by default: a failed write may
have committed. A touch-only transaction without preconditions, and
`writeSchema`, are idempotent on replay and may opt in for one call with
`{ retryPolicy }`; every other mutation rejects that opt-in. `deleteRelationships(filter, { limit })`
needs a positive limit (or `partial: DeletePartial.Disallowed` for the server's
all-or-nothing behavior); deleting everything in batches is
`deleteAllRelationshipsNonAtomic(filter)`.

## Streams and paging

Before (the whole stream buffered into an array):

```ts
const responses = await client.promises.readRelationships(v1.ReadRelationshipsRequest.create({ relationshipFilter }));
for (const r of responses) use(r.relationship);
```

After (incremental, paged automatically):

```ts
for await (const rel of client.readRelationships({ filter: { resourceType: "document" }, consistency: full() })) {
    use(rel, rel.afterResultCursor, rel.readAt);
}
```

To resume, pass `cursor: rel.afterResultCursor` with the same filter,
consistency and `pageSize`. `lookupResources`, `lookupSubjects`,
`exportRelationships` and `watch` work the same way.

A read that fails part-way (with retries off, or once they are spent) carries
the cursor to resume from on the error, so you need not track it yourself:

```ts
try {
    for await (const item of client.lookupResources(request)) use(item);
} catch (err) {
    if (err instanceof SpiceDBError && err.resumeCursor !== undefined)
        for await (const item of client.lookupResources({ ...request, cursor: err.resumeCursor })) use(item);
    else throw err;
}
```

`resumeCursor` is set on errors from `readRelationships`, `lookupResources`
and `exportRelationships` (after the last fully delivered export batch). It
is the request's own `cursor` when nothing was delivered, and absent when the
read should start over. `lookupSubjects` has no cursor (SpiceDB does not page
it). Faults injected into `InMemorySpiceDB` carry the same `resumeCursor`.

Each page request of these finite reads gets the client default deadline
(30 s), so a server that stalls mid-read fails the read with
`DeadlineExceededError` instead of hanging it.

A raw `readRelationships`/`lookupResources` call without a limit is one
unbounded request; this client pages by default. To reproduce the raw call
(and its final cursor), pass `paging: Paging.SingleRequest`: exactly one
request, with `limit` (or unbounded) as its limit and `pageSize` ignored.

## Consistency on schema reads

Every read that takes a consistency requires one, including `reflectSchema`,
`computablePermissions`, `dependentRelations` and `diffSchema`. Where your raw
requests left consistency unset, pass `minLatency()`, which is what SpiceDB
applies when it is absent.

## Retries for tools with their own policy

Disable or replace the client's policy client-wide (`retries:
Retries.Disabled`, `retryPolicy`), and override it for one call with
`{ retryPolicy }` in that call's options: for example
`{ ...DEFAULT_RETRY_POLICY, maxRetries: 0 }` on one read stream to never reopen
it, a policy on one `watch` (opened once by default) to reopen it before its
first event, or a policy on a touch-only `write` or a `writeSchema` to retry
those idempotent writes. Other mutations reject the opt-in. To apply a policy
to every call of one operation, set `operationRetryPolicies` on the client
(`{ watch: { ...DEFAULT_RETRY_POLICY, maxRetries: 0 } }`); a call's own policy
still wins. For `write` it retries only idempotent transactions
(`Transaction.isIdempotent()`), and operations that are never idempotent
(deletes, delete-all, imports, counter registration) reject a policy with
retries when the client is built.

## Errors

Before:

```ts
import { status } from "@grpc/grpc-js";
try { ... } catch (e) { if ((e as ServiceError).code === status.FAILED_PRECONDITION) { ... } }
```

After:

```ts
try { ... } catch (e) {
    if (e instanceof FailedPreconditionError) { ... }
    // or, for code that also sees raw Connect errors:
    if (errorCode(e) === Code.FailedPrecondition) { ... }
}
```

`err.message` keeps Connect's `[code]` prefix; forward `err.serverMessage` to
reproduce exactly what SpiceDB said. `toConnectError(err)` returns a
`ConnectError` with the matching code for code written against Connect.

A response the client cannot interpret is a `SpiceDBError` coded
`Code.Internal` whose message starts `client detected malformed response from
server:` and which has no `serverMessage`. That includes a check, bulk-check
item or lookup result whose permissionship is unspecified: `authzed-node`
returned it as `PERMISSIONSHIP_UNSPECIFIED`, which a `=== HAS_PERMISSION`
test read as a denial.

## Accessors

Responses are native, immutable data:

| authzed-node | @spicedb/client |
|---|---|
| `resp.relationship?.resource?.objectId` | `rel.resource.objectId` |
| `resp.relationship?.subject?.object?.objectId` | `rel.subject.object.objectId` |
| `resp.relationship?.subject?.optionalRelation` (`""` when direct) | `rel.subject.relation` (`undefined` when direct) |
| `resp.checkedAt?.token` | `result.checkedAt` (`ZedToken`) |
| `resp.resourceObjectId` | `item.resourceId` |
| `v1.PbStruct.fromJson(ctx)` | plain JSON objects (`context: { key: "value" }`) |

## Renamed members

Request and response members that changed name or shape. Members not listed
keep their protobuf name (`resource`, `permission`, `subject`, `consistency`,
`context`, `permissionship`, `afterResultCursor`, `lookedUpAt`).

| authzed-node (protobuf) | @spicedb/client |
|---|---|
| `client.promises.checkPermission(req)` | `client.check(req)` |
| `client.promises.checkBulkPermissions({ items })` | `client.checkBulk(requests)`; results in input order, not `pairs` |
| `CheckPermissionRequest.withTracing` | `CheckOptions.tracing: Tracing.Enabled` |
| `CheckPermissionResponse.partialCaveatInfo.missingRequiredContext` | `CheckResult.missingContext` |
| `CheckPermissionResponse.debugTrace` | `CheckResult.debugTrace` (native trace) |
| `CheckPermissionResponse.optionalExpiresAt` | `CheckResult.expiration` (`Date`) |
| `CheckBulkPermissionsPair.error` | thrown, or `CheckResult.error` with `itemErrors: BulkItemErrors.Returned` |
| `LookupResourcesRequest.resourceObjectType` | `resourceType` |
| `LookupResourcesRequest.optionalLimit` | `limit` (total) and `pageSize` (per request) |
| `LookupResourcesRequest.optionalCursor.token` | `cursor` (string) |
| `LookupResourcesResponse.resourceObjectId` | `LookupResource.resourceId` |
| `LookupSubjectsRequest.subjectObjectType` | `subjectType` |
| `LookupSubjectsRequest.optionalSubjectRelation` | `subjectRelation` |
| `LookupSubjectsRequest.optionalConcreteLimit` | `limit` |
| `LookupSubjectsRequest.wildcardOption` | `wildcards: Wildcards.Include / Exclude` |
| `LookupSubjectsResponse.subject.subjectObjectId` | `LookupSubject.subject.subjectId` |
| `ReadRelationshipsRequest.relationshipFilter` | `filter` |
| `ReadRelationshipsRequest.optionalLimit` | `limit` (total) and `pageSize` (per request) |
| `ReadRelationshipsResponse.relationship.*` | the relationship's fields directly on `ReadRelationship` |
| `ReadRelationshipsResponse.readAt` | `ReadRelationship.readAt` (`ZedToken`) |
| `RelationshipFilter.optionalResourceId` | `resourceId` |
| `RelationshipFilter.optionalResourceIdPrefix` | `resourceIdPrefix` |
| `RelationshipFilter.optionalRelation` | `relation` |
| `RelationshipFilter.optionalSubjectFilter` | `subject` |
| `SubjectFilter.optionalSubjectId` | `subjectId` |
| `SubjectFilter.optionalRelation.relation` | `relation: { kind: SubjectRelationKind.Named, relation }` (`Direct`, `Any`) |
| `SubjectReference.optionalRelation` | `SubjectRef.relation` |
| `Relationship.optionalCaveat.caveatName` / `.context` | `Relationship.caveat.name` / `.context` |
| `Relationship.optionalExpiresAt` | `Relationship.expiration` (`Date`) |
| `WriteRelationshipsRequest.updates` / `optionalPreconditions` | `Transaction.touch/create/delete` and `mustMatch/mustNotMatch` |
| `WriteRelationshipsRequest.optionalTransactionMetadata` | `Transaction.withMetadata(json)` |
| `WriteRelationshipsResponse.writtenAt` | `WriteResult.revision` |
| `DeleteRelationshipsRequest.relationshipFilter` | the `filter` argument |
| `DeleteRelationshipsRequest.optionalLimit` / `optionalAllowPartialDeletions` | `DeleteOptions.limit` / `partial: DeletePartial.Allowed or Disallowed` |
| `DeleteRelationshipsRequest.optionalPreconditions` | `DeleteOptions.mustMatch` / `mustNotMatch` |
| `DeleteRelationshipsResponse.deletedAt` | `DeleteResult.revision` |
| `DeleteRelationshipsResponse.relationshipsDeletedCount` | `DeleteResult.deletedCount` (`bigint`) |
| `DeleteRelationshipsResponse.deletionProgress` | `DeleteResult.progress` (`"complete"` / `"partial"` / `"unspecified"` when the server reports no progress) |
| `ReadSchemaResponse.schemaText` / `readAt` | `SchemaResult.schema` / `revision` |
| `WriteSchemaResponse.writtenAt` | the `ZedToken` `writeSchema` returns |
| `WatchRequest.optionalObjectTypes` | `objectTypes` |
| `WatchRequest.optionalStartCursor` | `startRevision` (`ZedToken`) |
| `WatchRequest.optionalRelationshipFilters` | `relationshipFilters` |
| `WatchRequest.optionalUpdateKinds` | `content: [WatchContent.Relationships, ...]` |
| `WatchResponse.changesThrough` | `WatchEvent.changesThrough` (`ZedToken`) |
| `ExpandPermissionTreeResponse.expandedAt` | `ExpandResult.revision` |
| `ZedToken.token` | `ZedToken.token` (a readonly field; `toString()` returns the same text) |
| `ReflectionSchemaDiff` (oneof case) | `SchemaDiff.kind` (`SchemaDiffKind` union, e.g. `"permission_expr_changed"`) |

## Raw protocol access

If a call has no native equivalent yet, `client.raw()` returns the
authenticated Connect clients (`raw().permissions.checkPermission(...)`). Prefer
`CallOptions.onHeader`/`onTrailer` for response metadata and the `protoconv`
namespace for protocol-shaped output before reaching for it.
