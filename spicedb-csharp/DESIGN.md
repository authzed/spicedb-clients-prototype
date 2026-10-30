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
    `ToString()` preserve its complete contents without normalization, and
    `System.Text.Json` reads and writes it as that string, never as `{"Value":...}`.
  - Every public revision input and result uses `ZedToken`, including checks,
    writes, deletes, schema, lookups, counters, watch, and materialize operations.
    Optional positions use `ZedToken?`; credentials and opaque resume cursors
    remain strings. `PermissionSetCursor` is a separate native value.
  - Reads require `Consistency.Full()`, `MinLatency()`, `AtLeast(token)`, or
    `Snapshot(token)`. Strict token strategies reject empty, whitespace-only and default tokens;
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
    An `ObjectRef` converts implicitly to its direct `SubjectRef`, so a direct subject is
    one step from an object. The subject relation `...` means direct everywhere:
    `new SubjectRef(obj, "...")` and `SubjectRef.Named(obj, "...")` build the direct
    subject and `SubjectRelationFilter.Named("...")` returns `SubjectRelationFilter.Direct`,
    as tuple and proto parsing already do; an empty relation stays invalid.
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
  - `DeleteRelationshipsAsync(filter, limit, options)` makes one call. In the default
    `PartialDeletion.Allowed` mode `limit` is required and positive; with
    `DeleteOptions.Partial = PartialDeletion.Disallowed` the delete is all-or-nothing (the
    server rejects a delete exceeding the limit) and a null `limit` uses the server default.
    `DeleteAllOptions.Progress` (`IProgress<DeleteResult>`) is reported synchronously after
    every acknowledged batch with the cumulative count, latest revision and cursor.
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
    `ClientOptions.DefaultDeadline = DefaultDeadline.Disabled` removes the default
    budget; per-operation `Timeout` and the caller's token still apply. Zero or
    negative durations stay invalid rather than meaning "disabled".
    Streams/imports have no default timeout but accept an explicit total timeout.
    Finite paged reads (reads, lookups, permission-set lookup, exports) apply the client
    default to each page request, from opening it to its last item; a per-call `Timeout`
    still bounds the whole read and wins when earlier. Watches keep no default. The
    deadline never counts the caller's processing: it is a watchdog on waiting for the next
    message, stopped while the caller holds an item (items still stream as they arrive), and
    only the total budget goes on the wire as the call deadline. Unpaged streams
    (LookupSubjects, `Paging.SingleRequest`) give each wait for a message the whole default.
  - A response the client cannot interpret (a missing relationship, resource, subject,
    object or id, an incomplete tree, a non-advancing cursor) fails with an
    `Internal`-status `SpiceDBException`, never skipped and never filled with empty values.
    Client-detected faults never report `Unknown`. Local validation, `FromTuple` included,
    stays `InvalidArgumentException`, which cannot also be an `ArgumentException` or
    `FormatException` (single inheritance), so the native shape wins and MIGRATION.md says so.
  - Caller cancellation preserves its native token; remote cancellation and
    deadline expiry retain separate typed categories and original diagnostics.
  - By default, idempotent reads retry Unavailable/Aborted up to three times with
    full-jitter backoff from 100 ms (`RetryPolicy.Default`). `ClientOptions.RetryPolicy`
    replaces it client-wide (`MaxRetries`, `RetryableCodes` such as an opted-in
    `ResourceExhausted`, `InitialBackoff`, `MaxBackoff`, and an `OnRetry` callback receiving
    a `RetryEvent`); `Retries.Disabled` makes every operation one attempt;
    `StreamRetries.Disabled` opens each server stream (reads, lookups, exports) once.
    Every options type's `RetryPolicy` overrides the client's for one call, stream
    establishment included (a policy re-enables retries for one call under
    `Retries.Disabled`). `ClientOptions.OperationRetryPolicies` (keyed by the
    `SpiceDBOperation` enum, one member per client method) applies the same override to every
    call of one operation; the most specific policy wins (per call, then per operation, then
    client-wide).
  - The client-wide policy (the default included, whatever `StreamRetries` says) never
    reopens a watch (`UpdatesAsync`, `ExperimentalWatchPermissionSetsAsync`): a watch
    reopened without a start revision begins at a later revision and silently skips the
    changes in between. Only a per-call or per-operation policy for that watch opts in to
    reopening, and only before its first event.
  - A paged read, lookup or export whose full page arrives without a new cursor (missing,
    empty or equal to the one sent) fails with an Internal-status `SpiceDBException`
    ("... cursor did not advance") instead of re-requesting the same page forever. Mutations are
    never replayed automatically: only a touch-only `WriteAsync` without preconditions and
    `WriteSchemaAsync` may opt in, per call or per operation. A per-operation `Write` policy
    retries only writes whose `Transaction.IsIdempotent` is true (the client's own rule:
    touches only, no preconditions) and leaves the others single-attempt without error;
    creates, deletes, preconditioned writes, imports and counter (un)registration reject a
    per-call opt-in with `InvalidArgumentException` before any request, and deletes,
    delete-all, imports and counter (un)registration reject a retrying per-operation policy
    when the client is built (a zero-retry policy is allowed for any operation). A partially
    consumed stream is never restarted.
  - Enumerator disposal, early loop exit, cancellation and client disposal stop
    active transport calls. Imports apply bounded backpressure and cancel and
    dispose producers on early server rejection, producer failure or cancellation.

- **Expose the complete protocol through native operations.**
  - Relationships, schema reflection/diff, permission trees, lookup exclusions,
    counters, watch metadata/checkpoints, and all nondeprecated materialize RPCs
    use native immutable results. Experimental operations are explicitly named.
  - Read/export pages default to 512. Lookup limits cap total yielded results;
    unsupported settings fail explicitly. Every page repeats the caller's consistency and
    one fixed page size, because SpiceDB hashes both into each cursor (and the cursor pins
    later pages to the first page's revision); `Limit` is enforced client-side, never by
    shrinking the last page. `ReadRelationshipsAsync` yields `ReadRelationship`
    (relationship, `AfterResultCursor`, `ReadAt`), `ExportRelationshipsAsync` yields
    `ExportedRelationship` (relationship, `AfterResultCursor`; export responses carry no
    revision), and `LookupResource` carries `AfterResultCursor` beside `LookedUpAt`.
    `ReadOptions`, `ExportOptions` and `LookupOptions` accept `ResumeCursor`; resuming
    requires the same request and page size. `ReadOptions.Paging` and `LookupOptions.Paging`
    = `Paging.SingleRequest` send exactly one request like the raw API (limit = `Limit` or
    unbounded, page size ignored, no follow-up page); LookupSubjects and roaring lookup
    reject it. Cursors are strings, never `ZedToken`. Permission-set lookup is one bounded
    page with `PermissionSetLookupOptions.ResumeCursor` for continuation.
  - `Transaction` collects create/touch/delete operations and match/nonmatch
    preconditions, snapshotting them for writes. Metadata remains native JSON.
    `Count` reports the update count without copying; `Updates` (`RelationshipUpdate`) and
    `Preconditions` (native `Precondition` with `PreconditionOperation`) expose snapshots.
    An empty transaction is sent, as SpiceDB accepts it: the server evaluates its
    preconditions and returns the current revision.
  - Imports send `ImportOptions.BatchSize` relationships per message (default 1,000). One
    call is one stream and one transaction; callers wanting several transactions make
    several calls. When the server aborts the stream (for example `ALREADY_EXISTS` for a
    duplicate) the call reports the server's final status as its typed exception, never
    the failed send.
  - `WatchOptions.Content` combines `WatchContent.SchemaUpdates` with relationships and
    checkpoints; `WatchEvent.SchemaUpdated` reports a schema change distinctly.
  - SpiceDB 1.30 and later is supported natively. Data older servers send only in
    trailers (such as pre-1.30 check debug traces) is reached through the transport
    escape hatch below, not first-class API.

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
  - Endpoints also accept gRPC targets the transport supports: `dns:///host:port` (DNS
    resolver, round-robin balancing, loopback check on the host) and plaintext
    `unix:/path`, `unix:///path` or `unix:relative/path` (dialed through a socket
    `ConnectCallback`, never as a host named `unix`). Other schemes fail at construction.
  - Channels connect lazily. `ConnectAsync(ConnectOptions?, CancellationToken)` is the
    startup readiness check: a wait-for-ready schema read (NotFound counts as ready) within
    `ConnectOptions.Timeout` or the client default, failing with
    `DeadlineExceededException` naming the endpoint, `UnavailableException` or
    `UnauthenticatedException`. It is on `ISpiceDBClient`; the in-memory double no-ops.

- **Verify observable contracts and keep examples executable.**
  - Unit and real local gRPC transport tests cover enum mapping/validation,
    lossless tokens, remote credential guards, strict bulk association,
    cumulative deletion, lifecycle cleanup, TLS, and cancellation.
  - Build both solutions, run the full unit suite and every integration example,
    verify formatting, and document public changes in the Unreleased changelog.

- **LookupResources can filter for unconditional grants on the client.**
  - The default `All` mode returns every response, including conditional and unknown permissionship values. Before mapping results to accessible IDs, require unconditional permission explicitly or opt into `new LookupOptions { PermissionshipFilter = LookupPermissionshipFilter.HasPermissionOnly }`.
  - The filter yields only exact HAS_PERMISSION responses; conditional and unknown values are skipped. An unspecified permissionship is a malformed response and fails the lookup with `Internal` whatever the filter. Limits count yielded matches, and skipped responses still advance pagination and cursors.
  - Filtering does not deduplicate results or change server caveat evaluation. LookupSubjects rejects the nondefault filter.

- **Relationship writes return a native WriteResult.**
  - Access `Revision` for the `ZedToken` to pass to consistency or watch APIs. Typed relationship mutations return the same wrapper.
  - Schema writes and filtered relationship deletion retain their operation-specific results; absent wire revisions retain their existing mapping.

- **Watch supports native relationship filters.**
  - `WatchOptions.RelationshipFilters` snapshots the filter list and forwards resource type/ID/prefix, relation, and subject constraints, including direct and named subject relations.
  - Pass an empty object-type list when specifying relationship filters. Nonempty object types and relationship filters are mutually exclusive and rejected before any RPC. Both lists empty watches all relationships.
  - Existing object-type calls remain available; watch examples prefer type-only relationship filters and retain cursor, cancellation, and content options.

- **Be the single copy of the generated protocol types.**
  - `SpiceDB.Proto` is the canonical binding for `Authzed.Api.V1` and
    `Authzed.Api.Materialize.V0`. The official legacy package, `Authzed.Net` (1.6.0 on
    NuGet), ships the same type names in its own assembly, so referencing both failed to
    compile (CS0433) and could load two copies of every message. .NET has no global
    descriptor registry that throws, so the conflict shows up at compile time and as
    split type identity.
  - The bridge is the .NET form of aliasing: `proto-clients/spicedb-csharp-proto/bridge/Authzed.Net`
    builds an assembly named `Authzed.Net` (2.0.0) containing only
    `[assembly: TypeForwardedTo]` for every exported generated type, written by
    `bridge/generator` (`mage bridge`). Source and already-compiled legacy binaries then
    resolve to `SpiceDB.Proto` types and pass values to this client without conversion.
    Authzed.Net's `V0` and `V1Alpha1` namespaces have no upstream counterpart any more and
    are not forwarded. The proto tier's tests load both assemblies in one process.

- **Serve tooling without the raw escape hatch.**
  - `CreateFromCallInvoker(callInvoker, options)` wraps a caller-owned `CallInvoker` (an
    in-memory or test-server channel's invoker, an intercepted invoker, or
    `channel.CreateCallInvoker()`). It adds no credentials, rejects
    `RemotePlaintextCredentials.Allow` (transport and credentials belong to the invoker),
    applies operation options (deadlines, retry policy), and never disposes anything on
    `DisposeAsync`. `CreateFromChannel` remains for borrowed channels that need the preshared key.
  - Every options type has `Transport` (`TransportOptions`): `Headers` are added to every
    RPC the operation makes (snapshotted, copied per attempt), and `ResponseMetadata`, a
    mutable sink, receives the response headers and trailers of the operation's last RPC,
    including on failure. Deadline and cancellation stay owned by the native options and
    `CancellationToken`, so per-call deadline or token overrides are not exposed.
  - `SpiceDB.Client.Protocol.ProtoConvert` exports native↔protocol conversions: object and
    subject refs, relationships, updates, filters, preconditions, consistency, tokens,
    `PermissionCheck`→`CheckPermissionRequest` or bulk request item
    (`ToCheckBulkRequestItem`), `Transaction`→`WriteRelationshipsRequest`, and results
    (`CheckResult` with its recursive trace, `LookupResource`, `LookupSubject`,
    `ExpandResult`/`PermissionTree`, `ReadRelationship`, `WriteResult`). The client uses
    the same conversions. Responses reproduce the server's wire shape:
    `ToCheckBulkResponse(requests, results, options)` rebuilds a whole bulk-check response
    (per-item errors as the server's own `google.rpc.Status`, details included), and a
    `LookupSubject` fills the deprecated top-level fields SpiceDB still sends. Tests
    round-trip server responses, including a live lookup-subjects stream.
  - `CheckOptions.ItemErrors = BulkItemErrors.Returned` makes bulk checks return every
    item; a failed item's `CheckResult.Error` holds its typed exception (with `InputIndex`),
    its permissionship is `Unspecified`, and `HasPermission` is false. `Fail` (default)
    throws for the first failing item. A malformed item in the server's response (such
    as an unset permissionship) still fails the whole call with the client-detected
    malformed-response error.

- **Interoperate with gRPC status inspection in both directions.**
  - `SpiceDBException.StatusCode` is the matching `Grpc.Core.StatusCode` for every error:
    server errors report the server's code; local validation is `InvalidArgument`, a local
    total-deadline expiry is `DeadlineExceeded`, and caller-built errors report their
    type's code. `Status`, `Trailers` and `ToRpcException()` return the server's own status
    and trailers (so `GetRpcStatus()` and `ErrorInfo` details survive) or ones built from
    the code. The server's `RpcException` stays the `InnerException`.
  - `ServerMessage` holds the server's status message verbatim and is never null: for
    local errors (validation, deadlines, caller-built errors) it is the error's `Message`.
    `Message` is the display message.
  - `SpiceDBException` does not derive from `Grpc.Core.RpcException`, although a legacy
    `catch (RpcException)` then stops matching. `RpcException` has no inner-exception
    constructor, so a subclass could not keep the server's `RpcException` as
    `InnerException` (where status, trailers and `ErrorInfo` are preserved), and the
    client's own `catch (RpcException)` mapping would re-catch native errors. MIGRATION.md
    gives the handler mapping (`StatusCode`, `Status`, `Trailers`, `ToRpcException()`,
    `CodeOf`).
  - There are no wrapper exception types: a bulk item's error is the item's own typed
    exception (with `InputIndex`), and deletion progress is attached to the failure itself
    (`DeleteProgress`). `Status`, `ToRpcException()` and `Message` therefore report the
    server's message once, never under operation prefixes.
  - Caller cancellation stays a native `OperationCanceledException` carrying the caller's
    token. `SpiceDBException.CodeOf(exception)` classifies any exception: native errors,
    raw `RpcException`s, cancellation (`Cancelled`), `TimeoutException`
    (`DeadlineExceeded`), and inner-exception chains. `SpiceDBException.FromRpcException`
    maps a raw `RpcException` (for example from a fake) to the typed exception.
  - `SpiceDBException.Create(code, message)` is the public constructor for test doubles: it
    returns the subclass for the code with `ServerMessage` set, exactly as the server
    mapping would.

- **Make native values observable and testable without a server.**
  - `ObjectRef`, `SubjectRef`, `Relationship`, `RelationshipUpdate` (`TOUCH(...)`),
    `Filter` (zed-like, `*` for unset), `SubjectRelationFilter` (`*`, `...`, name),
    `Precondition` and `Transaction` override `ToString`. Their public properties are the
    structured log and `System.Text.Json` form; every public enum carries
    `JsonStringEnumConverter` and serializes as its name (`"Permissionship":"HasPermission"`),
    `ZedToken` serializes as its string, and `SubjectRelationFilter` exposes `Kind`/`Name`,
    so nothing serializes as a number, a wrapper object or `{}`. A test asserts the
    converter on every exported enum.
  - Identifiers use `Id`: `LookupResource.ResourceId` and `ResolvedSubject.SubjectId`, like
    `ObjectRef.ObjectId` and `Relationship.ResourceId`. The earlier `ResourceID` and
    `SubjectID` remain as `[Obsolete]`, JSON-ignored aliases.
  - `Relationship` has short accessors (`ResourceType`, `ResourceId`, `SubjectType`,
    `SubjectId`, `SubjectRelation`, ignored by JSON) and copy helpers `WithResource`,
    `WithRelation`, `WithSubject` that keep caveat and expiration.
  - `Relationship.FromTuple` and `ToString` cover SpiceDB's full tuple syntax:
    `[caveat]`, `[caveat:{json}]` (keys sorted on output) and `[expiration:RFC 3339 UTC]`,
    with `...` as a direct subject. Tests use the vectors of spicedb-go's
    SpiceDB-parser compatibility test, and a live example round-trips caveats and
    expirations through the server. Sub-100ns expiration precision rounds to ticks.
  - `Filter.Matches(relationship)` applies SpiceDB filter semantics, including
    `Any`/`Direct`/`Named` subject relations and ID prefixes.
  - Constructors validate and throw `InvalidArgumentException`, so they already serve
    compile-time constants and tests; there are no separate panicking (`Must*`) variants.
  - `SpiceDB.Client.Testing.InMemorySpiceDBClient` is the in-memory double. It and
    `SpiceDBClient` both implement `ISpiceDBRelationshipClient` (checks, bulk checks,
    writes, bounded and complete deletes, reads, imports, exports) and
    `ISpiceDBSchemaClient` (read, write and reflect schema), with identical signatures,
    because C# cannot apply a caller-defined interface to a sealed class after the fact. It
    applies preconditions, CREATE conflicts, one-transaction imports and filters through
    `Filter.Matches`, honors cursors, limits and `TransportOptions.ResponseMetadata` sinks
    (`SetResponseMetadata`), and returns a registered reflection
    (`SetReflectSchemaResult`, empty without one, as SpiceDB reflects an empty schema).
    Its errors have the real client's types, codes and messages for the same failure:
    server failures carry SpiceDB's message (verified against a live server) and
    `ErrorInfo` reason, and local validation is an `InvalidArgumentException` whose server
    message is its message; it rejects per-call mutation retries as the client does.
  - The double resolves a check in the root design's fixed order: a result or error
    registered for exactly that question (`SetCheckResult`, `SetCheckError`); then the
    check resolver (`SetCheckResolver(Func<PermissionCheck, CheckResult?>)`), which
    receives the check with the call's `Context` merged in (the check's keys win), answers
    with a `CheckResult` (its `CheckedAt` replaced by the double's revision; return
    `ConditionalPermission` with missing context to emulate an unmet caveat) or declines
    with null, and never fails (failures come from the fault hooks and `SetCheckError`);
    `InMemorySpiceDBClient.GrantAll` is the grant-all resolver and `GrantAllChecks()`
    installs it; then the stored relationships, where an unexpired relationship on the
    resource whose relation is the permission's name and whose subject matches exactly, or
    as a wildcard (`type:*`) over a direct subject of that type, grants it, conditionally
    when caveated (caveats are not evaluated; an uncaveated match wins); anything else is
    `NoPermission`. It does not evaluate a schema.
  - `LookupResourcesAsync` and `LookupSubjectsAsync` enumerate the objects of the requested
    type that appear in the stored relationships (as resource or subject, in order of first
    appearance; resource lookups skip `*`, subject lookups report a stored wildcard as `*`)
    and yield those check resolution grants, conditional results as conditional with the
    resolved missing context in `PartialCaveat`. Resource lookups honor
    `PermissionshipFilter`, `Limit` (counting yielded results) and `ResumeCursor`: item n's
    `AfterResultCursor` is `CursorAfter(n)`, where n counts granted results including ones
    the filter drops. Subject lookups take `SubjectRelation` (direct when null or `...`),
    honor `Limit`, have no cursor (as the real client's have none) and reject the options
    the real client rejects. Both run the `Item` fault stage before each result; a
    registered check error for a candidate fails the lookup.
    It records every call (`Calls`, `CallsOf(SpiceDBOperation)`) as a `RecordedCall` with
    the operation, a per-operation index, the arguments and the options object as passed,
    and accepts fault injection (`SetFaultHook(Func<Fault, Exception?>)`) before any call,
    before each import batch (batch index, by the call's `BatchSize`), before an import
    commits and before each read, export or lookup item (item index); `FailNext(operation, error)`
    fails exactly one call, and `InMemorySpiceDBClient.ServerError(code, message, reason)`
    builds the real client's exception for a server failure. A failed write or import
    applies nothing.
  - `ISpiceDBClient` (composing `ISpiceDBRelationshipClient`, `ISpiceDBSchemaClient` and
    `IAsyncDisposable`) is the client's full public method set; a reflection test fails if
    it and `SpiceDBClient`'s public instance methods diverge in name, return type,
    parameters or defaults. `SpiceDBClient` and `InMemorySpiceDBClient` both implement it.
    In the double, `CheckAnyAsync`/`CheckAllAsync` reduce a bulk check (recorded as
    `CheckBulk`) and `DisposeAsync` does nothing; `RawProto()` throws
    `InvalidOperationException` as a client without a raw transport does. Code written
    against `ISpiceDBClient` (a typed wrapper included) therefore runs over the double.
    Every other operation (watch, schema introspection and diff, expansion, experimental
    operations) is recorded, consults the fault hook, validates its options, and fails with
    a `SpiceDBException` of code `Unimplemented` whose message and server message are
    "`<operation>` is not implemented by InMemorySpiceDBClient"; streams fail on the first
    move.
  - An import-batch fault fires once each batch is full or the input ends, and
    `Fault.Batch` holds that batch's relationships (empty at other stages).
    `InMemorySpiceDBClient.CursorAfter(n)` returns the resume cursor the double puts on item
    n (1-based) of a read or export, which resumes after the first n matches; its format, the
    invariant decimal n, is stable for tests. `SetReadSchemaRevision(SchemaRevision.Omitted)`
    makes `ReadSchemaAsync` report an empty revision, as some hosted deployments do.
  - Options are plain init-only objects, so a test double or wrapper reads them directly;
    no resolver is needed.
  - Watch rejects nonempty object types combined with relationship filters because SpiceDB
    does. Schema reads that take a consistency require it like every other read;
    `Consistency.MinLatency()` matches the raw API's default.

- **Document the path off Authzed.Net and keep dependency floors low.**
  - `MIGRATION.md` covers moving from `Authzed.Net`, with before/after snippets.
  - `AI_INTEGRATION_GUIDE.md` is the concrete C# guide for AI coding agents (native tier
    for all code until a C# generator exists, fail-closed checks, atomic writes, paging,
    tooling options, errors, the in-memory double). The `SpiceDBClient` doc comment and
    `examples/README.md` point to it. Every snippet is compiled against the current API
    (extracted into a throwaway project) whenever the guide or the APIs it shows change.
  - `SpiceDB.Client`, `SpiceDB.Proto` and the `Authzed.Net` bridge target `net8.0`,
    `net9.0` and `net10.0` (every supported .NET release), so a net8.0 or net9.0 host can
    reference them, and build with `TreatWarningsAsErrors`, so no compiler warning reaches
    a consumer's build. Test, example and tooling projects stay on `net10.0`.
  - `README.md` covers installation (including consuming the unpublished packages from a
    project reference or a local feed), a first call, and links to this document,
    MIGRATION.md and the AI integration guide. MIGRATION.md opens with a Requirements
    section and has a legacy-to-new table of renamed response members.
  - `SpiceDB.Client` and `SpiceDB.Proto` declare exact lowest working versions
    (`Grpc.Net.Client`/`Grpc.Core.Api`/`Grpc.StatusProto` 2.80.0, `Google.Protobuf`
    3.34.1, `Google.Api.CommonProtos` 2.17.0, matching `Authzed.Net` 1.6.0) instead of
    floating wildcards. Test frameworks live only in test and example projects: the proto
    tier's tests moved from `SpiceDB.Proto.csproj` to `tests/SpiceDB.Proto.Tests.csproj`,
    which `SpiceDB.Client.sln` runs.
