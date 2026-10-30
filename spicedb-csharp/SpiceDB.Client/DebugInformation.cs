using System.Text.Json.Serialization;
namespace SpiceDB.Client;

[JsonConverter(typeof(JsonStringEnumConverter<TracePermissionType>))]
public enum TracePermissionType { Unspecified, Relation, Permission }
[JsonConverter(typeof(JsonStringEnumConverter<CaveatEvaluationResult>))]
public enum CaveatEvaluationResult { Unspecified, Unevaluated, False, True, MissingSomeContext }
public sealed record DebugInformation(string SchemaUsed, CheckDebugTrace? Check)
{
    internal static DebugInformation? FromProto(Authzed.Api.V1.DebugInformation? value) => value is null ? null : new(value.SchemaUsed, CheckDebugTrace.FromProto(value.Check));
}
public sealed record CaveatEvaluation
{
    public string Expression { get; }
    public CaveatEvaluationResult Result { get; }
    public IReadOnlyDictionary<string, object?>? Context { get; }
    public IReadOnlyList<string> MissingContext { get; }
    public string CaveatName { get; }
    internal CaveatEvaluation(Authzed.Api.V1.CaveatEvalInfo value)
    {
        Expression = value.Expression;
        Result = Enum.IsDefined(typeof(CaveatEvaluationResult), (int)value.Result) ? (CaveatEvaluationResult)value.Result : CaveatEvaluationResult.Unspecified;
        Context = Native.FromStruct(value.Context);
        MissingContext = Native.List(value.PartialCaveatInfo?.MissingRequiredContext ?? Enumerable.Empty<string>());
        CaveatName = value.CaveatName;
    }
}
public sealed record CheckDebugTrace
{
    public ObjectRef? Resource { get; }
    public string Permission { get; }
    public TracePermissionType PermissionType { get; }
    public SubjectRef? Subject { get; }
    public Permissionship Result { get; }
    public CaveatEvaluation? CaveatEvaluation { get; }
    public TimeSpan? Duration { get; }
    public bool? WasCachedResult { get; }
    public IReadOnlyList<CheckDebugTrace>? SubProblems { get; }
    public DateTimeOffset? Expiration { get; }
    public string TraceOperationId { get; }
    public string Source { get; }
    private CheckDebugTrace(Authzed.Api.V1.CheckDebugTrace value)
    {
        Resource = value.Resource is null ? null : ObjectRef.FromProto(value.Resource);
        Permission = value.Permission;
        PermissionType = Enum.IsDefined(typeof(TracePermissionType), (int)value.PermissionType) ? (TracePermissionType)value.PermissionType : TracePermissionType.Unspecified;
        Subject = value.Subject is null ? null : SubjectRef.FromProto(value.Subject);
        Result = SpiceDBClient.ToCheckPermissionship((Authzed.Api.V1.CheckPermissionResponse.Types.Permissionship)(int)value.Result);
        CaveatEvaluation = value.CaveatEvaluationInfo is null ? null : new(value.CaveatEvaluationInfo);
        Duration = value.Duration?.ToTimeSpan();
        WasCachedResult = value.HasWasCachedResult ? value.WasCachedResult : null;
        SubProblems = value.SubProblems is null ? null : Native.List(value.SubProblems.Traces.Select(v => new CheckDebugTrace(v)));
        Expiration = value.OptionalExpiresAt?.ToDateTimeOffset();
        TraceOperationId = value.TraceOperationId;
        Source = value.Source;
    }
    internal static CheckDebugTrace? FromProto(Authzed.Api.V1.CheckDebugTrace? value) => value is null ? null : new(value);
}
