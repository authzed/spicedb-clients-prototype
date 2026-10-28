# Generated Java capability inventory

## Native forwarding

`TypedClient` adds explicit native instance overloads for every public `SpiceDBClient`
instance method except the three schema-aware operations. Relationship write/read,
bounded deletion (including the all-or-nothing `deleteRelationships(Filter, DeleteOptions)`),
non-atomic deletion, import/export, bulk check/any/all, schema
read/write/reflection/diff/dependencies/computable permissions, expansion, watch
(including relationship filters), counters, materialize, and raw channel access retain
native parameter and return types (`Stream<ReadRelationship>`,
`Stream<ExportedRelationship>`, ...). All forwarding returns native values/streams
directly without wrapping errors or materializing iterators.

The durable inventory is `TypedParityTest`: it discovers every public native instance
method, requires its exact parameter signature and generic result, and fails on newly
introduced omissions. It rejects native signatures for the three explicit schema-aware
exceptions: `check`, `lookupResources`, `lookupSubjects`. Those capabilities remain
directly available through their generated schema-aware signatures (plus
`check(Consistency, TypedCheck)` for runtime-validated pairs); adding permissive native
overloads would bypass subject constraints. `close` keeps owned-versus-borrowed lifecycle
semantics. Because native `check(PermissionCheck)` is deliberately absent, `TypedClient`
does not implement `PermissionsApi`; use `client()` where that interface is required.

Typed additions never shadow a native method: `writeTxn(Txn[, WriteOptions])` (native is
`write(Transaction)`), and the typed bulk check is the overload
`checkBulk(Consistency, List<TypedCheck>[, CheckOptions])`, distinct from the native
`checkBulk(Collection<PermissionCheck>[, CheckOptions])`. Both forward `CheckOptions`
unchanged, so tracing, context, deadlines and the bulk item-error mode apply.

Connection construction is not an instance capability. Existing generated
plaintext/system-TLS factories remain. Advanced native transport configuration (including
`SpiceDBClient.fromChannel`) uses `new TypedClient(nativeClient, ownsClient)` rather than
new permissive static factory overloads.

## Schema positions and native composition

| Item | Generated Java API |
| --- | --- |
| permission usersets | `organization.canAdminSubject()` → `OrganizationCanAdminRef` (+ `withExpiration(Instant)` → `OrganizationCanAdminExpiring`); `canAdmin()` stays the permission |
| observability | `Permission.resourceType()/resourceId()/name()/toString()` (`type:id#perm`), `ref()`; `PermissionRef.name()/toString()` (`type#perm`) |
| transactions | `new Txn().touch/create/delete/mustMatch/mustNotMatch`, `size()`, `build()` → native `Transaction`; `TypedClient.writeTxn` |
| native round trip | `TypedRelationship.toRelationship()`, `fromRelationship(Relationship)` (not schema-checked), `ofError(RuntimeException)`, `error()`, `toFilter()`, `toString()`, `equals` |
| bulk checks | `Permission.forSubject(S)`, `checksFor(subject, permissions...)`, `TypedClient.checkBulk(Consistency, List<TypedCheck>)`, `Permission.permissionCheck(S, Consistency)` |
| filters/constants | `OrganizationFilter()`, `ref.filter()`, `ref.adminFilter()`, `withSubject(Filter, Subject)`, `OrganizationRelations.ADMIN`, `OrganizationPermissions.CAN_ADMIN` |
| wildcards | `UserWildcard()` → `UserWildcardRef`; `user:*`-only writers admit only it |
| identity keys | `ref.inviterKey(subject)` → `RelationshipKey` (base subjects only), accepted by `Txn.delete` |
| relation checks | `ref.memberRelation()` → `Permission<OrganizationMemberRelationSubject>` |
| by-name | `permissionByName`, `relationByName`, `relationshipByName` (throw `NotInSchemaException` / `SubjectNotAllowedException`), `Permission.forDynamicSubject(Subject)`, `XRelations.names()`, `XPermissions.names()` |

## Verification ledger

- `go test ./java/` pins the generated surface, the package option, the formatter option,
  the 100-column limit and collision failures.
- `testdata/java` compiles the generated fixture, runs `spotlessCheck` (google-java-format
  1.22.0) against it, runs `AdoptionTest` (no server), `TypedParityTest`, the forwarding
  tests, and the live `PermissionsTest`/`AdoptionIntegrationTest`.
- `mage javaTypeErrors` compiles `type_errors/` with every diagnostic and requires an error
  on each of its lines marked `// should not compile`, no error elsewhere, and no
  "cannot find symbol" on a marked line.
