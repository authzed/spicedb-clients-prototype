using FluentAssertions;
using SpiceDB.Client;
using Xunit;

namespace SpiceDB.Client.Tests;

public class RelationshipTests
{
    [Fact]
    public void FromTriple_CreatesRelationship()
    {
        var rel = Relationship.FromTriple("document", "doc1", "viewer", "user", "alice");

        rel.ResourceType.Should().Be("document");
        rel.ResourceID.Should().Be("doc1");
        rel.ResourceRelation.Should().Be("viewer");
        rel.SubjectType.Should().Be("user");
        rel.SubjectID.Should().Be("alice");
        rel.SubjectRelation.Should().BeEmpty();
        rel.CaveatName.Should().BeNull();
        rel.CaveatContext.Should().BeNull();
        rel.Expiration.Should().BeNull();
    }

    [Fact]
    public void FromTriple_WithSubjectRelation()
    {
        var rel = Relationship.FromTriple("document", "doc1", "viewer", "group", "engineering", "member");

        rel.SubjectRelation.Should().Be("member");
    }

    [Fact]
    public void FromTriple_ThrowsOnEmptyResourceType()
    {
        var act = () => Relationship.FromTriple("", "doc1", "viewer", "user", "alice");
        act.Should().Throw<ArgumentException>();
    }

    [Fact]
    public void FromTriple_ThrowsOnEmptyResourceID()
    {
        var act = () => Relationship.FromTriple("document", "", "viewer", "user", "alice");
        act.Should().Throw<ArgumentException>();
    }

    [Fact]
    public void FromTriple_ThrowsOnEmptyResourceRelation()
    {
        var act = () => Relationship.FromTriple("document", "doc1", "", "user", "alice");
        act.Should().Throw<ArgumentException>();
    }

    [Fact]
    public void FromTriple_ThrowsOnEmptySubjectType()
    {
        var act = () => Relationship.FromTriple("document", "doc1", "viewer", "", "alice");
        act.Should().Throw<ArgumentException>();
    }

    [Fact]
    public void FromTriple_ThrowsOnEmptySubjectID()
    {
        var act = () => Relationship.FromTriple("document", "doc1", "viewer", "user", "");
        act.Should().Throw<ArgumentException>();
    }

    [Fact]
    public void FromTuple_ParsesBasicTuple()
    {
        var rel = Relationship.FromTuple("document:doc1#viewer@user:alice");

        rel.ResourceType.Should().Be("document");
        rel.ResourceID.Should().Be("doc1");
        rel.ResourceRelation.Should().Be("viewer");
        rel.SubjectType.Should().Be("user");
        rel.SubjectID.Should().Be("alice");
        rel.SubjectRelation.Should().BeEmpty();
    }

    [Fact]
    public void FromTuple_ParsesTupleWithSubjectRelation()
    {
        var rel = Relationship.FromTuple("document:doc1#viewer@group:engineering#member");

        rel.SubjectType.Should().Be("group");
        rel.SubjectID.Should().Be("engineering");
        rel.SubjectRelation.Should().Be("member");
    }

    [Fact]
    public void FromTuple_ThrowsOnMissingAt()
    {
        var act = () => Relationship.FromTuple("document:doc1#viewer");
        act.Should().Throw<FormatException>().WithMessage("*@*");
    }

    [Fact]
    public void FromTuple_ThrowsOnMissingHash()
    {
        var act = () => Relationship.FromTuple("document:doc1@user:alice");
        act.Should().Throw<FormatException>().WithMessage("*#*");
    }

    [Fact]
    public void FromTuple_ThrowsOnMissingResourceColon()
    {
        var act = () => Relationship.FromTuple("documentdoc1#viewer@user:alice");
        act.Should().Throw<FormatException>().WithMessage("*:*");
    }

    [Fact]
    public void FromTuple_ThrowsOnEmptyString()
    {
        var act = () => Relationship.FromTuple("");
        act.Should().Throw<FormatException>();
    }

    [Fact]
    public void WithCaveat_ReturnsCopyWithCaveat()
    {
        var original = Relationship.FromTriple("document", "doc1", "viewer", "user", "alice");
        var context = new Dictionary<string, object> { ["region"] = "us" };
        var withCaveat = original.WithCaveat("ip_allowlist", context);

        // Original unchanged
        original.CaveatName.Should().BeNull();
        original.CaveatContext.Should().BeNull();

        // New has caveat
        withCaveat.CaveatName.Should().Be("ip_allowlist");
        withCaveat.CaveatContext.Should().ContainKey("region");

        // Other fields unchanged
        withCaveat.ResourceType.Should().Be("document");
        withCaveat.SubjectID.Should().Be("alice");
    }

    [Fact]
    public void WithExpiration_ReturnsCopyWithExpiration()
    {
        var original = Relationship.FromTriple("document", "doc1", "viewer", "user", "alice");
        var expires = new DateTimeOffset(2026, 12, 31, 0, 0, 0, TimeSpan.Zero);
        var withExp = original.WithExpiration(expires);

        original.Expiration.Should().BeNull();
        withExp.Expiration.Should().Be(expires);
        withExp.ResourceType.Should().Be("document");
    }

    [Fact]
    public void ToString_BasicFormat()
    {
        var rel = Relationship.FromTriple("document", "doc1", "viewer", "user", "alice");
        rel.ToString().Should().Be("document:doc1#viewer@user:alice");
    }

    [Fact]
    public void ToString_WithSubjectRelation()
    {
        var rel = Relationship.FromTriple("document", "doc1", "viewer", "group", "eng", "member");
        rel.ToString().Should().Be("document:doc1#viewer@group:eng#member");
    }

    [Fact]
    public void ToProto_ConvertsCorrectly()
    {
        var rel = Relationship.FromTriple("document", "doc1", "viewer", "user", "alice");
        var proto = rel.ToProto();

        proto.Resource.ObjectType.Should().Be("document");
        proto.Resource.ObjectId.Should().Be("doc1");
        proto.Relation.Should().Be("viewer");
        proto.Subject.Object.ObjectType.Should().Be("user");
        proto.Subject.Object.ObjectId.Should().Be("alice");
    }

    [Fact]
    public void ToProto_WithCaveat()
    {
        var rel = Relationship.FromTriple("document", "doc1", "viewer", "user", "alice")
            .WithCaveat("my_caveat", new Dictionary<string, object> { ["key"] = "val" });
        var proto = rel.ToProto();

        proto.OptionalCaveat.Should().NotBeNull();
        proto.OptionalCaveat.CaveatName.Should().Be("my_caveat");
        proto.OptionalCaveat.Context.Fields.Should().ContainKey("key");
    }

    [Fact]
    public void ToProto_WithExpiration()
    {
        var expires = new DateTimeOffset(2026, 6, 15, 12, 0, 0, TimeSpan.Zero);
        var rel = Relationship.FromTriple("document", "doc1", "viewer", "user", "alice")
            .WithExpiration(expires);
        var proto = rel.ToProto();

        proto.OptionalExpiresAt.Should().NotBeNull();
        proto.OptionalExpiresAt.ToDateTimeOffset().Should().Be(expires);
    }

    [Fact]
    public void FromProto_ConvertsCorrectly()
    {
        var proto = new Authzed.Api.V1.Relationship
        {
            Resource = new Authzed.Api.V1.ObjectReference { ObjectType = "doc", ObjectId = "1" },
            Relation = "reader",
            Subject = new Authzed.Api.V1.SubjectReference
            {
                Object = new Authzed.Api.V1.ObjectReference { ObjectType = "user", ObjectId = "bob" },
                OptionalRelation = "member",
            },
        };

        var rel = Relationship.FromProto(proto);

        rel.ResourceType.Should().Be("doc");
        rel.ResourceID.Should().Be("1");
        rel.ResourceRelation.Should().Be("reader");
        rel.SubjectType.Should().Be("user");
        rel.SubjectID.Should().Be("bob");
        rel.SubjectRelation.Should().Be("member");
    }

    [Fact]
    public void RoundTrip_ToProtoAndBack()
    {
        var original = Relationship.FromTriple("document", "doc1", "viewer", "user", "alice", "member");
        var roundTripped = Relationship.FromProto(original.ToProto());

        roundTripped.ResourceType.Should().Be(original.ResourceType);
        roundTripped.ResourceID.Should().Be(original.ResourceID);
        roundTripped.ResourceRelation.Should().Be(original.ResourceRelation);
        roundTripped.SubjectType.Should().Be(original.SubjectType);
        roundTripped.SubjectID.Should().Be(original.SubjectID);
        roundTripped.SubjectRelation.Should().Be(original.SubjectRelation);
    }

    [Fact]
    public void ToFilter_CreatesMatchingFilter()
    {
        var rel = Relationship.FromTriple("document", "doc1", "viewer", "user", "alice");
        var filter = rel.ToFilter();

        filter.ResourceType.Should().Be("document");
        filter.ResourceID.Should().Be("doc1");
        filter.Relation.Should().Be("viewer");
        filter.SubjectType.Should().Be("user");
        filter.SubjectID.Should().Be("alice");
    }

    [Fact]
    public void Relationship_IsImmutableRecord()
    {
        var rel = Relationship.FromTriple("document", "doc1", "viewer", "user", "alice");

        // Records support value equality
        var same = Relationship.FromTriple("document", "doc1", "viewer", "user", "alice");
        rel.Should().Be(same);

        // Different should not be equal
        var different = Relationship.FromTriple("document", "doc2", "viewer", "user", "alice");
        rel.Should().NotBe(different);
    }
}
