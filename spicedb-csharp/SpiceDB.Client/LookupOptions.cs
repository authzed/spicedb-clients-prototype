namespace SpiceDB.Client;

/// <summary>Client-side LookupResources filtering. Conditional permission is not a grant.</summary>
public enum LookupPermissionshipFilter { All, HasPermissionOnly }

public sealed class LookupOptions
{
    private IReadOnlyDictionary<string, object?>? _context;
    public IReadOnlyDictionary<string, object?>? Context { get => _context; init => _context = Native.Context(value); }
    public TimeSpan? Timeout { get; init; }
    private LookupPermissionshipFilter _permissionshipFilter;
    public LookupPermissionshipFilter PermissionshipFilter { get => _permissionshipFilter; init => _permissionshipFilter = BehaviorOptions.Defined(value, nameof(PermissionshipFilter)); }
    private LookupDebug _debug;
    public LookupDebug Debug { get => _debug; init => _debug = BehaviorOptions.Defined(value, nameof(Debug)); }
    public int? PageSize { get; init; }
    public int? Limit { get; init; }
    public string? SubjectRelation { get; init; }
}
