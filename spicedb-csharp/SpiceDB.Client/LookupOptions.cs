using System.Text.Json.Serialization;
namespace SpiceDB.Client;

/// <summary>Client-side LookupResources filtering. Conditional permission is not a grant.</summary>
[JsonConverter(typeof(JsonStringEnumConverter<LookupPermissionshipFilter>))]
public enum LookupPermissionshipFilter { All, HasPermissionOnly }

public sealed class LookupOptions
{
    private IReadOnlyDictionary<string, object?>? _context;
    public IReadOnlyDictionary<string, object?>? Context { get => _context; init => _context = Native.Context(value); }
    public TimeSpan? Timeout { get; init; }
    public TransportOptions? Transport { get; init; }
    /// <summary>Overrides the client's retry policy for this call only, including reopening the stream before its first item. <see cref="Client.RetryPolicy.None"/> exempts it.</summary>
    public RetryPolicy? RetryPolicy { get; init; }
    /// <summary>
    /// LookupResources only: resumes after the item whose <see cref="LookupResource.AfterResultCursor"/>
    /// this is; only items with a non-null cursor are resume points. Repeat the request and page
    /// size (the response batch size may differ).
    /// </summary>
    public string? ResumeCursor { get; init; }
    private LookupPermissionshipFilter _permissionshipFilter;
    public LookupPermissionshipFilter PermissionshipFilter { get => _permissionshipFilter; init => _permissionshipFilter = BehaviorOptions.Defined(value, nameof(PermissionshipFilter)); }
    private LookupDebug _debug;
    public LookupDebug Debug { get => _debug; init => _debug = BehaviorOptions.Defined(value, nameof(Debug)); }
    public int? PageSize { get; init; }
    public int? Limit { get; init; }
    public string? SubjectRelation { get; init; }
    private Paging _paging;
    /// <summary>LookupResources only: <see cref="Client.Paging.SingleRequest"/> sends one request like the raw API, with <see cref="Limit"/> (or no limit) and no follow-up page.</summary>
    public Paging Paging { get => _paging; init => _paging = BehaviorOptions.Defined(value, nameof(Paging)); }
    /// <summary>
    /// LookupResources only: how many resources the server is asked to pack into each response
    /// message. Null uses the page size, capped at 1,000 (under <see cref="Client.Paging.SingleRequest"/>:
    /// <see cref="Limit"/>, capped at 1,000, or 1,000 without one). Must be positive; it is sent as
    /// given, and the server rejects a size above its maximum. Results are still yielded one at a
    /// time, and only the last item yielded from a batch carries an
    /// <see cref="LookupResource.AfterResultCursor"/>.
    /// </summary>
    public int? ResponseBatchSize { get; init; }
    private LookupBatching _batching;
    /// <summary>
    /// LookupResources only: <see cref="LookupBatching.Disabled"/> asks for one resource per
    /// response message, so every item carries its cursor; <see cref="ResponseBatchSize"/> is then
    /// ignored.
    /// </summary>
    public LookupBatching Batching { get => _batching; init => _batching = BehaviorOptions.Defined(value, nameof(Batching)); }
}

/// <summary>Whether LookupResources asks the server for batched responses.</summary>
[JsonConverter(typeof(JsonStringEnumConverter<LookupBatching>))]
public enum LookupBatching
{
    /// <summary>
    /// Requests batches of <see cref="LookupOptions.ResponseBatchSize"/> resources per response
    /// message. The default. Only the last item yielded from a batch carries an after-result
    /// cursor; servers that do not batch answer one resource per message, each with its cursor.
    /// </summary>
    Automatic,
    /// <summary>Requests one resource per response message, so every item carries its after-result cursor.</summary>
    Disabled,
}

/// <summary>The response batch sizes LookupResources requests, shared by the client and the in-memory double.</summary>
internal static class LookupBatch
{
    /// <summary>The server's default maximum response batch size, which caps the default.</summary>
    internal const int DefaultMaximum = 1000;

    /// <summary>
    /// The batch size a call requests: zero when batching is disabled, the caller's size as
    /// given, or by default the page size (in single-request mode the total limit) capped at
    /// <see cref="DefaultMaximum"/>.
    /// </summary>
    internal static int Size(LookupOptions? options, bool singleRequest, int pageSize)
    {
        if (options?.ResponseBatchSize is { } given && given <= 0) throw new InvalidArgumentException("ResponseBatchSize must be positive.");
        if (options?.Batching == LookupBatching.Disabled) return 0;
        return options?.ResponseBatchSize ?? Math.Min(singleRequest ? options?.Limit ?? DefaultMaximum : pageSize, DefaultMaximum);
    }

    /// <summary>
    /// The batch size one page request sends (zero leaves the field unset). A batch has one
    /// cursor, after its last result, and a total limit must end on a resume point, so the page
    /// on which a limit runs out is batched only if a batch ends exactly at the limit: its size
    /// becomes the remaining count when that is smaller, stays when it divides the remaining
    /// count, and is otherwise zero. With the unconditional-only filter the results that count
    /// toward the limit are unpredictable, so a limited call is not batched at all.
    /// <paramref name="remaining"/> is the limit minus the results yielded so far, or null
    /// without a limit.
    /// </summary>
    internal static int ForPage(int batchSize, bool singleRequest, int pageSize, int? remaining, bool unconditionalOnly)
    {
        if (batchSize == 0 || remaining is not { } left || singleRequest) return batchSize;
        if (unconditionalOnly) return 0;
        if (left >= pageSize || left % batchSize == 0) return batchSize;
        return left < batchSize ? left : 0;
    }
}
