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

## Verification

Generate all four target fixtures from testdata/sample.zed, build/type-check,
run negative type fixtures, and run live integration examples. Template changes
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

## Java naming rules

- Everything is nested in one `Permissions` class in the `--java.package` package.
  Each definition `d` gets the factory `D(id)` → record `DRef`, `DFilter()`,
  name-constant classes `DRelations` / `DPermissions` (`UPPER_SNAKE` constants plus
  `names()`), a `PermissionRef` constant `D_P` per permission, and, when `d:*` is
  allowed anywhere, `DWildcard()` → `DWildcardRef`.
- On `DRef`: permission `p` → `p()` (`Permission<DPSubject>`); relation `r` →
  writer `r(DRSubject)`, key `rKey(DRKeySubject)` → `RelationshipKey`, check
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
  silently renamed.
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
