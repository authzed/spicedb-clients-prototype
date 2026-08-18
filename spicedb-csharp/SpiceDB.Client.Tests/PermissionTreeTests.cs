// Tests for the native PermissionTree mapper (SpiceDBClient.ToPermissionTree),
// which replaces the leaked proto PermissionRelationshipTree type.

using Authzed.Api.V1;
using FluentAssertions;
using SpiceDB.Client;
using Xunit;

namespace SpiceDB.Client.Tests;

public class PermissionTreeTests
{
    private static ObjectReference ObjRef(string type, string id) =>
        new() { ObjectType = type, ObjectId = id };

    private static SubjectReference SubjRef(string type, string id, string optionalRelation) =>
        new() { Object = ObjRef(type, id), OptionalRelation = optionalRelation };

    [Fact]
    public void ToPermissionTree_MapsLeafNode()
    {
        var proto = new PermissionRelationshipTree
        {
            ExpandedObject = ObjRef("document", "doc1"),
            ExpandedRelation = "viewer",
            Leaf = new DirectSubjectSet
            {
                Subjects =
                {
                    SubjRef("user", "alice", ""),
                    SubjRef("group", "eng", "member"),
                },
            },
        };

        var tree = SpiceDBClient.ToPermissionTree(proto);

        tree.ExpandedObject.ObjectType.Should().Be("document");
        tree.ExpandedObject.ObjectID.Should().Be("doc1");
        tree.ExpandedRelation.Should().Be("viewer");
        tree.Intermediate.Should().BeNull();
        tree.Leaf.Should().NotBeNull();
        tree.Leaf!.Subjects.Should().HaveCount(2);
        tree.Leaf.Subjects[0].SubjectType.Should().Be("user");
        tree.Leaf.Subjects[0].SubjectID.Should().Be("alice");
        tree.Leaf.Subjects[0].OptionalRelation.Should().Be("");
        tree.Leaf.Subjects[1].SubjectType.Should().Be("group");
        tree.Leaf.Subjects[1].SubjectID.Should().Be("eng");
        tree.Leaf.Subjects[1].OptionalRelation.Should().Be("member");
    }

    [Fact]
    public void ToPermissionTree_MapsNestedIntermediateNodes()
    {
        // Nested INTERSECTION intermediate with a single leaf child.
        var nestedIntersection = new PermissionRelationshipTree
        {
            ExpandedObject = ObjRef("document", "doc1"),
            ExpandedRelation = "editor",
            Intermediate = new AlgebraicSubjectSet
            {
                Operation = AlgebraicSubjectSet.Types.Operation.Intersection,
                Children =
                {
                    new PermissionRelationshipTree
                    {
                        ExpandedObject = ObjRef("document", "doc1"),
                        ExpandedRelation = "editor",
                        Leaf = new DirectSubjectSet { Subjects = { SubjRef("user", "bob", "") } },
                    },
                },
            },
        };

        // Leaf child of the top-level UNION.
        var leafChild = new PermissionRelationshipTree
        {
            ExpandedObject = ObjRef("document", "doc1"),
            ExpandedRelation = "viewer",
            Leaf = new DirectSubjectSet
            {
                Subjects =
                {
                    SubjRef("user", "alice", ""),
                    SubjRef("group", "eng", "member"),
                },
            },
        };

        var root = new PermissionRelationshipTree
        {
            ExpandedObject = ObjRef("document", "doc1"),
            ExpandedRelation = "view",
            Intermediate = new AlgebraicSubjectSet
            {
                Operation = AlgebraicSubjectSet.Types.Operation.Union,
                Children = { leafChild, nestedIntersection },
            },
        };

        var tree = SpiceDBClient.ToPermissionTree(root);

        tree.ExpandedObject.ObjectType.Should().Be("document");
        tree.ExpandedObject.ObjectID.Should().Be("doc1");
        tree.ExpandedRelation.Should().Be("view");
        tree.Leaf.Should().BeNull();
        tree.Intermediate.Should().NotBeNull();
        tree.Intermediate!.Operation.Should().Be(TreeOperation.Union);
        tree.Intermediate.Children.Should().HaveCount(2);

        var mappedLeafChild = tree.Intermediate.Children[0];
        mappedLeafChild.Intermediate.Should().BeNull();
        mappedLeafChild.Leaf.Should().NotBeNull();
        mappedLeafChild.Leaf!.Subjects.Should().HaveCount(2);
        mappedLeafChild.Leaf.Subjects[0].SubjectID.Should().Be("alice");
        mappedLeafChild.Leaf.Subjects[1].OptionalRelation.Should().Be("member");

        var mappedIntersection = tree.Intermediate.Children[1];
        mappedIntersection.ExpandedRelation.Should().Be("editor");
        mappedIntersection.Leaf.Should().BeNull();
        mappedIntersection.Intermediate.Should().NotBeNull();
        mappedIntersection.Intermediate!.Operation.Should().Be(TreeOperation.Intersection);
        mappedIntersection.Intermediate.Children.Should().HaveCount(1);
        mappedIntersection.Intermediate.Children[0].Leaf!.Subjects[0].SubjectID.Should().Be("bob");
    }

    [Fact]
    public void ToPermissionTree_MapsUnspecifiedOperation()
    {
        var proto = new PermissionRelationshipTree
        {
            ExpandedObject = ObjRef("document", "doc1"),
            ExpandedRelation = "view",
            Intermediate = new AlgebraicSubjectSet
            {
                Operation = AlgebraicSubjectSet.Types.Operation.Unspecified,
            },
        };

        var tree = SpiceDBClient.ToPermissionTree(proto);

        tree.Intermediate!.Operation.Should().Be(TreeOperation.Unspecified);
        tree.Intermediate.Children.Should().BeEmpty();
    }

    [Fact]
    public void ToPermissionTree_HandlesNullInput()
    {
        var tree = SpiceDBClient.ToPermissionTree(null);

        tree.Intermediate.Should().BeNull();
        tree.Leaf.Should().BeNull();
        tree.ExpandedObject.ObjectType.Should().Be("");
        tree.ExpandedObject.ObjectID.Should().Be("");
        tree.ExpandedRelation.Should().Be("");
    }

    [Fact]
    public void ExpandResult_ExposesNoProtoType()
    {
        var properties = typeof(ExpandResult).GetProperties();
        properties.Should().NotBeEmpty();

        foreach (var property in properties)
        {
            property.PropertyType.Namespace.Should().NotStartWith(
                "Authzed.Api",
                because: $"ExpandResult must not expose proto types, found: {property.PropertyType}");
        }
    }
}
