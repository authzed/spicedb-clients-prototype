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

- **Treat caveat expressions as opaque.** Parsing replaces every caveat body
  with `true` (preserving line numbers) before compiling, so schemas whose
  caveats call functions outside SpiceDB's default CEL environment generate
  normally. Only names and parameter types are used; SpiceDB validates
  expressions on write.

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
  named; no identifier is silently renamed. `--go.rename=<definition>=<GoName>`
  (comma-separated for several) overrides a definition's identifier stem
  without changing the schema names sent to SpiceDB, e.g.
  `--go.rename=subject=SubjectDef` for a schema with `definition subject`.
  `--go.package` sets the package name, and output is gofmt-formatted.
- `RKey(subject)` is generated only for relations admitting a caveated or
  expiring subject; elsewhere the writer's `TypedRelationship` already
  identifies the tuple. The typed `Txn` exposes `Updates()`, `Preconditions()`
  and `Err()`. `WithSubject` always pins the subject relation (direct for a
  subject without one); `WithSubjectAnyRelation` does not.
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
  (base refs only; generated only when `r` admits a caveated or expiring
  subject, since otherwise the writer's `TypedRelationship` already identifies
  the tuple), check property `rRelation`, filter `rFilter()`. A userset
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
  `errRelationship`, `withSubject`, `withSubjectAnyRelation`, `NotInSchemaError`,
  `SubjectNotAllowedError`. `TypedTransaction` exposes `updates`,
  `preconditions` (native values; empty after an error) and `error`.
  `withSubject` always constrains the subject relation (direct for a subject
  or factory without one); `withSubjectAnyRelation` leaves it unconstrained.
- Any collision between generated identifiers fails generation with both sources
  named, including schema names that would shadow a global or support name
  (`Date`, `Permission`), a built-in ref member (`filter`, `toString`), or a
  function property for a permission static (`name`, `length`), and names the
  module imports from the native client (`definition subject` would produce
  `SubjectRef`). Nothing is silently renamed:
  `--typescript.rename=<definition>=<Name>` (comma-separated for several;
  `Name` is PascalCase) overrides a definition's identifier stem without
  changing the schema names sent to SpiceDB, e.g.
  `--typescript.rename=subject=SubjectDef`, and the collision error suggests it.
  Unknown definitions and invalid names fail generation. The integration test
  generates `testdata/typescript/rename.zed` this way into `renamed.ts`,
  type-checks it and runs `rename.test.ts`.
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

## Java naming rules

- Everything is nested in one `Permissions` class in the `--java.package` package.
  Each definition `d` gets the factory `D(id)` → record `DRef`, `DFilter()`,
  name-constant classes `DRelations` / `DPermissions` (`UPPER_SNAKE` constants plus
  `names()`), a `PermissionRef` constant `D_P` per permission, and, when `d:*` is
  allowed anywhere, `DWildcard()` → `DWildcardRef`.
- On `DRef`: permission `p` → `p()` (`Permission<DPSubject>`); relation `r` →
  writer `r(DRSubject)`, key `rKey(DRKeySubject)` → `RelationshipKey` (only when
  `r` admits a caveated or expiring subject; otherwise the writer's
  `TypedRelationship` already identifies the tuple), check
  `rRelation()` (`Permission<DRRelationSubject>`), filter `rFilter()`; plus
  `filter()`, `permissionByName`, `relationByName`, `relationshipByName`. A userset
  `d#r` on a relation gets the accessor `r()` and the writer moves to `forR`. A
  userset `d#p` on a permission gets the accessor `pSubject()`, so `p()` stays the
  permission accessor.
- Subject variants are records named `<Prefix><Caveat>[Expiring]` (prefix `D`, `DR`
  or `DWildcard`; the base adds `Ref`), reached by `with<Caveat>(ctx)` and
  `withExpiration(Instant)`. Admission is a sealed interface per position: writers
  (`DRSubject`) admit exactly the schema's subject shapes; keys (`DRKeySubject`)
  admit only base shapes; checks (`DRRelationSubject`, `DPSubject`) admit base and
  caveated shapes, the members behind userset subjects, and type sentinels (`DType`,
  `DRType`) for lookups. Sentinels are never writable.
- Any remaining collision (between types, static members, members of one record, or
  with the hand-written runtime and the native types it names) fails generation with
  both sources named, as does an identifier that is a Java reserved word; nothing is
  silently renamed. `--java.rename=<definition>=<JavaName>` (comma-separated for
  several; `JavaName` must start with an upper-case letter) overrides a definition's
  identifier stem without changing the schema names sent to SpiceDB, e.g.
  `--java.rename=subject=SubjectDef` for `definition subject` (whose `SubjectRef`
  would shadow the native type); unknown definitions and invalid names fail, and
  the collision error suggests the option.
- `Txn` exposes `updates()`, `preconditions()` (copies; empty after a construction
  error) and `error()`. `withSubject` always pins the subject relation (direct for
  a subject without one); `withSubjectAnyRelation` does not. Generated comments
  never interpolate a schema name after an article, so they need no "a"/"an"
  selection.
- Output is google-java-format: the template and the Go writer emit
  google-java-format's layout directly (the generator is Go and cannot run the Java
  formatter in-process), and the fixture's `spotlessCheck` (google-java-format
  1.22.0, the version spicedb-java uses) verifies it. `--java.formatter="<cmd>"`
  pipes the output through a formatter command, for names long enough to need
  wrapping the writer does not model.
- Negative type fixtures mark each forbidden line `// should not compile`.
  `mage javaTypeErrors` compiles them with every diagnostic (`-Xmaxerrs`) and
  requires an error on every marked line, no error on any unmarked line, and no
  "cannot find symbol" on a marked line.

## Python naming rules

- Each definition `d` gets one frozen dataclass `D(id)` that is both its
  resource ref and its direct subject ref (a definition may be both), plus
  `D.type_filter()`, `DRelations.R`/`DPermissions.P` name constants and
  `D_RELATION_NAMES`/`D_PERMISSION_NAMES` tuples (schema order). A userset
  `d#r` is the class `DR(id)`; `d:*` is `DWildcard()`. Caveat and expiration
  variants are `<Base><Caveat>[Expiring]`, built with `with_<caveat>(ctx)` and
  `with_expiration(t)`; a caveated, expiring subject chains through its
  caveated variant.
- On `D`: permission `p` → property `p` (a narrowed `Permission`); relation
  `r` → writer `r(subject)`, key `r_key(subject)` → `RelationshipKey` (only
  when `r` admits a caveated or expiring subject; elsewhere the writer's
  `TypedRelationship` already identifies the tuple), check property
  `r_relation`, filter `r_filter()`; plus `filter()`,
  `permission_by_name`, `relation_by_name` and `relationship_by_name` (raising
  `NotInSchemaError` / `SubjectNotAllowedError`). A userset `d#r` on a
  relation gets the property `r` and the writer moves to `for_r`. A userset
  `d#p` on a permission gets the property `p_subject`, so `p` stays the
  permission accessor. Keywords and shadowed builtins gain a trailing `_`.
- Module-level: `<D><R>Subject` (exact writer admission), `<D><R>KeySubject`
  (base refs only), `<D><P>Subject` / `<D><R>RelationSubject` (check
  admission, following usersets to the subjects that reach them, with caveated
  variants for context; wildcards admit their concrete objects) and the
  matching `...SubjectType` aliases for `lookup_subjects`. Static
  `lookup_resources` refs are `D_P` (e.g. `DOCUMENT_VIEW`).
- Composition: `Permission`/`PermissionRef` expose `resource_type`,
  `resource_id`, `name`, `ref` and `str()`; `Permission.permission_check(s)`
  and `checks_for(s, *perms)` build native `PermissionCheck`s (validated at
  runtime) for the native `check_bulk`. `TypedRelationship` converts with
  `to_relationship()`, `from_relationship()` (not schema-checked) and
  `from_error()`; writers defer construction errors into it. `TypedTransaction`
  defers the first error to `build()`/`TypedClient.write_txn`, which never
  shadows the native `write`, and exposes `updates`, `preconditions` (native
  values, empty after an error) and `error`. `with_subject(filter,
  subject_or_class)` narrows native filters and always pins the subject
  relation (direct for a subject without one); `with_subject_any_relation`
  leaves it unconstrained. `TypedClient.check_dynamic` checks by-name permissions with
  runtime subject validation.
- Sync and aio clients render from one template. Any collision between
  generated identifiers fails generation naming both sources; nothing is
  silently renamed. `--python.rename=<definition>=<ClassName>`
  (comma-separated for several) overrides a definition's class-name stem
  (and its `UPPER_SNAKE` constant stem) without changing the schema names sent
  to SpiceDB, e.g. `--python.rename=subject=SubjectDef` for a schema with
  `definition subject`; unknown definitions and non-PascalCase names fail, and
  the collision error suggests it. A test generates such a schema (with a
  caveat calling an undeclared CEL function) and type-checks it with pyright.
  `--python.module` names the types module (default
  `permissions`); `sync.py`/`aio.py` import it relatively. Output is
  `ruff format` clean at the default line length without running ruff.
- `check`, `lookup_resources` and `lookup_subjects` get one `@overload` per
  admitted permission/relation when there are at least two; a lone overload
  becomes the implementation's signature, since Python rejects a single
  overload.
- Negative type fixtures mark each forbidden line `# should not type-check`.
  The Magefile runs pyright with JSON output and requires an error on every
  marked line and on no other line.
