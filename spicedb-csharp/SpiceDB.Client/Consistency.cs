namespace SpiceDB.Client;

public readonly record struct ZedToken
{
    private readonly string? _value;
    public string Value => _value ?? "";
    public ZedToken(string value) => _value = value ?? throw new InvalidArgumentException("Token value cannot be null.");
    public override string ToString() => Value;
}

public sealed record Consistency
{
    private readonly int _kind;
    private readonly ZedToken _token;
    private Consistency(int kind, ZedToken token = default) { _kind = kind; _token = token; }
    public static Consistency Full() => new(0);
    public static Consistency MinLatency() => new(1);
    public static Consistency AtLeast(ZedToken token) => TokenStrategy(2, token);
    public static Consistency Snapshot(ZedToken token) => TokenStrategy(3, token);
    private static Consistency TokenStrategy(int kind, ZedToken token) => string.IsNullOrEmpty(token.Value)
        ? throw new InvalidArgumentException("A nonempty revision token is required.") : new(kind, token);
    public static Consistency AtLeastOrFull(ZedToken? token) => token is { } t && t.Value.Length > 0 ? AtLeast(t) : Full();
    public static Consistency AtLeastOrMinLatency(ZedToken? token) => token is { } t && t.Value.Length > 0 ? AtLeast(t) : MinLatency();
    internal Authzed.Api.V1.Consistency ToProto() => _kind switch
    {
        0 => new() { FullyConsistent = true },
        1 => new() { MinimizeLatency = true },
        2 => new() { AtLeastAsFresh = new() { Token = _token.Value } },
        _ => new() { AtExactSnapshot = new() { Token = _token.Value } },
    };
}
