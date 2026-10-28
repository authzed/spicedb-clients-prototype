# SpiceDB Client Libraries — Native API Design

This document defines the shared principles and behavior of the client libraries.
Each language's `DESIGN.md` specifies its native API and implementation details.

## API principles

- **Share semantics while following each language's conventions.**
  - Prefer native types, iteration, cancellation, ownership, and errors over
    identical method names or argument order.
  - Ordinary operations must not require protobuf or gRPC knowledge.

- **Keep one canonical, extensible API for each operation.**
  - Add optional behavior through an options container rather than parallel
    method families. Use functional options, keyword arguments, option objects,
    or builders as appropriate to the language.
  - Prefer named enums over string options or boolean flags that select behavior,
    making choices discoverable and allowing additional modes without changing
    the operation's signature. Reserve booleans for inherently binary values.
  - Bulk operations accept collections without consuming the operation's only
    variadic argument slot. Singular and bulk checks remain distinct operations.
  - Cover every non-deprecated protocol RPC, including schema inspection,
    expansion, import/export, watch, counters, and materialize services.
  - Mark experimental operations and upstream deprecations using native mechanisms.

- **Keep protocol access secondary and ownership explicit.**
  - The primary API uses native request and result types.
  - A documented escape hatch may expose the existing authenticated connection
    or protocol client; it must not create a connection or transfer ownership.
  - Supplied connections remain caller-owned unless ownership is explicitly transferred.
  - Every client has a constructor from an existing connection or channel, such as
    an in-memory or buffered-listener connection. Close never closes a supplied
    connection. Operation options (deadlines, retry policy) still apply;
    credential and transport options do not, because the connection already carries them.
  - Every operation accepts a transport escape hatch: per-call transport options
    and capture of response headers and trailers. This is the supported route to
    transport-level data, including data that pre-1.30 servers send in trailers.
    Callers do not need the raw protocol client for it.
  - Native↔protocol conversions for requests, results, relationships, filters,
    preconditions, consistency, updates and traces are exported in a secondary
    namespace, so tools that persist or print protocol-shaped data reuse them.
  - Generate protocol bindings from the upstream schema; do not edit generated
    protocol source by hand. The only hand-maintained files in a generated
    package are legacy helpers that must share the generated types' package (see
    "Be the single copy..."); each proto tier's DESIGN.md lists them by name.

- **Support SpiceDB 1.30 and later natively.**
  - Behavior that only older servers need is reachable through the transport
    escape hatch, not first-class API.

- **Be the single copy of the protocol bindings in any process.**
  - Protocol runtimes admit one definition per proto file and message name: Go's
    `protoregistry`, the Python and Ruby descriptor pools, and the JVM class path.
    A second, independently generated copy of `authzed.api.*` panics or misbinds
    at load time. The proto tier is the canonical binding. It must never be
    generated as a sibling copy beside a legacy `authzed-*` library.
  - Legacy libraries migrate by re-exporting (aliasing) the proto tier, not the
    other way around, so they can be deprecated without a flag day. The proto tier
    therefore generates everything legacy consumers use on those message types,
    such as Go fast-path marshalling, validation, and gateway output, and carries
    any legacy hand-written helpers bound to those types.
  - Each proto tier ships the bridge generator for its legacy library and a test
    that loads both in one process. Until a language's bridge exists, its README
    documents the conflict instead of implying coexistence.

- **Evolve public APIs deliberately.**
  - Public APIs are append-only unless a breaking change is explicitly approved.
  - Keep designs, implementations, tests, examples, generated fixtures, API
    reports, and README snippets consistent.
  - Describe changes in each client's existing Unreleased changelog section;
    include before/after migration examples for breaking changes.

## Domain types and checks

- **Separate object identity, persisted relationships, and permission questions.**
  - `ObjectRef` identifies an object by type and ID. `SubjectRef` adds an optional
    userset relation; absence means a direct subject. Wildcards retain server semantics.
  - `Relationship` contains resource, relation, subject, optional persisted
    caveat context, and expiration. It never carries check-time context.
  - `PermissionCheck` (`CheckRequest` in TypeScript) contains resource,
    permission, subject, and optional evaluation context.

- **Return extensible native results from relationship writes.**
  - Every client returns `WriteResult` with a native `ZedToken` revision rather
    than returning the token alone. Generated touch/create/delete helpers return
    the same result; callers explicitly extract its revision for later reads.

- **Make consistency an explicit caller decision.**
  - Every consistency-bearing read requires a strategy, in its request or a
    required argument. Reject mixed bulk strategies or tokens before any RPC.
  - Provide full, minimize-latency, at-least-token, and exactly-token strategies.
    Fallback helpers must explicitly name the strategy used when a token is absent.
  - Every language exposes a dedicated native `ZedToken` value type with an opaque
    representation and lossless string construction and serialization. Public APIs
    accept and return this type rather than plain strings; token behavior belongs
    on the type.

- **Return authorization results that cannot silently turn uncertainty into a grant.**
  - Singular and bulk checks return `CheckResult`, with permissionship, missing
    context keys, and the checked-at revision.
  - Only an unconditional grant satisfies the permission predicate. Conditional,
    denied, and unspecified results remain distinguishable; unknown values never grant.
  - Ruby and JavaScript examples use the explicit predicate rather than object
    truthiness. Python may provide safe boolean conversion.
  - Any aggregate helpers count unconditional grants only and return false for
    empty input.
  - Preserve singular response expiration independently of tracing.

- **Make unconditional resource lookup results easy to select safely.**
  - `LookupResources` exposes a named client-side permissionship filter: all
    results (default), or unconditional `HAS_PERMISSION` results only.
  - Filtering excludes conditional and unknown states before yielding. Limits
    count yielded matches; pagination advances through skipped results.
  - Examples presenting accessible resource IDs select unconditional results
    explicitly. Examples resolving caveats may retain conditional results and
    must distinguish them from grants. Generated facades forward the filter.

- **Preserve the association between bulk inputs and results.**
  - Validate response cardinality and each echoed request before exposing
    decisions; reject missing, duplicated, mismatched, or reordered pairs.
  - Per-item failures remain typed errors with the caller's absolute input index.
  - Split checks into at most 1,000 items per request. Each result carries its
    own chunk's revision; chunks do not imply a shared snapshot.
  - By default the first per-item failure fails the call. A named mode instead
    returns every item's result, with per-item errors carried on the result
    alongside the other items' decisions. An errored item never grants.

- **Preserve caller data and represent immutability honestly.**
  - Merge call-level check context with per-item overrides by key; the item wins.
    Absent context remains absent on the wire.
  - Preserve JSON types recursively, including null. Reject unrepresentable
    caller data with a typed error naming the affected field or key.
  - Immutable values snapshot nested caller-owned collections. Mutable builders
    are documented as mutable and snapshot data when producing immutable values.
  - Model mutually exclusive variants structurally where supported, such as
    Rust enums and TypeScript discriminated unions. Represent unknown server states explicitly.

- **Make native values observable and testable without the server.**
  - Relationships, updates, filters, preconditions and transaction builders have
    readable string forms and structured log/serialization forms. No native value
    logs, prints or serializes as an empty object. Transactions expose their update
    count without copying.
  - Relationship filters match a native relationship client-side with server
    semantics: resource type, ID or ID prefix, relation, subject type and ID, and
    the `Any`/`Direct`/`Named` subject relation distinction. Test doubles use this
    rather than reimplementing filter semantics.
  - Each client ships an in-memory test double for relationship writes (with
    preconditions and create conflicts), filtered reads and deletes, and
    registered check results. Its surface matches the real client's so one
    caller-defined interface or protocol accepts both.
  - Languages whose constructors return errors provide panicking/asserting
    variants for compile-time constants and tests, such as Go `Must*`.
  - Relationship accessors offer short paths for the most common fields
    (resource ID and subject ID) in addition to the nested object accessors.
  - Tuple parsing and formatting cover SpiceDB's full syntax, including
    `[caveat:{...}]` and `[expiration:...]`, and round-trip with SpiceDB's own
    parser. Copy helpers replace a relationship's resource or subject.

## Check debug tracing

- **Make tracing an optional diagnostic on both singular and bulk checks.**
  - Tracing defaults to disabled. Singular checks use `CheckPermission` and its
    `with_tracing` flag; bulk checks apply their flag to every chunk.
  - Check tracing is distinct from lookup `with_debug` and never changes the
    permission predicate.

- **Return complete, structured native traces.**
  - `CheckResult` carries an optional native trace; absent traces remain absent.
    Bulk results retain their own per-item traces.
  - Preserve schema and recursive check details: resource, subject, permission,
    permission type, result, caveat evaluation, duration, resolution, expiration,
    operation ID, and source.
  - Caveat evaluation includes expression, result, native context, missing keys,
    and name. Resolution preserves cached-result versus subproblems presence,
    including an explicitly false cached flag.
  - Use native timestamps, durations, and safe unknown enum values, without
    exposing protobuf objects or reducing the trace to a formatted string.

- **Preserve tracing through typed wrappers and verify it against the server.**
  - Generated facades forward the option and return the same trace-bearing result.
  - Tests cover flag transmission, recursive conversion, absent traces, and
    per-item bulk traces. A live example asserts populated singular trace data.

## Deadlines, cancellation, and retry

- **Treat a deadline as a budget for the entire operation.**
  - Unary calls default to 30 seconds. The budget covers attempts, backoff,
    chunks, and automatically fetched pages, starting when execution begins.
  - Use monotonic time and pass only the remaining budget to subsequent work.
    An earlier caller deadline wins; an attempt timeout may shorten the budget,
    never extend it.
  - Streams and imports have no default lifetime timeout, but accept explicit
    total deadlines. Finite lookups and exports support deadlines even when
    their underlying RPC streams.
  - A named mode disables the client default deadline. The caller's own deadline
    or cancellation still applies. Zero or negative durations are never used as
    a magic "disabled" value.

- **Carry native cancellation through every layer.**
  - Check cancellation before new work and during backoff; distinguish caller
    cancellation from a server-originated cancelled status.
  - Go preserves context error identity; Python propagates task cancellation;
    TypeScript accepts `AbortSignal`; C# preserves the caller's cancellation token.
  - Rust releases owned calls when futures or streams are dropped. Java and
    Ruby expose working explicit stream cancellation.

- **Retry only when replay is safe.**
  - By default, retry idempotent reads on `UNAVAILABLE` or `ABORTED`, up to three
    retries, using full-jitter exponential backoff starting at 100 ms. The default
    never retries `RESOURCE_EXHAUSTED`.
  - Never replay mutations, including writes, deletes, imports, and counter
    registration, under any policy.
  - Stream establishment may retry only before any item is exposed. Never
    restart a partially consumed stream.
  - All retries share the original deadline and cancellation signal.
  - A client-wide retry policy option replaces the default: max attempts,
    retryable read codes (a caller may opt into others, such as
    `RESOURCE_EXHAUSTED`), backoff, and an on-retry callback. A named mode
    disables retries entirely, for callers that apply their own policy. Stream-establishment retry has its own named
    setting, so long-lived streams like watch can opt out. No configuration can
    make a mutation replay.

## Streams and ownership

- **Make resource release deterministic and visible in the API.**
  - Every stream has a supported close or cancel path that releases the actual
    transport, including while waiting for the next item.
  - Use native loop or scope cleanup; do not rely on garbage collection.
  - Python supports `with`/`async with`; Ruby supports block consumption and a
    closeable Enumerable; Java uses try-with-resources.
  - TypeScript returns `AsyncIterableIterator` directly. Go iterator exit,
    C# enumerator disposal, and Rust drop release their calls.
  - Tests observe server-side termination after early abandonment.

- **Import incrementally with backpressure and predictable cleanup.**
  - Accept native iterable or async-iterable inputs.
  - Producer errors, cancellation, and conversion failures cancel an active upload.
  - When the server aborts a client stream, report the server's final status
    (for example `ALREADY_EXISTS` for a duplicate), never the transport's
    end-of-stream signal from the failed send.
  - The relationships-per-message batch size is configurable. One import call is
    one stream and one transaction; callers wanting several transactions make
    several calls.

- **Expose paging state on paged reads.**
  - Relationship reads, exports and resource lookups yield each item with its
    after-result cursor and, where the response carries one, its read-at or
    looked-up-at revision, and accept a starting cursor. Resuming requires
    repeating the original request and page size. Cursors stay a type distinct
    from revision tokens.
  - Every page of a paged read repeats the caller's consistency and one fixed
    page size. SpiceDB hashes request arguments, including consistency and
    limit, into each cursor and rejects a cursor sent with different arguments;
    the cursor itself pins later pages to the first page's revision. Enforce a
    total limit client-side rather than by shrinking the last page's limit.
    Live tests must read across more than one page.

## Filters and deletion

- **Expose full relationship filters on every watch API.**
  - Accept native relationship filters and forward them to the protocol's
    relationship-filter field; multiple filters match with OR semantics.
  - Examples prefer filters, including a resource-type-only filter for a whole
    type. Reject nonempty object types combined with nonempty relationship filters.
  - Snapshot caller-owned collections and preserve exact subject relation scope.
  - A named mode requests schema-change events, combinable with checkpoints.
    Events report a schema-updated fact distinct from relationship updates.

- **Filters must preserve the caller's exact intended scope.**
  - Distinguish subject relation filters `Any`, `Direct`, and `Named(name)`.
    Absence means Any; Direct is a present empty relation filter.
  - Converting a relationship to an exact filter preserves all identifying tuple
    fields, including the subject relation. Caveat and expiration are not filterable identity.
  - Reject filters the protocol cannot represent; never silently widen them.

- **Separate bounded deletion from deletion to completion.**
  - Bounded deletion issues one request and returns revision, count, completion,
    and any server resume cursor. In the default partial mode it requires a
    positive total limit.
  - A named all-or-nothing mode disallows partial deletion: the server rejects a
    delete that exceeds its limit instead of deleting part of it. In that mode
    the limit may be omitted to use the server's default.
  - Delete-to-completion accepts a per-batch progress callback with the
    cumulative acknowledged count, revision and cursor.
  - Name the operation `deleteAllRelationshipsNonAtomic` using native language
    conventions. It uses a separate positive page size, defaulting to 1,000, and
    one operation budget. An `autoPage` flag must not change the meaning of a limit.
  - Forward each non-empty response deletion cursor to the next batch unchanged.
    An absent or empty cursor is omitted on the next request. Failure progress
    retains the cursor from the latest acknowledged response.
  - Re-evaluate preconditions on each request. Each batch is atomic; the entire
    operation is not, and completed batches remain committed on failure.
  - Native `DeleteResult` exposes its revision as `revision`, matching
    `WriteResult` and following each language's naming conventions.
  - Unknown completion states are errors, never evidence of completion or permission
    to continue deleting.

- **Report acknowledged progress without claiming certainty about lost responses.**
  - Provide discoverable typed access to progress on failures and cancellation,
    preserving the language's native cancellation behavior.
  - Interrupted delete-all attaches cumulative acknowledged count and the latest
    revision and available cursor to its native error or cancellation result.
  - Preserve acknowledged progress even when the latest response is malformed.
  - An unacknowledged request may have committed. Do not report otherwise or
    automatically replay it.

## Errors and transport security

- **Expose native errors without losing server diagnostics.**
  - Map statuses, including `OUT_OF_RANGE` and `UNAUTHENTICATED`, to typed errors.
  - Preserve the original status or cause and structured ErrorInfo reason,
    domain, and metadata. Local validation uses the documented invalid-argument category.
  - Keep the server's status message verbatim in its own field. The display
    message may add operation context, but callers that forward errors must be
    able to reproduce exactly what the server said.
  - Native errors interoperate with the ecosystem's standard status inspection in
    both directions. A native error reports its matching transport status code
    through the language's status accessor, including errors for local
    cancellation, local validation, and errors built by caller code. Code
    classification helpers also accept raw transport status errors.
  - Provide a public constructor for native errors from a code and message, so
    test doubles produce errors that every predicate treats like real ones.

- **Use secure transport defaults and explicit trust configuration.**
  - Secure constructors delegate to the ecosystem's default trust source.
    Python, Ruby, and Node support custom CA material because their defaults may
    differ from the host's managed trust store.
  - Reject TLS material combined with plaintext. Document actual TLS and mTLS
    capabilities rather than inferring them from system-root support.

- **Require an explicit decision before sending credentials over remote plaintext.**
  - Every client library must expose a documented, named opt-in that permits
    authenticated plaintext connections to non-loopback hosts, including remote
    hostnames and IP addresses. This capability must be available through the
    normal public API, without requiring a raw transport escape hatch.
  - Reject remote plaintext credentials unless that opt-in is supplied. Validate
    using the transport's endpoint parser or fail closed when it is unavailable.
  - Constructors communicate transport posture. Raw access does not bypass validation.
  - Configuration escape hatches document where arbitrary settings can supersede guards.

## Generation and validation

- **Keep typed generation a thin facade over the native clients.**
  - Forward options, consistency, cancellation, tokens, results, and stream ownership.
  - Generated clients expose every native capability directly unless a documented
    language constraint prevents it. Keep schema-aware signatures where useful;
    use native request/options/results for the remaining operations, including
    non-atomic filter deletion and watch.
  - Schema-derived context may feed permission checks, but must not become
    check-only data on persisted relationships.

- **Generate every schema position a caller can name, with exact constraints.**
  - Usersets on permissions (`org#can_admin`) get subject refs just like usersets
    on relations. Name collisions resolve with one documented rule per language.
  - Wildcard subjects (`user:*`) get a distinct wildcard ref. A wildcard-only
    relation admits only that ref; `user | user:*` admits both.
  - Relations are checkable as well as writable, through an accessor that cannot
    collide with writer, subject-ref or permission accessors.
  - Deletes and exact filters identify a relationship by tuple identity alone.
    Callers never invent caveat context or expiration to delete one.
  - Validated by-name lookups resolve runtime permission and relation names to
    typed values, rejecting names (and subjects) the schema does not allow.
    Generated name lists support startup verification.

- **Make generated values composable with the native layer in both directions.**
  - Typed permissions, permission refs and relationships expose their resource
    type, ID, permission or relation and a canonical string form.
  - Typed relationships convert to native relationships, and native relationships
    wrap into typed ones, documented as not schema-checked. Typed values also
    produce native exact filters, type-level filters, relation-scoped filters,
    native permission checks, and relation-name constants.
  - A typed transaction builder mixes touch, create, delete and preconditions in
    one atomic write. It defers the first construction error to build/write time
    instead of requiring a check per step, and never shadows a promoted native method.
  - Typed bulk checks forward native options and preserve input order, per-chunk
    revisions and per-item errors.
  - Generated output is formatted with the language's canonical formatter, and
    the output package or module name is configurable.
  - Generate fixtures from templates, then compile, type-check, and execute them.
    Include positive and negative type tests. Each forbidden construct must fail
    on its own: a check that only proves "something failed to compile" lets one
    error mask a regression elsewhere.
  - Generation fails, naming both sources, when schema names would produce
    colliding identifiers. It never silently renames.

- **Guide AI coding agents to the right tier and safe integration.**
  - The root `AI_INTEGRATION_GUIDE.md` is the language-neutral decision guide:
    typed by default for a known schema, idiomatic for schema-agnostic code,
    raw only through the escape hatch, never a second protocol copy. It also
    states the integration rules (fail-closed checks, deliberate consistency,
    atomic writes, no blind write retries, paging resumption, test doubles).
  - Each client ships `AI_INTEGRATION_GUIDE.md` in its package directory,
    following that structure with concrete, current code for its language,
    and its package-level documentation points to it. Snippets are checked to
    compile against the current API (generated snippets against the sample
    schema fixture). Update the guide with any change to the APIs it shows.

- **Document the path off the legacy libraries.**
  - Each client with an official `authzed-*` predecessor ships `MIGRATION.md`
    with before/after snippets for every observable behavior change: request
    shapes, defaults (deadlines, retries), error matching, transport security
    opt-ins, constructor arguments, and accessor paths.
  - Library modules declare the lowest dependency versions they actually need,
    and keep build tooling out of the requirements consumers inherit.

- **Verify observable behavior rather than merely successful execution.**
  - Reconcile examples against their runners by name, execute them in CI, and
    require assertions that fail when the demonstrated behavior is removed.
  - Explicitly report skips; a filter matching zero tests is not a passing check.
  - Cover immutability, JSON conversion, context precedence, safe unknown values,
    exact filters, deletion bounds/progress, cancellation, and shared deadlines.
  - Security and lifecycle tests exercise real TLS handshakes and transport release.

## CI conventions

- **Give each language an independently runnable workflow.**
  - Use `.github/workflows/<lang>.yaml`; keep cross-cutting checks in `meta.yaml`
    and generator checks in `spicedb-gen.yaml`.
  - Include `paths-filter`, `lint`, `unit`, `integration`, and, where supported,
    `apicompat`. Work jobs require changed paths; compatibility jobs also require a PR.
  - Integration targets start and stop their own Docker services. Run at most
    one integration target per job because targets share the default local port.

- **Keep compatibility decisions visible and scoped to a change.**
  - Mechanically checked languages belong in `apiCompatLanguages`: Go, Python,
    TypeScript, C#, Java, and Rust. Ruby relies on tests and review until a
    reliable signature-diff gate is integrated.
  - The PR label `breaking-change-acknowledged` explicitly skips compatibility
    checks. Read its current state from the API with `pull-requests: read`;
    a local `mage updateAllowBreak` does not grant a CI exception.
  - The skip also suppresses compatibility-tool failures, so it remains limited
    to the labelled PR and is announced in job output.

- **Trigger checks for every dependency they actually consume.**
  - Language filters include their workflow, native and protocol directories,
    root `Magefile.go`, and root Go module files.
  - Generator filters include the native packages imported by generated fixtures.
    Pair filter coverage with tests that detect incompatible changes.

- **Keep workflow infrastructure consistent and proportionate.**
  - Default to `depot-ubuntu-24.04-arm-4`; use `arm-small` for trivial checks and
    `arm-8` for cross-language work. Explain deviations in comments.
  - Pin actions outside `actions/*` and `authzed/*` to commit SHAs with version
    comments; maintain them through Dependabot.
  - A new language adds its workflow, dependency update configuration, README
    quick start, and root language registration, including compatibility where applicable.
  - Add new cross-language check types consistently to every language workflow,
    including appropriate path filters.
