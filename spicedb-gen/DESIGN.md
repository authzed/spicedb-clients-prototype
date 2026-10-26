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
