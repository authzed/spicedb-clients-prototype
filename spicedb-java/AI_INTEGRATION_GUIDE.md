# spicedb-java: integration guide for AI coding agents

Concrete Java for the rules in the repository-wide
[AI_INTEGRATION_GUIDE.md](../AI_INTEGRATION_GUIDE.md). Read that first for
*why*; this file is *how*. Packages:

| Package | Artifact | Role |
|---|---|---|
| typed (generated) | your code, e.g. `com.example.authz.Permissions` | schema-checked API; `Permissions.TypedClient` forwards every native method |
| `com.authzed.spicedb` | `spicedb-java` (`lib` module) | native client, options, results, `Relationship`, `Filter`, `Transaction`, `Consistency`, `ZedToken` |
| `com.authzed.spicedb.errors` | same | typed exceptions, `ErrorMapper` |
| `com.authzed.spicedb.testing` | same | `InMemorySpiceDB` test double |
| `com.authzed.spicedb.protoconv` | same | `ProtoConv` protocol conversions (tools only) |
| `com.authzed.api.v1` | `com.authzed.spicedb:spicedb-java-proto` | protocol classes; reach them only through `rawChannel()` |

Do **not** add `com.authzed.api:authzed` (authzed-java) next to this client: it
ships a second copy of `com.authzed.api.v1`. Use the bridged artifact
(`gradle bridgeAuthzedJava` in `proto-clients/spicedb-java-proto`) if legacy code
must stay, and see [MIGRATION.md](MIGRATION.md). Do not build requests from
`com.authzed.api.v1` classes.

## Generate the typed client (application code)

```bash
spicedb-gen --schema src/main/resources/authz/schema.zed --lang java \
  --java.package=com.example.authz \
  --out src/main/java/com/example/authz/Permissions.java
```

Commit the output; regenerate whenever the schema changes; never edit it. The
output is google-java-format clean. The examples below use this schema and
`import static com.example.authz.Permissions.*;`:

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

Naming: `Organization(id)` builds a ref. Permission `p` → `p()`. Relation `r` →
writer `r(subject)`, key `rKey(subject)`, check `rRelation()`, filter
`rFilter()`. A userset on a permission → `pSubject()` (e.g. `canAdminSubject()`).
`user:*` → `UserWildcard()`. Name constants: `OrganizationRelations.ADMIN`,
`OrganizationPermissions.CAN_ADMIN`, `Organization_View`. Collisions and Java
reserved words fail generation; nothing is renamed silently.

## Connect once

```java
try (var tc = TypedClient.createSystemTls("spicedb.example.com:443", System.getenv("SPICEDB_TOKEN"))) {
  // share tc for the life of the application; it also has every native method
}
```

`createPlaintext` is for local development; a non-loopback plaintext target
additionally needs
`ClientOptions.builder().remotePlaintextCredentials(RemotePlaintextCredentials.ALLOW).build()`.
For an existing `io.grpc.Channel` (in-process, SpiceDB's test server), use
`new TypedClient(SpiceDBClient.fromChannel(channel), true)`; `close` never shuts a
supplied channel down.

## Check (fail closed)

```java
CheckResult result;
try {
  result = tc.check(Consistency.minLatency(), Organization(orgId).view(), User(userId));
} catch (SpiceDBException e) {
  throw new SecurityException("authorization unavailable", e); // errors deny
}
if (!result.hasPermission()) {
  throw new SecurityException("forbidden: " + Organization(orgId).view()); // CONDITIONAL denies too
}
```

- Need to observe a write? Store `written.revision().value()` and check with
  `Consistency.atLeast(new ZedToken(stored))`.
- Reporting what was denied: `perm.toString()` is `organization:acme#view`.
- Several at once:

```java
List<CheckResult> results =
    tc.checkBulk(
        Consistency.minLatency(),
        checksFor(User(userId), Organization("a").view(), Organization("b").view()));
// results.get(i) matches input i; only results.get(i).hasPermission() grants.
```

- Permission names chosen at runtime (e.g. per API method):

```java
var perm = Organization(orgId).permissionByName(name); // throws NotInSchemaException
CheckResult byName = tc.check(Consistency.minLatency(), perm.forDynamicSubject(User(userId)));
```

  At startup, verify every configured name is in `OrganizationPermissions.names()`.

## List what a subject can access

```java
List<String> ids;
try (var found =
    tc.lookupResources(
        Consistency.minLatency(),
        Organization_View,
        User(userId),
        LookupOptions.builder()
            .permissionshipFilter(LookupPermissionshipFilter.HAS_PERMISSION_ONLY)
            .build())) {
  ids = found.map(LookupResult.LookupResource::resourceId).toList();
}
```

Omit the filter only if you handle `CONDITIONAL_PERMISSION` results explicitly.
Every stream belongs in try-with-resources.

## Write atomically

```java
var txn =
    new Txn()
        .mustMatch(withSubject(Organization(orgId).adminFilter(), User(otherAdmin))) // another admin remains
        .delete(Organization(orgId).admin(User(userId)))
        .touch(Organization(orgId).member(User(userId)));
try {
  WriteResult written = tc.writeTxn(txn); // the first construction error surfaces here, before any RPC
} catch (FailedPreconditionException e) {
  throw new IllegalStateException("cannot remove the last admin", e);
}
```

- `touch` upserts; `create` fails with `AlreadyExistsException` if present.
- Delete without inventing an expiration:
  `new Txn().delete(Invitation(id).inviterKey(User(u)))`.
- Delete what you just read:
  `new Txn().delete(TypedRelationship.fromRelationship(r.relationship()))`.
- **Do not retry a failed write blindly**; it may have committed. The client
  never retries writes, deletes or imports.

## Read relationships

```java
List<String> admins;
try (var rels =
    tc.readRelationships(Consistency.minLatency(), Organization(orgId).adminFilter())) {
  admins = rels.map(r -> r.relationship().subjectId()).toList();
}
```

Each item is a `ReadRelationship` carrying `afterResultCursor()` and `readAt()`.
To resume, pass `ReadOptions.builder().cursor(cursor)` with the **same filter,
consistency and page size**.

## Schema-agnostic code (native tier)

```java
var r = Relationship.fromTuple("document:readme#viewer@user:alice");
var nativeTxn = new Transaction();
nativeTxn.touch(r);
WriteResult written = client.write(nativeTxn);

CheckResult nativeResult =
    client.check(
        PermissionCheck.builder()
            .resource(new ObjectRef("document", "readme"))
            .permission("view")
            .subject(SubjectRef.direct(new ObjectRef("user", "alice")))
            .consistency(Consistency.atLeast(written.revision()))
            .build());
```

Constructors and `fromTuple` throw `InvalidArgumentException` on bad input, so
there are no separate panicking variants; catch it when the input is runtime
data. Tuples use SpiceDB's full syntax, including `[caveat:{...}]` and
`[expiration:...]`. Tools with their own policies:

```java
var toolOptions =
    ClientOptions.builder()
        .retries(Retries.DISABLED) // or .retryPolicy(RetryPolicy.builder().maxRetries(2).build())
        .defaultDeadline(DefaultDeadline.DISABLED)
        .streamRetries(StreamRetries.DISABLED)
        .build();
var metadata = new ResponseMetadata();
client.readSchema(
    CallOptions.builder()
        .transport(TransportOptions.builder().responseMetadata(metadata).build())
        .build());
com.authzed.api.v1.Relationship wire = ProtoConv.toProto(r); // protocol-shaped output
```

Every operation's options accept `.transport(...)`. Use `ProtoConv` with
protobuf's `JsonFormat` for protocol-shaped JSON.

## Errors

```java
try {
  tc.writeTxn(txn);
} catch (FailedPreconditionException e) {
  // precondition failed
} catch (OutOfRangeException e) {
  // stale ZedToken: drop it, re-read fully consistent
} catch (UnavailableException e) {
  // transient; reads were already retried
}
```

`Status.fromThrowable(e).getCode()` also works for every `SpiceDBException`,
including local validation errors and the client's `CancellationException`.
`ErrorMapper.codeOf(t)` classifies any throwable, including raw
`StatusRuntimeException`. Forward `e.serverMessage()`, not `getMessage()`, when
relaying an error to another party.

## Test

```java
var db = new InMemorySpiceDB();
var seed = new Transaction();
seed.touch(Relationship.fromTuple("organization:acme#admin@user:alice"));
db.write(seed);
db.setCheckResult(
    new ObjectRef("organization", "acme"),
    "view",
    SubjectRef.direct(new ObjectRef("user", "alice")),
    Permissionship.HAS_PERMISSION);
SpiceDBException expected = ErrorMapper.newError(Status.Code.FAILED_PRECONDITION, "guard");
```

Depend on `PermissionsApi` (`write`, `readRelationships`, `deleteRelationships`,
`check`, `checkBulk`); both `SpiceDBClient` and `InMemorySpiceDB` implement it,
and a `TypedClient` provides one through `tc.client()`. The double applies
preconditions, `CREATE` conflicts and filter semantics like the server.
For integration tests, run `authzed/spicedb serve-testing` and assert both
granted and denied cases.
