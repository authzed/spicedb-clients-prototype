// Filter specifies criteria for matching relationships.

using Authzed.Api.V1;

namespace SpiceDB.Client;

/// <summary>
/// Specifies criteria for matching relationships. Immutable — each With*
/// method returns a new Filter with the additional constraint.
/// </summary>
public sealed record Filter
{
    public string ResourceType { get; init; } = "";
    public string? ResourceID { get; init; }
    public string? ResourceIDPrefix { get; init; }
    public string? Relation { get; init; }
    public string? SubjectType { get; init; }
    public string? SubjectID { get; init; }
    public string? SubjectRelation { get; init; }

    /// <summary>
    /// Creates a filter that matches relationships of the given resource type.
    /// </summary>
    /// <exception cref="ArgumentException">Thrown when resourceType is null or empty.</exception>
    public Filter(string resourceType)
    {
        if (string.IsNullOrEmpty(resourceType))
            throw new ArgumentException("Resource type is required.", nameof(resourceType));
        ResourceType = resourceType;
    }

    // Private parameterless constructor for record `with` expressions.
    private Filter() { }

    /// <summary>Narrows the filter to a specific resource ID.</summary>
    public Filter WithResourceID(string id) => this with { ResourceID = id };

    /// <summary>Narrows the filter to resource IDs with the given prefix.</summary>
    public Filter WithResourceIDPrefix(string prefix) => this with { ResourceIDPrefix = prefix };

    /// <summary>Narrows the filter to a specific relation.</summary>
    public Filter WithRelation(string relation) => this with { Relation = relation };

    /// <summary>Narrows the filter to a specific subject type.</summary>
    public Filter WithSubjectType(string subjectType) => this with { SubjectType = subjectType };

    /// <summary>Narrows the filter to a specific subject ID.</summary>
    public Filter WithSubjectID(string subjectID) => this with { SubjectID = subjectID };

    /// <summary>Narrows the filter to a specific subject relation.</summary>
    public Filter WithSubjectRelation(string relation) => this with { SubjectRelation = relation };

    /// <summary>
    /// Converts this filter to its proto representation.
    /// </summary>
    public RelationshipFilter ToProto()
    {
        var filter = new RelationshipFilter
        {
            ResourceType = ResourceType,
        };

        if (!string.IsNullOrEmpty(ResourceID))
            filter.OptionalResourceId = ResourceID;

        if (!string.IsNullOrEmpty(ResourceIDPrefix))
            filter.OptionalResourceIdPrefix = ResourceIDPrefix;

        if (!string.IsNullOrEmpty(Relation))
            filter.OptionalRelation = Relation;

        if (!string.IsNullOrEmpty(SubjectType))
        {
            filter.OptionalSubjectFilter = new SubjectFilter
            {
                SubjectType = SubjectType,
            };

            if (!string.IsNullOrEmpty(SubjectID))
                filter.OptionalSubjectFilter.OptionalSubjectId = SubjectID;

            if (!string.IsNullOrEmpty(SubjectRelation))
            {
                filter.OptionalSubjectFilter.OptionalRelation = new SubjectFilter.Types.RelationFilter
                {
                    Relation = SubjectRelation,
                };
            }
        }

        return filter;
    }
}
