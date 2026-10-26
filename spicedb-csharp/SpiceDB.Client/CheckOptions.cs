namespace SpiceDB.Client;

public sealed class CheckOptions
{
    private IReadOnlyDictionary<string, object?>? _context;
    public IReadOnlyDictionary<string, object?>? Context { get => _context; init => _context = Native.Context(value); }
    public TimeSpan? Timeout { get; init; }
    private CheckTracing _tracing;
    public CheckTracing Tracing { get => _tracing; init => _tracing = BehaviorOptions.Defined(value, nameof(Tracing)); }
}
