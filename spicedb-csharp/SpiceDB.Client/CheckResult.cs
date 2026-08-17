// CheckResult.cs — native result type for CheckPermissionAsync and
// CheckPermissionsAsync. Replaces bare bool returns with a record that
// carries the server's four-valued Permissionship, the caveat context that
// was missing (if any), and the ZedToken the check was evaluated at —
// mirroring spicedb-go's client/check_types.go.
//
// RULE (root DESIGN.md, "Only an unconditional grant is true"): only
// HasPermission may ever be treated as a grant. A ConditionalPermission
// result means the server found a matching relationship but could not
// evaluate its caveat because the required context was not supplied — that
// is the server asking for more information, not granting access.

namespace SpiceDB.Client;

/// <summary>
/// The outcome of a permission check. <see cref="Permissionship"/> carries
/// the server's four-valued answer — a
/// <see cref="Client.Permissionship.ConditionalPermission"/> result means the
/// server needed caveat context that was not supplied, and is NOT a grant.
/// Prefer <see cref="HasPermission"/> over comparing
/// <see cref="Permissionship"/> directly for the common case.
/// <para>
/// <b>Clause-4 decision:</b> this type deliberately does NOT define
/// <c>operator true</c>/<c>operator false</c> or an implicit/explicit
/// conversion to <c>bool</c>. <c>if (result)</c> is a compile error by
/// design — callers must go through <see cref="HasPermission"/> (or compare
/// <see cref="Permissionship"/> explicitly) to get a boolean answer. C#
/// records don't implicitly participate in boolean contexts, so omitting
/// these operators is the safe default: it keeps the compile error rather
/// than introducing a truthy conversion that could silently disagree with
/// <see cref="HasPermission"/> for a <see cref="Client.Permissionship.ConditionalPermission"/>
/// result. See root DESIGN.md, "RULE: Only an unconditional grant is true",
/// clause 4.
/// </para>
/// </summary>
public sealed record CheckResult
{
    /// <summary>The server's answer. Prefer <see cref="HasPermission"/> for the common case.</summary>
    public Permissionship Permissionship { get; init; }

    /// <summary>
    /// Caveat context keys the server needed and did not receive. Empty
    /// unless <see cref="Permissionship"/> is
    /// <see cref="Client.Permissionship.ConditionalPermission"/>.
    /// </summary>
    public IReadOnlyList<string> MissingContext { get; init; } = [];

    /// <summary>
    /// The revision this check was evaluated at. Thread it into
    /// <see cref="Consistency.AtLeast"/> to make a later read observe this
    /// check (read-your-writes).
    /// </summary>
    public string CheckedAt { get; init; } = "";

    /// <summary>
    /// True ONLY when <see cref="Permissionship"/> is
    /// <see cref="Client.Permissionship.HasPermission"/> — false for
    /// <see cref="Client.Permissionship.ConditionalPermission"/>,
    /// <see cref="Client.Permissionship.NoPermission"/>, and
    /// <see cref="Client.Permissionship.Unspecified"/> alike. A single
    /// equality comparison, never a disjunction: treating a conditional
    /// result as a grant would authorize on a caveat the server never
    /// evaluated.
    /// </summary>
    public bool HasPermission => Permissionship == Permissionship.HasPermission;
}
