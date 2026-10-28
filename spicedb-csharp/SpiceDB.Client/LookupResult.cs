// LookupResult.cs — native result types for LookupResourcesAsync and
// LookupSubjectsAsync. These replace bare string yields with records that
// carry permissionship, partial-caveat info, and (critically) excluded
// subjects for wildcard "*" matches, mirroring spicedb-go's
// client/lookup_types.go.

namespace SpiceDB.Client;

/// <summary>
/// Indicates whether a check or lookup result reflects a full grant, a full
/// denial, or is conditional on caveat context that was not fully evaluated
/// by the server. Callers MUST check this before treating a result as a full
/// grant — a <see cref="ConditionalPermission"/> result may resolve to false
/// once the missing caveat context is supplied.
/// <para>
/// This type serves both the check surface (<see cref="CheckResult"/>) and
/// the lookup surface (<see cref="LookupResource"/>, <see cref="ResolvedSubject"/>).
/// Lookups never yield <see cref="NoPermission"/>: a subject/resource pair
/// that lacks the permission is simply absent from a lookup stream rather
/// than being yielded with that permissionship. <see cref="NoPermission"/>
/// only appears on <see cref="CheckResult"/>, where the server is answering a
/// question about one specific pair and "no" is itself an answer.
/// </para>
/// </summary>
public enum Permissionship
{
    Unspecified,
    HasPermission,
    ConditionalPermission,

    /// <summary>
    /// Added after <see cref="HasPermission"/> and
    /// <see cref="ConditionalPermission"/>, and deliberately appended here —
    /// not inserted next to <see cref="Unspecified"/> — so the underlying
    /// int values of the two pre-existing members are not renumbered.
    /// </summary>
    NoPermission,
}

/// <summary>
/// Lists caveat context that was missing to fully evaluate a conditional
/// result.
/// </summary>
public sealed record PartialCaveatInfo
{
    private IReadOnlyList<string> _MissingRequiredContext = Array.Empty<string>();
    public IReadOnlyList<string> MissingRequiredContext { get => _MissingRequiredContext; init => _MissingRequiredContext = Native.List(value); }
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

    /// <summary>
    /// The revision this result was computed at. Identical for every item
    /// yielded by a single <see cref="SpiceDBClient.LookupResourcesAsync"/>
    /// call — a property of the call, not of the individual resource.
    /// </summary>
    public ZedToken LookedUpAt { get; init; }

    /// <summary>
    /// Resumes the lookup after this item through <see cref="LookupOptions.ResumeCursor"/>,
    /// repeating the request and page size. Distinct from revision tokens.
    /// </summary>
    public string? AfterResultCursor { get; init; }
}

/// <summary>A relationship yielded by <see cref="SpiceDBClient.ReadRelationshipsAsync"/>, with its paging state.</summary>
/// <param name="Relationship">The relationship.</param>
/// <param name="AfterResultCursor">Resumes the read after this item through <see cref="ReadOptions.ResumeCursor"/>.</param>
/// <param name="ReadAt">The revision of the response that carried this item.</param>
public sealed record ReadRelationship(Relationship Relationship, string? AfterResultCursor, ZedToken ReadAt);

/// <summary>A relationship yielded by <see cref="SpiceDBClient.ExportRelationshipsAsync"/>, with its paging state. Export responses carry no revision.</summary>
/// <param name="Relationship">The relationship.</param>
/// <param name="AfterResultCursor">Resumes the export after the response that carried this item, through <see cref="ExportOptions.ResumeCursor"/>. Every item in one response shares it.</param>
public sealed record ExportedRelationship(Relationship Relationship, string? AfterResultCursor);

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
    private IReadOnlyList<ResolvedSubject> _ExcludedSubjects = Array.Empty<ResolvedSubject>();
    public IReadOnlyList<ResolvedSubject> ExcludedSubjects { get => _ExcludedSubjects; init => _ExcludedSubjects = Native.List(value); }

    /// <summary>
    /// The revision this result was computed at. Identical for every item
    /// yielded by a single <see cref="SpiceDBClient.LookupSubjectsAsync"/>
    /// call — a property of the call, not of the individual subject.
    /// </summary>
    public ZedToken LookedUpAt { get; init; }
}
