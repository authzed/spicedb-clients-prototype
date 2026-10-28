// Transaction builder for batching relationship writes.

using System.Text.Json.Serialization;
using V1 = Authzed.Api.V1;

namespace SpiceDB.Client;

/// <summary>Whether a <see cref="Precondition"/> requires its filter to match or not match.</summary>
[JsonConverter(typeof(JsonStringEnumConverter<PreconditionOperation>))]
public enum PreconditionOperation { MustMatch, MustNotMatch }

/// <summary>A write or delete precondition: a filter that must (or must not) match.</summary>
public sealed record Precondition
{
    public PreconditionOperation Operation { get; }
    public Filter Filter { get; }
    public Precondition(PreconditionOperation operation, Filter filter)
    {
        Operation = BehaviorOptions.Defined(operation, nameof(operation));
        Filter = Native.Required(filter, nameof(filter));
    }
    /// <summary>Formats as <c>MUST_MATCH(filter)</c> or <c>MUST_NOT_MATCH(filter)</c>.</summary>
    public override string ToString() => (Operation == PreconditionOperation.MustMatch ? "MUST_MATCH" : "MUST_NOT_MATCH") + "(" + Filter + ")";
    internal V1.Precondition ToProto() => new()
    {
        Operation = Operation == PreconditionOperation.MustMatch ? V1.Precondition.Types.Operation.MustMatch : V1.Precondition.Types.Operation.MustNotMatch,
        Filter = Filter.ToProto(),
    };
}

/// <summary>
/// A mutable transaction builder for batching relationship mutations. Create, Touch,
/// and Delete add operations; MustNotMatch and MustMatch add preconditions.
/// Pass the completed transaction to <see cref="SpiceDBClient.WriteAsync"/>, which
/// snapshots it. <see cref="Updates"/>, <see cref="Preconditions"/> and
/// <see cref="ToString"/> expose its contents for logging and test doubles.
/// </summary>
public sealed class Transaction
{
    private readonly List<RelationshipUpdate> _updates = [];
    private readonly List<Precondition> _preconditions = [];

    /// <summary>The number of updates, without copying them.</summary>
    public int Count => _updates.Count;

    /// <summary>A snapshot of the updates, in order.</summary>
    public IReadOnlyList<RelationshipUpdate> Updates => _updates.ToArray();

    /// <summary>A snapshot of the preconditions, in order.</summary>
    public IReadOnlyList<Precondition> Preconditions => _preconditions.ToArray();

    internal IReadOnlyList<V1.RelationshipUpdate> V1Updates => _updates.Select(ToProto).ToArray();
    internal IReadOnlyList<V1.Precondition> V1Preconditions => _preconditions.Select(x => x.ToProto()).ToArray();

    internal static V1.RelationshipUpdate ToProto(RelationshipUpdate update) => new()
    {
        Operation = update.Operation switch
        {
            UpdateOperation.Create => V1.RelationshipUpdate.Types.Operation.Create,
            UpdateOperation.Touch => V1.RelationshipUpdate.Types.Operation.Touch,
            UpdateOperation.Delete => V1.RelationshipUpdate.Types.Operation.Delete,
            _ => throw new InvalidArgumentException("Relationship update operation is unspecified."),
        },
        Relationship = update.Relationship.ToProto(),
    };

    private void Add(UpdateOperation operation, Relationship relationship)
    {
        Native.Required(relationship, nameof(relationship));
        relationship.ToProto(); // validate now, as before, rather than at write time
        _updates.Add(new(operation, relationship));
    }

    /// <summary>Adds a relationship create. Fails at write time if the relationship already exists.</summary>
    public void Create(Relationship relationship) => Add(UpdateOperation.Create, relationship);

    /// <summary>Adds a relationship touch: creates or updates the relationship.</summary>
    public void Touch(Relationship relationship) => Add(UpdateOperation.Touch, relationship);

    /// <summary>Adds a relationship delete.</summary>
    public void Delete(Relationship relationship) => Add(UpdateOperation.Delete, relationship);

    /// <summary>Adds a precondition that no relationships match the given filter.</summary>
    public void MustNotMatch(Filter filter)
    {
        Native.Required(filter, nameof(filter)).ToProto();
        _preconditions.Add(new(PreconditionOperation.MustNotMatch, filter));
    }

    /// <summary>Adds a precondition that at least one relationship matches the given filter.</summary>
    public void MustMatch(Filter filter)
    {
        Native.Required(filter, nameof(filter)).ToProto();
        _preconditions.Add(new(PreconditionOperation.MustMatch, filter));
    }

    /// <summary>Lists preconditions then updates, e.g. <c>[MUST_MATCH(...), TOUCH(...)]</c>.</summary>
    public override string ToString() => "[" + string.Join(", ", _preconditions.Select(p => p.ToString()).Concat(_updates.Select(u => u.ToString()))) + "]";

    /// <summary>
    /// Builds a precondition proto from an operation and filter. Shared with
    /// <see cref="SpiceDBClient.DeleteRelationshipsAsync"/> so both build preconditions identically.
    /// </summary>
    internal static V1.Precondition BuildPrecondition(V1.Precondition.Types.Operation operation, Filter filter)
    {
        Native.Required(filter, nameof(filter));
        return new V1.Precondition { Operation = operation, Filter = filter.ToProto() };
    }
}
