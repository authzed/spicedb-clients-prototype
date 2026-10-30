using System.Text.Json;
using System.Text.Json.Serialization;

namespace SpiceDB.Client;

/// <summary>
/// An opaque SpiceDB revision token. <c>System.Text.Json</c> reads and writes it as its string,
/// not as an object.
/// </summary>
[JsonConverter(typeof(ZedTokenJsonConverter))]
public readonly record struct ZedToken
{
    private readonly string? _value;
    public string Value => _value ?? "";
    public ZedToken(string value) => _value = value ?? throw new InvalidArgumentException("Token value cannot be null.");
    public override string ToString() => Value;
}

/// <summary>Serializes a <see cref="ZedToken"/> as its string value.</summary>
internal sealed class ZedTokenJsonConverter : JsonConverter<ZedToken>
{
    public override ZedToken Read(ref Utf8JsonReader reader, Type typeToConvert, JsonSerializerOptions options) =>
        reader.TokenType == JsonTokenType.String
            ? new ZedToken(reader.GetString()!)
            : throw new JsonException("A ZedToken is a JSON string.");

    public override void Write(Utf8JsonWriter writer, ZedToken value, JsonSerializerOptions options) =>
        writer.WriteStringValue(value.Value);
}

/// <summary>Validates an optional caller-supplied revision (watch start, materialize at-revision).</summary>
internal static class RevisionInput
{
    // Null means absent. A present token that is empty or whitespace-only is unusable, so it
    // fails locally instead of being sent (where it would read as "no revision").
    internal static string? Optional(ZedToken? token, string name) => token is not { } t ? null
        : string.IsNullOrWhiteSpace(t.Value) ? throw new InvalidArgumentException($"{name} must be null or a nonempty revision token.")
        : t.Value;
}

/// <summary>
/// A read consistency strategy. <see cref="ToString"/> names it (<c>Full</c>, <c>MinLatency</c>,
/// <c>AtLeast(&lt;token&gt;)</c>, <c>Snapshot(&lt;token&gt;)</c>), and <c>System.Text.Json</c>
/// writes it as <c>{"strategy":"atLeast","token":"&lt;token&gt;"}</c> (no token for
/// <c>full</c> and <c>minLatency</c>) and reads that form back. The <c>AtLeastOr*</c> helpers
/// return the strategy they chose, so it shows as that strategy.
/// </summary>
[JsonConverter(typeof(ConsistencyJsonConverter))]
public sealed record Consistency
{
    private static readonly string[] StrategyNames = ["full", "minLatency", "atLeast", "snapshot"];

    /// <summary><c>Full</c>, <c>MinLatency</c>, <c>AtLeast(&lt;token&gt;)</c> or <c>Snapshot(&lt;token&gt;)</c>.</summary>
    public override string ToString() => _kind switch
    {
        0 => "Full",
        1 => "MinLatency",
        2 => $"AtLeast({_token.Value})",
        _ => $"Snapshot({_token.Value})",
    };

    /// <summary>The JSON <c>strategy</c> name.</summary>
    internal string StrategyName => StrategyNames[_kind];

    /// <summary>The revision of an at-least or snapshot strategy; null otherwise.</summary>
    internal ZedToken? JsonToken => _kind >= 2 ? _token : null;

    internal static Consistency FromJson(string? strategy, ZedToken? token) => (strategy, token) switch
    {
        ("full", null) => Full(),
        ("minLatency", null) => MinLatency(),
        ("atLeast", { } t) => AtLeast(t),
        ("snapshot", { } t) => Snapshot(t),
        _ => throw new JsonException($"A Consistency is {{\"strategy\":\"full\"|\"minLatency\"}} or {{\"strategy\":\"atLeast\"|\"snapshot\",\"token\":\"...\"}}; got strategy {(strategy is null ? "null" : $"\"{strategy}\"")}{(token is null ? " without" : " with")} a token."),
    };

    private readonly int _kind;
    private readonly ZedToken _token;
    private Consistency(int kind, ZedToken token = default) { _kind = kind; _token = token; }
    public static Consistency Full() => new(0);
    public static Consistency MinLatency() => new(1);
    public static Consistency AtLeast(ZedToken token) => TokenStrategy(2, token);
    public static Consistency Snapshot(ZedToken token) => TokenStrategy(3, token);
    // A whitespace-only token is no more usable than an empty one, so both are rejected (and
    // both select the fallback in the AtLeastOr* helpers).
    private static Consistency TokenStrategy(int kind, ZedToken token) => string.IsNullOrWhiteSpace(token.Value)
        ? throw new InvalidArgumentException("A nonempty revision token is required.") : new(kind, token);
    public static Consistency AtLeastOrFull(ZedToken? token) => token is { } t && !string.IsNullOrWhiteSpace(t.Value) ? AtLeast(t) : Full();
    public static Consistency AtLeastOrMinLatency(ZedToken? token) => token is { } t && !string.IsNullOrWhiteSpace(t.Value) ? AtLeast(t) : MinLatency();
    internal Authzed.Api.V1.Consistency ToProto() => _kind switch
    {
        0 => new() { FullyConsistent = true },
        1 => new() { MinimizeLatency = true },
        2 => new() { AtLeastAsFresh = new() { Token = _token.Value } },
        _ => new() { AtExactSnapshot = new() { Token = _token.Value } },
    };
}

/// <summary>Serializes a <see cref="Consistency"/> as <c>{"strategy":...,"token":...}</c>.</summary>
internal sealed class ConsistencyJsonConverter : JsonConverter<Consistency>
{
    public override Consistency Read(ref Utf8JsonReader reader, Type typeToConvert, JsonSerializerOptions options)
    {
        if (reader.TokenType != JsonTokenType.StartObject)
            throw new JsonException("A Consistency is a JSON object.");
        string? strategy = null;
        ZedToken? token = null;
        while (reader.Read() && reader.TokenType != JsonTokenType.EndObject)
        {
            var name = reader.GetString();
            reader.Read();
            switch (name)
            {
                case "strategy" when reader.TokenType == JsonTokenType.String:
                    strategy = reader.GetString();
                    break;
                case "token" when reader.TokenType == JsonTokenType.String:
                    token = new ZedToken(reader.GetString()!);
                    break;
                case "token" when reader.TokenType == JsonTokenType.Null:
                    break;
                default:
                    throw new JsonException($"Unexpected Consistency property \"{name}\".");
            }
        }
        try
        {
            return Consistency.FromJson(strategy, token);
        }
        catch (InvalidArgumentException e)
        {
            throw new JsonException(e.Message, e);
        }
    }

    public override void Write(Utf8JsonWriter writer, Consistency value, JsonSerializerOptions options)
    {
        writer.WriteStartObject();
        writer.WriteString("strategy", value.StrategyName);
        if (value.JsonToken is { } token)
            writer.WriteString("token", token.Value);
        writer.WriteEndObject();
    }
}
