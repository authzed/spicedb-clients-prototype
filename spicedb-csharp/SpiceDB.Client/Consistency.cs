// Consistency provides factory methods for SpiceDB consistency strategies.
//
// Every read operation on the SpiceDB client requires an explicit consistency
// strategy. This prevents silent defaults and makes consistency choices visible.

using Authzed.Api.V1;

namespace SpiceDB.Client;

/// <summary>
/// Represents a consistency requirement for read operations.
/// Use the static factory methods to create a Strategy.
/// </summary>
public sealed record ConsistencyStrategy
{
    /// <summary>
    /// Exposes the underlying proto type for advanced use cases.
    /// </summary>
    public Authzed.Api.V1.Consistency V1Consistency { get; }

    internal ConsistencyStrategy(Authzed.Api.V1.Consistency v1Consistency)
    {
        V1Consistency = v1Consistency ?? throw new ArgumentNullException(nameof(v1Consistency));
    }
}

/// <summary>
/// Static factory methods for creating consistency strategies.
/// </summary>
public static class Consistency
{
    /// <summary>
    /// Returns a strategy that requires full consistency. This is the least
    /// performant option but guarantees the most up-to-date results.
    /// </summary>
    public static ConsistencyStrategy Full() =>
        new(new Authzed.Api.V1.Consistency
        {
            FullyConsistent = true,
        });

    /// <summary>
    /// Returns a strategy that uses SpiceDB's preferred revision for optimal
    /// performance. This is the recommended default for most read operations.
    /// </summary>
    public static ConsistencyStrategy MinLatency() =>
        new(new Authzed.Api.V1.Consistency
        {
            MinimizeLatency = true,
        });

    /// <summary>
    /// Returns a strategy that ensures results are at least as fresh as the
    /// given revision. Use this for read-after-write consistency.
    /// </summary>
    /// <param name="revision">A ZedToken revision string from a previous write.</param>
    /// <exception cref="ArgumentException">Thrown when revision is null or empty.</exception>
    public static ConsistencyStrategy AtLeast(string revision)
    {
        if (string.IsNullOrEmpty(revision))
            throw new ArgumentException("Revision must not be null or empty.", nameof(revision));

        return new(new Authzed.Api.V1.Consistency
        {
            AtLeastAsFresh = new ZedToken { Token = revision },
        });
    }

    /// <summary>
    /// Returns a strategy that reads at the exact given revision.
    /// </summary>
    /// <param name="revision">A ZedToken revision string.</param>
    /// <exception cref="ArgumentException">Thrown when revision is null or empty.</exception>
    public static ConsistencyStrategy Snapshot(string revision)
    {
        if (string.IsNullOrEmpty(revision))
            throw new ArgumentException("Revision must not be null or empty.", nameof(revision));

        return new(new Authzed.Api.V1.Consistency
        {
            AtExactSnapshot = new ZedToken { Token = revision },
        });
    }

    /// <summary>
    /// Returns AtLeast(revision) if revision is non-empty, otherwise Full().
    /// Use this when you have an optional revision from a previous write and
    /// want the safest fallback.
    /// </summary>
    public static ConsistencyStrategy AtLeastOrFull(string? revision)
    {
        if (string.IsNullOrEmpty(revision))
            return Full();
        return AtLeast(revision);
    }

    /// <summary>
    /// Returns AtLeast(revision) if revision is non-empty, otherwise MinLatency().
    /// Use this when you have an optional revision but prefer performance over
    /// full consistency as the fallback.
    /// </summary>
    public static ConsistencyStrategy AtLeastOrMinLatency(string? revision)
    {
        if (string.IsNullOrEmpty(revision))
            return MinLatency();
        return AtLeast(revision);
    }
}
