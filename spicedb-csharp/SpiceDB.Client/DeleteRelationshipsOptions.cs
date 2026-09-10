namespace SpiceDB.Client;

/// <summary>
/// Call-level options for <see cref="SpiceDBClient.DeleteRelationshipsWithOptionsAsync"/>.
/// </summary>
/// <remarks>
/// This type exists so that a new delete option is a new property here rather
/// than a new parameter or a new method. See root DESIGN.md, "RULE: Every RPC
/// wrapper must have one place to add an option".
/// <para>
/// A <see cref="System.Threading.CancellationToken"/> is deliberately not an
/// option here. .NET convention is that cancellation is its own trailing
/// parameter, and every operation takes one whether or not options are
/// supplied.
/// </para>
/// </remarks>
public sealed class DeleteRelationshipsOptions
{
    /// <summary>
    /// Preconditions that must match for the delete to proceed. The server
    /// rejects the whole call (deleting nothing for it) if any of these
    /// filters find no matching relationship. Re-sent on every page of the
    /// auto-paging loop, so a delete spanning multiple pages re-evaluates
    /// these on every page rather than once for the whole call.
    /// </summary>
    public IReadOnlyList<Filter>? MustMatch { get; init; }

    /// <summary>
    /// Preconditions that must NOT match for the delete to proceed. The
    /// server rejects the whole call (deleting nothing for it) if any of
    /// these filters finds a matching relationship. Re-sent on every page,
    /// same caveat as <see cref="MustMatch"/>.
    /// </summary>
    public IReadOnlyList<Filter>? MustNotMatch { get; init; }

    /// <summary>
    /// Overrides the default 1,000-item page size used while auto-paging
    /// through a large delete.
    /// </summary>
    public uint? Limit { get; init; }

    /// <summary>
    /// Resumes a deletion left incomplete by an earlier call, from the
    /// cursor it returned. Opaque — obtained from
    /// <c>DeleteRelationshipsResponse.AfterResultCursor</c> via
    /// <see cref="SpiceDBClient.RawProto"/>, since the auto-paging
    /// <see cref="SpiceDBClient.DeleteRelationshipsAsync"/>/
    /// <see cref="SpiceDBClient.DeleteRelationshipsWithOptionsAsync"/> always
    /// run to completion themselves and never hand one back. Only
    /// datastores whose deletion can be ordered and resumed honor it; others
    /// reject a request that supplies one. Leave <c>null</c> to start a new
    /// deletion, which is the same as omitting options entirely.
    /// </summary>
    public string? Cursor { get; init; }

    /// <summary>
    /// Deadline applied fresh to each page of this call, overriding the
    /// client's default. See root DESIGN.md, "RULE: A unary call must have a
    /// deadline" — leaving this null applies the client default rather than
    /// removing the bound.
    /// </summary>
    public TimeSpan? Timeout { get; init; }
}
