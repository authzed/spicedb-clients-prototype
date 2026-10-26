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
  - Generate protocol bindings from the upstream schema; do not edit generated
    protocol source by hand.

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

- **Preserve caller data and represent immutability honestly.**
  - Merge call-level check context with per-item overrides by key; the item wins.
    Absent context remains absent on the wire.
  - Preserve JSON types recursively, including null. Reject unrepresentable
    caller data with a typed error naming the affected field or key.
  - Immutable values snapshot nested caller-owned collections. Mutable builders
    are documented as mutable and snapshot data when producing immutable values.
  - Model mutually exclusive variants structurally where supported, such as
    Rust enums and TypeScript discriminated unions. Represent unknown server states explicitly.

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

- **Carry native cancellation through every layer.**
  - Check cancellation before new work and during backoff; distinguish caller
    cancellation from a server-originated cancelled status.
  - Go preserves context error identity; Python propagates task cancellation;
    TypeScript accepts `AbortSignal`; C# preserves the caller's cancellation token.
  - Rust releases owned calls when futures or streams are dropped. Java and
    Ruby expose working explicit stream cancellation.

- **Retry only when replay is safe.**
  - Retry idempotent reads on `UNAVAILABLE` or `ABORTED`, up to three retries,
    using full-jitter exponential backoff starting at 100 ms.
  - Never automatically retry `RESOURCE_EXHAUSTED` or replay mutations,
    including writes, deletes, imports, and counter registration.
  - Stream establishment may retry only before any item is exposed. Never
    restart a partially consumed stream.
  - All retries share the original deadline and cancellation signal.

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

## Filters and deletion

- **Expose full relationship filters on every watch API.**
  - Accept native relationship filters and forward them to the protocol's
    relationship-filter field; multiple filters match with OR semantics.
  - Examples prefer filters, including a resource-type-only filter for a whole
    type. Reject nonempty object types combined with nonempty relationship filters.
  - Snapshot caller-owned collections and preserve exact subject relation scope.

- **Filters must preserve the caller's exact intended scope.**
  - Distinguish subject relation filters `Any`, `Direct`, and `Named(name)`.
    Absence means Any; Direct is a present empty relation filter.
  - Converting a relationship to an exact filter preserves all identifying tuple
    fields, including the subject relation. Caveat and expiration are not filterable identity.
  - Reject filters the protocol cannot represent; never silently widen them.

- **Separate bounded deletion from deletion to completion.**
  - Bounded deletion requires a positive total limit, issues one bounded request,
    and returns revision, count, completion, and any server resume cursor.
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
  - Generate fixtures from templates, then compile, type-check, and execute them.
    Include positive and negative type tests.

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
