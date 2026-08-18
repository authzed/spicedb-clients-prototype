// LookupResult.cs — native result types for LookupResourcesAsync and
// LookupSubjectsAsync. These replace bare string yields with records that
// carry permissionship, partial-caveat info, and (critically) excluded
// subjects for wildcard "*" matches, mirroring spicedb-go's
// client/lookup_types.go.

namespace SpiceDB.Client;

/// <summary>
/// Indicates whether a lookup result reflects a full grant or is conditional
/// on caveat context that was not fully evaluated by the server. Callers
/// MUST check this before treating a result as a full grant — a
/// <see cref="ConditionalPermission"/> result may resolve to false once the
/// missing caveat context is supplied.
/// </summary>
public enum Permissionship
{
    Unspecified,
    HasPermission,
    ConditionalPermission,
}

/// <summary>
/// Lists caveat context that was missing to fully evaluate a conditional
/// result.
/// </summary>
public sealed record PartialCaveatInfo
{
    public IReadOnlyList<string> MissingRequiredContext { get; init; } = [];
}

/// <summary>One result from <see cref="SpiceDBClient.LookupResourcesAsync"/>.</summary>
public sealed record LookupResource
{
    public string ResourceID { get; init; } = "";
    public Permissionship Permissionship { get; init; }

    /// <summary>
    /// Non-null when <see cref="Permissionship"/> is
    /// <see cref="Client.Permissionship.ConditionalPermission"/>.
    /// </summary>
    public PartialCaveatInfo? PartialCaveat { get; init; }
}

/// <summary>
/// A subject resolved by <see cref="SpiceDBClient.LookupSubjectsAsync"/> —
/// either the matched subject, or (when found in
/// <see cref="LookupSubject.ExcludedSubjects"/>) a subject excluded from a
/// wildcard match.
/// </summary>
public sealed record ResolvedSubject
{
    public string SubjectID { get; init; } = "";
    public Permissionship Permissionship { get; init; }
    public PartialCaveatInfo? PartialCaveat { get; init; }
}

/// <summary>
/// One result from <see cref="SpiceDBClient.LookupSubjectsAsync"/>. When
/// <see cref="Subject"/>'s <see cref="ResolvedSubject.SubjectID"/> is the
/// wildcard "*", <see cref="ExcludedSubjects"/> lists subjects excluded from
/// that wildcard grant — callers MUST check <see cref="ExcludedSubjects"/>
/// before treating a wildcard match as a blanket grant, or they risk
/// granting access to subjects the server explicitly excluded.
/// </summary>
public sealed record LookupSubject
{
    public ResolvedSubject Subject { get; init; } = new();
    public IReadOnlyList<ResolvedSubject> ExcludedSubjects { get; init; } = [];
}
