namespace SpiceDB.Client;

/// <summary>How bulk checks report per-item failures.</summary>
public enum BulkItemErrors
{
    /// <summary>The first failing item fails the whole call with an exception carrying its input index. The default.</summary>
    Fail,
    /// <summary>Every item gets a result; a failed item's <see cref="CheckResult.Error"/> is set and it never grants.</summary>
    Returned,
}

public sealed class CheckOptions
{
    private IReadOnlyDictionary<string, object?>? _context;
    public IReadOnlyDictionary<string, object?>? Context { get => _context; init => _context = Native.Context(value); }
    public TimeSpan? Timeout { get; init; }
    public TransportOptions? Transport { get; init; }
    private CheckTracing _tracing;
    public CheckTracing Tracing { get => _tracing; init => _tracing = BehaviorOptions.Defined(value, nameof(Tracing)); }
    private BulkItemErrors _itemErrors;
    /// <summary>Bulk checks only: <see cref="BulkItemErrors.Returned"/> returns per-item errors on the results.</summary>
    public BulkItemErrors ItemErrors { get => _itemErrors; init => _itemErrors = BehaviorOptions.Defined(value, nameof(ItemErrors)); }
}
