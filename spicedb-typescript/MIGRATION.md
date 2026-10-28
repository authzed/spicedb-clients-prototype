# Migrating from `@authzed/authzed-node`

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
| Deadline | 30 s on every RPC, streams included (a built-in `deadlineInterceptor`), unless the call sets its own | 30 s per unary operation (attempts, backoff, chunks and pages share it); streams, watch and imports have no default lifetime limit; `defaultDeadline: DefaultDeadline.Disabled` turns the unary default off |
| Retries | none | idempotent reads retry Unavailable/Aborted up to 3 times (full jitter from 100 ms); never mutations. `retries: Retries.Disabled` or a `retryPolicy` changes it |
| Streams | `promises.*` collect a stream into an array | `AsyncIterableIterator`, paged automatically; break to cancel |
| Check result | `permissionship` enum compared by hand | `CheckResult.hasPermission()` is true only for an unconditional grant |
| Errors | grpc-js `ServiceError` with numeric `.code` | `SpiceDBError` subclasses with `.code` (Connect `Code`), `.serverMessage`, `.reason` |
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

`NewClientWithChannelCredentials` has no direct equivalent; build a Connect
transport yourself and use `SpiceDBClient.fromTransport(transport)`. The client
never closes a transport you supply. A custom `deadlineInterceptor(ms)` becomes
`defaultTimeoutMs: ms`; a long stream that relied on the old 30 s cut-off now
needs an explicit `timeoutMs` or `signal`.

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
those idempotent writes. Other mutations reject the opt-in.

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

## Raw protocol access

If a call has no native equivalent yet, `client.raw()` returns the
authenticated Connect clients (`raw().permissions.checkPermission(...)`). Prefer
`CallOptions.onHeader`/`onTrailer` for response metadata and the `protoconv`
namespace for protocol-shaped output before reaching for it.
