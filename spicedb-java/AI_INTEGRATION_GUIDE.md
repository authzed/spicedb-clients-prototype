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
writer `r(subject)`, key `rKey(subject)` (only for relations admitting a
caveated or expiring subject), check `rRelation()`, filter `rFilter()`. A userset on a permission → `pSubject()` (e.g. `canAdminSubject()`).
`user:*` → `UserWildcard()`. Name constants: `OrganizationRelations.ADMIN`,
`OrganizationPermissions.CAN_ADMIN`, `Organization_View`. Collisions and Java
reserved words fail generation; nothing is renamed silently. Keep the schema and
pass `--java.rename=<definition>=<JavaName>` (e.g. `subject=SubjectDef` for
`definition subject`) to change only a definition's Java identifiers.
`withSubject(filter, subject)` always pins the subject relation (direct for a
plain ref); `withSubjectAnyRelation` matches direct and userset subjects.

## Connect once

```java
try (var tc = TypedClient.createSystemTls("spicedb.example.com:443", System.getenv("SPICEDB_TOKEN"))) {
  // share tc for the life of the application; it also has every native method
}
```

The client connects lazily. To fail at startup when the endpoint is wrong or
down, call `awaitReady(Duration.ofSeconds(10))` on the client right after
creating it: it throws `UnavailableException` or `DeadlineExceededException`
naming the endpoint. Endpoints are `host:port`, `http://host:port`,
`https://host[:port]`, `dns:///host:port` (client-side load balancing) or
`unix:/path` (Linux only; refused at construction elsewhere).

`createPlaintext` is for local development; a non-loopback plaintext target
additionally needs
`ClientOptions.builder().remotePlaintextCredentials(RemotePlaintextCredentials.ALLOW).build()`.
Keepalive and other channel settings go beside the options:
`TypedClient.createSystemTls(endpoint, token, options, b ->
b.keepAliveTime(60, TimeUnit.SECONDS))` (the same overload exists for
`createPlaintext` and on `SpiceDBClient`). The customizer must not change the
transport security.
Install the artifacts and check the protobuf-java floor (4.33.2; Spring Boot 4.1
needs no override) as [README.md](README.md) describes.
For an existing `io.grpc.Channel` (in-process, SpiceDB's test server), use
`new TypedClient(SpiceDBClient.fromChannel(channel), true)`; `close` never shuts a
supplied channel down. `new TypedClient(api)` wraps any `SpiceDBApi`, including
`InMemorySpiceDB` for unit tests.

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

- Type and permission names received at runtime (policy expressions such as a
  Spring Security permission evaluator, admin tools) stay typed, with no switch over
  definitions and no native call:

```java
var check =
    definitionByName(type) // throws NotInSchemaException for an unknown type
        .permissionByName(id, permission)
        .forDynamicSubject(User(userId)); // throws SubjectNotAllowedException
boolean allowed = tc.check(Consistency.minLatency(), check).hasPermission();

try (var found =
    tc.lookupResources(Consistency.minLatency(), type, permission, User(userId), grantedOnly)) {
  // names and subject are validated against the schema before the call
}
```

  `permissionRefByName(type, permission)` gives a resource-less `PermissionRef<?>`.
  Use `Definitions.ORGANIZATION` and `Definitions.names()` instead of a hand-kept type
  list, and `tc.writeSchema(Permissions.SCHEMA)` (the schema text the code was
  generated from) instead of loading the `.zed` from the classpath.

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
  never retries writes, deletes or imports on its own. A write of touches
  without preconditions, and `writeSchema`, may opt in per call with
  `WriteOptions.builder().retryPolicy(...)`; other mutations reject that.
  `txn.isIdempotent()` is the client's own eligibility rule.

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
        // every call of one operation; a per-call .retryPolicy(...) still wins
        .operationRetryPolicy(ClientOperation.WATCH, RetryPolicy.builder().maxRetries(0).build())
        .build();
var metadata = new ResponseMetadata();
client.readSchema(
    CallOptions.builder()
        .transport(TransportOptions.builder().responseMetadata(metadata).build())
        .build());
com.authzed.api.v1.Relationship wire = ProtoConv.toProto(r); // protocol-shaped output
```

Deletes: `client.deleteRelationships(filter)` is one all-or-nothing request (the
raw filter-only `DeleteRelationships`); `deleteRelationships(filter, limit)` may
delete part of the matches; `deleteAllRelationshipsNonAtomic(filter)` deletes
any number in batches.

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

Local validation (`Relationship.fromTuple`, builders) throws
`LocalValidationException`, a subclass of `InvalidArgumentException` that is
not an `IllegalArgumentException`: catch `LocalValidationException` around
parsing (the server's `INVALID_ARGUMENT` is a plain `InvalidArgumentException`).
`serverMessage()` is never null; for a local error it is the message. Reads, lookups and exports put the
client default deadline (30 s) on each page (your processing time between items
does not count), so a stalled server ends them with
`DeadlineExceededException`; a response missing a required field fails with
`INTERNAL` and is not retried.

## Test

The in-memory double does not evaluate your schema. A permission such as
`view = reader + writer + parent->read` is granted on the double only by a
stored relationship whose relation is named `view`, or by a check resolver
you install; the double never derives it from `reader`, `writer` or `parent`.
Use the double for code that consumes check and lookup results (model the
permission with the resolver), and run SpiceDB itself (`spicedb serve-testing`
or a container) for tests of the permission logic in the schema.

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
db.failNext(
    ClientOperation.WRITE, InMemorySpiceDB.serverError(Status.Code.UNAVAILABLE, "try again"));
List<InMemorySpiceDB.Call> writes = db.callsOf(ClientOperation.WRITE);
String resumeAfterTwo = InMemorySpiceDB.cursorAfter(2);
db.setReadSchemaRevision(InMemorySpiceDB.SchemaRevision.OMITTED);
// Checks with no registered result: a resolver (null declines), or grant everything.
db.setCheckResolver(
    check ->
        check.permission().equals("view")
            ? new CheckResult(Permissionship.HAS_PERMISSION, List.of(), null, null, null)
            : null);
db.setCheckResolver(InMemorySpiceDB.grantAll());
```

Depend on `PermissionsApi` (`write`, `readRelationships`, `deleteRelationships`,
`check`, `checkBulk`, `importRelationships`, `exportRelationships`, `readSchema`,
`writeSchema`, `reflectSchema`), or on `SpiceDBApi` for the client's full method set;
both `SpiceDBClient` and `InMemorySpiceDB` implement both. `TypedClient` wraps either
(`new TypedClient(new InMemorySpiceDB())`) and returns it from `tc.client()` as
`SpiceDBApi`. Checks resolve in order: registered result, resolver, then a
stored relationship whose relation is the permission's name (exact subject or `type:*`
wildcard), else no permission; the double never evaluates the schema. `lookupResources`
and `lookupSubjects` apply that resolution to every object of the requested type the
stored relationships mention. Operations the double cannot run in memory (expand,
watch, schema analysis, experimental calls) are recorded and throw `UNIMPLEMENTED` unless
the fault hook fails them first. The double applies
preconditions, `CREATE` conflicts and filter semantics like the server. It
records every call (`calls()`, `callsOf(op)`: arguments and options) and
injects faults with `setFaultHook` (before a call, each import batch, the
import commit, each read/export/lookup item) or `failNext`; a failed write or import
applies nothing.
For integration tests, run `authzed/spicedb serve-testing` and assert both
granted and denied cases.
