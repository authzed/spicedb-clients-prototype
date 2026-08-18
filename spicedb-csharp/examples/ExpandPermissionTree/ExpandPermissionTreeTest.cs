using Xunit;
// Example ExpandPermissionTree demonstrates expanding a permission into the
// native PermissionTree of subjects with access.

using SpiceDB.Client;
using static SpiceDB.Client.Consistency;

namespace ExpandPermissionTree;

public class ExpandPermissionTreeTest
{
    private const string Schema = """
        definition user {}

        definition document {
            relation viewer: user
            relation editor: user
            permission view = viewer + editor
        }
        """;

    [Fact]
    public async Task ExpandPermissionTree_WalksUnionOfViewerAndEditor()
    {
        await using var client = SpiceDBClient.CreatePlaintext("localhost:50051", "somerandomkeyhere");

        await client.WriteSchemaAsync(Schema);

        var txn = new Transaction();
        txn.Touch(Relationship.FromTriple("document", "report", "viewer", "user", "alice"));
        txn.Touch(Relationship.FromTriple("document", "report", "editor", "user", "bob"));
        var revision = await client.WriteAsync(txn);

        var result = await client.ExpandPermissionTreeAsync(
            AtLeast(revision), "document", "report", "view");

        // The root node identifies the resource/permission that was expanded.
        Assert.Equal("document", result.Tree.ExpandedObject.ObjectType);
        Assert.Equal("report", result.Tree.ExpandedObject.ObjectID);
        Assert.Equal("view", result.Tree.ExpandedRelation);

        // "view = viewer + editor" is a union, so the root is an
        // IntermediateNode with a Union operation and no Leaf.
        Assert.Null(result.Tree.Leaf);
        Assert.NotNull(result.Tree.Intermediate);
        Assert.Equal(TreeOperation.Union, result.Tree.Intermediate!.Operation);

        // Walk the tree recursively, collecting every subject found at a leaf.
        var subjectIDs = CollectLeafSubjects(result.Tree)
            .Select(s => s.SubjectID)
            .ToHashSet();

        Assert.Contains("alice", subjectIDs);
        Assert.Contains("bob", subjectIDs);
    }

    /// <summary>
    /// Recursively walks a <see cref="PermissionTree"/>, yielding every
    /// subject found at a leaf node. Exactly one of Intermediate/Leaf is
    /// non-null on each node, so both cases must be handled.
    /// </summary>
    private static IEnumerable<SubjectRef> CollectLeafSubjects(PermissionTree tree)
    {
        if (tree.Leaf is { } leaf)
        {
            foreach (var subject in leaf.Subjects)
            {
                yield return subject;
            }
        }

        if (tree.Intermediate is { } intermediate)
        {
            foreach (var child in intermediate.Children)
            {
                foreach (var subject in CollectLeafSubjects(child))
                {
                    yield return subject;
                }
            }
        }
    }
}
