using System.Text.Json.Serialization;

namespace SpiceDB.Client;

/// <summary>Which subject relations a <see cref="SubjectRelationFilter"/> admits.</summary>
[JsonConverter(typeof(JsonStringEnumConverter<SubjectRelationKind>))]
public enum SubjectRelationKind { Any, Direct, Named }

public sealed record SubjectRelationFilter
{
    internal string? Relation { get; }
    private SubjectRelationFilter(string? relation) => Relation = relation;
    public static SubjectRelationFilter Any { get; } = new((string?)null);
    public static SubjectRelationFilter Direct { get; } = new("");
    /// <summary>Subjects with relation <paramref name="relation"/>; <c>...</c> is the direct relation and gives <see cref="Direct"/>.</summary>
    public static SubjectRelationFilter Named(string relation) => relation == SubjectRef.DirectRelation ? Direct : new(Native.Required(relation, nameof(relation)));

    /// <summary>Any (no constraint), Direct (no subject relation) or Named (<see cref="Name"/>).</summary>
    public SubjectRelationKind Kind => Relation switch { null => SubjectRelationKind.Any, "" => SubjectRelationKind.Direct, _ => SubjectRelationKind.Named };

    /// <summary>The relation name for <see cref="SubjectRelationKind.Named"/>; otherwise null.</summary>
    public string? Name => Kind == SubjectRelationKind.Named ? Relation : null;

    /// <summary>Whether a subject with relation <paramref name="subjectRelation"/> (null for direct) is admitted.</summary>
    public bool Matches(string? subjectRelation) => Kind switch
    {
        SubjectRelationKind.Direct => subjectRelation is null,
        SubjectRelationKind.Named => subjectRelation == Relation,
        _ => true,
    };

    /// <summary>Formats as <c>*</c> (any), <c>...</c> (direct) or the relation name.</summary>
    public override string ToString() => Kind switch { SubjectRelationKind.Direct => "...", SubjectRelationKind.Named => Relation!, _ => "*" };
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

    /// <summary>
    /// Whether <paramref name="relationship"/> is selected by this filter, with SpiceDB's
    /// semantics: resource type, resource ID or ID prefix, relation, subject type, subject ID,
    /// and the Any/Direct/Named subject relation distinction. Unset fields match anything;
    /// caveats and expiration never affect matching. Use it in test doubles instead of
    /// reimplementing filter semantics.
    /// </summary>
    public bool Matches(Relationship relationship)
    {
        Native.Required(relationship, nameof(relationship));
        return ResourceType == relationship.ResourceType
            && (ResourceId is null || ResourceId == relationship.ResourceId)
            && (ResourceIdPrefix is null || relationship.ResourceId.StartsWith(ResourceIdPrefix, StringComparison.Ordinal))
            && (Relation is null || Relation == relationship.Relation)
            && (SubjectType is null || SubjectType == relationship.SubjectType)
            && (SubjectId is null || SubjectId == relationship.SubjectId)
            && SubjectRelation.Matches(relationship.SubjectRelation);
    }

    /// <summary>
    /// Formats the filter in a zed-like form with <c>*</c> for unset fields, e.g.
    /// <c>document:*#viewer@user:alice</c> or <c>document:doc-%*#*@*</c>.
    /// </summary>
    public override string ToString()
    {
        var id = ResourceIdPrefix is not null ? ResourceIdPrefix + "%*" : ResourceId ?? "*";
        var text = $"{ResourceType}:{id}#{Relation ?? "*"}@{SubjectType ?? "*"}";
        if (SubjectType is not null)
        {
            text += ":" + (SubjectId ?? "*");
            if (SubjectRelation.Kind != SubjectRelationKind.Any) text += "#" + SubjectRelation;
        }
        return text;
    }

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
