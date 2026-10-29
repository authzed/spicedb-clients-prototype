using Authzed.Api.SpiceDB.Proto;

namespace SpiceDB.Client;

/// <summary>
/// The full public surface of <see cref="SpiceDBClient"/>: the relationship and schema surfaces
/// plus lookups, watch, schema introspection, expansion, the experimental operations,
/// <see cref="RawProto"/> and disposal. <see cref="SpiceDBClient"/> and the in-memory test double
/// <see cref="Testing.InMemorySpiceDBClient"/> both implement it, with signatures identical to the
/// client's methods; a test keeps it in sync with the client. Depend on it (or on
/// <see cref="ISpiceDBRelationshipClient"/> / <see cref="ISpiceDBSchemaClient"/>) where a test
/// substitutes the double for a real server.
/// </summary>
public interface ISpiceDBClient : ISpiceDBRelationshipClient, ISpiceDBSchemaClient, IAsyncDisposable
{
    /// <summary>The authenticated protocol-level client. A client without a raw transport throws <see cref="InvalidOperationException"/>.</summary>
    SpiceDBProtoClient RawProto();

    /// <summary>Startup readiness check: fails when the endpoint is not ready within the timeout. The in-memory double completes immediately.</summary>
    Task ConnectAsync(ConnectOptions? options = null, CancellationToken cancellationToken = default);

    Task<bool> CheckAnyAsync(IReadOnlyList<PermissionCheck> requests, CheckOptions? options = null, CancellationToken cancellationToken = default);
    Task<bool> CheckAllAsync(IReadOnlyList<PermissionCheck> requests, CheckOptions? options = null, CancellationToken cancellationToken = default);
    IAsyncEnumerable<LookupResource> LookupResourcesAsync(Consistency consistency, string resourceType, string permission, SubjectRef subject, LookupOptions? options = null, CancellationToken cancellationToken = default);
    IAsyncEnumerable<LookupSubject> LookupSubjectsAsync(Consistency consistency, ObjectRef resource, string permission, string subjectType, LookupOptions? options = null, CancellationToken cancellationToken = default);
    IAsyncEnumerable<WatchEvent> UpdatesAsync(IReadOnlyList<string> objectTypes, ZedToken? startRevision, WatchOptions? options = null, CancellationToken cancellationToken = default);
    Task<ComputablePermissionsResult> ComputablePermissionsAsync(Consistency consistency, string definitionName, string relationName, CallOptions? options = null, CancellationToken cancellationToken = default);
    Task<DependentRelationsResult> DependentRelationsAsync(Consistency consistency, string definitionName, string permissionName, CallOptions? options = null, CancellationToken cancellationToken = default);
    Task<DiffSchemaResult> DiffSchemaAsync(Consistency consistency, string comparisonSchema, CallOptions? options = null, CancellationToken cancellationToken = default);
    Task<ExpandResult> ExpandPermissionTreeAsync(Consistency consistency, ObjectRef resource, string permission, CallOptions? options = null, CancellationToken cancellationToken = default);
    Task ExperimentalRegisterRelationshipCounterAsync(string name, Filter filter, CallOptions? options = null, CancellationToken cancellationToken = default);
    Task<CountResult> ExperimentalCountRelationshipsAsync(string name, CallOptions? options = null, CancellationToken cancellationToken = default);
    Task ExperimentalUnregisterRelationshipCounterAsync(string name, CallOptions? options = null, CancellationToken cancellationToken = default);
    Task<RoaringLookupResourcesResult> ExperimentalRoaringLookupResourcesAsync(Consistency consistency, string resourceType, string permission, SubjectRef subject, LookupOptions? options = null, CancellationToken cancellationToken = default);
    Task<CountResult> ExperimentalCountRelationshipsByFilterAsync(Filter filter, CallOptions? options = null, CancellationToken cancellationToken = default);
    IAsyncEnumerable<PermissionSetEvent> ExperimentalWatchPermissionSetsAsync(ZedToken? startRevision, CallOptions? options = null, CancellationToken cancellationToken = default);
    IAsyncEnumerable<LookupPermissionSet> ExperimentalLookupPermissionSetsAsync(int limit, ZedToken? atRevision, PermissionSetLookupOptions? options = null, CancellationToken cancellationToken = default);
    Task<DownloadPermissionSetsResult> ExperimentalDownloadPermissionSetsAsync(ZedToken? atRevision, CallOptions? options = null, CancellationToken cancellationToken = default);
}
