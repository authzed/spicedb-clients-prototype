# Migrating to spicedb-go

This guide covers moving from [authzed-go](https://github.com/authzed/authzed-go)
or [gochugaru](https://github.com/authzed/gochugaru) to `spicedb-go`. Each
section lists a behavior change you will observe, followed by a before/after.

## Linking both during the migration

`spicedb-go`'s protocol packages are the canonical Go binding for
`authzed.api.*`. Two independently generated copies cannot be linked into one
binary: the second registration panics with `proto: file
"authzed/api/v1/core.proto" is already registered`. To use authzed-go and
spicedb-go together, use an authzed-go release whose `proto/authzed/api/...`
packages are aliases of `spicedb-go-proto` (produced by
`proto-clients/spicedb-go-proto/cmd/authzed-go-bridge`). With the bridge,
authzed-go and spicedb-go message types are the same Go types, so values pass
between them without conversion.

Do not set `GOLANG_PROTOBUF_REGISTRATION_CONFLICT=warn`: the global registry
would then resolve `authzed.api.v1.*` names to whichever copy registered first.

## Constructors and transport security

The preshared key is a positional argument rather than a dial option, and
remote plaintext needs an explicit opt-in. Loopback addresses and Unix sockets
work without it.

```go
// authzed-go
c, err := authzed.NewClient("spicedb:50051",
    grpcutil.WithInsecureBearerToken("key"),
    grpc.WithTransportCredentials(insecure.NewCredentials()))

// gochugaru
c, err := client.NewPlaintext("spicedb:50051", "key")

// spicedb-go
c, err := client.NewPlaintext("spicedb:50051", "key",
    client.WithRemotePlaintextCredentials(client.RemotePlaintextAllowed))
```

`NewSystemTLS` uses the system trust store. `NewWithOpts` with
`WithDialOptions` covers custom trust and mTLS. Remove any
`grpcutil.WithBearerToken` from your dial options, because the key is already
attached.

## Existing connections

If you already hold a connection (an in-memory or buffered-listener connection,
or one from SpiceDB's test server), wrap it instead of dialing:

```go
c, err := client.NewFromConn(conn) // conn stays yours; c.Close() leaves it open
```

## Deadlines and retries

Every unary call (including bulk checks and paged reads) gets a 30-second
budget unless the context already has an earlier deadline, or you pass a
per-call `With<Operation>Timeout` or a client-wide `WithDefaultTimeout`.
Idempotent reads retry `Unavailable` and `Aborted` up to three times with
jittered backoff, and writes are never retried. Streams have no default
deadline.

Tools with their own policy should not stack it on the client's:

```go
c, err := client.NewPlaintext(endpoint, key,
    client.WithDefaultDeadline(client.DefaultDeadlineDisabled), // no 30s budget
    client.WithRetries(client.RetriesDisabled),                  // or WithRetryPolicy(...)
    client.WithStreamRetries(client.StreamRetriesDisabled))     // e.g. never reopen Watch
```

## Checks

`Check` sends a singular `CheckPermission`. gochugaru sent
`CheckBulkPermissions` even for one item, so the server-side path (and its
metrics) changes. Use `CheckBulk` for several checks.

Only an unconditional grant satisfies `HasPermission()`. A conditional result
is not a grant: `result.Permissionship` and `result.MissingContext` say why.

```go
// authzed-go
resp, err := c.CheckPermission(ctx, &v1.CheckPermissionRequest{
    Consistency: &v1.Consistency{Requirement: &v1.Consistency_FullyConsistent{FullyConsistent: true}},
    Resource:    &v1.ObjectReference{ObjectType: "document", ObjectId: "readme"},
    Permission:  "view",
    Subject:     &v1.SubjectReference{Object: &v1.ObjectReference{ObjectType: "user", ObjectId: "alice"}},
})
allowed := resp.Permissionship == v1.CheckPermissionResponse_PERMISSIONSHIP_HAS_PERMISSION

// spicedb-go
result, err := c.Check(ctx, client.PermissionCheck{
    Resource:    rel.MustNewObject("document", "readme"),
    Permission:  "view",
    Subject:     rel.MustNewSubject(rel.MustNewObject("user", "alice"), ""),
    Consistency: consistency.Full(),
})
allowed := result.HasPermission()
```

## Consistency on schema reads

Every read takes an explicit consistency, including `ReflectSchema` and the
other schema reads. Where your raw requests left consistency unset, pass
`consistency.MinLatency()`, which is what SpiceDB applies when it is absent.

## Retries for tools with their own policy

Disable or replace the client's policy client-wide (`WithRetries`,
`WithRetryPolicy`), and override it per call with
`client.WithCallRetryPolicy[O](policy)`: for example `RetryPolicy{}` on one
`Updates` call to never reopen a supervised watch, or a policy on a touch-only
`Write` or a `WriteSchema` to retry those idempotent writes. Other mutations
reject the opt-in.

## Errors

Errors are `*client.Error`. Match them with the sentinels, or keep using
`status.Code`: `*client.Error` implements `GRPCStatus`, including for local
cancellation and validation errors and for errors built with `client.NewError`.

```go
// before
if status.Code(err) == codes.FailedPrecondition { ... }

// after (either works)
if errors.Is(err, client.ErrFailedPrecondition) { ... }
if status.Code(err) == codes.FailedPrecondition { ... }

// classify anything, including a raw status error from a fake
switch client.CodeOf(err) { case client.CodeFailedPrecondition: ... }
```

`Error.Message` is `"spicedb: <operation>: <server message>"`. If you forward
errors to another party, use `Error.ServerMessage`, which is the server's
message verbatim.

## Relationships, filters and transactions

Relationship fields become accessors. Short paths cover the common fields:

```go
// authzed-go / gochugaru
id := r.Subject.Object.ObjectId   // or r.SubjectID
// spicedb-go
id := r.SubjectID()               // also ResourceType, ResourceID, SubjectType, SubjectRelation
```

`rel.MustFromTuple("document:readme#viewer@user:alice")` and the other `Must*`
constructors cover constants and tests (gochugaru's `rel.MustFromTriple`).
`FromTuple` and `String()` use SpiceDB's full syntax, including
`[caveat:{...}]` and `[expiration:...]`, so you no longer need SpiceDB's
`tuple.Parse`. Use `WithResource`/`WithRelation`/`WithSubject` to rewrite one.

Writes are `rel.Txn` values passed to `Write`. Preconditions use
`MustMatch`/`MustNotMatch`. Every native value prints, logs (`slog`, or JSON
for zerolog's `Interface`) and compares as text:

```go
var txn rel.Txn
_ = txn.MustNotMatch(rel.MustNewFilter("org").WithResourceID("acme").WithRelation("admin"))
_ = txn.Touch(rel.MustFromTuple("org:acme#admin@user:alice"))
logger.Info("writing", "updates", txn.Updates(), "count", txn.Len())
result, err := c.Write(ctx, client.WriteRequest{Transaction: txn})
```

## Paging, cursors and transport data

Raw `LookupResources`/`ReadRelationships` calls without a limit are one
unbounded request; this client pages by default. To reproduce the raw call
(and its final cursor), pass `client.WithLookupResourcesPaging(client.PagingSingleRequest)`
(or the `ReadRelationships` equivalent).

Reads, exports and resource lookups page internally, and each item carries its
`AfterResultCursor` (and, for reads, `ReadAt`). To resume, repeat the same
request and page size with `WithReadRelationshipsCursor` (or the export/lookup
equivalent); SpiceDB rejects a cursor sent with different arguments.

Anything at the transport level (server version headers, trailers that SpiceDB
versions before 1.30 used for debug traces) is available on every operation:

```go
var header metadata.MD
_, err := c.ReadSchema(ctx, client.ReadSchemaRequest{}, client.WithResponseMetadata[client.ReadSchemaOption](&header, nil))
```

For output that must stay protocol-shaped (JSON for scripts, backup files), use
`client/protoconv` with `protojson`.

## Revisions

ZedTokens are `consistency.ZedToken` values. Store `token.String()` and
restore it with `consistency.NewZedToken(s)`. Pass them back with
`consistency.AtLeast(token)` or `consistency.Snapshot(token)`. Writes return
`client.WriteResult`, whose token is in `.Revision`.

## Testing

`client/clienttest.Store` is an in-memory double with the same method
signatures as `*client.Client` for `Write`, `ReadRelationships`,
`DeleteRelationships`, `Check` and `CheckBulk`. It applies SpiceDB filter,
precondition and CREATE semantics; register check results with
`SetCheckResult`. If you keep your own fake, `rel.Filter.Matches` and
`client.NewError` replace hand-written filter matching and `&client.Error{}`
literals.

## Typed (generated) clients

`spicedb-gen --lang go --go.package=<name>` emits a schema-checked facade over
`spicedb-go`. Beyond typed `Check`/`Touch`/`Create`/`Delete`, it generates:

| Need | Generated API |
|---|---|
| Log or report a checked permission | `perm.ResourceType()`, `ResourceID()`, `Name()`, `String()` |
| Mix deletes, touches and preconditions atomically | `(&permissions.Txn{}).MustNotMatch(f).Delete(a).Touch(b)` + `tc.WriteTxn(ctx, txn)` |
| Delete relationships read back from SpiceDB | `txn.Delete(permissions.FromRelationship(r))` |
| Delete without inventing a caveat or expiration | `invitation.InviterKey(user)` |
| Check several permissions | `permissions.CheckBulk(ctx, tc, cs, permissions.ChecksFor(user, a, b))` |
| Filters without string literals | `org.AdminFilter()`, `permissions.OrganizationFilter()`, `permissions.WithSubject(f, user)` |
| Relation names | `permissions.OrganizationRelations.Admin` |
| `user:*` subjects | `permissions.UserWildcard()` |
| Usersets on permissions | `org.CanAdminSubject()` |
| Check a relation | `org.MemberRelation()` |
| Runtime permission names | `org.PermissionByName(name)`, `org.RelationshipByName(rel, subject)`, `permissions.OrganizationPermissionNames()` |
