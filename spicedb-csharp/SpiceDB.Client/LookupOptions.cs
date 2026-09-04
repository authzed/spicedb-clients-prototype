namespace SpiceDB.Client;

/// <summary>
/// Call-level options for the lookup operations:
/// <see cref="SpiceDBClient.LookupResourcesWithOptionsAsync"/> and
/// <see cref="SpiceDBClient.LookupSubjectsWithOptionsAsync"/>.
/// </summary>
/// <remarks>
/// This type exists so that a new lookup option is a new property here rather
/// than a new parameter. <c>with_debug</c> arrived upstream and, with nowhere
/// to put it, became a required positional parameter on
/// <c>LookupResourcesAsync</c> — breaking every existing caller for the sake
/// of one optional field. See root DESIGN.md, "RULE: Every RPC wrapper must
/// have one place to add an option".
/// </remarks>
public sealed class LookupOptions
{
    /// <summary>
    /// Asks the server to attach debug information to the error when the
    /// lookup fails by exceeding the maximum dispatch depth. It has no effect
    /// on a successful call, and no effect on any other failure.
    /// </summary>
    public bool WithDebug { get; init; }
}
