// Transaction builder for batching relationship writes.

using Authzed.Api.V1;

namespace SpiceDB.Client;

/// <summary>
/// A transaction builder for batching relationship mutations. Create, Touch,
/// and Delete add operations; MustNotMatch and MustMatch add preconditions.
/// Pass the completed transaction to <see cref="SpiceDBClient.WriteAsync"/>.
/// </summary>
public sealed class Transaction
{
    private readonly List<Authzed.Api.V1.RelationshipUpdate> _updates = [];
    private readonly List<Precondition> _preconditions = [];

    /// <summary>
    /// Exposes the underlying proto updates for advanced use cases.
    /// </summary>
    public IReadOnlyList<Authzed.Api.V1.RelationshipUpdate> V1Updates => _updates;

    /// <summary>
    /// Returns the preconditions added to this transaction.
    /// </summary>
    public IReadOnlyList<Precondition> Preconditions => _preconditions;

    /// <summary>
    /// Adds a relationship create to the transaction. Fails if the
    /// relationship already exists.
    /// </summary>
    public void Create(Relationship relationship)
    {
        ArgumentNullException.ThrowIfNull(relationship);
        _updates.Add(new Authzed.Api.V1.RelationshipUpdate
        {
            Operation = Authzed.Api.V1.RelationshipUpdate.Types.Operation.Create,
            Relationship = relationship.ToProto(),
        });
    }

    /// <summary>
    /// Adds a relationship touch to the transaction. Creates or updates
    /// the relationship.
    /// </summary>
    public void Touch(Relationship relationship)
    {
        ArgumentNullException.ThrowIfNull(relationship);
        _updates.Add(new Authzed.Api.V1.RelationshipUpdate
        {
            Operation = Authzed.Api.V1.RelationshipUpdate.Types.Operation.Touch,
            Relationship = relationship.ToProto(),
        });
    }

    /// <summary>
    /// Adds a relationship delete to the transaction.
    /// </summary>
    public void Delete(Relationship relationship)
    {
        ArgumentNullException.ThrowIfNull(relationship);
        _updates.Add(new Authzed.Api.V1.RelationshipUpdate
        {
            Operation = Authzed.Api.V1.RelationshipUpdate.Types.Operation.Delete,
            Relationship = relationship.ToProto(),
        });
    }

    /// <summary>
    /// Adds a precondition that no relationships match the given filter.
    /// </summary>
    public void MustNotMatch(Filter filter)
    {
        ArgumentNullException.ThrowIfNull(filter);
        _preconditions.Add(BuildPrecondition(Precondition.Types.Operation.MustNotMatch, filter));
    }

    /// <summary>
    /// Adds a precondition that at least one relationship matches the given filter.
    /// </summary>
    public void MustMatch(Filter filter)
    {
        ArgumentNullException.ThrowIfNull(filter);
        _preconditions.Add(BuildPrecondition(Precondition.Types.Operation.MustMatch, filter));
    }

    /// <summary>
    /// Builds a <see cref="Precondition"/> proto from an operation and filter.
    /// Shared by <see cref="MustMatch"/>/<see cref="MustNotMatch"/> and
    /// <see cref="SpiceDBClient.DeleteRelationshipsAsync"/> so both build
    /// preconditions identically. Internal — exposed to the test assembly via
    /// InternalsVisibleTo; not part of the public API.
    /// </summary>
    internal static Precondition BuildPrecondition(Precondition.Types.Operation operation, Filter filter)
    {
        ArgumentNullException.ThrowIfNull(filter);
        return new Precondition
        {
            Operation = operation,
            Filter = filter.ToProto(),
        };
    }
}
