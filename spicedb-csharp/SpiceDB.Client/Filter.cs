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
    /// <exception cref="InvalidArgumentException">
    /// Thrown when <see cref="SubjectID"/> or <see cref="SubjectRelation"/> is set without
    /// <see cref="SubjectType"/>. The wire's <c>SubjectFilter.subject_type</c> is a required
    /// field, so there is no way to express a subject ID/relation constraint without it — and
    /// silently dropping the constraint (rather than raising) would widen the filter to match
    /// every subject instead of the one the caller asked for.
    /// </exception>
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
        else if (!string.IsNullOrEmpty(SubjectID) || !string.IsNullOrEmpty(SubjectRelation))
        {
            // The proto cannot express a subject ID/relation constraint without a subject
            // type (SubjectFilter.subject_type is required), which makes silently widening
            // the filter to "no subject constraint at all" the one unsafe resolution: a
            // caller who wrote `new Filter("document").WithSubjectID("alice")`, expecting to
            // narrow to alice's relationships, would instead match every subject on every
            // document -- e.g. DeleteRelationshipsAsync would delete every relationship on
            // every document, not just alice's. Root DESIGN.md, "RULE: A conversion that
            // cannot preserve meaning must fail", clause 1: caller-supplied data the client
            // cannot represent MUST raise a typed error naming what could not be converted.
            var missing = string.IsNullOrEmpty(SubjectID) ? nameof(SubjectRelation) : nameof(SubjectID);
            throw new InvalidArgumentException(
                $"Filter has {missing} set without SubjectType. The wire format requires " +
                "SubjectType whenever a subject constraint is present -- call WithSubjectType(...) " +
                $"before With{missing}(...).");
        }

        return filter;
    }
}
