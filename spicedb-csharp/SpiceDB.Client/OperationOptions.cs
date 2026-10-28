using Grpc.Core;

namespace SpiceDB.Client;

public sealed class CallOptions
{
    public TimeSpan? Timeout { get; init; }
    /// <summary>Per-call transport escape hatch: request headers, call credentials and response metadata capture.</summary>
    public TransportOptions? Transport { get; init; }
    /// <summary>
    /// Overrides the client's retry policy for this call only, including reopening a stream
    /// before its first item (whatever <see cref="ClientOptions.StreamRetries"/> says).
    /// <see cref="Client.RetryPolicy.None"/> exempts this call. Mutations stay single-attempt
    /// unless they opt in here, which only touch-only writes without preconditions and schema
    /// writes may; other mutations reject a policy with retries before any request.
    /// </summary>
    public RetryPolicy? RetryPolicy { get; init; }
    private IReadOnlyList<SchemaFilter> _schemaFilters = Array.Empty<SchemaFilter>();
    public IReadOnlyList<SchemaFilter> SchemaFilters { get => _schemaFilters; init => _schemaFilters = Native.List(value); }
    public string? DefinitionNameFilter { get; init; }
    internal void Validate(bool reflect = false, bool computable = false)
    {
        if ((!reflect && SchemaFilters.Count != 0) || (!computable && DefinitionNameFilter is not null))
            throw new InvalidArgumentException("Schema filters are unsupported by this operation.");
    }

}

/// <summary>
/// The supported escape hatch for transport-level data on any operation, so callers do not
/// need <see cref="SpiceDBClient.RawProto"/> for it. Applies to every attempt, chunk and page
/// of the operation. Data that SpiceDB versions before 1.30 send in trailers (for example check
/// debug traces) is reachable through <see cref="ResponseMetadata"/>.
/// </summary>
public sealed class TransportOptions
{
    private Metadata? _headers;
    /// <summary>Request headers added to every RPC the operation makes. Snapshotted when set.</summary>
    public Metadata? Headers
    {
        get => _headers;
        init
        {
            if (value is null) { _headers = null; return; }
            var copy = new Metadata();
            foreach (var entry in value) copy.Add(entry);
            _headers = copy;
        }
    }
    /// <summary>A mutable sink that receives the response headers and trailers. When an operation makes several RPCs (retries, chunks, pages), the last one wins.</summary>
    public ResponseMetadata? ResponseMetadata { get; init; }
}

/// <summary>
/// A mutable sink for response headers and trailers, filled in by the client. Create one per
/// operation and read it after the operation completes or fails. Not thread-safe.
/// </summary>
public sealed class ResponseMetadata
{
    /// <summary>The response headers of the operation's last RPC, if the server sent any.</summary>
    public Metadata? Headers { get; internal set; }
    /// <summary>The trailers of the operation's last completed RPC.</summary>
    public Metadata? Trailers { get; internal set; }
}

public sealed class ClientOptions
{
    public TimeSpan DefaultTimeout { get; init; } = TimeSpan.FromSeconds(30); private RemotePlaintextCredentials _remotePlaintextCredentials;
    public RemotePlaintextCredentials RemotePlaintextCredentials { get => _remotePlaintextCredentials; init => _remotePlaintextCredentials = BehaviorOptions.Defined(value, nameof(RemotePlaintextCredentials)); }
    private DefaultDeadline _defaultDeadline;
    /// <summary><see cref="Client.DefaultDeadline.Disabled"/> removes the default unary budget. Per-operation timeouts and the caller's token still apply.</summary>
    public DefaultDeadline DefaultDeadline { get => _defaultDeadline; init => _defaultDeadline = BehaviorOptions.Defined(value, nameof(DefaultDeadline)); }
    private Retries _retries;
    /// <summary><see cref="Client.Retries.Disabled"/> makes every operation a single attempt, for callers that apply their own policy.</summary>
    public Retries Retries { get => _retries; init => _retries = BehaviorOptions.Defined(value, nameof(Retries)); }
    /// <summary>The retry policy for idempotent reads. Null uses <see cref="Client.RetryPolicy.Default"/>. Mutations never retry under the client-wide policy; <see cref="OperationRetryPolicies"/> overrides it for one operation and each options type's <c>RetryPolicy</c> for one call.</summary>
    public RetryPolicy? RetryPolicy { get; init; }
    private StreamRetries _streamRetries;
    /// <summary><see cref="Client.StreamRetries.Disabled"/> opens each server stream (reads, lookups, exports, watch) exactly once.</summary>
    public StreamRetries StreamRetries { get => _streamRetries; init => _streamRetries = BehaviorOptions.Defined(value, nameof(StreamRetries)); }
    private IReadOnlyDictionary<SpiceDBOperation, RetryPolicy> _operationRetryPolicies = new Dictionary<SpiceDBOperation, RetryPolicy>();
    /// <summary>
    /// The retry policy for every call of one operation, including stream establishment, for
    /// example never reopening watches while everything else keeps the client's policy:
    /// <code>OperationRetryPolicies = new Dictionary&lt;SpiceDBOperation, RetryPolicy&gt; { [SpiceDBOperation.Updates] = RetryPolicy.None }</code>
    /// The most specific policy wins: a per-call <c>RetryPolicy</c>, then this, then
    /// <see cref="RetryPolicy"/>. For <see cref="SpiceDBOperation.Write"/> it applies only to
    /// writes that are idempotent on replay (<see cref="Transaction.IsIdempotent"/>); other
    /// writes are not retried and not rejected. Operations that are never idempotent (deletes,
    /// delete-all, imports, counter registration and unregistration) reject a policy with
    /// retries when the client is built; a zero-retry policy is allowed for any operation.
    /// Snapshotted when set.
    /// </summary>
    public IReadOnlyDictionary<SpiceDBOperation, RetryPolicy> OperationRetryPolicies
    {
        get => _operationRetryPolicies;
        init => _operationRetryPolicies = new Dictionary<SpiceDBOperation, RetryPolicy>(value ?? throw new InvalidArgumentException("OperationRetryPolicies cannot be null."));
    }

    /// <summary>Operations that are never idempotent on replay, so a retrying per-operation policy for them is rejected.</summary>
    private static readonly HashSet<SpiceDBOperation> NeverReplayable =
    [
        SpiceDBOperation.DeleteRelationships, SpiceDBOperation.DeleteAllRelationshipsNonAtomic, SpiceDBOperation.ImportRelationships,
        SpiceDBOperation.ExperimentalRegisterRelationshipCounter, SpiceDBOperation.ExperimentalUnregisterRelationshipCounter,
    ];

    /// <summary>Validates the per-operation retry policies, so a client is never built from options it would reject later.</summary>
    internal void ValidateOperationRetryPolicies()
    {
        foreach (var (operation, policy) in _operationRetryPolicies)
        {
            BehaviorOptions.Defined(operation, nameof(SpiceDBOperation));
            if (policy is null) throw new InvalidArgumentException($"The retry policy for {operation} cannot be null.");
            policy.Validated();
            if (policy.MaxRetries > 0 && NeverReplayable.Contains(operation))
                throw new InvalidArgumentException($"{operation} cannot be retried: it is not idempotent on replay.");
        }
    }
}

/// <summary>
/// Identifies one <see cref="SpiceDBClient"/> operation, named after its method without the
/// <c>Async</c> suffix. Used by <see cref="ClientOptions.OperationRetryPolicies"/> and by the
/// in-memory test double's call recording and fault injection.
/// <see cref="SpiceDBClient.CheckAnyAsync"/> and <see cref="SpiceDBClient.CheckAllAsync"/> are
/// <see cref="CheckBulk"/> calls.
/// </summary>
public enum SpiceDBOperation
{
    Check,
    CheckBulk,
    Write,
    DeleteRelationships,
    DeleteAllRelationshipsNonAtomic,
    ReadRelationships,
    LookupResources,
    LookupSubjects,
    ImportRelationships,
    ExportRelationships,
    Updates,
    ReadSchema,
    WriteSchema,
    ReflectSchema,
    ComputablePermissions,
    DependentRelations,
    DiffSchema,
    ExpandPermissionTree,
    ExperimentalRegisterRelationshipCounter,
    ExperimentalCountRelationships,
    ExperimentalUnregisterRelationshipCounter,
    ExperimentalRoaringLookupResources,
    ExperimentalCountRelationshipsByFilter,
    ExperimentalWatchPermissionSets,
    ExperimentalLookupPermissionSets,
    ExperimentalDownloadPermissionSets,
}

/// <summary>Whether unary operations get the client's default deadline.</summary>
public enum DefaultDeadline { Enabled, Disabled }
/// <summary>Whether idempotent reads are retried at all.</summary>
public enum Retries { Enabled, Disabled }
/// <summary>Whether opening a server stream is retried before any item is exposed. A partially consumed stream is never restarted.</summary>
public enum StreamRetries { Enabled, Disabled }

/// <summary>Describes a retry about to happen, for <see cref="RetryPolicy.OnRetry"/>.</summary>
/// <param name="Operation">The client method, e.g. <c>ReadSchemaAsync</c>.</param>
/// <param name="Attempt">The attempt that failed, starting at 1.</param>
/// <param name="Error">The attempt's error, mapped as the client would throw it.</param>
/// <param name="Delay">The backoff before the next attempt.</param>
public sealed record RetryEvent(string Operation, int Attempt, SpiceDBException Error, TimeSpan Delay);

/// <summary>
/// Retry policy for idempotent reads, set client-wide (<see cref="ClientOptions.RetryPolicy"/>)
/// per operation (<see cref="ClientOptions.OperationRetryPolicies"/>) or per call (each options
/// type's <c>RetryPolicy</c>). Mutations are never replayed automatically: only a touch-only
/// write without preconditions and a schema write may opt in; deletes, creates, preconditioned writes, imports and counter registration reject
/// the opt-in before any request.
/// </summary>
public sealed class RetryPolicy
{
    /// <summary>Three retries of Unavailable and Aborted, full-jitter backoff starting at 100ms.</summary>
    public static RetryPolicy Default { get; } = new();
    /// <summary>No retries. As a per-call override it exempts one call (such as a supervised watch) while the client retries everything else.</summary>
    public static RetryPolicy None { get; } = new() { MaxRetries = 0 };
    /// <summary>Retries after the first attempt. Zero disables retries.</summary>
    public int MaxRetries { get; init; } = 3;
    private IReadOnlyList<StatusCode> _codes = [StatusCode.Unavailable, StatusCode.Aborted];
    /// <summary>Status codes that are retried. Callers may opt into others, such as ResourceExhausted.</summary>
    public IReadOnlyList<StatusCode> RetryableCodes { get => _codes; init => _codes = Native.List(value); }
    /// <summary>Upper bound of the first full-jitter delay; each retry doubles it.</summary>
    public TimeSpan InitialBackoff { get; init; } = TimeSpan.FromMilliseconds(100);
    /// <summary>Caps the delay bound. Null means uncapped (apart from the operation budget).</summary>
    public TimeSpan? MaxBackoff { get; init; }
    /// <summary>Called before each backoff, for example to log retries. Runs on the operation's thread.</summary>
    public Action<RetryEvent>? OnRetry { get; init; }
    internal RetryPolicy Validated() =>
        MaxRetries < 0 || (MaxRetries > 0 && InitialBackoff <= TimeSpan.Zero) || MaxBackoff is { } m && m <= TimeSpan.Zero
            ? throw new InvalidArgumentException("RetryPolicy needs non-negative MaxRetries, a positive InitialBackoff and a positive MaxBackoff when set.")
            : this;
}

/// <summary>How a paged read fetches results.</summary>
public enum Paging
{
    /// <summary>Fetches page after page of <c>PageSize</c> until results or <c>Limit</c> run out. The default.</summary>
    Automatic,
    /// <summary>
    /// Sends exactly one request, as the raw API does: its limit is <c>Limit</c> (unbounded
    /// when null), <c>PageSize</c> is ignored, and no follow-up page is requested. Each item
    /// still carries its after-result cursor.
    /// </summary>
    SingleRequest,
}

public sealed class ReadOptions
{
    public TimeSpan? Timeout { get; init; }
    public TransportOptions? Transport { get; init; }
    /// <summary>
    /// Overrides the client's retry policy for this call only, including reopening a stream
    /// before its first item (whatever <see cref="ClientOptions.StreamRetries"/> says).
    /// <see cref="Client.RetryPolicy.None"/> exempts this call. Mutations stay single-attempt
    /// unless they opt in here, which only touch-only writes without preconditions and schema
    /// writes may; other mutations reject a policy with retries before any request.
    /// </summary>
    public RetryPolicy? RetryPolicy { get; init; }
    /// <summary>Relationships per page (default 512). Every page repeats this size and the caller's consistency.</summary>
    public int PageSize { get; init; } = 512; public int? Limit { get; init; }
    /// <summary>Resumes after the item whose <see cref="ReadRelationship.AfterResultCursor"/> this is. Repeat the request, consistency and page size.</summary>
    public string? ResumeCursor { get; init; }
    private Paging _paging;
    /// <summary><see cref="Client.Paging.SingleRequest"/> sends one request like the raw API, with <see cref="Limit"/> (or no limit) and no follow-up page.</summary>
    public Paging Paging { get => _paging; init => _paging = BehaviorOptions.Defined(value, nameof(Paging)); }
}
public sealed class ExportOptions
{
    public TimeSpan? Timeout { get; init; }
    public TransportOptions? Transport { get; init; }
    /// <summary>
    /// Overrides the client's retry policy for this call only, including reopening a stream
    /// before its first item (whatever <see cref="ClientOptions.StreamRetries"/> says).
    /// <see cref="Client.RetryPolicy.None"/> exempts this call. Mutations stay single-attempt
    /// unless they opt in here, which only touch-only writes without preconditions and schema
    /// writes may; other mutations reject a policy with retries before any request.
    /// </summary>
    public RetryPolicy? RetryPolicy { get; init; }
    public int PageSize { get; init; } = 512; public int? Limit { get; init; }
    /// <summary>Resumes after the response whose <see cref="ExportedRelationship.AfterResultCursor"/> this is. Repeat the request and page size.</summary>
    public string? ResumeCursor { get; init; }
}
public sealed class ImportOptions
{
    public TimeSpan? Timeout { get; init; }
    public TransportOptions? Transport { get; init; }
    /// <summary>
    /// Overrides the client's retry policy for this call only, including reopening a stream
    /// before its first item (whatever <see cref="ClientOptions.StreamRetries"/> says).
    /// <see cref="Client.RetryPolicy.None"/> exempts this call. Mutations stay single-attempt
    /// unless they opt in here, which only touch-only writes without preconditions and schema
    /// writes may; other mutations reject a policy with retries before any request.
    /// </summary>
    public RetryPolicy? RetryPolicy { get; init; }
    /// <summary>Relationships per stream message (default 1,000). One import call is one stream and one transaction; make several calls for several transactions.</summary>
    public int BatchSize { get; init; } = 1000;
}
public sealed class WatchOptions
{
    public TimeSpan? Timeout { get; init; }
    public TransportOptions? Transport { get; init; }
    /// <summary>
    /// Overrides the client's retry policy for this call only, including reopening a stream
    /// before its first item (whatever <see cref="ClientOptions.StreamRetries"/> says).
    /// <see cref="Client.RetryPolicy.None"/> exempts this call. Mutations stay single-attempt
    /// unless they opt in here, which only touch-only writes without preconditions and schema
    /// writes may; other mutations reject a policy with retries before any request.
    /// </summary>
    public RetryPolicy? RetryPolicy { get; init; }
    private WatchContent _content = WatchContent.Relationships;
    public WatchContent Content { get => _content; init => _content = BehaviorOptions.Content(value); }
    private IReadOnlyList<Filter> _relationshipFilters = Array.Empty<Filter>();
    public IReadOnlyList<Filter> RelationshipFilters { get => _relationshipFilters; init => _relationshipFilters = Native.List(value); }
}
public sealed class WriteOptions
{
    public TimeSpan? Timeout { get; init; }
    public TransportOptions? Transport { get; init; }
    /// <summary>
    /// Overrides the client's retry policy for this call only, including reopening a stream
    /// before its first item (whatever <see cref="ClientOptions.StreamRetries"/> says).
    /// <see cref="Client.RetryPolicy.None"/> exempts this call. Mutations stay single-attempt
    /// unless they opt in here, which only touch-only writes without preconditions and schema
    /// writes may; other mutations reject a policy with retries before any request.
    /// </summary>
    public RetryPolicy? RetryPolicy { get; init; }
    private IReadOnlyDictionary<string, object?>? _metadata; public IReadOnlyDictionary<string, object?>? Metadata { get => _metadata; init => _metadata = Native.Context(value); }
}

/// <summary>Whether a bounded delete may delete only part of the matching relationships.</summary>
public enum PartialDeletion
{
    /// <summary>Deletes up to the limit and reports completion. The default; the limit is required.</summary>
    Allowed,
    /// <summary>All-or-nothing: the server rejects a delete exceeding the limit. A null limit uses the server default.</summary>
    Disallowed,
}

public sealed class DeleteOptions
{
    private IReadOnlyDictionary<string, object?>? _metadata; public IReadOnlyDictionary<string, object?>? Metadata { get => _metadata; init => _metadata = Native.Context(value); }
    public TimeSpan? Timeout { get; init; }
    public TransportOptions? Transport { get; init; }
    /// <summary>
    /// Overrides the client's retry policy for this call only, including reopening a stream
    /// before its first item (whatever <see cref="ClientOptions.StreamRetries"/> says).
    /// <see cref="Client.RetryPolicy.None"/> exempts this call. Mutations stay single-attempt
    /// unless they opt in here, which only touch-only writes without preconditions and schema
    /// writes may; other mutations reject a policy with retries before any request.
    /// </summary>
    public RetryPolicy? RetryPolicy { get; init; }
    private PartialDeletion _partial;
    /// <summary><see cref="PartialDeletion.Disallowed"/> makes the delete all-or-nothing.</summary>
    public PartialDeletion Partial { get => _partial; init => _partial = BehaviorOptions.Defined(value, nameof(Partial)); }

    public string? ResumeCursor { get; init; }
    private IReadOnlyList<Filter> _MustMatch = Array.Empty<Filter>(); public IReadOnlyList<Filter> MustMatch { get => _MustMatch; init => _MustMatch = Native.List(value ?? throw new InvalidArgumentException("MustMatch cannot be null.")); }
    private IReadOnlyList<Filter> _MustNotMatch = Array.Empty<Filter>(); public IReadOnlyList<Filter> MustNotMatch { get => _MustNotMatch; init => _MustNotMatch = Native.List(value ?? throw new InvalidArgumentException("MustNotMatch cannot be null.")); }
}
public sealed class DeleteAllOptions
{
    private IReadOnlyDictionary<string, object?>? _metadata; public IReadOnlyDictionary<string, object?>? Metadata { get => _metadata; init => _metadata = Native.Context(value); }
    public TimeSpan? Timeout { get; init; }
    public TransportOptions? Transport { get; init; }
    /// <summary>
    /// Overrides the client's retry policy for this call only, including reopening a stream
    /// before its first item (whatever <see cref="ClientOptions.StreamRetries"/> says).
    /// <see cref="Client.RetryPolicy.None"/> exempts this call. Mutations stay single-attempt
    /// unless they opt in here, which only touch-only writes without preconditions and schema
    /// writes may; other mutations reject a policy with retries before any request.
    /// </summary>
    public RetryPolicy? RetryPolicy { get; init; }
    public int PageSize { get; init; } = 1000;
    public string? ResumeCursor { get; init; }
    /// <summary>
    /// Reported after every acknowledged batch with the cumulative result so far: total deleted
    /// count, latest revision and cursor. Invoked synchronously on the operation's thread; the
    /// operation budget keeps running while it executes.
    /// </summary>
    public IProgress<DeleteResult>? Progress { get; init; }
    private IReadOnlyList<Filter> _MustMatch = Array.Empty<Filter>(); public IReadOnlyList<Filter> MustMatch { get => _MustMatch; init => _MustMatch = Native.List(value ?? throw new InvalidArgumentException("MustMatch cannot be null.")); }
    private IReadOnlyList<Filter> _MustNotMatch = Array.Empty<Filter>(); public IReadOnlyList<Filter> MustNotMatch { get => _MustNotMatch; init => _MustNotMatch = Native.List(value ?? throw new InvalidArgumentException("MustNotMatch cannot be null.")); }
}
