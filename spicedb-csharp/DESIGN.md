# C# client design

- **Use native asynchronous C# values and explicit operation options.**
  - `PermissionCheck.CreateBuilder()` offers `WithResource`, `WithPermission`, `WithSubject`, `WithConsistency` and `WithContext`; `Build()` validates required fields and produces an immutable request. Context is snapshotted when supplied.
  - The [root design](../DESIGN.md) defines shared semantics, RPC coverage, security, and verification.
  - `SpiceDBClient` is sealed and `IAsyncDisposable`. Operations use `Task`,
    `IAsyncEnumerable`, immutable native models, `DateTimeOffset`, and `TimeSpan`.
  - Each operation accepts required arguments, one nullable options object, and
    a final `CancellationToken`. Null options select documented defaults.
  - The [shared design](../DESIGN.md) defines cross-language semantics.

- **Keep revision tokens opaque and distinct from credentials and cursors.**
  - `ZedToken` is a readonly value constructed from a string. `Value` and
    `ToString()` preserve its complete contents without normalization.
  - Every public revision input and result uses `ZedToken`, including checks,
    writes, deletes, schema, lookups, counters, watch, and materialize operations.
    Optional positions use `ZedToken?`; credentials and opaque resume cursors
    remain strings. `PermissionSetCursor` is a separate native value.
  - Reads require `Consistency.Full()`, `MinLatency()`, `AtLeast(token)`, or
    `Snapshot(token)`. Strict token strategies reject empty/default tokens;
    `AtLeastOrFull` and `AtLeastOrMinLatency` provide explicitly named fallbacks.

- **Make behavioral choices discoverable with named enums.**
  - `CheckOptions.Tracing` uses `CheckTracing.Disabled` (default) or `Enabled`.
    Singular checks use `CheckPermission`; bulk checks forward tracing on every
    chunk and preserve each item's complete native recursive trace.
  - `LookupOptions.Debug` uses `LookupDebug.Disabled` (default) or `Enabled`.
    LookupSubjects and roaring lookup reject unsupported debug settings.
  - `WatchOptions.Content` is a flags enum: `WatchContent.Relationships` (default),
    `Checkpoints`, and `SchemaUpdates`. Combine flags to request multiple kinds;
    zero and unknown bits are invalid.
  - `ClientOptions.RemotePlaintextCredentials` uses
    `RemotePlaintextCredentials.Reject` (default) or `Allow`.
  - Undefined enum values fail with `InvalidArgumentException`. Binary result
    facts such as `HasPermission`, `IsCheckpoint`, and `Completed` stay boolean.

- **Separate persisted tuples from permission questions.**
  - `ObjectRef`, `SubjectRef.Direct`, and `SubjectRef.Named` describe identity.
    `Relationship` contains stored caveat context and optional expiration.
    `PermissionCheck` contains permission, explicit consistency, and evaluation context.
  - `CheckAsync`, `CheckBulkAsync`, `CheckAnyAsync`, and `CheckAllAsync` accept
    request values. Only unconditional grants satisfy `HasPermission`; empty
    aggregates are false. Conditional, denied, and unspecified remain distinct.
  - Bulk calls validate equal consistency before RPC, split at 1,000, validate
    cardinality and every echoed request, and preserve absolute error indexes
    and per-chunk revisions. Singular expiration is independent of tracing.
  - Context values recursively snapshot JSON-compatible data, preserve null,
    and reject unsupported values with their paths. Per-request keys override
    call-level defaults; absent context remains absent.

- **Preserve filter scope and acknowledged deletion progress.**
  - `Filter` is immutable. Subject relation filters distinguish `Any`, `Direct`,
    and `Named`; exact relationship filters retain all tuple identity fields.
  - `DeleteRelationshipsAsync(filter, limit, options)` makes one bounded call.
    `DeleteAllRelationshipsNonAtomicAsync(filter, options)` uses a separate positive
    page size (default 1,000), one budget, and preconditions on every page.
  - `DeleteResult` preserves count, revision, completion and any resume cursor.
    Unknown completion is an error. Failures carry cumulative `DeleteProgress`,
    including acknowledged malformed responses and the latest token/cursor.
    `error.GetDeleteProgress()` retrieves it from client errors and native cancellation
    exceptions without changing exception identity or cancellation tokens.
  - Every batch is transactional; the complete operation is non-atomic. Previously
    completed batches remain committed when later batches fail or are canceled.
    Read the result's `Revision` for its native revision token. Nonempty response
    cursors seed the next batch; absent or empty cursors are omitted on the wire,
    retaining the no-cursor fallback. Results and progress retain acknowledged cursors.

- **Share deadlines and release transport resources deterministically.**
  - Unary calls default to a 30-second total monotonic budget, configurable via
    client or operation options. Retries, chunks and pages spend the same budget.
    Streams/imports have no default timeout but accept an explicit total timeout.
  - Caller cancellation preserves its native token; remote cancellation and
    deadline expiry retain separate typed categories and original diagnostics.
  - Retry idempotent reads only for unavailable/aborted errors with bounded
    jittered backoff. Never replay mutations or partially consumed streams.
  - Enumerator disposal, early loop exit, cancellation and client disposal stop
    active transport calls. Imports apply bounded backpressure and cancel and
    dispose producers on early server rejection, producer failure or cancellation.

- **Expose the complete protocol through native operations.**
  - Relationships, schema reflection/diff, permission trees, lookup exclusions,
    counters, watch metadata/checkpoints, and all nondeprecated materialize RPCs
    use native immutable results. Experimental operations are explicitly named.
  - Read/export pages default to 512. Lookup limits cap total yielded results;
    unsupported settings fail explicitly. Permission-set lookup is one bounded
    page with `PermissionSetLookupOptions.ResumeCursor` for continuation.
  - `Transaction` collects create/touch/delete operations and match/nonmatch
    preconditions, snapshotting them for writes. Metadata remains native JSON.

- **Require an explicit decision for remote plaintext credentials.**
  - `CreateSystemTls(endpoint, key, options)` uses platform trust.
    `CreatePlaintext(endpoint, key, options)` permits loopback by default and
    rejects authenticated remote hosts/IPs unless options explicitly set
    `RemotePlaintextCredentials = RemotePlaintextCredentials.Allow`.
  - `CreateFromChannel(channel, key, options)` applies the same policy and borrows
    its channel. The transport's parsed address establishes scheme/loopback;
    unavailable metadata fails closed unless explicitly opted in. Arbitrary
    caller transport configuration can supersede endpoint security assumptions.
  - `RawProto()` exposes authenticated services on the existing connection and
    bypasses native mapping/retry/deadlines. Deprecated RPCs remain raw-only.

- **Verify observable contracts and keep examples executable.**
  - Unit and real local gRPC transport tests cover enum mapping/validation,
    lossless tokens, remote credential guards, strict bulk association,
    cumulative deletion, lifecycle cleanup, TLS, and cancellation.
  - Build both solutions, run the full unit suite and every integration example,
    verify formatting, and document public changes in the Unreleased changelog.

- **LookupResources can filter for unconditional grants on the client.**
  - The default `All` mode returns every response, including conditional and unknown permissionship values. Before mapping results to accessible IDs, require unconditional permission explicitly or opt into `new LookupOptions { PermissionshipFilter = LookupPermissionshipFilter.HasPermissionOnly }`.
  - The filter yields only exact HAS_PERMISSION responses; conditional, unspecified, and unknown values are skipped. Limits count yielded matches, and skipped responses still advance pagination and cursors.
  - Filtering does not deduplicate results or change server caveat evaluation. LookupSubjects rejects the nondefault filter.

- **Relationship writes return a native WriteResult.**
  - Access `Revision` for the `ZedToken` to pass to consistency or watch APIs. Typed relationship mutations return the same wrapper.
  - Schema writes and filtered relationship deletion retain their operation-specific results; absent wire revisions retain their existing mapping.

- **Watch supports native relationship filters.**
  - `WatchOptions.RelationshipFilters` snapshots the filter list and forwards resource type/ID/prefix, relation, and subject constraints, including direct and named subject relations.
  - Pass an empty object-type list when specifying relationship filters. Nonempty object types and relationship filters are mutually exclusive and rejected before any RPC. Both lists empty watches all relationships.
  - Existing object-type calls remain available; watch examples prefer type-only relationship filters and retain cursor, cancellation, and content options.
