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
  - `deleteRelationships(filter, limit[, options])` performs one request; in the default `PartialDeletion.ALLOWED` mode it requires a positive total cap. `deleteRelationships(filter, options)` omits the limit (server default) and is valid only with `PartialDeletion.DISALLOWED`, where the server rejects an over-limit delete (`INVALID_ARGUMENT`) instead of deleting part of it. `DeleteOptions` supplies timeout, preconditions, partial mode, transport options and optional resume cursor.
  - `deleteAllRelationshipsNonAtomic` uses `DeleteAllOptions.pageSize` (default 1,000) per request and one operation budget. Preconditions apply each page. Unknown completion raises an error. Each batch is transactional; the complete operation is non-atomic, and completed batches remain committed on later failure or cancellation. `DeleteResult.revision()` exposes the native revision token. Nonempty response cursors seed the next batch; absent or empty cursors are omitted on the wire, retaining the no-cursor fallback. Result and failure progress preserve the last acknowledged cursor.
  - Results preserve count, revision, completion, and cursor. Failures and cancellation preserve immutable `DeleteProgress` with acknowledged count, last token/cursor, completed pages, and whether the failed request may have committed.

- **Cover native services and preserve stream ownership.**
  - Native operations cover relationships, schema read/write/reflection/diff, computable permissions, dependent relations, permission expansion, lookup, counters, import/export, and watch.
  - Experimental materialize covers roaring resource lookup, filter count, permission-set watch, lookup, and download. Native results retain bitmap/cardinality, cursor fields, revision, event alternatives, manifests, and timestamps. Unknown alternatives remain explicit; deprecated WatchPermissions is raw-only.
  - Lookup results retain conditional answers, exclusions, duplicates, and revisions. Read/export page size defaults to 512; optional limits cap total results. Watch accepts an optional starting token and preserves checkpoint revisions.
  - Streams require try-with-resources. Closing, exhaustion, conversion failures, interruption, and client closure release active calls, including blocked reads. Consumer errors are covered by scope cleanup.
  - Import consumes incrementally under readiness with bounded batches (default 1,000); producer failure, interruption, timeout, and server failure cancel uploads and close AutoCloseable input iterators.

- **Apply one operation budget and safe retries.**
  - Unary calls default to 30 seconds. Positive client and operation timeouts configure one monotonic budget spanning validation, retries, chunks, and pages. `ClientOptions.defaultDeadline(DefaultDeadline.DISABLED)` removes the default budget; an operation `timeout` and the caller's gRPC `Context` deadline still apply. Zero or negative durations are never a disabled value.
  - Streams/imports have no default lifetime timeout; explicit budgets begin when lazy consumption starts.
  - By default only idempotent reads retry, on UNAVAILABLE/ABORTED, three times with full-jitter backoff from 100 ms. `ClientOptions.retryPolicy(RetryPolicy)` replaces the default (max retries, retryable read codes including an opt-in `RESOURCE_EXHAUSTED`, initial/max backoff, `onRetry` callback); `retries(Retries.DISABLED)` turns retries off; `streamRetries(StreamRetries.DISABLED)` stops retrying stream establishment (watch, reads, lookups, exports, materialize). Mutations, imports, counter registration and streams that exposed results are never replayed under any policy.
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

- **Adoption contract (root DESIGN.md as reconciled 2026-10-28).** How this client meets each cross-cutting rule:
  - *Single protocol copy / legacy bridge:* the proto tier compiles buf's `gen/` output as `com.authzed.api.v1.*` (authzed-java's class names) and never depends on BSR SDKs; `bridgeAuthzedJava` repackages authzed-java without its generated classes, and `LegacyBridgeTest` proves one copy per class and that code compiled against authzed-java runs on it. See `proto-clients/spicedb-java-proto/DESIGN.md` "Canonical binding" and `MIGRATION.md`.
  - *Existing-connection constructor:* `SpiceDBClient.fromChannel(Channel[, ClientOptions])`. The channel stays caller-owned (`close()` cancels only this client's streams); TLS material and `remotePlaintextCredentials(ALLOW)` are rejected because the channel already carries transport and credentials; deadline and retry options apply.
  - *Per-call transport options and response metadata:* every options class has `.transport(TransportOptions)`; `TransportOptions.callOptions(UnaryOperator<io.grpc.CallOptions>)` adjusts each call (seeing the client's deadline already applied) and `responseMetadata(ResponseMetadata)` records the latest call's headers and trailers. Applies to every attempt, chunk and page.
  - *Exported protocol conversions:* `com.authzed.spicedb.protoconv.ProtoConv` (both directions for tokens, consistency, objects, subjects, contexts, relationships, filters, preconditions, mutations, watch updates, reads, lookups, traces and expand trees; native→proto for check results, transactions and write results). The client delegates to it, so both always agree.
  - *1.30+ floor:* behavior only older servers need (for example trailer-only debug traces) is reachable only through `TransportOptions`.
  - *Error status interop both ways, constructor, verbatim message:* every `SpiceDBException` carries a status cause (the server's, or one built from the subclass code for local and caller-built errors), so `Status.fromThrowable(e)`, `e.code()`, `e.status()` and `e.toStatusRuntimeException()` agree. The client's `CancellationException`s carry a `CANCELLED` status cause. `ErrorMapper.codeOf(Throwable)` classifies raw `StatusRuntimeException`/`StatusException` too; `ErrorMapper.toSpiceDBException` converts them into the typed hierarchy. `ErrorMapper.newError(code, message)` builds the typed error for doubles. `serverMessage()` is the status message verbatim; `getMessage()` may add context such as a bulk item index.
  - *Filter matching:* `Filter.matches(Relationship)` with server semantics (type, ID or prefix, relation, subject type/ID, `Any`/`Direct`/`Named`).
  - *In-memory test double:* `com.authzed.spicedb.testing.InMemorySpiceDB` implements `PermissionsApi`, the interface `SpiceDBClient` also implements (write, readRelationships, deleteRelationships, check, checkBulk with their options overloads): atomic writes with preconditions (`FAILED_PRECONDITION`), `CREATE` conflicts (`ALREADY_EXISTS`), duplicate updates (`INVALID_ARGUMENT`), filtered reads with resume cursors, bounded and all-or-nothing deletes, expiration, and registered check results/errors. A reflection test keeps the two surfaces identical.
  - *Panicking constructors:* not applicable. Java constructors and `fromTuple` throw the unchecked `InvalidArgumentException`, so constants and tests already need no error plumbing.
  - *String/log/serialization forms and transaction length:* `toString()` is tuple syntax on `Relationship`, `ObjectRef`, `SubjectRef`, a readable filter form on `Filter`, and `OP(...)` lists on `Transaction`, `Mutation`, `Precondition`; `toJson()` (dependency-free, never `{}`) on `Relationship`, `Filter`, `Mutation`, `Precondition` and `Transaction`; records print their fields. `Transaction.size()` counts mutations without copying.
  - *Short relationship accessors:* `resourceType()`, `resourceId()`, `subjectType()`, `subjectId()`, `subjectRelation()` beside the nested records.
  - *Full tuple syntax and copy helpers:* `Relationship.fromTuple`/`toString` handle `[caveat]`, `[caveat:{json}]` and `[expiration:RFC3339]`, emitting SpiceDB's canonical form (sorted keys, `", "` separators, trimmed fractional seconds); `NativeValuesTest` pins SpiceDB `pkg/tuple` parse+format outputs and rejections, and an example round-trips caveats and expiration through a live server. `withResource`, `withRelation`, `withSubject` copy.
  - *Bulk item-error mode:* `CheckOptions.bulkItemErrors(BulkItemErrors.RETURNED)`; errored items have `CheckResult.error()` and `UNSPECIFIED` permissionship, so `hasPermission()` is false.
  - *Default-deadline disable mode:* `DefaultDeadline.DISABLED` (above).
  - *Retry policy, disable, stream-establishment retry:* `RetryPolicy`, `Retries`, `StreamRetries` (above).
  - *Import:* an aborted upload reports the server's final status (the async stub's `onError`), never an end-of-stream; `ImportOptions.batchSize` sets relationships per message; one call is one stream and one transaction.
  - *Paging:* `readRelationships` yields `ReadRelationship` (cursor, read-at), `exportRelationships` yields `ExportedRelationship` (cursor; exports have no read-at), `LookupResource.afterResultCursor()` (beside `lookedUpAt`). `ReadOptions/ExportOptions/LookupOptions.cursor(...)` resume; callers repeat the request and page size. Every page repeats the caller's consistency and the fixed page size, and a total `limit` is enforced client-side, because SpiceDB hashes both into the cursor.
  - *Watch schema changes:* `WatchOptions.schemaChanges(WatchSchemaChanges.INCLUDED)`, combinable with checkpoints; `WatchEvent.schemaUpdated()`.
  - *All-or-nothing delete and delete-all progress:* `PartialDeletion.DISALLOWED` (above); `DeleteAllOptions.onProgress(Consumer<DeleteResult>)` receives cumulative count, latest revision and cursor after each acknowledged batch.
  - *MIGRATION.md and dependency floors:* `MIGRATION.md` covers authzed-java. Floors: protobuf-java 4.36.2 (the gencode version; generated classes refuse older runtimes), one gRPC BOM version (1.84.0, at least the stub plugin's). The proto tier exposes protobuf, grpc-protobuf, grpc-stub and proto-google-common-protos as `api`, keeps pgv/protovalidate descriptor dependencies `implementation`, and netty `runtimeOnly`; lib exposes the proto tier and `grpc-api` as `api` and keeps netty/stub/protobuf glue `implementation`. Spotless and mage are build-only.
  - *AI integration guide:* `AI_INTEGRATION_GUIDE.md`, every snippet compile-checked against the generated sample fixture.
