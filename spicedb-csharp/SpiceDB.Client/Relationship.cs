using Google.Protobuf.WellKnownTypes;
namespace SpiceDB.Client;

public sealed record Relationship
{
    public ObjectRef Resource { get; }
    public string Relation { get; }
    public SubjectRef Subject { get; }
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
    public static Relationship FromTuple(string tuple)
    {
        if (tuple is null) throw new InvalidArgumentException("Tuple is required.");
        var match = System.Text.RegularExpressions.Regex.Match(tuple, @"^([^:#@]+):([^#@]+)#([^@]+)@([^:#@]+):([^#@]+)(?:#([^#@]+))?$");
        if (!match.Success) throw new InvalidArgumentException("Invalid relationship tuple; expected type:id#relation@type:id[#relation].");
        return new(new(match.Groups[1].Value, match.Groups[2].Value), match.Groups[3].Value,
            new(new(match.Groups[4].Value, match.Groups[5].Value), match.Groups[6].Success ? match.Groups[6].Value : null));
    }
    public Filter ToFilter() => new Filter(Resource.ObjectType).WithResourceId(Resource.ObjectId).WithRelation(Relation)
        .WithSubjectType(Subject.Object.ObjectType).WithSubjectId(Subject.Object.ObjectId)
        .WithSubjectRelation(Subject.Relation is null ? SubjectRelationFilter.Direct : SubjectRelationFilter.Named(Subject.Relation));
    public override string ToString() => $"{Resource.ObjectType}:{Resource.ObjectId}#{Relation}@{Subject.Object.ObjectType}:{Subject.Object.ObjectId}" + (Subject.Relation is null ? "" : "#" + Subject.Relation);
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
public enum UpdateOperation { Unspecified, Create, Touch, Delete }
public sealed record RelationshipUpdate(UpdateOperation Operation, Relationship Relationship);
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
