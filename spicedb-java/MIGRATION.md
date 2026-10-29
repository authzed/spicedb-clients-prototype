# Migrating from authzed-java

[authzed-java](https://github.com/authzed/authzed-java) (`com.authzed.api:authzed`)
is the generated gRPC stubs for `authzed.api.v1` plus a `BearerToken` helper.
spicedb-java replaces it with a native client: Java records, streams and typed
exceptions over the same RPCs. This guide lists every behavior a migrating team
will observe, with before/after code. Snippets assume
`import com.authzed.spicedb.*;`.

## Running both during the migration

Both libraries define `com.authzed.api.v1.*`. Never put the published
authzed-java jar on the same class path as spicedb-java: the JVM silently picks
one copy of each class per class loader, so a version skew between the two
misbinds at run time. Instead, replace it with the bridged artifact, which keeps
authzed-java's `BearerToken` and drops its generated classes in favour of
spicedb-java-proto's identical ones:

```bash
cd proto-clients/spicedb-java-proto
gradle bridgeAuthzedJava bridgeAuthzedJavaPom   # build/bridge/authzed-1.6.0-spicedb-bridge.{jar,pom}
```

Publish that jar and POM to your artifact repository, then:

```kotlin
// Gradle: use the bridged coordinate wherever authzed-java is requested.
configurations.all {
    resolutionStrategy.dependencySubstitution {
        substitute(module("com.authzed.api:authzed"))
            .using(module("com.authzed.api:authzed:1.6.0-spicedb-bridge"))
    }
}
```

Code compiled against authzed-java keeps working unchanged
(`LegacyBridgeTest` in spicedb-java-proto runs exactly such code), and its
messages are the same classes spicedb-java uses, so you can migrate call sites
one at a time. Remove the bridge once nothing imports `com.authzed.grpcutil`.

## Connecting

```java
// Before
ManagedChannel channel =
    ManagedChannelBuilder.forTarget("spicedb.example.com:443").useTransportSecurity().build();
var permissions =
    PermissionsServiceGrpc.newBlockingStub(channel).withCallCredentials(new BearerToken(token));

// After: the client owns the channel; close it (try-with-resources) at shutdown.
SpiceDBClient client = SpiceDBClient.createSystemTls("spicedb.example.com:443", token);
```

- The preshared key is a constructor argument, not call credentials.
- `createPlaintext` sends the key in cleartext only to loopback by default. A
  remote plaintext host needs an explicit opt-in:
  `SpiceDBClient.createPlaintext(endpoint, token,
  ClientOptions.builder().remotePlaintextCredentials(RemotePlaintextCredentials.ALLOW).build())`.
- TLS uses the JVM trust store; custom CA and client certificate/key PEM paths
  go in `ClientOptions`.
- Keep an existing channel (in-process, custom transport) with
  `SpiceDBClient.fromChannel(channel)`. It sends no key itself, so attach
  credentials to the channel (for example `ClientInterceptors.intercept(channel,
  MetadataUtils.newAttachHeadersInterceptor(bearerMetadata))`), and `close()`
  leaves the channel open.
- `client.rawChannel()` is the authenticated channel for any stub the native API
  does not cover; it is not yours to close.

## Checks

```java
// Before
CheckPermissionResponse response =
    permissions.checkPermission(
        CheckPermissionRequest.newBuilder()
            .setConsistency(Consistency.newBuilder().setMinimizeLatency(true))
            .setResource(ObjectReference.newBuilder().setObjectType("document").setObjectId("readme"))
            .setPermission("view")
            .setSubject(SubjectReference.newBuilder()
                .setObject(ObjectReference.newBuilder().setObjectType("user").setObjectId("alice")))
            .build());
boolean allowed =
    response.getPermissionship()
        == CheckPermissionResponse.Permissionship.PERMISSIONSHIP_HAS_PERMISSION;

// After
CheckResult result =
    client.check(
        new PermissionCheck(
            new ObjectRef("document", "readme"),
            "view",
            SubjectRef.direct(new ObjectRef("user", "alice")),
            Consistency.minLatency()));
boolean allowed = result.hasPermission(); // only an unconditional grant is true
```

- Consistency is required on every read; there is no implicit default.
  Revisions are `ZedToken` values: `Consistency.atLeast(written.revision())`.
- `check` uses the singular `CheckPermission` RPC. `checkBulk(List<PermissionCheck>)`
  uses `CheckBulkPermissions` in chunks of 1,000, returns results in input order,
  and by default throws the first item's error (`BulkItemErrors.RETURNED` returns
  every item instead).
- With a schema in your repository, prefer the generated typed client
  (`spicedb-gen --lang java`), which makes wrong names and subject types compile
  errors.

## Writes, reads and deletes

```java
// Before
permissions.writeRelationships(
    WriteRelationshipsRequest.newBuilder()
        .addUpdates(RelationshipUpdate.newBuilder()
            .setOperation(RelationshipUpdate.Operation.OPERATION_TOUCH)
            .setRelationship(/* nested builders */))
        .build());

// After
var txn = new Transaction();
txn.touch(Relationship.fromTuple("document:readme#viewer@user:alice"));
WriteResult written = client.write(txn); // written.revision() is a ZedToken
```

- Reads page automatically and return a closeable `Stream<ReadRelationship>`:

```java
// Before: one server stream per page; you carried cursors and re-sent requests.
// After
try (var rels = client.readRelationships(Consistency.full(), Filter.of("document"))) {
  rels.forEach(r -> System.out.println(r.relationship().subjectId()));
}
```

  Each item carries `afterResultCursor()` and `readAt()`; resume with
  `ReadOptions.builder().cursor(c)` and the same filter, consistency and page
  size. Exports and resource lookups work the same way.
- Accessor paths shorten: `rel.getSubject().getObject().getObjectId()` becomes
  `rel.subjectId()` (also `resourceType()`, `resourceId()`, `subjectType()`,
  `subjectRelation()`); the nested `resource()`/`subject()` records remain.
- `deleteRelationships(filter, limit)` is one bounded request;
  `deleteAllRelationshipsNonAtomic(filter)` loops to completion and reports
  progress. Server-side all-or-nothing deletes use
  `DeleteOptions.builder().partialDeletion(PartialDeletion.DISALLOWED)`.
- Filters distinguish any, direct and named subject relations
  (`SubjectRelationFilter.any/direct/named`) instead of an optional proto field.

## Deadlines and retries

authzed-java stubs have no deadline and no retries. spicedb-java:

- gives unary calls a 30 second budget (covering retries, chunks and pages).
  Override per call with `.timeout(...)` on the operation's options, change the
  default with `ClientOptions.builder().defaultTimeout(...)`, or remove it with
  `.defaultDeadline(DefaultDeadline.DISABLED)`. Streams and imports have no
  default lifetime.
- retries idempotent reads on `UNAVAILABLE`/`ABORTED` up to three times with
  jittered backoff, and retries opening a stream before any item is exposed.
  Writes, deletes, imports and counter registration are never retried. Tune with
  `.retryPolicy(RetryPolicy.builder()...)`, or turn retries off with
  `.retries(Retries.DISABLED)` / `.streamRetries(StreamRetries.DISABLED)` if
  you already have a policy, so the two do not stack.
- overrides that policy for one call with `.retryPolicy(...)` on the
  operation's options: for example
  `WatchOptions.builder().retryPolicy(RetryPolicy.builder().maxRetries(3).build())`
  to let one watch reopen before its first event (the client-wide policy never
  reopens a watch), or a policy on a touch-only `write` or a
  `writeSchema` to retry those idempotent writes. Other mutations reject the
  opt-in.
- sets that override for every call of one operation with
  `ClientOptions.builder().operationRetryPolicy(ClientOperation.WATCH, ...)`
  (a per-call policy still wins). A `WRITE` policy retries only transactions
  where `isIdempotent()` is true; deletes, imports and counter registration
  accept only a zero-retry policy.

## Consistency on schema reads

Every schema read except `readSchema` (whose request has no consistency) takes
an explicit `Consistency`, including `reflectSchema`, `diffSchema`,
`computablePermissions` and `dependentRelations`. Where your raw requests left
consistency unset, pass `Consistency.minLatency()`, which is what SpiceDB applies
when it is absent.

## Single-request paging

A raw `LookupResources`/`ReadRelationships` call without a limit is one
unbounded request; this client pages by default. To reproduce the raw call (and
its final cursor), pass `LookupOptions.builder().paging(Paging.SINGLE_REQUEST)`
(or the `ReadOptions` equivalent).

## Errors

```java
// Before
try {
  permissions.writeRelationships(request);
} catch (StatusRuntimeException e) {
  if (e.getStatus().getCode() == Status.Code.FAILED_PRECONDITION) { /* ... */ }
}

// After
try {
  client.write(txn);
} catch (com.authzed.spicedb.errors.FailedPreconditionException e) {
  /* ... */
}
```

- Every failure is an unchecked `SpiceDBException` subclass
  (`NotFoundException`, `AlreadyExistsException`, `OutOfRangeException`,
  `UnauthenticatedException`, ...). Existing status checks keep working:
  `Status.fromThrowable(e).getCode()` and `e.code()` report the gRPC code, the
  original `StatusRuntimeException` (with details and trailers) is
  `e.toStatusRuntimeException()`, and `ErrorMapper.codeOf(t)` classifies raw
  status errors too.
- `getMessage()` may add context (for example a bulk item index);
  `serverMessage()` is what SpiceDB said, verbatim. SpiceDB's `ErrorInfo` reason
  and metadata are `getReason()` and `getReasonMetadata()`.
- Local cancellation (thread interrupt, closed stream or client) raises
  `java.util.concurrent.CancellationException` with a `CANCELLED` status cause.

## Transport-level data

Response headers and trailers (for example a server version, or the check debug
trace that SpiceDB before 1.30 sent in a trailer) come from any operation with:

```java
var md = new ResponseMetadata();
client.readSchema(
    CallOptions.builder().transport(TransportOptions.builder().responseMetadata(md).build()).build());
```

The minimum natively supported SpiceDB is 1.30; older-server behaviors are
reachable only this way. `TransportOptions.callOptions(...)` adjusts each call's
gRPC `CallOptions`. Tools that print or persist protocol-shaped data convert with
`com.authzed.spicedb.protoconv.ProtoConv` instead of re-implementing it.

## Dependencies

spicedb-java needs Java 17, protobuf-java 4.36.2 or later (the version its
generated code was produced with) and gRPC 1.84.0 (a single BOM version for every
`io.grpc` artifact). authzed-java 1.6.0 required protobuf-java 4.33.5 and gRPC
1.78.0, so adopting spicedb-java raises both. Build tooling (Spotless, mage) is
not part of the published dependencies.

## Testing

Replace hand-written stub fakes with `com.authzed.spicedb.testing.InMemorySpiceDB`,
which implements the same `PermissionsApi` interface as `SpiceDBClient` and
applies preconditions, `CREATE` conflicts and filter semantics like the server,
and also covers imports, exports and schema reads and writes, with errors shaped
like the client's. Build expected errors with `ErrorMapper.newError(Status.Code.X, message)`
(or `InMemorySpiceDB.serverError`). Instead of a strict mock, assert on the
recorded `calls()`/`callsOf(op)` (arguments and options), and replace stubbed
failures with `failNext(op, error)` or a `setFaultHook` that fails at a call,
an import batch, the import commit, or a read/export item.
