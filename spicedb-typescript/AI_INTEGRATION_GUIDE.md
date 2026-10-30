# @spicedb/client: integration guide for AI coding agents

Concrete TypeScript for the rules in the repository-wide
[AI_INTEGRATION_GUIDE.md](../AI_INTEGRATION_GUIDE.md). Read that first for
*why*; this file is *how*. Modules:

| Module | Import | Role |
|---|---|---|
| typed (generated) | your file, e.g. `./authz/permissions.gen` | schema-checked API; `TypedClient` exposes every native method |
| `@spicedb/client` | `import { ... } from "@spicedb/client"` | native client, requests, results, enums, errors |
| `protoconv` | `import { protoconv } from "@spicedb/client"` | protocol conversions (tools only) |
| `InMemorySpiceDB` | `import { InMemorySpiceDB } from "@spicedb/client"` | in-memory test double |
| `@spicedb/proto` | internal | generated Connect stubs; do not build requests with it |

`@authzed/authzed-node` can stay installed while you migrate (no registration
conflict, see [MIGRATION.md](MIGRATION.md)), but do not mix its messages into
new code.

## Generate the typed client (application code)

```bash
spicedb-gen --schema src/authz/schema.zed --lang typescript \
  --typescript.format=prettier --out src/authz/permissions.gen.ts
```

Commit the output; regenerate whenever the schema changes; never edit it.
`--typescript.format=prettier` uses your project's Prettier;
`--typescript.client-module=<specifier>` changes the import of `@spicedb/client`.
The examples below use this schema:

```zed
use expiration

definition user {}

definition organization {
    relation admin: user
    relation member: user
    relation creator: user:*
    permission can_admin = admin
    permission view = admin + member
}

definition invitation {
    relation grant: organization#can_admin
    relation inviter: user with expiration
    permission accept = grant
}
```

Naming (members are camelCase): `Organization(id)` builds a ref. Permission
`p` → property `p` (a `Permission`). Relation `r` → writer `r(subject)`, key
`rKey(subject)` (only where `r` admits caveated or expiring subjects; elsewhere
delete the writer's relationship), check property `rRelation`, filter
`rFilter()`. A userset on a permission → `pSubject()` (e.g.
`canAdminSubject()`); a userset on a relation → `r()` with no argument.
`user:*` → `UserWildcard()`. `Organization.view` is the type-level permission
for lookups. Collisions fail generation; nothing is renamed silently. To keep a
production schema whose names collide (such as `definition subject`), pass
`--typescript.rename=subject=SubjectDef`; the names sent to SpiceDB stay the
schema's.

## Connect once

```ts
import { createSpiceDBClient } from "@spicedb/client";
import { TypedClient } from "./authz/permissions.gen";

const client = createSpiceDBClient("spicedb.example.com:443", process.env.SPICEDB_TOKEN!);
await client.ready({ timeoutMs: 5000 }); // fail at startup if SpiceDB is unreachable or rejects the token
const tc = new TypedClient(client); // tc also has every native method
// ... on shutdown:
tc.close();
```

The client connects lazily; `client.ready()` makes one cheap call so a wrong
or unreachable endpoint fails at startup (`UnavailableError`,
`DeadlineExceededError`, or `UnauthenticatedError` for a bad token).
`InMemorySpiceDB.ready()` does nothing. `dns:///` and `unix:` targets are
rejected at construction: use `host:port` or an `http://`/`https://` URL.

TLS is the default. For local development pass
`{ transportSecurity: TransportSecurity.Plaintext }`; a non-loopback plaintext
target additionally needs `remoteCredentials: RemoteCredentials.AllowPlaintext`.
A private CA goes in `tls: { caCert }`. For an existing Connect `Transport`
(in-memory, a test server) use `SpiceDBClient.fromTransport(transport)`;
`close()` leaves that transport open.

An empty or whitespace-only `token` (usually an unset environment variable)
throws `InvalidArgumentError` at construction, and so do `tls.caCert`,
`tls.clientCert` or `tls.clientKey` values that do not parse, or a client key
that does not match the client certificate; the message names the option(s).

Names and IDs must be strings: a number, `null` or object as an object type or
ID, relation, permission, caveat name, filter field or cursor throws
`InvalidArgumentError` naming the field before any RPC (use `String(id)` for
numeric IDs). A revision you pass (`atLeast`, `snapshot`, watch/permission-set
start revisions) that is empty or whitespace-only throws `InvalidArgumentError`;
`atLeastOrFull`/`atLeastOrMinLatency` fall back for it as for `undefined`.

## Check (fail closed)

```ts
const result = await tc.check(minLatency(), Organization(orgId).view, User(userId));
if (!result.hasPermission()) {
    throw new ForbiddenError(`${Organization(orgId).view} denied`); // errors throw; conditional results deny
}
```

- Need to observe a write? Store `written.revision.toString()` and check with
  `atLeast(new ZedToken(stored))`.
- Reporting what was denied: `String(perm)` is `organization:acme#view`;
  `perm.resourceId` and `perm.name` are available too.
- Several at once:

```ts
const results = await tc.checkBulkTyped(minLatency(),
    checksFor(User(userId), Organization("a").view, Organization("b").view));
// results[i] matches input i; only results[i].hasPermission() grants.
```

- Permission names chosen at runtime (e.g. per API method):

```ts
const perm = Organization(orgId).permissionByName(name); // throws NotInSchemaError
```

  At startup, verify every configured name is in `OrganizationPermissionNames`.

- Type names chosen at runtime too (policy expressions, admin tools) stay
  validated against the schema:

```ts
const def = definitionByName(typeName);              // throws NotInSchemaError
const perm = def.ref(id).permissionByName(permName); // a checkable Permission
const ref = permissionRefByName(typeName, permName); // resource-less, for lookups
for await (const res of tc.lookupResourcesByName(minLatency(), typeName, permName, User(userId))) { ... }
```

  `DefinitionNames` (and the `Definitions` constants) list every definition,
  and `SchemaText` is the schema the module was generated from, so startup
  code can `writeSchema(SchemaText)` without shipping the `.zed` file.

## List what a subject can access

```ts
const ids: string[] = [];
for await (const res of tc.lookupResources(minLatency(), Organization.view, User(userId),
    { permissionshipFilter: LookupPermissionshipFilter.HasPermissionOnly })) {
    ids.push(res.resourceId);
}
```

Omit the filter only if you handle `conditionalPermission` results explicitly.

## Write atomically

```ts
const txn = new TypedTransaction()
    .mustMatch(withSubject(Organization(orgId).adminFilter(), User(otherAdmin))) // invariant: another admin remains
    .delete(Organization(orgId).admin(User(userId)))
    .touch(Organization(orgId).member(User(userId)));
try {
    const written = await tc.writeTransaction(txn); // the first construction error throws here, before any RPC
    return written.revision;
} catch (err) {
    if (err instanceof FailedPreconditionError) throw new LastAdminError();
    throw err;
}
```

- `touch` upserts; `create` fails with `AlreadyExistsError` if present.
- Delete without inventing an expiration: `.delete(Invitation(id).inviterKey(User(u)))`.
- Delete what you just read: `.delete(fromRelationship(rel))`.
- Inspect before writing: `txn.updates`, `txn.preconditions`, `txn.error`.
- `withSubject(filter, User(u))` matches direct relationships only; use
  `withSubjectAnyRelation` to include usersets such as `team:eng#member`.
- **Do not retry a failed write blindly**; it may have committed. Only a
  touch-only transaction without preconditions (`txn.isIdempotent()`, and
  `writeSchema`) may opt in per call with `{ retryPolicy }`; other mutations
  reject it. A client-wide `operationRetryPolicies: { write: policy }` retries
  only idempotent writes and leaves the others un-retried.

## Read relationships

```ts
const admins: string[] = [];
let resumeAt: string | undefined;
for await (const rel of tc.readRelationships(minLatency(), Organization(orgId).adminFilter(), { pageSize: 500 })) {
    admins.push(rel.subject.object.objectId);
    resumeAt = rel.afterResultCursor; // rel.readAt is the page's revision
}
```

To resume, pass `cursor: resumeAt` with the **same filter, consistency and
`pageSize`**. `limit` caps the total without changing page sizes. An error
thrown part-way through `readRelationships`, `lookupResources` or
`exportRelationships` carries `err.resumeCursor` (the cursor after the last
delivered item), so a caller with retries off can continue with
`{ ...request, cursor: err.resumeCursor }`. Each page request gets the client
default deadline (30 s), so a stalled server fails the read rather than
hanging it; a call's `timeoutMs` bounds the whole read.

## Schema-agnostic code (native tier)

```ts
const written = await client.write(new Transaction()
    .touch(relationshipFromTuple("document:readme#viewer@user:alice")));
const check = await client.check({
    resource: { objectType: "document", objectId: "readme" },
    permission: "view",
    subject: { object: { objectType: "user", objectId: "alice" } },
    consistency: atLeast(written.revision),
});
```

Validate runtime input before building requests; `relationshipFromTuple` and
`relationship` throw `InvalidArgumentError` on malformed text. Tools with their
own policies:

```ts
const tool = createSpiceDBClient(endpoint, token, {
    retries: Retries.Disabled,                 // or retryPolicy: { ...DEFAULT_RETRY_POLICY, onRetry: e => log(e) }
    defaultDeadline: DefaultDeadline.Disabled, // the call's own timeoutMs/signal still apply
    streamRetries: StreamRetries.Disabled,     // e.g. a supervised watch
    // Per operation, for every call (a call's own retryPolicy still wins):
    operationRetryPolicies: { watch: { ...DEFAULT_RETRY_POLICY, maxRetries: 0 } },
});
const { schema } = await tool.readSchema({ onHeader: h => log(h.get("x-request-id")), onTrailer: t => log([...t.keys()]) });
const wire = protoconv.relationshipToProto(relationshipFromTuple("document:readme#viewer@user:alice"));
// One request, as the raw API sends it (limit or unbounded; no follow-up page).
for await (const rel of tool.readRelationships({ filter: { resourceType: "document" }, consistency: minLatency(), paging: Paging.SingleRequest })) log(rel);
// Per-call retry policy: retry this idempotent touch-only write, or exempt one stream.
await tool.write(new Transaction().touch(relationshipFromTuple("document:readme#viewer@user:bob")), { retryPolicy: DEFAULT_RETRY_POLICY });
```

## Errors

```ts
try { /* ... */ } catch (err) {
    if (err instanceof FailedPreconditionError) { /* precondition failed */ }
    else if (err instanceof OutOfRangeError) { /* stale ZedToken: drop it, re-read with full() */ }
    else if (err instanceof UnavailableError) { /* transient; reads were already retried */ }
    else throw err;
}
```

A caller abort (`signal`, iterator `return()`, `close()`) rejects with
`CancelledError` (`Code.Canceled`, `name` `"AbortError"`, `cause` the signal's
reason), never a `DOMException`. A response the client cannot interpret (a
missing required field, an unset permissionship on a check, bulk-check
item or lookup result, a cursor that does not advance) fails with
`Code.Internal` and a message starting `client detected malformed response from
server:`, so it is not mistaken for a status SpiceDB sent: treat it as a bug
report, not a retry. A malformed bulk-check item fails the whole call even with
`itemErrors: BulkItemErrors.Returned`, which returns only the per-item errors
the server reports. A permissionship value newer than the client is not
malformed: it maps to `"unspecified"` and never grants.

Every native error has `code` (Connect's `Code`); `errorCode(err)` classifies
any error, including raw `ConnectError`s, and `toConnectError(err)` converts for
code written against Connect. Forward `err.serverMessage`, not `err.message`,
when relaying an error to another party.

## Test

The in-memory double does not evaluate your schema. A permission such as
`view = reader + writer + parent->read` is granted on the double only by a
stored relationship whose relation is named `view`, or by a check resolver
you install; the double never derives it from `reader`, `writer` or `parent`.
Use the double for code that consumes check and lookup results (model the
permission with the resolver), and run SpiceDB itself (`spicedb serve-testing`
or a container) for tests of the permission logic in the schema. Projects whose
permissions are all computed (unions, arrows) get little from stored-relation
grants on the double, so model them with the resolver or test against SpiceDB.

```ts
const db = new InMemorySpiceDB([relationshipFromTuple("organization:acme#view@user:alice")]);
const tc = new TypedClient(db); // the typed client wraps the double
db.setCheckResult({ resource: { objectType: "organization", objectId: "acme" }, permission: "can_admin",
    subject: { object: { objectType: "user", objectId: "alice" } } }, "hasPermission");
db.setCheckResolver(check => check.resource.objectId.startsWith("tmp-") ? "hasPermission" : undefined);
// or grant everything: db.setCheckResolver(InMemorySpiceDB.grantAll);
const authz: Pick<SpiceDBClient, "write" | "check" | "readRelationships"> = db; // or a real client
```

Depend on `SpiceDBClientInterface` (the client's full method set) or a
`Pick<SpiceDBClient, ...>` of the methods you use; both `SpiceDBClient` and
`InMemorySpiceDB` implement it, and a generated `TypedClient` accepts either.
The double does not evaluate a schema. It resolves a check in this order: the
result registered with `setCheckResult` for that exact check; then the
resolver set with `setCheckResolver` (it sees the call's default context merged
in; return `undefined` to fall through; inject failures with the fault hook); then
a stored relationship on the resource whose relation is the permission's name
and whose subject matches exactly or by wildcard (caveated:
`conditionalPermission`); otherwise `noPermission`. `lookupResources` and
`lookupSubjects` enumerate the objects of the requested type found in stored
relationships and yield those that resolution grants, honoring the
permissionship filter, wildcards, `limit` and `cursor`. The double also works
in memory for `write`, `readRelationships`, `deleteRelationships`,
`deleteAllRelationshipsNonAtomic`, `importRelationships`,
`exportRelationships`, `readSchema`, `writeSchema`, `reflectSchema` (register
its result with `setReflectSchemaResult`), `diffSchema` (a name-level diff of
the stored schema text: definitions, relations and subject types, permissions
and their expressions, caveats; no doc-comment changes), `checkBulk` and
`close`; `ready` does nothing; its other methods are recorded and fail with
`Code.Unimplemented`. `ZedToken` keeps its text in the readonly `token` field,
so `expect(a).toEqual(b)` compares tokens by value.
`InMemorySpiceDB.cursorAfter(n)` builds a resume cursor after n matching (or,
for lookups, granted) items, and `db.setReadSchemaRevision(SchemaRevision.Omitted)`
emulates servers whose schema reads report no revision. Its errors have the real client's classes, codes and
message shape; it honors cursors, limits, the export filter and
`onHeader`/`onTrailer` (see `setResponseMetadata`). Build expected errors with
`SpiceDBError.fromCode(Code.FailedPrecondition, "...")`, or
`InMemorySpiceDB.serverError(code, message)` for one shaped like a server
failure. It records every call (`db.calls`, `db.callsOf("write")`: operation,
index, request, options) and injects faults: `db.failNext("check", err)` fails
one call; `db.setFaultHook(f => ...)` is consulted at `FaultStage.Call`,
`ImportBatch` (`f.item` is the batch index, `f.batch` its relationships), `ImportCommit` and `Item` (each
read, export or lookup item), and a failed write or import applies nothing.
A fault injected into `readRelationships`, `exportRelationships` or
`lookupResources` carries `resumeCursor` exactly as the real client's error
does (the last delivered item's cursor, else the request's), so resume logic
can be tested on the double. The double never answers with an `"unspecified"`
permissionship: `setCheckResult` rejects it, and a resolver returning it fails
the call with `InvalidArgumentError`.

```ts
db.setFaultHook(f => f.call.operation === "importRelationships" && f.stage === FaultStage.ImportBatch && f.item === 2
    ? InMemorySpiceDB.serverError(Code.AlreadyExists, "duplicate relationship") : undefined);
```

For integration tests,
run `authzed/spicedb serve-testing` and assert both granted and denied cases.
