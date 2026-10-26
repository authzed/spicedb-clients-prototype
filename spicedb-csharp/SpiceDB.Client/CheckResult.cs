namespace SpiceDB.Client;

public sealed record CheckResult
{
    public Permissionship Permissionship { get; }
    public IReadOnlyList<string> MissingContext { get; }
    public ZedToken CheckedAt { get; }
    public DebugInformation? DebugTrace { get; }
    public DateTimeOffset? Expiration { get; }
    public bool HasPermission => Permissionship == Permissionship.HasPermission;
    public CheckResult(Permissionship permissionship, IEnumerable<string> missingContext, ZedToken checkedAt, DebugInformation? debugTrace = null, DateTimeOffset? expiration = null)
    { Permissionship = permissionship; MissingContext = Native.List(missingContext); CheckedAt = checkedAt; DebugTrace = debugTrace; Expiration = expiration; }
}
