# Typed Client Generator Design

- The [root design](../DESIGN.md) defines shared semantics, RPC coverage, security, and verification.

## Contract

The generator reads a .zed schema through SpiceDB's DSL compiler, constructs
its existing language-neutral model, and emits typed facades for Go, Python
(sync and async), TypeScript, and Java. Keep generator registration and schema
semantics consistent with the upstream schema.

The root DESIGN.md and each target client's DESIGN.md define the contract.
Regenerate fixtures and keep examples and negative type tests aligned with the
current native APIs.

## Generated APIs

- Resource/subject factories continue to enforce schema-derived relation,
  permission, and subject compatibility at compile/type-check time.
- Construct the idiomatic PermissionCheck/CheckRequest, never a Relationship
  with a fake relation. Check-time context is distinct from stored caveat data.
- Forward the tracing option for singular and bulk checks; expose native debug
  traces unchanged on results and exercise forwarding in generator tests.
- All checks return the idiomatic CheckResult including conditional state,
  missing keys and revision. Only its grant predicate authorizes access.
- Forward explicit consistency, caller cancellation and operation options.
  Generated check/write/lookup methods must not erase timeout or signal support.
- Subject-attached caveat context becomes per-check context and overrides
  call-level defaults by key. Write operations instead persist the caveat.
- Streaming methods return native closeable iteration directly. TypeScript
  lookup methods are not async functions returning promises of iterators.
  Python exposes the closeable sync/async protocol used by its idiomatic client.
  Java keeps Stream.close ownership; Go preserves context and iterator cleanup.
- Tokens use the target client's dedicated opaque native ZedToken value type,
  including Python; never plain strings or proto wrappers.
  - Preserve lossless explicit serialization at storage boundaries.
- Behavioral options use the target client's named enums.
  - Forward tracing, lookup modes, transport security and remote credential
    policies without replacing them with boolean or string shortcuts.
- Client factories forward the target client's normal connection configuration;
  wrapping an existing client remains supported with explicit ownership.
  - Every generated connection helper supports the documented explicit opt-in
    for authenticated plaintext connections to remote hostnames and IPs.

- **Expose the native client’s full capabilities directly.**
  - Schema-aware operations retain compile-time relation, permission and subject
    checks. Remaining operations forward native request/options/results, including
    bounded and non-atomic deletion, watch filters, schema, counters and materialize.
  - Callers need not reach through the native-client accessor for normal operations.
  - Preserve all operation options, lazy stream ownership, cancellation and errors.
    Explicit signatures retain static discoverability; avoid dynamic catch-all proxies.
  - Coverage checks detect native capabilities absent from generated facades.

- **Generate every nameable schema position** (root DESIGN.md, "Generate every
  schema position a caller can name, with exact constraints").
  - Subject refs exist for direct objects, usersets on relations, usersets on
    permissions, and wildcards, each with its caveat and expiration variants
    (caveated-and-expiring variants are reached through the caveated variant).
  - Writers admit exactly the schema's subjects: a wildcard-only relation admits
    only the wildcard ref; an expiration-only relation admits only expiring refs.
  - Every relation has an identity-only key builder (base subjects only) for
    deletes and exact filters, and a check accessor.
  - Validated by-name lookups for permissions, relations and relationships
    (checking the subject is admitted), plus generated name lists.
  - Each language documents one collision rule below. Any remaining collision
    fails generation with both sources named.

- **Compose with the native layer** (root DESIGN.md, "Make generated values
  composable with the native layer in both directions").
  - Typed permissions/refs/relationships expose their parts and a canonical
    string; typed relationships convert to and from native ones (wrapping is not
    schema-checked) and can carry a deferred construction error.
  - Typed filters (type, resource, relation-scoped, subject-narrowed), relation
    and permission name constants, typed bulk checks, and a typed transaction
    builder that defers its first error and never shadows a native method.
  - Output is formatted with the language's canonical formatter; the output
    package/module name is a generator option.

## Verification

Generate all four target fixtures from testdata/sample.zed, build/type-check,
run negative type fixtures, and run live integration examples. sample.zed
includes the adoption cases (`organization`/`invitation`: a permission
userset, wildcard-only and mixed wildcard relations, an expiring userset, and an
expiration-only relation); every target's fixtures exercise them. Negative type
fixtures prove each forbidden line fails independently, not merely that the
file fails to compile. Template changes
must also pass generator unit tests. Typed and untyped checks must return the
same authorization state and context merge results. Tests cover cancellation
and options forwarding rather than just generated text fragments.

- **Keep lookup examples and options safe for authorization decisions.**
  - Resource lookups forward the native client-side permissionship filter.
    Examples listing accessible IDs select unconditional grants explicitly.
  - Subject lookup examples distinguish conditional results and preserve
    wildcard exclusions; a wildcard is not a concrete authorized subject ID.

- **Preserve extensible relationship-write results.**
  - Generated touch/create/delete helpers return the native client's `WriteResult`
    unchanged. Callers extract its revision explicitly; helpers do not unwrap it
    to a bare token.

## Go naming rules

- Each definition `d` gets `D(id) DRef`, `DFilter()`, `DRelations`,
  `DPermissions`, `DRelationNames()`, `DPermissionNames()`, and, when `d:*` is
  allowed anywhere, `DWildcard() DWildcardRef`.
- On `DRef`: permission `p` → `P() Permission`; relation `r` → writer
  `R(subject)`, key `RKey(subject)`, check `RRelation()`, filter `RFilter()`.
  A userset `d#r` on a relation gets the accessor `R()` and the writer moves to
  `ForR`. A userset `d#p` on a permission gets the accessor `PSubject()`, so
  `P()` stays the permission accessor.
- Subject variants are named `<Prefix><Caveat>[Expiring]`, where the prefix is
  `D`, `DR` or `DWildcard` and the base ref adds `Ref`.
- Any collision between generated identifiers fails generation with both sources
  named; no identifier is silently renamed. `--go.package` sets the package
  name, and output is gofmt-formatted.
- Negative type fixtures mark each forbidden line `// should not compile`. The
  Magefile compiles with `-gcflags=-e` and requires an error on every marked line.

## TypeScript naming rules

- Each definition `d` (PascalCase `D`) gets `D(id): DRef`, `DFilter()`,
  `DRelations`/`DPermissions` (camelCase key → raw name), `DRelationNames`,
  `DPermissionNames`, the `DRelationName`/`DPermissionName` literal unions, a
  `D.<permission>` `PermissionRef` static for lookups, and, when `d:*` is allowed
  anywhere, `DWildcard(): DWildcardRef`. Userset subject types get sentinels such
  as `TeamMemberType` for `withSubject`.
- On `DRef` (members are camelCase): permission `p` → property `p:
  Permission<"d", "p">`; relation `r` → writer `r(subject)`, key `rKey(subject)`
  (base refs only), check property `rRelation`, filter `rFilter()`. A userset
  `d#r` on a relation adds the no-argument overload `r(): DRRef` (TypeScript
  overloads by arity, so the writer keeps its name). A userset `d#p` on a
  permission gets `pSubject()`, so `p` stays the permission. Every ref also has
  `filter()`, `permissionByName`, `relationByName`, `relationshipByName` and
  `toString()`.
- Subject variants are named `<Prefix><Caveat>[Expiring]Ref` with prefix `D`,
  `DR` or `DWildcard`, reached by `withCaveatName(ctx)` and `withExpiration(at)`.
  Every ref carries a `_ref` literal naming its type, so structurally similar
  variants never assign to each other: a wildcard-only relation admits only
  `DWildcardRef`, an expiration-only relation only `...ExpiringRef`.
- Check subjects: a permission or relation check accepts its reachable subjects
  without expiration, a wildcard as its concrete base type, and usersets
  expanded to the subjects that can be their members (so `accept = grant` with
  `grant: organization#can_admin` checks a `UserRef`).
- Support types: `Permission`, `PermissionRef`, `TypedRelationship` and
  `RelationshipKey` (nominal through `#private` fields, so a key never passes as
  a writable relationship), `TypedTransaction` (written by
  `TypedClient.writeTransaction`, so it never shadows native `write`),
  `TypedCheck`/`checksFor` with `TypedClient.checkBulkTyped`, `fromRelationship`,
  `errRelationship`, `withSubject`, `NotInSchemaError`, `SubjectNotAllowedError`.
- Any collision between generated identifiers fails generation with both sources
  named, including schema names that would shadow a global or support name
  (`Date`, `Permission`), a built-in ref member (`filter`, `toString`), or a
  function property for a permission static (`name`, `length`). Nothing is
  silently renamed.
- Options: `--typescript.client-module=<specifier>` sets the module the output
  imports the native client from (default `@spicedb/client`).
  `--typescript.format=prettier` formats the output with the Prettier installed in
  the current project (`npx --no-install prettier`, honoring its configuration);
  the default `none` emits the generator's normalized layout. TypeScript has no
  toolchain-bundled formatter, and a Go binary cannot assume Node, so formatting
  is an explicit option rather than the default. The fixture is generated with
  `--typescript.format=prettier` and the Magefile checks it with
  `prettier --check`.
- Negative type fixtures put `// @ts-expect-error` on the line before each
  forbidden single-line statement. tsc reports an unused directive as an error, so
  each line must fail on its own; the Magefile rejects directives that do not
  precede a one-line statement.
