namespace SpiceDB.Client;

/// <summary>
/// The relationship and check surface shared by <see cref="SpiceDBClient"/> and the in-memory
/// test double <see cref="Testing.InMemorySpiceDBClient"/>. Depend on this interface where a
/// test should substitute the double for a real server. Signatures match the real client's
/// methods exactly.
/// </summary>
public interface ISpiceDBRelationshipClient
{
    Task<CheckResult> CheckAsync(PermissionCheck request, CheckOptions? options = null, CancellationToken cancellationToken = default);
    Task<IReadOnlyList<CheckResult>> CheckBulkAsync(IReadOnlyList<PermissionCheck> requests, CheckOptions? options = null, CancellationToken cancellationToken = default);
    Task<WriteResult> WriteAsync(Transaction transaction, WriteOptions? options = null, CancellationToken cancellationToken = default);
    Task<DeleteResult> DeleteRelationshipsAsync(Filter filter, int? limit, DeleteOptions? options = null, CancellationToken cancellationToken = default);
    Task<DeleteResult> DeleteAllRelationshipsNonAtomicAsync(Filter filter, DeleteAllOptions? options = null, CancellationToken cancellationToken = default);
    IAsyncEnumerable<ReadRelationship> ReadRelationshipsAsync(Consistency consistency, Filter filter, ReadOptions? options = null, CancellationToken cancellationToken = default);
}
