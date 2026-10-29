using System.Globalization;
using System.Text;
using System.Text.Json;
using System.Text.Json.Serialization;
using Google.Protobuf.WellKnownTypes;
namespace SpiceDB.Client;

public sealed record Relationship
{
    public ObjectRef Resource { get; private init; }
    public string Relation { get; private init; }
    public SubjectRef Subject { get; private init; }
    public string? CaveatName { get; private init; }
    public IReadOnlyDictionary<string, object?>? CaveatContext { get; private init; }
    public DateTimeOffset? Expiration { get; private init; }
    public Relationship(ObjectRef resource, string relation, SubjectRef subject)
    {
        Resource = resource ?? throw new InvalidArgumentException("Resource is required.");
        Relation = Native.Required(relation, nameof(relation));
        Subject = subject ?? throw new InvalidArgumentException("Subject is required.");
    }
    public Relationship WithCaveat(string name, IReadOnlyDictionary<string, object?>? context = null) => this with { CaveatName = Native.Required(name, nameof(name)), CaveatContext = Native.Context(context) };
    public Relationship WithExpiration(DateTimeOffset expiration) => this with { Expiration = expiration };

    /// <summary>Shorthand for <c>Resource.ObjectType</c>.</summary>
    [JsonIgnore] public string ResourceType => Resource.ObjectType;
    /// <summary>Shorthand for <c>Resource.ObjectId</c>.</summary>
    [JsonIgnore] public string ResourceId => Resource.ObjectId;
    /// <summary>Shorthand for <c>Subject.Object.ObjectType</c>.</summary>
    [JsonIgnore] public string SubjectType => Subject.Object.ObjectType;
    /// <summary>Shorthand for <c>Subject.Object.ObjectId</c>.</summary>
    [JsonIgnore] public string SubjectId => Subject.Object.ObjectId;
    /// <summary>Shorthand for <c>Subject.Relation</c>; null for a direct subject.</summary>
    [JsonIgnore] public string? SubjectRelation => Subject.Relation;

    /// <summary>Returns a copy on a different resource, keeping relation, subject, caveat and expiration.</summary>
    public Relationship WithResource(ObjectRef resource) => this with { Resource = resource ?? throw new InvalidArgumentException("Resource is required.") };
    /// <summary>Returns a copy with a different relation.</summary>
    public Relationship WithRelation(string relation) => this with { Relation = Native.Required(relation, nameof(relation)) };
    /// <summary>Returns a copy with a different subject.</summary>
    public Relationship WithSubject(SubjectRef subject) => this with { Subject = subject ?? throw new InvalidArgumentException("Subject is required.") };

    /// <summary>
    /// Parses SpiceDB's tuple syntax: <c>type:id#relation@type:id[#relation]</c>, optionally
    /// followed by <c>[caveat_name]</c> or <c>[caveat_name:{json context}]</c> and then
    /// <c>[expiration:&lt;RFC 3339&gt;]</c>. A subject relation of <c>...</c> is a direct subject.
    /// Sub-tick (below 100ns) expiration precision is rounded to <see cref="DateTimeOffset"/> ticks.
    /// </summary>
    public static Relationship FromTuple(string tuple)
    {
        if (tuple is null) throw new InvalidArgumentException("Tuple is required.");
        var bracket = tuple.IndexOf('[');
        var main = bracket < 0 ? tuple : tuple[..bracket];
        var suffix = bracket < 0 ? "" : tuple[bracket..];
        var match = System.Text.RegularExpressions.Regex.Match(main, @"^([^:#@]+):([^#@]+)#([^@]+)@([^:#@]+):([^#@]+)(?:#([^#@]+))?$");
        if (!match.Success) throw new InvalidArgumentException("Invalid relationship tuple; expected type:id#relation@type:id[#relation][caveat][expiration].");
        var subjectRelation = match.Groups[6].Success && match.Groups[6].Value != "..." ? match.Groups[6].Value : null;
        var result = new Relationship(new(match.Groups[1].Value, match.Groups[2].Value), match.Groups[3].Value,
            new(new(match.Groups[4].Value, match.Groups[5].Value), subjectRelation));
        const string ExpirationPrefix = "[expiration:";
        if (suffix.Length > 0 && (!suffix.StartsWith(ExpirationPrefix, StringComparison.Ordinal) || suffix.StartsWith(ExpirationPrefix + "{", StringComparison.Ordinal)))
        {
            var (name, context, rest) = ParseCaveat(suffix);
            result = result.WithCaveat(name, context);
            suffix = rest;
        }
        if (suffix.Length == 0) return result;
        if (!suffix.StartsWith(ExpirationPrefix, StringComparison.Ordinal) || !suffix.EndsWith(']'))
            throw new InvalidArgumentException($"Invalid tuple suffix \"{suffix}\".");
        var text = suffix[ExpirationPrefix.Length..^1];
        if (!text.Contains('T') || !DateTimeOffset.TryParse(text, CultureInfo.InvariantCulture, DateTimeStyles.AssumeUniversal | DateTimeStyles.AdjustToUniversal, out var expiration))
            throw new InvalidArgumentException($"Invalid tuple expiration \"{text}\".");
        return result.WithExpiration(expiration);
    }

    private static (string Name, IReadOnlyDictionary<string, object?>? Context, string Remainder) ParseCaveat(string suffix)
    {
        var body = suffix[1..];
        var end = body.IndexOfAny([':', ']']);
        if (end <= 0) throw new InvalidArgumentException($"Invalid tuple caveat \"{suffix}\".");
        var name = body[..end];
        if (body[end] == ']') return (name, null, body[(end + 1)..]);
        var bytes = Encoding.UTF8.GetBytes(body[(end + 1)..]);
        var reader = new Utf8JsonReader(bytes);
        JsonElement element;
        try
        {
            if (!JsonDocument.TryParseValue(ref reader, out var document)) throw new InvalidArgumentException($"Invalid tuple caveat \"{suffix}\".");
            using (document) element = document.RootElement.Clone();
        }
        catch (JsonException ex) { throw new InvalidArgumentException($"Invalid tuple caveat context: {ex.Message}", ex); }
        if (element.ValueKind != JsonValueKind.Object) throw new InvalidArgumentException("Tuple caveat context must be a JSON object.");
        var consumed = (int)reader.BytesConsumed;
        var after = Encoding.UTF8.GetString(bytes, consumed, bytes.Length - consumed);
        if (!after.StartsWith(']')) throw new InvalidArgumentException($"Invalid tuple caveat \"{suffix}\".");
        return (name, (IReadOnlyDictionary<string, object?>?)FromJson(element), after[1..]);
    }

    private static object? FromJson(JsonElement e) => e.ValueKind switch
    {
        JsonValueKind.Object => e.EnumerateObject().ToDictionary(p => p.Name, p => FromJson(p.Value)).AsReadOnly(),
        JsonValueKind.Array => e.EnumerateArray().Select(FromJson).ToList().AsReadOnly(),
        JsonValueKind.String => e.GetString(),
        JsonValueKind.Number => e.GetDouble(),
        JsonValueKind.True => true,
        JsonValueKind.False => false,
        _ => null,
    };

    public Filter ToFilter() => new Filter(Resource.ObjectType).WithResourceId(Resource.ObjectId).WithRelation(Relation)
        .WithSubjectType(Subject.Object.ObjectType).WithSubjectId(Subject.Object.ObjectId)
        .WithSubjectRelation(Subject.Relation is null ? SubjectRelationFilter.Direct : SubjectRelationFilter.Named(Subject.Relation));

    /// <summary>
    /// Formats the relationship in SpiceDB's tuple syntax, including the caveat (context keys
    /// sorted) and expiration (UTC, RFC 3339) when present. <see cref="FromTuple"/> parses it.
    /// </summary>
    public override string ToString()
    {
        var text = new StringBuilder($"{Resource}#{Relation}@{Subject}");
        if (CaveatName is not null)
        {
            text.Append('[').Append(CaveatName);
            if (CaveatContext is { Count: > 0 }) text.Append(':').Append(JsonSerializer.Serialize(Sorted(CaveatContext)));
            text.Append(']');
        }
        if (Expiration is { } time)
            text.Append("[expiration:").Append(time.UtcDateTime.ToString("yyyy-MM-dd'T'HH:mm:ss.FFFFFFF'Z'", CultureInfo.InvariantCulture)).Append(']');
        return text.ToString();
    }

    private static object? Sorted(object? value) => value switch
    {
        IReadOnlyDictionary<string, object?> map => new SortedDictionary<string, object?>(map.ToDictionary(p => p.Key, p => Sorted(p.Value)), StringComparer.Ordinal),
        IReadOnlyList<object?> list => list.Select(Sorted).ToList(),
        _ => value,
    };

    internal Authzed.Api.V1.Relationship ToProto() => new()
    {
        Resource = Resource.ToProto(),
        Relation = Relation,
        Subject = Subject.ToProto(),
        OptionalCaveat = CaveatName is null ? null : new() { CaveatName = CaveatName, Context = Native.Struct(CaveatContext) },
        OptionalExpiresAt = Expiration is { } time ? Timestamp.FromDateTimeOffset(time) : null,
    };
    internal static Relationship FromProto(Authzed.Api.V1.Relationship value)
    {
        var result = new Relationship(ObjectRef.FromProto(value.Resource), value.Relation, SubjectRef.FromProto(value.Subject));
        if (value.OptionalCaveat is { } caveat) result = result.WithCaveat(caveat.CaveatName, Native.FromStruct(caveat.Context));
        if (value.OptionalExpiresAt is { } time) result = result.WithExpiration(time.ToDateTimeOffset());
        return result;
    }
}
[JsonConverter(typeof(JsonStringEnumConverter<UpdateOperation>))]
public enum UpdateOperation { Unspecified, Create, Touch, Delete }
public sealed record RelationshipUpdate(UpdateOperation Operation, Relationship Relationship)
{
    /// <summary>Formats the update as <c>OPERATION(relationship)</c>, e.g. <c>TOUCH(document:1#viewer@user:a)</c>.</summary>
    public override string ToString() => $"{Operation.ToString().ToUpperInvariant()}({Relationship})";
}
public sealed record WatchEvent
{
    public IReadOnlyList<RelationshipUpdate> Updates { get; }
    public ZedToken ChangesThrough { get; }
    public bool IsCheckpoint { get; }
    public bool SchemaUpdated { get; }
    public IReadOnlyDictionary<string, object?>? TransactionMetadata { get; }
    public IReadOnlyList<IReadOnlyDictionary<string, object?>> FullRevisionMetadata { get; }
    public WatchEvent(IEnumerable<RelationshipUpdate> updates, ZedToken changesThrough, bool isCheckpoint, bool schemaUpdated = false, IReadOnlyDictionary<string, object?>? transactionMetadata = null, IEnumerable<IReadOnlyDictionary<string, object?>>? fullRevisionMetadata = null)
    {
        Updates = Native.List(updates); ChangesThrough = changesThrough; IsCheckpoint = isCheckpoint; SchemaUpdated = schemaUpdated;
        TransactionMetadata = Native.Context(transactionMetadata); FullRevisionMetadata = Native.List((fullRevisionMetadata ?? []).Select(x => Native.Context(x)!));
    }
}
