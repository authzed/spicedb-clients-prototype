using System.Runtime.CompilerServices;
using M = Authzed.Api.Materialize.V0;
namespace SpiceDB.Client;

public enum PermissionSetOperation { Unspecified, Added, Removed }
public sealed record PermissionSetReference(ObjectRef Object, string PermissionOrRelation);
public sealed record PermissionSetMember(ObjectRef Object, string? PermissionOrRelation);
public abstract record PermissionSetChild
{
    private PermissionSetChild() { }
    public sealed record Set(PermissionSetReference Reference) : PermissionSetChild;
    public sealed record Member(PermissionSetMember Reference) : PermissionSetChild;
    public sealed record Unspecified : PermissionSetChild;
}
public sealed record PermissionSetChange(ZedToken AtRevision, PermissionSetOperation Operation, PermissionSetReference? ParentSet, PermissionSetChild Child);
public sealed record WatchedPermission(string ResourceType, string Permission, string SubjectType, string? SubjectRelation);
public abstract record PermissionSetEvent
{
    private PermissionSetEvent() { }
    public sealed record Change(PermissionSetChange Value) : PermissionSetEvent;
    public sealed record Completed(ZedToken Revision) : PermissionSetEvent;
    public sealed record LookupRequired(ZedToken Revision) : PermissionSetEvent;
    public sealed record SchemaChanged : PermissionSetEvent
    {
        public ZedToken Revision { get; }
        public IReadOnlyList<WatchedPermission> AffectedPermissions { get; }
        public SchemaChanged(ZedToken revision, IEnumerable<WatchedPermission> permissions) { Revision = revision; AffectedPermissions = Native.List(permissions); }
    }
    public sealed record Unspecified : PermissionSetEvent;
}
public sealed record PermissionSetCursor(uint Limit, ZedToken Token, uint StartingIndex, bool CompletedMembers, string StartingKey)
{
    internal M.Cursor ToProto() => new() { Limit = Limit, Token = new() { Token = Token.Value }, StartingIndex = StartingIndex, CompletedMembers = CompletedMembers, StartingKey = StartingKey };
    internal static PermissionSetCursor? FromProto(M.Cursor? c) => c is null ? null : new(c.Limit, new(c.Token?.Token ?? ""), c.StartingIndex, c.CompletedMembers, c.StartingKey);
}
public sealed record LookupPermissionSet(PermissionSetChange? Change, PermissionSetCursor? Cursor);
public sealed record PermissionSetFile(string Name, string Url);
public sealed record DownloadPermissionSetsResult
{
    public IReadOnlyList<PermissionSetFile> Files { get; }
    public DateTimeOffset? Timestamp { get; }
    public ZedToken AtRevision { get; }
    public DownloadPermissionSetsResult(IEnumerable<PermissionSetFile> files, DateTimeOffset? timestamp, ZedToken revision) { Files = Native.List(files); Timestamp = timestamp; AtRevision = revision; }
}
public sealed class PermissionSetLookupOptions
{
    public TimeSpan? Timeout { get; init; }
    public TransportOptions? Transport { get; init; }
    /// <summary>Overrides the client's retry policy for this call only, including reopening the stream before its first item. <see cref="Client.RetryPolicy.None"/> exempts it.</summary>
    public RetryPolicy? RetryPolicy { get; init; }
    public PermissionSetCursor? ResumeCursor { get; init; }
}
public sealed partial class SpiceDBClient
{
    /// <summary>Experimental materialize count by relationship filter.</summary>
    public async Task<CountResult> ExperimentalCountRelationshipsByFilterAsync(Filter filter, CallOptions? options = null, CancellationToken cancellationToken = default)
    {
        using var budget = Begin(options?.Timeout, cancellationToken, transport: options?.Transport, retry: options?.RetryPolicy);
        options?.Validate();
        var response = await Unary(budget, o => RawProto().MaterializeRelationships.ExperimentalCountRelationshipsByFilterAsync(new() { RelationshipFilter = filter.ToProto() }, o.Headers, o.Deadline, o.CancellationToken));
        return new() { RelationshipCount = response.RelationshipCount, Revision = new(response.ReadAt?.Token ?? "") };
    }
    /// <summary>
    /// Experimental materialized permission-set watch. Dispose the enumerator to release the call.
    /// The client-wide retry policy never reopens it; only a per-call <see cref="CallOptions.RetryPolicy"/>
    /// or a <see cref="ClientOptions.OperationRetryPolicies"/> entry for
    /// <see cref="SpiceDBOperation.ExperimentalWatchPermissionSets"/> reopens it, and only before its first event.
    /// </summary>
    public async IAsyncEnumerable<PermissionSetEvent> ExperimentalWatchPermissionSetsAsync(ZedToken? startRevision, CallOptions? options = null, [EnumeratorCancellation] CancellationToken cancellationToken = default)
    {
        using var budget = Begin(options?.Timeout, cancellationToken, false, transport: options?.Transport, retry: options?.RetryPolicy, watch: true);
        options?.Validate();
        var request = new M.WatchPermissionSetsRequest { OptionalStartingAfter = startRevision is { } token ? new() { Token = token.Value } : null };
        await foreach (var response in Stream(budget, o => RawProto().MaterializePermissionSets.WatchPermissionSets(request, o.Headers, o.Deadline, o.CancellationToken)))
            yield return response.ResponseCase switch
            {
                M.WatchPermissionSetsResponse.ResponseOneofCase.Change => new PermissionSetEvent.Change(MapSetChange(response.Change)),
                M.WatchPermissionSetsResponse.ResponseOneofCase.CompletedRevision => new PermissionSetEvent.Completed(new(response.CompletedRevision.Token)),
                M.WatchPermissionSetsResponse.ResponseOneofCase.LookupPermissionSetsRequired => new PermissionSetEvent.LookupRequired(new(response.LookupPermissionSetsRequired.RequiredLookupAt?.Token ?? "")),
                M.WatchPermissionSetsResponse.ResponseOneofCase.BreakingSchemaChange => new PermissionSetEvent.SchemaChanged(new(response.BreakingSchemaChange.ChangeAt?.Token ?? ""), response.BreakingSchemaChange.AffectedPermissions.Select(x => new WatchedPermission(x.ResourceType, x.Permission, x.SubjectType, x.OptionalSubjectRelation == "" ? null : x.OptionalSubjectRelation))),
                _ => new PermissionSetEvent.Unspecified(),
            };
    }
    /// <summary>Experimental explicit single-page lookup. Retain the last cursor to request the next page.</summary>
    public async IAsyncEnumerable<LookupPermissionSet> ExperimentalLookupPermissionSetsAsync(int limit, ZedToken? atRevision, PermissionSetLookupOptions? options = null, [EnumeratorCancellation] CancellationToken cancellationToken = default)
    {
        using var budget = Begin(options?.Timeout, cancellationToken, false, transport: options?.Transport, retry: options?.RetryPolicy);
        var request = new M.LookupPermissionSetsRequest { Limit = (uint)Positive(limit, nameof(limit)), OptionalAtRevision = atRevision is { } token ? new() { Token = token.Value } : null, OptionalStartingAfterCursor = options?.ResumeCursor?.ToProto() };
        await foreach (var response in Stream(budget, o => RawProto().MaterializePermissionSets.LookupPermissionSets(request, o.Headers, o.Deadline, o.CancellationToken)))
            yield return new(response.Change is null ? null : MapSetChange(response.Change), PermissionSetCursor.FromProto(response.Cursor));
    }
    /// <summary>Experimental download metadata; returned URLs are not fetched automatically.</summary>
    public async Task<DownloadPermissionSetsResult> ExperimentalDownloadPermissionSetsAsync(ZedToken? atRevision, CallOptions? options = null, CancellationToken cancellationToken = default)
    {
        using var budget = Begin(options?.Timeout, cancellationToken, transport: options?.Transport, retry: options?.RetryPolicy);
        options?.Validate();
        var request = new M.DownloadPermissionSetsRequest { OptionalAtRevision = atRevision is { } token ? new() { Token = token.Value } : null };
        var response = await Unary(budget, o => RawProto().MaterializePermissionSets.DownloadPermissionSetsAsync(request, o.Headers, o.Deadline, o.CancellationToken));
        return new(response.Files.Select(x => new PermissionSetFile(x.Name, x.Url)), response.Timestamp?.ToDateTimeOffset(), new(response.AtRevision?.Token ?? ""));
    }
    internal static PermissionSetChange MapSetChange(M.PermissionSetChange value) => new(new(value.AtRevision?.Token ?? ""),
        value.Operation switch { M.PermissionSetChange.Types.SetOperation.Added => PermissionSetOperation.Added, M.PermissionSetChange.Types.SetOperation.Removed => PermissionSetOperation.Removed, _ => PermissionSetOperation.Unspecified },
        value.ParentSet is null ? null : MapSet(value.ParentSet), value.ChildCase switch
        {
            M.PermissionSetChange.ChildOneofCase.ChildSet => new PermissionSetChild.Set(MapSet(value.ChildSet)),
            M.PermissionSetChange.ChildOneofCase.ChildMember => new PermissionSetChild.Member(new(new(value.ChildMember.ObjectType, value.ChildMember.ObjectId), value.ChildMember.OptionalPermissionOrRelation == "" ? null : value.ChildMember.OptionalPermissionOrRelation)),
            _ => new PermissionSetChild.Unspecified(),
        });
    private static PermissionSetReference MapSet(M.SetReference value) => new(new(value.ObjectType, value.ObjectId), value.PermissionOrRelation);
}
