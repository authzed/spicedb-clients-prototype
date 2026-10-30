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

public sealed record Consistency
{
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
