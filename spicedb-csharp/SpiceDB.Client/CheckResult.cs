namespace SpiceDB.Client;

public sealed record CheckResult
{
    public Permissionship Permissionship { get; }
    public IReadOnlyList<string> MissingContext { get; }
    public ZedToken CheckedAt { get; }
    public DebugInformation? DebugTrace { get; }
    public DateTimeOffset? Expiration { get; }
    /// <summary>
    /// The item's error under <see cref="BulkItemErrors.Returned"/>, with
    /// <see cref="SpiceDBException.InputIndex"/> set. An errored result has
    /// <see cref="Permissionship.Unspecified"/> and never grants.
    /// </summary>
    public SpiceDBException? Error { get; }
    public bool HasPermission => Error is null && Permissionship == Permissionship.HasPermission;
    public CheckResult(Permissionship permissionship, IEnumerable<string> missingContext, ZedToken checkedAt, DebugInformation? debugTrace = null, DateTimeOffset? expiration = null)
    { Permissionship = permissionship; MissingContext = Native.List(missingContext); CheckedAt = checkedAt; DebugTrace = debugTrace; Expiration = expiration; }

    /// <summary>A failed bulk item: unspecified permissionship carrying <paramref name="error"/>.</summary>
    public CheckResult(SpiceDBException error, ZedToken checkedAt) : this(Permissionship.Unspecified, [], checkedAt)
    { Error = Native.Required(error, nameof(error)); }
}
