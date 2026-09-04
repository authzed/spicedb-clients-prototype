// Relationship is a flat, immutable representation of a SpiceDB relationship.
// It avoids nested proto types in favor of plain C# record fields.

using Authzed.Api.V1;
using Google.Protobuf.WellKnownTypes;

namespace SpiceDB.Client;

/// <summary>
/// A flat, immutable representation of a SpiceDB relationship.
/// Use the static factory methods to create instances.
/// </summary>
public sealed record Relationship
{
    public string ResourceType { get; init; } = "";
    public string ResourceID { get; init; } = "";
    public string ResourceRelation { get; init; } = "";
    public string SubjectType { get; init; } = "";
    public string SubjectID { get; init; } = "";
    public string SubjectRelation { get; init; } = "";
    public string? CaveatName { get; init; }
    public IReadOnlyDictionary<string, object>? CaveatContext { get; init; }
    public DateTimeOffset? Expiration { get; init; }

    /// <summary>
    /// Per-item caveat context for CHECK-TIME evaluation only. Distinct from
    /// <see cref="CaveatContext"/>: <see cref="CaveatContext"/> is stored
    /// WITH the relationship (write-time — sent by <c>WriteAsync</c> as part
    /// of the relationship's caveat), while <see cref="CheckContext"/> is
    /// supplied fresh on every check call and is NEVER written to SpiceDB —
    /// <see cref="ToProto"/> never reads it. Conflating the two would leak
    /// check-time-only data into a stored relationship the next time this
    /// record round-trips through <c>WriteAsync</c>.
    /// <para>
    /// When a call-level default context is also supplied to a check call
    /// (see <c>SpiceDBClient.CheckPermissionsWithOptionsAsync</c> and
    /// siblings), the two are merged key by key: this relationship's own
    /// keys win on conflict, and call-level keys this relationship doesn't
    /// specify are retained unchanged.
    /// </para>
    /// </summary>
    public IReadOnlyDictionary<string, object>? CheckContext { get; init; }

    /// <summary>
    /// Creates a Relationship from explicit resource and subject fields.
    /// </summary>
    /// <exception cref="ArgumentException">Thrown when any required field is empty.</exception>
    public static Relationship FromTriple(
        string resourceType, string resourceID, string resourceRelation,
        string subjectType, string subjectID, string subjectRelation = "")
    {
        if (string.IsNullOrEmpty(resourceType) || string.IsNullOrEmpty(resourceID) || string.IsNullOrEmpty(resourceRelation))
            throw new ArgumentException("Resource type, ID, and relation are required.");
        if (string.IsNullOrEmpty(subjectType) || string.IsNullOrEmpty(subjectID))
            throw new ArgumentException("Subject type and ID are required.");

        return new Relationship
        {
            ResourceType = resourceType,
            ResourceID = resourceID,
            ResourceRelation = resourceRelation,
            SubjectType = subjectType,
            SubjectID = subjectID,
            SubjectRelation = subjectRelation,
        };
    }

    /// <summary>
    /// Parses a relationship from a tuple string in the format:
    /// resourceType:resourceID#relation@subjectType:subjectID[#subjectRelation]
    /// </summary>
    /// <exception cref="FormatException">Thrown when the tuple string is malformed.</exception>
    public static Relationship FromTuple(string tuple)
    {
        if (string.IsNullOrEmpty(tuple))
            throw new FormatException("Tuple string must not be null or empty.");

        var atParts = tuple.Split('@', 2);
        if (atParts.Length != 2)
            throw new FormatException("Invalid tuple format: missing '@' separator.");

        var resourcePart = atParts[0];
        var subjectPart = atParts[1];

        // Parse resource: type:id#relation
        var resourceHash = resourcePart.Split('#', 2);
        if (resourceHash.Length != 2)
            throw new FormatException("Invalid tuple format: missing '#' in resource.");

        var resourceTypeId = resourceHash[0].Split(':', 2);
        if (resourceTypeId.Length != 2)
            throw new FormatException("Invalid tuple format: missing ':' in resource type:id.");

        // Parse subject: type:id[#relation]
        var subjectHash = subjectPart.Split('#', 2);
        var subjectTypeId = subjectHash[0].Split(':', 2);
        if (subjectTypeId.Length != 2)
            throw new FormatException("Invalid tuple format: missing ':' in subject type:id.");

        var subjectRelation = subjectHash.Length == 2 ? subjectHash[1] : "";

        return FromTriple(
            resourceTypeId[0], resourceTypeId[1], resourceHash[1],
            subjectTypeId[0], subjectTypeId[1], subjectRelation);
    }

    /// <summary>
    /// Returns a copy of the relationship with the given caveat.
    /// </summary>
    public Relationship WithCaveat(string name, IReadOnlyDictionary<string, object>? context = null) =>
        this with { CaveatName = name, CaveatContext = context };

    /// <summary>
    /// Returns a copy of the relationship with the given expiration.
    /// </summary>
    public Relationship WithExpiration(DateTimeOffset expiration) =>
        this with { Expiration = expiration };

    /// <summary>
    /// Returns a copy of the relationship carrying per-item caveat context
    /// for check calls. See <see cref="CheckContext"/> for how this differs
    /// from <see cref="WithCaveat"/>'s write-time context, and how it merges
    /// with any call-level default supplied to the check call.
    /// </summary>
    public Relationship WithCheckContext(IReadOnlyDictionary<string, object>? context) =>
        this with { CheckContext = context };

    /// <summary>
    /// Converts this relationship to its proto representation. CaveatContext
    /// values are converted via <see cref="SpiceDBClient.ToProtoValue"/> —
    /// the same type-dispatching converter used for check-time caveat
    /// context — so a numeric, boolean, null, or nested map/list value lands
    /// on its matching <c>kind</c> oneof case instead of being stringified.
    /// A caveat like <c>now &lt; 100</c> stored against a stringified "50"
    /// fails to evaluate, and fails silently as a conditional result — and
    /// unlike a bad check-time context (which fails one call), a bad
    /// write-time context is persisted: every future check against this
    /// relationship mis-evaluates, and re-checking with correct context
    /// never repairs it, only rewriting the relationship does.
    /// </summary>
    public Authzed.Api.V1.Relationship ToProto()
    {
        var rel = new Authzed.Api.V1.Relationship
        {
            Resource = new ObjectReference
            {
                ObjectType = ResourceType,
                ObjectId = ResourceID,
            },
            Relation = ResourceRelation,
            Subject = new SubjectReference
            {
                Object = new ObjectReference
                {
                    ObjectType = SubjectType,
                    ObjectId = SubjectID,
                },
                OptionalRelation = SubjectRelation,
            },
        };

        if (!string.IsNullOrEmpty(CaveatName))
        {
            rel.OptionalCaveat = new ContextualizedCaveat
            {
                CaveatName = CaveatName,
            };
            if (CaveatContext != null)
            {
                var structValue = new Struct();
                foreach (var (key, value) in CaveatContext)
                {
                    structValue.Fields[key] = SpiceDBClient.ToProtoValueForKey(key, value);
                }
                rel.OptionalCaveat.Context = structValue;
            }
        }

        if (Expiration.HasValue)
        {
            rel.OptionalExpiresAt = Timestamp.FromDateTimeOffset(Expiration.Value);
        }

        return rel;
    }

    /// <summary>
    /// Converts a proto Relationship to an idiomatic Relationship.
    /// CaveatContext values are converted via
    /// <see cref="SpiceDBClient.FromProtoValue"/>, the inverse of
    /// <see cref="SpiceDBClient.ToProtoValue"/> used by <see cref="ToProto"/>
    /// — this dispatches on each <c>Value</c>'s <c>kind</c> oneof so the
    /// round trip through <see cref="ToProto"/>/<see cref="FromProto"/>
    /// preserves types symmetrically instead of reading every value back as
    /// a string.
    /// </summary>
    public static Relationship FromProto(Authzed.Api.V1.Relationship proto)
    {
        var rel = new Relationship
        {
            ResourceType = proto.Resource?.ObjectType ?? "",
            ResourceID = proto.Resource?.ObjectId ?? "",
            ResourceRelation = proto.Relation ?? "",
            SubjectType = proto.Subject?.Object?.ObjectType ?? "",
            SubjectID = proto.Subject?.Object?.ObjectId ?? "",
            SubjectRelation = proto.Subject?.OptionalRelation ?? "",
        };

        if (proto.OptionalCaveat != null)
        {
            var context = proto.OptionalCaveat.Context?.Fields
                .ToDictionary(f => f.Key, f => SpiceDBClient.FromProtoValue(f.Value)!);

            rel = rel with
            {
                CaveatName = proto.OptionalCaveat.CaveatName,
                CaveatContext = context,
            };
        }

        if (proto.OptionalExpiresAt != null)
        {
            rel = rel with { Expiration = proto.OptionalExpiresAt.ToDateTimeOffset() };
        }

        return rel;
    }

    /// <summary>
    /// Returns a Filter that matches the exact resource/subject of this relationship.
    /// </summary>
    public Filter ToFilter() => new Filter(ResourceType)
        .WithResourceID(ResourceID)
        .WithRelation(ResourceRelation)
        .WithSubjectType(SubjectType)
        .WithSubjectID(SubjectID);

    /// <summary>
    /// Returns the tuple string representation.
    /// </summary>
    public override string ToString()
    {
        var s = $"{ResourceType}:{ResourceID}#{ResourceRelation}@{SubjectType}:{SubjectID}";
        if (!string.IsNullOrEmpty(SubjectRelation))
            s += "#" + SubjectRelation;
        return s;
    }
}

/// <summary>
/// Represents a relationship mutation (create, touch, or delete).
/// </summary>
public sealed record RelationshipUpdate
{
    public UpdateOperation Operation { get; init; }
    public Relationship Relationship { get; init; } = null!;
}

/// <summary>
/// The type of mutation in a RelationshipUpdate.
/// </summary>
public enum UpdateOperation
{
    /// <summary>
    /// The server sent an operation this client does not recognize — either
    /// <c>OPERATION_UNSPECIFIED</c> on the wire, or a future operation value added after this
    /// client shipped. Never treat this as a write: a cache or index mirror consuming the watch
    /// stream that upserts on an unrecognized operation could turn a delete it doesn't
    /// understand into a silent write.
    /// </summary>
    Unspecified = 0,
    Create = 1,
    Touch = 2,
    Delete = 3,
}

/// <summary>
/// A single event from <see cref="SpiceDBClient.UpdatesAsync"/>, corresponding to one
/// <c>WatchResponse</c> from the server.
/// </summary>
public sealed record WatchEvent
{
    public IReadOnlyList<RelationshipUpdate> Updates { get; init; } = Array.Empty<RelationshipUpdate>();

    /// <summary>
    /// The point in time this event is current through. Always populated. Proto:
    /// "the ZedToken that represents the point in time that the watch response is current
    /// through. This token can be used in a subsequent WatchRequest to resume watching from this
    /// point." Pass it as <c>startRevision</c> to a later <see cref="SpiceDBClient.UpdatesAsync"/>
    /// call to resume after a dropped stream, instead of restarting from the original
    /// <c>startRevision</c> (reprocessing everything since, possibly past the GC window) or from
    /// head (silently losing every change in the gap).
    /// </summary>
    public string ChangesThrough { get; init; } = "";

    /// <summary>
    /// True for a checkpoint event, which carries no <see cref="Updates"/> — it exists only to
    /// advertise a fresh <see cref="ChangesThrough"/> and, behind a proxy that aborts idle
    /// connections, to keep the stream alive. Checkpoints are only sent when
    /// <c>includeCheckpoints</c> is passed to <see cref="SpiceDBClient.UpdatesAsync"/>.
    /// </summary>
    public bool IsCheckpoint { get; init; }
}
