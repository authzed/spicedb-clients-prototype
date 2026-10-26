# Java client design

- **Expose synchronous native Java APIs.**
  - `PermissionCheck.builder()` names resource, permission, subject, consistency and optional context; `build()` validates required fields and produces an immutable request. Context is snapshotted when supplied.
  - `SpiceDBClient` is `AutoCloseable`; immutable records, collections, `Instant`, `Duration`, and closeable sequential `Stream` represent application data.
  - Operations have required arguments and an optional immutable options object built by a mutable builder. Explicit null options are invalid. Protocol access is a secondary authenticated, non-owning escape hatch.
  - The [root design](../DESIGN.md) defines shared semantics, RPC coverage, security, and verification.

- **Separate identities, relationships, checks, and revisions.**
  - `ObjectRef` identifies an object. `SubjectRef.direct` and `SubjectRef.named` distinguish direct subjects from usersets; empty names and `"..."` are invalid public userset names.
  - `Relationship` stores tuple identity, caveat context, and expiration. `PermissionCheck` stores permission, required consistency, and optional evaluation context.
  - `ZedToken(String)` is an opaque native revision value. `value()` and `toString()` preserve its exact string contents; null is invalid. All revision inputs and results, including schema, watch, deletion, counters, and materialize, use it. Authentication keys and opaque pagination cursors remain separate strings.
  - `Consistency.full`, `minLatency`, `atLeast`, and `snapshot` require an explicit decision. Strict token strategies reject empty tokens; `atLeastOrFull` and `atLeastOrMinLatency` name the fallback for absent or empty tokens.

- **Make behavior discoverable through enums.**
  - `CheckOptions.tracing` accepts `CheckTracing.DISABLED` (default) or `ENABLED`; singular and every bulk chunk forward tracing.
  - `LookupOptions.debug` accepts `LookupDebug.DISABLED` (default) or `ENABLED`. Unsupported lookup options are rejected; schema-derived userset relations survive typed forwarding and conflicting relations are invalid.
  - `WatchOptions.content` accepts `WatchContent.RELATIONSHIPS` (default) or `RELATIONSHIPS_AND_CHECKPOINTS`. `WatchEvent.isCheckpoint` remains a binary result fact.
  - `ClientOptions.remotePlaintextCredentials` accepts `RemotePlaintextCredentials.REJECT` (default) or `ALLOW`. Null enum values are invalid.

- **Preserve authorization uncertainty and complete diagnostics.**
  - `CheckResult` preserves permissionship, missing context, checked revision, optional trace, and singular expiration. Only `HAS_PERMISSION` satisfies `hasPermission`; unknown states remain `UNSPECIFIED`.
  - `check` uses the singular RPC. `checkBulk`, `checkAny`, and `checkAll` accept collections, preserve iteration order, and use chunks of at most 1,000 requests. Empty aggregates are false.
  - Bulk requests require equal consistency strategies and tokens. Cardinality and exact echoed requests are validated; each result retains its chunk revision and failures retain absolute input indexes.
  - Native traces preserve recursive checks, schema, caveat expression/context/missing keys, timestamps, duration, source, operation ID, and resolution presence including cached false.

- **Snapshot caller data and preserve JSON.**
  - Context accepts string-keyed maps, lists, strings, booleans, representable finite numbers, and null. Values recursively copy into immutable collections; invalid values, cycles, and keys produce path-bearing `InvalidArgumentException`.
  - Call-level check context merges by top-level key with item context winning, including null. Absent context stays absent. Stored caveat context is independent.
  - Result collections and bitmap bytes are copied. Builders snapshot data when producing reusable immutable options.

- **Preserve exact filters and bounded deletion.**
  - `SubjectRelationFilter.any`, `direct`, and `named` distinguish omitted, present empty, and named wire constraints. `Relationship.toFilter` retains every tuple identity field. Unsupported filters are rejected.
  - `deleteRelationships(filter, limit)` performs one request with a positive total cap. `DeleteOptions` supplies timeout, preconditions, and optional resume cursor.
  - `deleteAllRelationshipsNonAtomic` uses `DeleteAllOptions.pageSize` (default 1,000) per request and one operation budget. Preconditions apply each page. Unknown completion raises an error. Each batch is transactional; the complete operation is non-atomic, and completed batches remain committed on later failure or cancellation. `DeleteResult.revision()` exposes the native revision token. Nonempty response cursors seed the next batch; absent or empty cursors are omitted on the wire, retaining the no-cursor fallback. Result and failure progress preserve the last acknowledged cursor.
  - Results preserve count, revision, completion, and cursor. Failures and cancellation preserve immutable `DeleteProgress` with acknowledged count, last token/cursor, completed pages, and whether the failed request may have committed.

- **Cover native services and preserve stream ownership.**
  - Native operations cover relationships, schema read/write/reflection/diff, computable permissions, dependent relations, permission expansion, lookup, counters, import/export, and watch.
  - Experimental materialize covers roaring resource lookup, filter count, permission-set watch, lookup, and download. Native results retain bitmap/cardinality, cursor fields, revision, event alternatives, manifests, and timestamps. Unknown alternatives remain explicit; deprecated WatchPermissions is raw-only.
  - Lookup results retain conditional answers, exclusions, duplicates, and revisions. Read/export page size defaults to 512; optional limits cap total results. Watch accepts an optional starting token and preserves checkpoint revisions.
  - Streams require try-with-resources. Closing, exhaustion, conversion failures, interruption, and client closure release active calls, including blocked reads. Consumer errors are covered by scope cleanup.
  - Import consumes incrementally under readiness with bounded batches (default 1,000); producer failure, interruption, timeout, and server failure cancel uploads and close AutoCloseable input iterators.

- **Apply one operation budget and safe retries.**
  - Unary calls default to 30 seconds. Positive client and operation timeouts configure one monotonic budget spanning validation, retries, chunks, and pages.
  - Streams/imports have no default lifetime timeout; explicit budgets begin when lazy consumption starts.
  - Only idempotent reads retry UNAVAILABLE/ABORTED with bounded jittered backoff. Mutations, imports, RESOURCE_EXHAUSTED, and streams that exposed results are not replayed.
  - Interruption preserves the interrupt flag and raises native cancellation. Remote cancellation and deadline expiry map to typed errors. Errors preserve causes and rich status details.

- **Make transport posture explicit.**
  - `createSystemTls(endpoint, key, options)` uses runtime trust roots with optional CA and paired client certificate/key PEM paths.
  - `createPlaintext(endpoint, key, options)` rejects authenticated remote plaintext by default. `ClientOptions.builder().remotePlaintextCredentials(RemotePlaintextCredentials.ALLOW).build()` explicitly permits remote hostnames and IPs. TLS material combined with plaintext is invalid.
  - Generated typed factories forward the same options and opt-in. Supplied native clients remain borrowed; factory-created clients are owned.
  - The secondary `create(..., ClientOption...)` transport escape hatch documents that arbitrary builder callbacks can supersede guards. `rawChannel()` is authenticated and non-owning; raw calls bypass native mapping, deadlines, and retries.

- **Verify native and generated contracts.**
  - Unit and transport tests cover exact token propagation, enum wire mapping, null validation, bulk echoes, context ownership, deletion progress, deadlines, TLS, and cancellation.
  - Generated fixtures come from templates and undergo positive compilation, negative type checks, and forwarding tests. Examples assert observable behavior and run in CI; formatting uses Spotless.

- **LookupResources can filter for unconditional grants on the client.**
  - The default `ALL` mode returns every response, including conditional and unknown permissionship values. Before mapping results to accessible IDs, require unconditional permission explicitly or opt into `LookupOptions.builder().permissionshipFilter(LookupPermissionshipFilter.HAS_PERMISSION_ONLY)`.
  - The filter yields only exact HAS_PERMISSION responses; conditional, unspecified, and unknown values are skipped. Limits count yielded matches, and skipped responses still advance pagination and cursors.
  - Filtering does not deduplicate results or change server caveat evaluation. LookupSubjects rejects the nondefault filter.

- **Relationship writes return a native WriteResult.**
  - Access `revision()` for the `ZedToken` to pass to consistency or watch APIs. Typed relationship mutations return the same wrapper.
  - Schema writes and filtered relationship deletion retain their operation-specific results; absent wire revisions retain their existing mapping.

- **Watch supports native relationship filters.**
  - `WatchOptions.builder().relationshipFilters(List<Filter>)` snapshots the filter list and forwards resource type/ID/prefix, relation, and subject constraints, including direct and named subject relations.
  - Pass an empty object-type list when specifying relationship filters. Nonempty object types and relationship filters are mutually exclusive and rejected before any RPC. Both lists empty watches all relationships.
  - Existing object-type calls remain available; watch examples prefer type-only relationship filters and retain cursor, cancellation, and content options.

- **Generated Java clients expose native operations directly.**
  - Explicit forwarding methods preserve native options, results, exception progress, and closeable stream identity for relationship writes/read/delete/import/export, bulk checks, schema/reflection, watch filters, counters, and materialize operations.
  - `check`, `lookupResources`, and `lookupSubjects` keep schema-aware signatures without permissive native fallbacks. `close` preserves generated-client ownership semantics. Custom transport setup can construct a native client and pass it to `TypedClient`; the existing typed plaintext/system-TLS factories remain available.
  - A reflection inventory compares every native instance signature and generic result to its generated counterpart, with the three schema-aware operation names as explicit exceptions.
