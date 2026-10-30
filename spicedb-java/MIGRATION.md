# Migrating from authzed-java

[authzed-java](https://github.com/authzed/authzed-java) (`com.authzed.api:authzed`)
is the generated gRPC stubs for `authzed.api.v1` plus a `BearerToken` helper.
spicedb-java replaces it with a native client: Java records, streams and typed
exceptions over the same RPCs. This guide lists every behavior a migrating team
will observe, with before/after code. Snippets assume
`import com.authzed.spicedb.*;`.

## Requirements

| | Floor | Notes |
|---|---|---|
| Java | 17 | |
| protobuf-java | 4.33.2 | The version the protocol classes were generated with; they refuse an older runtime. Spring Boot 4.1 manages 4.34.2, so a Spring Boot host needs no override. authzed-java 1.6.0 needed 4.33.5. |
| gRPC (`io.grpc:*`) | 1.80.0 | The version the service stubs were generated with. Tested from 1.80.0 to 1.84.0; one version for every `io.grpc` artifact. The client declares 1.84.0 (through `grpc-bom`); Spring Boot 4.1 resolves 1.80.0, which works with no override. authzed-java 1.6.0 needed 1.78.0. |
| proto-google-common-protos | 2.65.0 | |
| SpiceDB | 1.30 | Older-server behavior is reachable only through `TransportOptions` (see "Transport-level data"). |

Coordinates: `com.authzed.spicedb:spicedb-java` (the client) and
`com.authzed.spicedb:spicedb-java-proto` (protocol classes, pulled in by the
client), at the same version. Until they are published, install both with
`gradle publishToMavenLocal` (see [README.md](README.md#installation-before-publication)).
The client's POM versions every dependency, so it is the only line a Maven or
Gradle build needs.

**Spring Boot and other dependency managers.** A BOM that pins protobuf-java
wins over the version the client asks for. If it pins one below 4.33.2, nothing
fails at build time, so the client checks at construction: the first
`SpiceDBClient` factory call throws `IllegalStateException` naming the version
found and the property to raise, `<protobuf-java.version>` in Maven or Spring
Boot. In Gradle, require `com.google.protobuf:protobuf-java:4.33.2` or later.
The same applies to gRPC: a grpc-java older than 1.80.0 makes the first
factory call throw `IllegalStateException` naming the version found and
what to raise: `<grpc.version>` in Maven, `<grpc-java.version>` in Spring Boot
(the property its dependency management uses), or an imported
`io.grpc:grpc-bom`. If the gRPC version cannot be
read from the class path, for example in a repackaged jar, that check is skipped.

## Legacy-to-new member map

Requests become method arguments and options; responses become records. The
members a migration touches most:

| authzed-java (`com.authzed.api.v1`) | spicedb-java |
|---|---|
| `ZedToken.getToken()` | `ZedToken.value()` |
| `CheckPermissionResponse.getPermissionship() == PERMISSIONSHIP_HAS_PERMISSION` | `CheckResult.hasPermission()` (or `permissionship() == Permissionship.HAS_PERMISSION`) |
| `CheckPermissionResponse.getCheckedAt()` | `CheckResult.checkedAt()` |
| `CheckPermissionResponse.getPartialCaveatInfo().getMissingRequiredContextList()` | `CheckResult.missingContext()` |
| `CheckPermissionRequest.setContext(Struct)` | `PermissionCheck.withContext(Map)` or `CheckOptions.builder().context(Map)` |
| `WriteRelationshipsResponse.getWrittenAt()` | `WriteResult.revision()` |
| `DeleteRelationshipsResponse.getDeletedAt()` | `DeleteResult.revision()` |
| `DeleteRelationshipsResponse.getDeletionProgress()` | `DeleteResult.completed()` |
| `DeleteRelationshipsRequest` with only a filter | `deleteRelationships(filter)` |
| `DeleteRelationshipsRequest.setOptionalLimit(n).setOptionalAllowPartialDeletions(true)` | `deleteRelationships(filter, n)` |
| `ReadRelationshipsResponse.getRelationship()` | `ReadRelationship.relationship()` |
| `ReadRelationshipsResponse.getReadAt()` | `ReadRelationship.readAt()` |
| `ReadRelationshipsResponse.getAfterResultCursor().getToken()` | `ReadRelationship.afterResultCursor()` |
| `Relationship.getResource().getObjectType()` / `getObjectId()` | `Relationship.resourceType()` / `resourceId()` |
| `Relationship.getRelation()` | `Relationship.relation()` |
| `Relationship.getSubject().getObject().getObjectType()` / `getObjectId()` | `Relationship.subjectType()` / `subjectId()` |
| `Relationship.getSubject().getOptionalRelation()` (`""` for direct) | `Relationship.subjectRelation()` (`null` for direct) |
| `Relationship.getOptionalCaveat().getCaveatName()` / `getContext()` | `Relationship.caveatName()` / `caveatContext()` |
| `Relationship.getOptionalExpiresAt()` | `Relationship.expiration()` (`Instant`) |
| `RelationshipFilter.setResourceType(t).setOptionalResourceId(id)` | `Filter.of(t).withResourceId(id)` |
| `RelationshipFilter...setOptionalRelation(r)` | `Filter...withRelation(r)` |
| `SubjectFilter.setSubjectType(t).setOptionalSubjectId(id)` | `Filter...withSubjectType(t).withSubjectId(id)` |
| `SubjectFilter.setOptionalRelation(RelationFilter...)` | `Filter...withSubjectRelation(SubjectRelationFilter.direct()/named(r))` |
| `LookupResourcesResponse.getResourceObjectId()` | `LookupResult.LookupResource.resourceId()` |
| `LookupResourcesResponse.getPermissionship()` | `LookupResource.permissionship()` |
| `LookupResourcesResponse.getLookedUpAt()` | `LookupResource.lookedUpAt()` |
| `LookupResourcesResponse.getAfterResultCursor().getToken()` | `LookupResource.afterResultCursor()` |
| `LookupSubjectsResponse.getSubject().getSubjectObjectId()` | `LookupResult.LookupSubject.subject().subjectId()` |
| `LookupSubjectsResponse.getExcludedSubjectsList()` | `LookupSubject.excludedSubjects()` |
| `ReadSchemaResponse.getSchemaText()` | `SchemaResult.schema()` |
| `ReadSchemaResponse.getReadAt()` | `SchemaResult.revision()` |
| `WriteSchemaResponse.getWrittenAt()` | the `ZedToken` `writeSchema` returns |
| `StatusRuntimeException.getStatus().getCode()` | `SpiceDBException.code()` (also `Status.fromThrowable(e)`) |
| `StatusRuntimeException.getStatus().getDescription()` | `SpiceDBException.serverMessage()` |

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
- The endpoint is `host:port` (as `forTarget` took), `http://host:port`
  (plaintext constructor only; the port is required) or `https://host[:port]`
  (TLS constructor only; the port defaults to 443), `dns:///host:port` (gRPC's
  DNS target, for client-side load balancing; the loopback check runs on the
  host) or `unix:/path`. A bare host without a port dials 443 over TLS;
  `createPlaintext("localhost", ...)` fails, so write `localhost:50051`.
  `unix:` needs Netty's native epoll transport (Linux); elsewhere, such as
  macOS, it fails at construction instead of building a client whose calls
  fail. Other target schemes and any path, query, fragment or userinfo fail at
  construction with `InvalidArgumentException` naming the accepted forms.
- The channel connects lazily, as a legacy `ManagedChannel` did. To fail at
  startup when the endpoint is wrong or down, call
  `client.awaitReady(Duration.ofSeconds(10))`: it throws
  `UnavailableException` (could not connect) or `DeadlineExceededException`
  (still connecting), both naming the endpoint.
- `createPlaintext` sends the key in cleartext only to loopback by default. A
  remote plaintext host needs an explicit opt-in:
  `SpiceDBClient.createPlaintext(endpoint, token,
  ClientOptions.builder().remotePlaintextCredentials(RemotePlaintextCredentials.ALLOW).build())`.
  A Docker Compose service name such as `spicedb:50051` is such a host: use
  `SpiceDBClient.createPlaintext("spicedb:50051", token, ClientOptions.builder().remotePlaintextCredentials(RemotePlaintextCredentials.ALLOW).build())`.
- TLS uses the JVM trust store; custom CA and client certificate/key PEM paths
  go in `ClientOptions`.
- Keepalive and other channel settings (what a legacy `ManagedChannelBuilder`
  configured) go in a customizer passed beside `ClientOptions`, on either
  constructor:
  `SpiceDBClient.createSystemTls(endpoint, token, options, b -> b.keepAliveTime(60, TimeUnit.SECONDS).keepAliveTimeout(10, TimeUnit.SECONDS).keepAliveWithoutCalls(true))`.
  It runs after the TLS or plaintext decision and must not change it.
- Keep an existing channel (in-process, custom transport) with
  `SpiceDBClient.fromChannel(channel)`. It sends no key itself, so attach
  credentials to the channel (for example `ClientInterceptors.intercept(channel,
  MetadataUtils.newAttachHeadersInterceptor(bearerMetadata))`), and `close()`
  leaves the channel open.
- `client.rawChannel()` is the authenticated channel for any stub the native API
  does not cover; it is not yours to close.

An empty or whitespace-only preshared key (usually an unset environment
variable) throws `LocalValidationException` at construction, and so does a
`caCertificate`, `clientCertificate` or `clientKey` file that cannot be read or
parsed, or a `clientKey` that is not the private key of `clientCertificate`;
the message names the option and path.

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
  `atLeast` and `snapshot` reject an empty or whitespace-only token with
  `LocalValidationException`; `atLeastOrFull`/`atLeastOrMinLatency` treat one
  as absent.
- `check` uses the singular `CheckPermission` RPC. `checkBulk(List<PermissionCheck>)`
  uses `CheckBulkPermissions` in chunks of 1,000, returns results in input order,
  and by default throws the first item's error (`BulkItemErrors.RETURNED` returns
  every item instead; a malformed item in the server's response, such as an
  unset permissionship, still fails the whole call).
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
- A raw `DeleteRelationships` request carrying only a filter becomes
  `deleteRelationships(filter)`: the same single all-or-nothing request with the
  server's default limit (more matches than the limit fail with
  `INVALID_ARGUMENT` and delete nothing). It is shorthand for
  `deleteRelationships(filter, DeleteOptions.builder().partialDeletion(PartialDeletion.DISALLOWED).build())`,
  which also takes preconditions and a timeout.

  ```java
  // Before
  permissions.deleteRelationships(
      DeleteRelationshipsRequest.newBuilder().setRelationshipFilter(filter).build());

  // After
  client.deleteRelationships(Filter.of("document").withResourceId("readme"));
  ```

- `deleteRelationships(filter, limit)` is one bounded request that may delete
  part of the matches (`optional_allow_partial_deletions`);
  `deleteAllRelationshipsNonAtomic(filter)` loops to completion and reports
  progress.
- Filters distinguish any, direct and named subject relations
  (`SubjectRelationFilter.any/direct/named`) instead of an optional proto field.

## Deadlines and retries

authzed-java stubs have no deadline and no retries. spicedb-java:

- gives unary calls a 30 second budget (covering retries, chunks and pages).
  Override per call with `.timeout(...)` on the operation's options, change the
  default with `ClientOptions.builder().defaultTimeout(...)`, or remove it with
  `.defaultDeadline(DefaultDeadline.DISABLED)`. Watches and imports have no
  default lifetime.
- applies that default to each page of a finite paged read (relationship
  reads, lookups, exports), so a server that stalls mid-read fails the read
  with `DeadlineExceededException` instead of hanging it. A legacy blocking
  iterator over a stalled stream waited forever. Only waiting on the server
  counts, never the time your code spends on items. An operation `timeout` still
  bounds the whole read and wins when earlier; `DefaultDeadline.DISABLED`
  removes the per-page budget too.
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
- A response the client cannot interpret (a relationship, resource, subject or
  object missing; a check, bulk-check item, looked-up resource or subject with an
  unset permissionship; a cursor that does not advance) fails with
  `INTERNAL`, which is not retried by default. It is never skipped or filled
  with empty values. Only the unset permissionship is malformed: a value newer
  than the client maps to `UNSPECIFIED`, which never grants, without failing
  the call. The message begins `client detected malformed response
  from server:`, so it is not mistaken for a status the server sent.
- Local validation (`Relationship.fromTuple`, `ObjectRef`, option builders,
  endpoint parsing) throws `LocalValidationException`, a subclass of
  `InvalidArgumentException`, not `IllegalArgumentException`. Java has single class inheritance and `catch`
  needs a class, so one exception cannot be both a `SpiceDBException` (with
  `code()`, the status accessors and `catch (SpiceDBException e)`) and an
  `IllegalArgumentException`. Code that wrapped legacy parsing in
  `catch (IllegalArgumentException e)` should catch
  `LocalValidationException` (local validation only) or
  `InvalidArgumentException` (local or server), for example
  `catch (IllegalArgumentException | LocalValidationException e)`. Both report
  `INVALID_ARGUMENT`; the server's rejection is a plain
  `InvalidArgumentException`, never a `LocalValidationException`. A null
  pre-shared key, a null `ClientOption` and a null consistency on
  `readRelationships` or `lookupResources` are local validation too, no longer
  a `NullPointerException` or a `Bearer null` header.
  `serverMessage()` is never `null`: for a local error it is the message.

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

See [Requirements](#requirements). Build tooling (Spotless, mage) is not part of
the published dependencies.

## Testing

Replace hand-written stub fakes with `com.authzed.spicedb.testing.InMemorySpiceDB`,
which implements the same `PermissionsApi` interface as `SpiceDBClient` and
applies preconditions, `CREATE` conflicts and filter semantics like the server,
and also covers imports, exports and schema reads and writes, with errors shaped
like the client's. Build expected errors with `ErrorMapper.newError(Status.Code.X, message)`
(or `InMemorySpiceDB.serverError`). Instead of a strict mock, assert on the
recorded `calls()`/`callsOf(op)` (arguments and options), and replace stubbed
failures with `failNext(op, error)` or a `setFaultHook` that fails at a call,
an import batch, the import commit, or a read, export or lookup item.

The double answers checks without a schema, in this order: a result registered
with `setCheckResult`/`setCheckError`; then a resolver
(`setCheckResolver(check -> ...)`, returning a `CheckResult` or `null` to
decline; `setCheckResolver(InMemorySpiceDB.grantAll())` grants everything, which
suits tests over IDs generated at run time); then a stored relationship whose
relation is the permission's name and whose subject matches (or is a `type:*`
wildcard); otherwise no permission. `lookupResources` and `lookupSubjects` run
the same resolution over every object of the requested type that its stored
relationships mention. Permissions computed through arrows or other relations
need the resolver or registered results; for full schema evaluation, test
against `spicedb serve-testing`.
