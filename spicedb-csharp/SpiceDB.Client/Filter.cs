namespace SpiceDB.Client;

public sealed record SubjectRelationFilter
{
    internal string? Relation { get; }
    private SubjectRelationFilter(string? relation) => Relation = relation;
    public static SubjectRelationFilter Any { get; } = new((string?)null);
    public static SubjectRelationFilter Direct { get; } = new("");
    public static SubjectRelationFilter Named(string relation) => relation == "..." ? throw new InvalidArgumentException("Use Direct for a direct relation.") : new(Native.Required(relation, nameof(relation)));
}
public sealed record Filter
{
    public string ResourceType { get; }
    public string? ResourceId { get; private init; }
    public string? ResourceIdPrefix { get; private init; }
    public string? Relation { get; private init; }
    public string? SubjectType { get; private init; }
    public string? SubjectId { get; private init; }
    public SubjectRelationFilter SubjectRelation { get; private init; } = SubjectRelationFilter.Any;
    public Filter(string resourceType) => ResourceType = Native.Required(resourceType, nameof(resourceType));
    public Filter WithResourceId(string id) => ResourceIdPrefix is not null ? throw new InvalidArgumentException("ResourceId and ResourceIdPrefix are exclusive.") : this with { ResourceId = Native.Required(id, nameof(id)) };
    public Filter WithResourceIdPrefix(string prefix) => ResourceId is not null ? throw new InvalidArgumentException("ResourceId and ResourceIdPrefix are exclusive.") : this with { ResourceIdPrefix = Native.Required(prefix, nameof(prefix)) };
    public Filter WithRelation(string relation) => this with { Relation = Native.Required(relation, nameof(relation)) };
    public Filter WithSubjectType(string type) => this with { SubjectType = Native.Required(type, nameof(type)) };
    public Filter WithSubjectId(string id) => this with { SubjectId = Native.Required(id, nameof(id)) };
    public Filter WithSubjectRelation(SubjectRelationFilter relation) => this with { SubjectRelation = relation ?? throw new InvalidArgumentException("SubjectRelation is required.") };
    internal Authzed.Api.V1.RelationshipFilter ToProto()
    {
        if (SubjectType is null && (SubjectId is not null || SubjectRelation != SubjectRelationFilter.Any))
            throw new InvalidArgumentException("SubjectId/SubjectRelation require SubjectType.");
        return new()
        {
            ResourceType = ResourceType,
            OptionalResourceId = ResourceId ?? "",
            OptionalResourceIdPrefix = ResourceIdPrefix ?? "",
            OptionalRelation = Relation ?? "",
            OptionalSubjectFilter = SubjectType is null ? null : new()
            {
                SubjectType = SubjectType,
                OptionalSubjectId = SubjectId ?? "",
                OptionalRelation = SubjectRelation.Relation is null ? null : new() { Relation = SubjectRelation.Relation },
            },
        };
    }
}
