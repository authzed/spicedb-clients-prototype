namespace SpiceDB.Client;

/// <summary>Client-side LookupResources filtering. Conditional permission is not a grant.</summary>
public enum LookupPermissionshipFilter { All, HasPermissionOnly }

public sealed class LookupOptions
{
    private IReadOnlyDictionary<string, object?>? _context;
    public IReadOnlyDictionary<string, object?>? Context { get => _context; init => _context = Native.Context(value); }
    public TimeSpan? Timeout { get; init; }
    public TransportOptions? Transport { get; init; }
    /// <summary>Overrides the client's retry policy for this call only, including reopening the stream before its first item. <see cref="Client.RetryPolicy.None"/> exempts it.</summary>
    public RetryPolicy? RetryPolicy { get; init; }
    /// <summary>LookupResources only: resumes after the item whose <see cref="LookupResource.AfterResultCursor"/> this is. Repeat the request and page size.</summary>
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
}
