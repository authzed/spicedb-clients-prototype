# spicedb-go: integration guide for AI coding agents

Concrete Go for the rules in the repository-wide
[AI_INTEGRATION_GUIDE.md](../AI_INTEGRATION_GUIDE.md). Read that first for
*why*; this file is *how*. Package paths:

| Package | Import | Role |
|---|---|---|
| typed (generated) | your package, e.g. `.../internal/authz/permissions` | schema-checked API; embeds the native client |
| `client` | `github.com/authzed/spicedb-clients/spicedb-go/client` | native client, options, errors, results |
| `rel` | `github.com/authzed/spicedb-clients/spicedb-go/rel` | objects, subjects, relationships, filters, `Txn` |
| `consistency` | `github.com/authzed/spicedb-clients/spicedb-go/consistency` | strategies and `ZedToken` |
| `client/clienttest` | `.../spicedb-go/client/clienttest` | in-memory test double |
| `client/protoconv` | `.../spicedb-go/client/protoconv` | protocol conversions (tools only) |

Do **not** import `github.com/authzed/authzed-go` alongside this module unless
your authzed-go release aliases `spicedb-go-proto` (see [MIGRATION.md](MIGRATION.md)).
Do not import `spicedb-go-proto` packages to build requests.

## Generate the typed client (application code)

```bash
spicedb-gen --schema internal/authz/schema.zed --lang go --go.package=permissions \
  --out internal/authz/permissions/permissions.gen.go
```

Commit the output; regenerate whenever the schema changes; never edit it. The
examples below use this schema:

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

Naming: `Organization(id)` builds a ref. Permission `p` → `P()`. Relation `r`
→ writer `R(subject)`, key `RKey(subject)`, check `RRelation()`, filter
`RFilter()`. A userset on a permission → `PSubject()` (e.g. `CanAdminSubject()`).
`user:*` → `UserWildcard()`. Collisions fail generation; nothing is renamed
silently.

## Connect once

```go
c, err := client.NewSystemTLS("spicedb.example.com:443", os.Getenv("SPICEDB_TOKEN"))
if err != nil {
    return err
}
defer c.Close()
tc := permissions.NewTypedClient(c) // tc also has every native method
```

`client.NewPlaintext` is for local development; a non-loopback plaintext target
additionally needs `client.WithRemotePlaintextCredentials(client.RemotePlaintextAllowed)`.
Use `client.NewFromConn(conn)` for an existing `grpc.ClientConnInterface` (in-memory,
SpiceDB's test server); `Close` leaves that connection open.

## Check (fail closed)

```go
result, err := permissions.Check(ctx, tc, consistency.MinLatency(),
    permissions.Organization(orgID).View(), permissions.User(userID))
if err != nil || !result.HasPermission() {
    return ErrForbidden // errors and Conditional results both deny
}
```

- Need to observe a write? Store `written.Revision.String()` and check with
  `consistency.AtLeast(consistency.NewZedToken(stored))`.
- Reporting what was denied: `perm.String()` is `organization:acme#view`.
- Several at once:

```go
results, err := permissions.CheckBulk(ctx, tc, consistency.MinLatency(),
    permissions.ChecksFor(permissions.User(userID),
        permissions.Organization("a").View(), permissions.Organization("b").View()))
// results[i] matches input i; only results[i].HasPermission() grants.
```

- Permission names chosen at runtime (e.g. per API method):

```go
perm, err := permissions.Organization(orgID).PermissionByName(name) // errors.Is(err, permissions.ErrNotInSchema)
```

  At startup, verify every configured name is in `permissions.OrganizationPermissionNames()`.

## List what a subject can access

```go
for res, err := range permissions.LookupResources(ctx, tc, consistency.MinLatency(),
    permissions.Organization_View, permissions.User(userID),
    client.WithLookupResourcesPermissionshipFilter(client.LookupHasPermissionOnly)) {
    if err != nil {
        return err
    }
    ids = append(ids, res.ResourceID)
}
```

Omit the filter only if you handle `Conditional` results explicitly.

## Write atomically

```go
txn := (&permissions.Txn{}).
    MustMatch(permissions.WithSubject(permissions.Organization(orgID).AdminFilter(), permissions.User(otherAdmin))). // invariant: another admin remains
    Delete(permissions.Organization(orgID).Admin(permissions.User(userID))).
    Touch(permissions.Organization(orgID).Member(permissions.User(userID)))
written, err := tc.WriteTxn(ctx, txn) // the first construction error surfaces here, before any RPC
if errors.Is(err, client.ErrFailedPrecondition) {
    return ErrLastAdmin
}
```

- `Touch` upserts; `Create` fails with `client.ErrAlreadyExists` if present.
- Delete without inventing an expiration: `Delete(permissions.Invitation(id).InviterKey(permissions.User(u)))`.
- Delete what you just read: `Delete(permissions.FromRelationship(r.Relationship))`.
- **Do not retry a failed write blindly**; it may have committed.

## Read relationships

```go
for r, err := range tc.ReadRelationships(ctx, client.ReadRelationshipsRequest{
    Filter:      permissions.Organization(orgID).AdminFilter(),
    Consistency: consistency.MinLatency(),
}) {
    if err != nil {
        return err
    }
    admins = append(admins, r.SubjectID())
}
```

Each item also carries `r.AfterResultCursor` and `r.ReadAt`. To resume, pass
`client.WithReadRelationshipsCursor(cursor)` with the **same request and page size**.

## Schema-agnostic code (native tier)

```go
r := rel.MustFromTuple("document:readme#viewer@user:alice") // constants and tests only
var txn rel.Txn
if err := txn.Touch(r); err != nil {
    return err
}
written, err := c.Write(ctx, client.WriteRequest{Transaction: txn})
```

```go
result, err := c.Check(ctx, client.PermissionCheck{
    Resource:    rel.MustNewObject("document", "readme"),
    Permission:  "view",
    Subject:     rel.MustNewSubject(rel.MustNewObject("user", "alice"), ""),
    Consistency: consistency.AtLeast(written.Revision),
})
```

With runtime input, use the error-returning constructors (`rel.NewObject`,
`rel.FromTuple`), not `Must*`. Tools with their own retry/deadline policy:
`client.WithRetries(client.RetriesDisabled)` or `client.WithRetryPolicy(...)`,
`client.WithDefaultDeadline(client.DefaultDeadlineDisabled)`, and
`client.WithStreamRetries(client.StreamRetriesDisabled)`.
Transport metadata: `client.WithResponseMetadata[client.ReadSchemaOption](&header, &trailer)`.
Protocol-shaped output: `client/protoconv` with `protojson`.

## Errors

```go
switch {
case errors.Is(err, client.ErrFailedPrecondition): // precondition failed
case errors.Is(err, client.ErrOutOfRange):         // stale ZedToken: drop it, re-read fully consistent
case errors.Is(err, client.ErrUnavailable):        // transient; reads were already retried
}
```

`status.Code(err)` also works. `client.CodeOf(err)` classifies any error,
including raw status errors. Forward `(*client.Error).ServerMessage`, not
`Error()`, when relaying an error to another party.

## Test

```go
store := clienttest.NewStore(rel.MustFromTuple("organization:acme#admin@user:alice"))
store.SetCheckResult(rel.MustNewObject("organization", "acme"), "view",
    rel.MustNewSubject(rel.MustNewObject("user", "alice"), ""),
    client.CheckResult{Permissionship: client.PermissionshipHasPermission})
```

Depend on a small interface of the `*client.Client` methods you use; both
`*client.Client` and `*clienttest.Store` satisfy it for `Write`,
`ReadRelationships`, `DeleteRelationships`, `Check` and `CheckBulk`. Build
expected errors with `client.NewError(client.CodeFailedPrecondition, "...")`.
For integration tests, run `authzed/spicedb serve-testing` and assert both
granted and denied cases.
