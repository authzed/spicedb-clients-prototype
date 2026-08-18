// PermissionTree is a native, immutable representation of an expanded
// permission tree. It avoids leaking the proto PermissionRelationshipTree
// type in favor of plain C# records.

namespace SpiceDB.Client;

/// <summary>
/// The set operation combining an <see cref="IntermediateNode"/>'s children.
/// </summary>
public enum TreeOperation
{
    Unspecified,
    Union,
    Intersection,
    Exclusion,
}

/// <summary>Identifies a resource or subject object.</summary>
public sealed record ObjectRef
{
    public string ObjectType { get; init; } = "";
    public string ObjectID { get; init; } = "";
}

/// <summary>A subject with access at a leaf of a <see cref="PermissionTree"/>.</summary>
public sealed record SubjectRef
{
    public string SubjectType { get; init; } = "";
    public string SubjectID { get; init; } = "";
    public string OptionalRelation { get; init; } = "";
}

/// <summary>Combines child subtrees with a set operation.</summary>
public sealed record IntermediateNode
{
    public TreeOperation Operation { get; init; }
    public IReadOnlyList<PermissionTree> Children { get; init; } = [];
}

/// <summary>Holds the concrete subjects at a leaf of a <see cref="PermissionTree"/>.</summary>
public sealed record LeafNode
{
    public IReadOnlyList<SubjectRef> Subjects { get; init; } = [];
}

/// <summary>
/// A native node of an expanded permission tree. Returned by
/// <see cref="SpiceDBClient.ExpandPermissionTreeAsync"/>. Exactly one of
/// <see cref="Intermediate"/> or <see cref="Leaf"/> is non-null.
/// </summary>
public sealed record PermissionTree
{
    public ObjectRef ExpandedObject { get; init; } = new();
    public string ExpandedRelation { get; init; } = "";
    public IntermediateNode? Intermediate { get; init; }
    public LeafNode? Leaf { get; init; }
}
