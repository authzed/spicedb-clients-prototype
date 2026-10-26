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

/// <summary>Combines child subtrees with a set operation.</summary>
public sealed record IntermediateNode
{
    public TreeOperation Operation { get; init; }
    private IReadOnlyList<PermissionTree> _Children = Array.Empty<PermissionTree>();
    public IReadOnlyList<PermissionTree> Children { get => _Children; init => _Children = Native.List(value); }
}

/// <summary>Holds the concrete subjects at a leaf of a <see cref="PermissionTree"/>.</summary>
public sealed record LeafNode
{
    private IReadOnlyList<SubjectRef> _Subjects = Array.Empty<SubjectRef>();
    public IReadOnlyList<SubjectRef> Subjects { get => _Subjects; init => _Subjects = Native.List(value); }
}

/// <summary>
/// A native node of an expanded permission tree. Returned by
/// <see cref="SpiceDBClient.ExpandPermissionTreeAsync"/>. Exactly one of
/// <see cref="Intermediate"/> or <see cref="Leaf"/> is non-null.
/// </summary>
public sealed record PermissionTree
{
    public ObjectRef? ExpandedObject { get; }
    public string ExpandedRelation { get; }
    public IntermediateNode? Intermediate { get; }
    public LeafNode? Leaf { get; }
    public bool IsUnspecified => Intermediate is null && Leaf is null;
    public PermissionTree(ObjectRef? expandedObject = null, string expandedRelation = "", IntermediateNode? intermediate = null, LeafNode? leaf = null)
    {
        if (intermediate is not null && leaf is not null) throw new InvalidArgumentException("A permission tree node cannot be both intermediate and leaf.");
        ExpandedObject = expandedObject; ExpandedRelation = expandedRelation; Intermediate = intermediate; Leaf = leaf;
    }
}
