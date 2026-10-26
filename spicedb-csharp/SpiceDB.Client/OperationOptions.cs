namespace SpiceDB.Client;

public sealed class CallOptions
{
    public TimeSpan? Timeout { get; init; }
    private IReadOnlyList<SchemaFilter> _schemaFilters = Array.Empty<SchemaFilter>();
    public IReadOnlyList<SchemaFilter> SchemaFilters { get => _schemaFilters; init => _schemaFilters = Native.List(value); }
    public string? DefinitionNameFilter { get; init; }
    internal void Validate(bool reflect = false, bool computable = false)
    {
        if ((!reflect && SchemaFilters.Count != 0) || (!computable && DefinitionNameFilter is not null))
            throw new InvalidArgumentException("Schema filters are unsupported by this operation.");
    }

}
public sealed class ClientOptions
{
    public TimeSpan DefaultTimeout { get; init; } = TimeSpan.FromSeconds(30); private RemotePlaintextCredentials _remotePlaintextCredentials;
    public RemotePlaintextCredentials RemotePlaintextCredentials { get => _remotePlaintextCredentials; init => _remotePlaintextCredentials = BehaviorOptions.Defined(value, nameof(RemotePlaintextCredentials)); }
}
public sealed class ReadOptions
{
    public TimeSpan? Timeout { get; init; }
    public int PageSize { get; init; } = 512; public int? Limit { get; init; }
}
public sealed class ExportOptions
{
    public TimeSpan? Timeout { get; init; }
    public int PageSize { get; init; } = 512; public int? Limit { get; init; }
}
public sealed class ImportOptions
{
    public TimeSpan? Timeout { get; init; }
    public int BatchSize { get; init; } = 1000;
}
public sealed class WatchOptions
{
    public TimeSpan? Timeout { get; init; }
    private WatchContent _content = WatchContent.Relationships;
    public WatchContent Content { get => _content; init => _content = BehaviorOptions.Content(value); }
    private IReadOnlyList<Filter> _relationshipFilters = Array.Empty<Filter>();
    public IReadOnlyList<Filter> RelationshipFilters { get => _relationshipFilters; init => _relationshipFilters = Native.List(value); }
}
public sealed class WriteOptions
{
    public TimeSpan? Timeout { get; init; }
    private IReadOnlyDictionary<string, object?>? _metadata; public IReadOnlyDictionary<string, object?>? Metadata { get => _metadata; init => _metadata = Native.Context(value); }
}
public sealed class DeleteOptions
{
    private IReadOnlyDictionary<string, object?>? _metadata; public IReadOnlyDictionary<string, object?>? Metadata { get => _metadata; init => _metadata = Native.Context(value); }
    public TimeSpan? Timeout { get; init; }

    public string? ResumeCursor { get; init; }
    private IReadOnlyList<Filter> _MustMatch = Array.Empty<Filter>(); public IReadOnlyList<Filter> MustMatch { get => _MustMatch; init => _MustMatch = Native.List(value ?? throw new InvalidArgumentException("MustMatch cannot be null.")); }
    private IReadOnlyList<Filter> _MustNotMatch = Array.Empty<Filter>(); public IReadOnlyList<Filter> MustNotMatch { get => _MustNotMatch; init => _MustNotMatch = Native.List(value ?? throw new InvalidArgumentException("MustNotMatch cannot be null.")); }
}
public sealed class DeleteAllOptions
{
    private IReadOnlyDictionary<string, object?>? _metadata; public IReadOnlyDictionary<string, object?>? Metadata { get => _metadata; init => _metadata = Native.Context(value); }
    public TimeSpan? Timeout { get; init; }
    public int PageSize { get; init; } = 1000;
    public string? ResumeCursor { get; init; }
    private IReadOnlyList<Filter> _MustMatch = Array.Empty<Filter>(); public IReadOnlyList<Filter> MustMatch { get => _MustMatch; init => _MustMatch = Native.List(value ?? throw new InvalidArgumentException("MustMatch cannot be null.")); }
    private IReadOnlyList<Filter> _MustNotMatch = Array.Empty<Filter>(); public IReadOnlyList<Filter> MustNotMatch { get => _MustNotMatch; init => _MustNotMatch = Native.List(value ?? throw new InvalidArgumentException("MustNotMatch cannot be null.")); }
}
