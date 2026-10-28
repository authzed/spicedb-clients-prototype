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
  `r` → writer `r(subject)`, key `r_key(subject)` → `RelationshipKey`, check
  property `r_relation`, filter `r_filter()`; plus `filter()`,
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
  shadows the native `write`. `with_subject(filter, subject_or_class)` narrows
  native filters. `TypedClient.check_dynamic` checks by-name permissions with
  runtime subject validation.
- Sync and aio clients render from one template. Any collision between
  generated identifiers fails generation naming both sources; nothing is
  silently renamed. `--python.module` names the types module (default
  `permissions`); `sync.py`/`aio.py` import it relatively. Output is
  `ruff format` clean at the default line length without running ruff.
- Negative type fixtures mark each forbidden line `# should not type-check`.
  The Magefile runs pyright with JSON output and requires an error on every
  marked line and on no other line.
