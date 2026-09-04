namespace SpiceDB.Client;

/// <summary>
/// Call-level options for the permission-check operations:
/// <see cref="SpiceDBClient.CheckPermissionWithOptionsAsync"/>,
/// <see cref="SpiceDBClient.CheckPermissionsWithOptionsAsync"/>,
/// <see cref="SpiceDBClient.CheckAnyWithOptionsAsync"/> and
/// <see cref="SpiceDBClient.CheckAllWithOptionsAsync"/>.
/// </summary>
/// <remarks>
/// <para>
/// This type exists so that a new check option is a new property here rather
/// than a new parameter or a new method. C# substitutes an optional
/// parameter's default at the <em>call site</em>, so adding one is
/// binary-breaking for every already-compiled assembly; and a method per
/// option — <c>CheckPermissionsAsync</c>, <c>CheckPermissionsWithContextAsync</c>,
/// and whatever the next option would have required — grows a surface nobody
/// can hold in their head. See root DESIGN.md, "RULE: Every RPC wrapper must
/// have one place to add an option".
/// </para>
/// <para>
/// A <see cref="System.Threading.CancellationToken"/> is deliberately not an
/// option here. .NET convention is that cancellation is its own trailing
/// parameter, and every operation takes one whether or not options are
/// supplied.
/// </para>
/// </remarks>
public sealed class CheckOptions
{
    /// <summary>
    /// Call-level default caveat context, applied to every relationship the
    /// call evaluates. Caveat context supplies named values (for example
    /// "now") that SpiceDB needs to evaluate a caveat expression encountered
    /// during the check; without it a caveated match comes back as
    /// <see cref="Permissionship.ConditionalPermission"/> rather than a grant,
    /// and <see cref="CheckResult.MissingContext"/> names what was needed.
    /// </summary>
    /// <remarks>
    /// Merged key-by-key with each relationship's own caveat context: where a
    /// key is present in both, the relationship's own value wins, and default
    /// keys it does not override are retained. This is a key-level merge, not
    /// a wholesale replacement.
    /// </remarks>
    public IReadOnlyDictionary<string, object>? Context { get; init; }

    /// <summary>
    /// Deadline for this call, overriding the client's default. See root
    /// DESIGN.md, "RULE: A unary call must have a deadline" — leaving this
    /// null applies the client default rather than removing the bound.
    /// </summary>
    public TimeSpan? Timeout { get; init; }
}
