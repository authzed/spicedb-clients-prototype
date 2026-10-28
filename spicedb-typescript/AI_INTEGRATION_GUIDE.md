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
const tc = new TypedClient(client); // tc also has every native method
// ... on shutdown:
tc.close();
```

TLS is the default. For local development pass
`{ transportSecurity: TransportSecurity.Plaintext }`; a non-loopback plaintext
target additionally needs `remoteCredentials: RemoteCredentials.AllowPlaintext`.
A private CA goes in `tls: { caCert }`. For an existing Connect `Transport`
(in-memory, a test server) use `SpiceDBClient.fromTransport(transport)`;
`close()` leaves that transport open.

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
  touch-only transaction without preconditions (and `writeSchema`) may opt in
  per call with `{ retryPolicy }`; other mutations reject it.

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
`pageSize`**. `limit` caps the total without changing page sizes.

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

Every native error has `code` (Connect's `Code`); `errorCode(err)` classifies
any error, including raw `ConnectError`s, and `toConnectError(err)` converts for
code written against Connect. Forward `err.serverMessage`, not `err.message`,
when relaying an error to another party.

## Test

```ts
const db = new InMemorySpiceDB([relationshipFromTuple("organization:acme#admin@user:alice")]);
db.setCheckResult({ resource: { objectType: "organization", objectId: "acme" }, permission: "view",
    subject: { object: { objectType: "user", objectId: "alice" } } }, "hasPermission");
const authz: Pick<SpiceDBClient, "write" | "check" | "readRelationships"> = db; // or a real client
```

Depend on a `Pick<SpiceDBClient, ...>` of the methods you use; both
`SpiceDBClient` and `InMemorySpiceDB` satisfy it for `write`,
`readRelationships`, `deleteRelationships`, `deleteAllRelationshipsNonAtomic`,
`importRelationships`, `exportRelationships`, `readSchema`, `writeSchema`,
`reflectSchema` (register its result with `setReflectSchemaResult`), `check`,
`checkBulk` and `close`. Its errors have the real client's classes, codes and
message shape; it honors cursors, limits, the export filter and
`onHeader`/`onTrailer` (see `setResponseMetadata`). Build expected errors with
`SpiceDBError.fromCode(Code.FailedPrecondition, "...")`. For integration tests,
run `authzed/spicedb serve-testing` and assert both granted and denied cases.
