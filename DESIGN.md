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
    transport options do not. Credential options apply only where the
    language's connection type cannot carry credentials itself (a tonic
    `Channel`), in which case the constructor accepts the token alone.
  - Every operation accepts a transport escape hatch: per-call transport options
    and capture of response headers and trailers. This is the supported route to
    transport-level data, including data that pre-1.30 servers send in trailers.
    Callers do not need the raw protocol client for it.
  - Native↔protocol conversions for requests, results, relationships, filters,
    preconditions, consistency, updates and traces are exported in a secondary
    namespace, so tools that persist or print protocol-shaped data reuse them.
    Conversions to response messages reproduce the server's wire shape,
    including whole bulk-check responses with per-item errors and deprecated
    fields the server still populates.
  - Match the server's own acceptance rules. A client rejects locally only what
    the server would reject or what the protocol cannot express; for example an
    empty relationship write is sent (SpiceDB evaluates its preconditions and
    returns the current revision), and watch rejects object types combined with
    filters because the server does.
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
    alongside the other items' decisions. An errored item never grants. A
    malformed item in the server's response (such as an unset permissionship)
    still fails the whole call, since it is not a per-item error.

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
    preconditions and create conflicts), filtered reads and deletes, permission
    checks, lookups, imports, exports and schema reads and writes.
    Its errors carry the same codes and message shape as the real client's for
    the same failure. It records every call with its request and resolved
    options, and accepts fault injection at defined points (before any call,
    before each import batch, before an import commits, before each read or
    export item; an import-batch fault sees the batch's relationships), so
    failure paths and option assertions need neither a fake transport nor a
    strict mock. Its resume cursors have a documented format or constructor,
    and it can emulate servers whose schema reads report no revision.
  - The double resolves a check in a fixed order: a result registered for
    that exact question; then a caller-supplied check resolver (a callback
    receiving the check, so tests over generated IDs need no per-ID
    registration, and grant-all is one line); then the stored relationships,
    where a relationship on the resource whose relation is the permission's
    name and whose subject matches exactly or by wildcard grants it; anything
    else is no permission. The double does not evaluate a schema, and each
    double's docs and AI guide say so, pointing schema-logic tests at SpiceDB
    itself (`serve-testing` or a container). The resolver
    sees the check with the call's default context merged in (the check's own
    keys win), returns a result or declines (falling through), and never
    fails: failures come from the fault hooks. A wildcard matches only direct
    subjects of its type. The double does not evaluate caveats, so a caveated
    stored relationship grants conditionally, an uncaveated match wins over a
    caveated one, and an expired relationship never grants.
  - The double serves LookupResources and LookupSubjects: it enumerates the
    objects of the requested type that appear in its stored relationships (as
    resource or subject) and returns those its check resolution grants,
    reporting conditional results as conditional and honoring the
    permissionship filter, limit and cursor with the double's cursor format.
    Objects are enumerated in first-appearance order, and a cursor counts
    granted results, including ones the permissionship filter drops (as the
    real client's cursor advances past them). A stored wildcard subject is
    returned by LookupSubjects as the `*` subject.
  - Each client exports an interface (or protocol) with the full client's
    method set, kept in sync with the client by a test, and the double
    implements all of it: operations without in-memory semantics are recorded,
    consult the fault hook, and fail as unimplemented in the client's error
    shape. Its surface matches the real client's so one
    interface or protocol accepts both: caller-defined where the language is
    structurally typed (Go, TypeScript, Python protocols), library-provided and
    implemented by both where it is nominally typed (C#, Java). Generated typed
    clients take that interface, so a typed client wraps the double.
  - Languages whose constructors return errors make constants and test fixtures
    one call: panicking variants where that is idiomatic (Go `Must*`), or a
    one-call parse from tuple text where call-site assertion is the convention
    (Rust `FromStr` plus `.expect()`). Languages whose constructors throw need neither.
  - Option values may be opaque, but each client exposes a way to resolve a
    call's options into an inspectable snapshot (cursor, limits, page size,
    response-metadata targets, retry override), so test doubles and wrappers
    can honor them without a fake transport.
  - A direct subject is one call from an object (an implicit conversion, `From`,
    or a `direct` constructor), and the subject relation `...` means a direct
    subject everywhere a subject relation is accepted, as tuple and proto
    parsing already treat it.
  - Native results serialize with the ecosystem's default serializer as their
    canonical forms: enums as names and tokens as strings, not as numbers or
    wrapper objects.
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
  - Finite paged reads (relationship reads, lookups, exports) apply the client
    default deadline to each page request, from opening it to receiving its
    last item, so a server that stalls mid-read fails the read instead of
    hanging it. The deadline bounds waiting on the server, never the caller's
    processing: time the caller spends between items does not count (buffer
    the page, which is at most the page size, or pause the timer while the
    caller holds an item), so a slow consumer never fails a healthy read. A caller's total deadline still bounds the whole operation and
    wins when earlier. Long-lived streams (watch) keep no default.
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
  - Never replay a mutation automatically. A caller may opt a single call into
    retries only where replay is idempotent: a relationship write containing
    only touches and no preconditions, and a schema write. Creates, deletes,
    preconditioned writes, imports and counter registration refuse the opt-in
    before any request.
  - Stream establishment may retry only before any item is exposed. Never
    restart a partially consumed stream.
  - All retries share the original deadline and cancellation signal.
  - A client-wide retry policy option replaces the default: max attempts,
    retryable read codes (a caller may opt into others, such as
    `RESOURCE_EXHAUSTED`), backoff, and an on-retry callback. A named mode
    disables retries entirely, for callers that apply their own policy.
  - A per-call retry policy overrides the client's for one operation, including
    stream establishment, so a caller can exempt one stream (a supervised watch)
    while retrying everything else.
  - A client-wide per-operation retry policy applies the same override to every
    call of one operation (never reopen watches, for example). Precedence is
    per-call, then per-operation, then client-wide. A per-operation write policy
    retries only writes that are idempotent on replay; operations that never are
    reject a retrying per-operation policy when the client is built.
  - The idempotence rule (touch-only, no preconditions) is public on the
    transaction type, so callers decide eligibility with the client's own rule. Stream-establishment retry has its own named
    setting, so long-lived streams like watch can opt out. No configuration can
    make a mutation replay.
  - The client-wide policy, default included, never reopens a watch: a reopened
    watch with no start revision begins later and silently skips the changes in
    between. Only a per-call or per-operation policy for watch opts in.

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
    total limit client-side rather than by shrinking any page's limit, so the
    wire limit is the page size alone and a cursor from a limited call resumes
    an unlimited one.
    Live tests must read across more than one page.
  - A named single-request mode sends exactly one request, as the raw API does:
    its limit is the total limit or unbounded, and no follow-up page is fetched.

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
  - Where the language has a standard invalid-input exception that callers
    already catch around parsing (Python's `ValueError`), local validation
    errors are also that type; server invalid-argument errors are not.
  - Native errors interoperate with the ecosystem's standard status inspection in
    both directions. A native error reports its matching transport status code
    through the language's status accessor, including errors for local
    cancellation, local validation, and errors built by caller code. Code
    classification helpers also accept raw transport status errors.
  - Where the legacy client surfaced a transport exception type that the
    language lets a library subclass, native errors are that type for the
    matching code (or match it in the language's rescue/catch idiom), so
    existing handlers keep catching. MIGRATION.md states the interop either
    way. The verbatim server-message field always has a value: for local
    errors it is the error's message.
  - A response the client cannot interpret (a missing required field, an
    unset enum the server always sets such as a check's permissionship, a
    cursor that does not advance) fails with `INTERNAL`, which no retry policy
    treats as retryable by default. Clients never skip such a response or fill
    its missing fields with empty values: a skipped item makes a full page look
    short and silently ends a paged read early. The error says the client
    detected it, so it is not mistaken for a status the server sent. Only the
    unset (zero) value is malformed: an enum value newer than the client maps
    to the documented safe unknown (which never grants) and never fails the
    call, so upgrading SpiceDB cannot turn into a client outage.
    Docs, comments and error messages call the malformed zero value "unset"
    (for example `permissionship is unset`); "unspecified" names only the safe
    enum value, so a reader never takes an `Unspecified` result for a failure.
  - Local cancellation surfaces as the client's cancelled error, never as the
    platform's own abort exception, whose fields may resemble a status code.
  - Provide a public constructor for native errors from a code and message, so
    test doubles produce errors that every predicate treats like real ones.
  - Wrapper errors (bulk item, deletion progress) report their inner status
    through the status accessor, so the server's message appears once rather
    than under repeated operation prefixes.

- **Use secure transport defaults and explicit trust configuration.**
  - Secure constructors delegate to the ecosystem's default trust source.
    Python, Ruby, and Node support custom CA material because their defaults may
    differ from the host's managed trust store.
  - Reject TLS material combined with plaintext. Document actual TLS and mTLS
    capabilities rather than inferring them from system-root support.

- **Accept the endpoint forms deployments use, or reject them at construction.**
  - Constructors accept `host:port` and, where the transport understands them,
    `http://` and `https://` URLs (the scheme selects plaintext or TLS and must
    agree with the constructor). Any other form fails at construction with an
    error naming the expected format: an endpoint never builds a client whose
    every call fails. The loopback check runs on the parsed host. An
    `http://` URL needs an explicit port (no client silently dials port 80);
    an `https://` URL without one uses 443. The same holds for a bare host:
    plaintext needs a port, TLS defaults to 443. Malformed forms (path, query,
    userinfo, non-numeric port) fail with an error naming that problem,
    checked before the remote-plaintext policy, whose message appears only
    for a well-formed remote host.
  - Connection errors display the endpoint and the root cause (refused, DNS,
    TLS), not only a generic transport category.
  - gRPC target syntax beyond `host:port` (`dns:///host:port` for client-side
    load balancing, `unix:` sockets) is accepted where the transport supports
    it, with the loopback check on the resolved host, and rejected at
    construction elsewhere with an error naming the accepted forms. A target a
    client accepts must work; clients do not pass unknown schemes through.
  - Clients that connect lazily offer a startup readiness check (an
    eager-connect option or an awaitable connect/ready call), so a wrong or
    unreachable endpoint can still fail at startup.

- **Reject unusable credentials and trust material at construction.**
  - An empty or whitespace-only token is a local validation error, not a
    bare `Bearer` header the server rejects later (a blank environment
    variable is the usual cause).
  - Custom CA, client certificate and key material is parsed when the client
    is built; material that does not parse, or a client key that does not
    match its certificate, fails with a local validation error naming which
    input is wrong, never as an unexplained transport failure on the first
    call.
  - Caller-supplied revision tokens follow the same rule as credentials: an
    empty or whitespace-only token is a local validation error.
  - Dynamically typed clients check the types of names and IDs before use:
    a value of the wrong type is a local validation error, never a server
    round trip with a coerced value. Where a client deliberately accepts
    another type for an ID (Ruby Integers, converted with `to_s`), its docs
    say so.

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
  - Code that receives type and permission names at runtime (policy
    expressions, admin tools) stays typed: generated code offers a schema-level
    lookup of a definition by name, a resource-less permission ref by
    definition and permission name, and a lookup of resources by runtime names,
    each validated against the schema.
  - String-keyed code reaches every typed value without hand-written
    dispatch: a typed object from a type name and ID or from `type:id` text, a
    subject set from type, ID and relation, a typed subject (object, subject
    set or allowed wildcard) from `type:id[#relation]` text, a typed
    relationship from tuple text whose subject the relation admits, checks
    and bulk checks by name, and lookups of subjects as well as resources by
    name, plus a type naming the union of generated objects. Schema
    rejections of these names are local validation errors.
  - By-name lookups accept relations as well as permissions, as SpiceDB does.
    When the subject's type and relation cannot reach the target, they return
    an empty result without a call, which is SpiceDB's answer; unknown names
    still fail.
  - Generated code exposes definition-name constants and the schema text it
    was generated from, so callers need neither a hand-kept name list nor a
    copy of the schema file to write it at startup.
  - A names-only mode emits the name constants and schema text with no client
    dependency, so a shared API package can use the names alone.

- **Make generated values composable with the native layer in both directions.**
  - Typed permissions, permission refs and relationships expose their resource
    type, ID, permission or relation and a canonical string form.
  - Typed relationships convert to native relationships, and native relationships
    wrap into typed ones, documented as not schema-checked. Typed values also
    produce native exact filters, type-level filters, relation-scoped filters,
    native permission checks, and relation-name constants.
  - A typed transaction builder mixes touch, create, delete and preconditions in
    one atomic write and exposes its pending updates and preconditions. It defers the first construction error to build/write time
    instead of requiring a check per step, and never shadows a promoted native method.
  - Typed bulk checks forward native options and preserve input order, per-chunk
    revisions and per-item errors.
  - Generated output is formatted with the language's canonical formatter, and
    the output package or module name is configurable.
  - Generate fixtures from templates, then compile, type-check, and execute them.
    Include positive and negative type tests. Each forbidden construct must fail
    on its own: a check that only proves "something failed to compile" lets one
    error mask a regression elsewhere.
  - The generator's help lists every language's options. A schema may be read
    from standard input, so schemas kept in source code need no temporary file.
  - Generation fails, naming both sources, when schema names would produce
    colliding identifiers. It never silently renames, and offers an explicit
    per-definition identifier override so production schemas need not change.
  - Caveat expressions are opaque to generation: only caveat names and
    parameter types are read, so caveats calling functions outside SpiceDB's
    default CEL environment generate normally. The server validates
    expressions when the schema is written.

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
    and keep build tooling out of the requirements consumers inherit. "Need"
    includes security: a floor is raised when an older version lets a guard be
    bypassed (grpc for Python and Ruby before 1.73 resolves `UNIX://host/...`
    as a DNS target, defeating the remote-plaintext loopback check). Verify
    floors by running the suites at exactly those versions.
  - Floors and framework targets match the hosts consumers use: target every
    supported LTS framework (.NET 8 and later), the current major of the
    transport ecosystem (tonic and prost for Rust), and protobuf runtimes no
    newer than the dominant host framework manages (Spring Boot for Java). A
    runtime below the floor fails at client construction with a message naming
    what to override.
  - Each MIGRATION.md opens with a Requirements section listing runtime,
    framework and dependency floors, and has a legacy-to-new table for renamed
    request and response members.
  - Each client directory has a README covering installation (including how to
    consume it before it is published), a first call, and links to DESIGN,
    MIGRATION and the AI integration guide.
  - Published artifacts declare every dependency with a version, verified on
    the generated manifest (POM, nuspec, gemspec, Cargo manifest), and the
    client and proto artifacts share coordinates and version.
  - Library builds treat compiler warnings as errors, so none surface in
    consumer builds.
  - Each client documents whether one instance may be shared across threads or
    tasks, how it behaves across a fork, and where to build it in common
    application servers.

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
