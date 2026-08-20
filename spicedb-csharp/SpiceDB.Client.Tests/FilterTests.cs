using FluentAssertions;
using SpiceDB.Client;
using Xunit;

namespace SpiceDB.Client.Tests;

public class FilterTests
{
    [Fact]
    public void Constructor_SetsResourceType()
    {
        var filter = new Filter("document");

        filter.ResourceType.Should().Be("document");
        filter.ResourceID.Should().BeNull();
        filter.ResourceIDPrefix.Should().BeNull();
        filter.Relation.Should().BeNull();
        filter.SubjectType.Should().BeNull();
        filter.SubjectID.Should().BeNull();
        filter.SubjectRelation.Should().BeNull();
    }

    [Fact]
    public void Constructor_ThrowsOnEmptyResourceType()
    {
        var act = () => new Filter("");
        act.Should().Throw<ArgumentException>();
    }

    [Fact]
    public void Constructor_ThrowsOnNullResourceType()
    {
        var act = () => new Filter(null!);
        act.Should().Throw<ArgumentException>();
    }

    [Fact]
    public void WithResourceID_ReturnsNewFilter()
    {
        var original = new Filter("document");
        var withId = original.WithResourceID("doc1");

        original.ResourceID.Should().BeNull(); // original unchanged
        withId.ResourceID.Should().Be("doc1");
        withId.ResourceType.Should().Be("document");
    }

    [Fact]
    public void WithResourceIDPrefix_ReturnsNewFilter()
    {
        var filter = new Filter("document").WithResourceIDPrefix("test-");

        filter.ResourceIDPrefix.Should().Be("test-");
    }

    [Fact]
    public void WithRelation_ReturnsNewFilter()
    {
        var filter = new Filter("document").WithRelation("viewer");

        filter.Relation.Should().Be("viewer");
    }

    [Fact]
    public void WithSubjectType_ReturnsNewFilter()
    {
        var filter = new Filter("document").WithSubjectType("user");

        filter.SubjectType.Should().Be("user");
    }

    [Fact]
    public void WithSubjectID_ReturnsNewFilter()
    {
        var filter = new Filter("document").WithSubjectID("alice");

        filter.SubjectID.Should().Be("alice");
    }

    [Fact]
    public void WithSubjectRelation_ReturnsNewFilter()
    {
        var filter = new Filter("document").WithSubjectRelation("member");

        filter.SubjectRelation.Should().Be("member");
    }

    [Fact]
    public void Chaining_AllMethods()
    {
        var filter = new Filter("document")
            .WithResourceID("doc1")
            .WithRelation("viewer")
            .WithSubjectType("user")
            .WithSubjectID("alice")
            .WithSubjectRelation("member");

        filter.ResourceType.Should().Be("document");
        filter.ResourceID.Should().Be("doc1");
        filter.Relation.Should().Be("viewer");
        filter.SubjectType.Should().Be("user");
        filter.SubjectID.Should().Be("alice");
        filter.SubjectRelation.Should().Be("member");
    }

    [Fact]
    public void ToProto_BasicFilter()
    {
        var filter = new Filter("document");
        var proto = filter.ToProto();

        proto.ResourceType.Should().Be("document");
        proto.OptionalResourceId.Should().BeEmpty();
        proto.OptionalRelation.Should().BeEmpty();
        proto.OptionalSubjectFilter.Should().BeNull();
    }

    [Fact]
    public void ToProto_WithResourceID()
    {
        var proto = new Filter("document").WithResourceID("doc1").ToProto();

        proto.OptionalResourceId.Should().Be("doc1");
    }

    [Fact]
    public void ToProto_WithResourceIDPrefix()
    {
        var proto = new Filter("document").WithResourceIDPrefix("test-").ToProto();

        proto.OptionalResourceIdPrefix.Should().Be("test-");
    }

    [Fact]
    public void ToProto_WithRelation()
    {
        var proto = new Filter("document").WithRelation("viewer").ToProto();

        proto.OptionalRelation.Should().Be("viewer");
    }

    [Fact]
    public void ToProto_WithSubjectFilter()
    {
        var proto = new Filter("document")
            .WithSubjectType("user")
            .WithSubjectID("alice")
            .ToProto();

        proto.OptionalSubjectFilter.Should().NotBeNull();
        proto.OptionalSubjectFilter.SubjectType.Should().Be("user");
        proto.OptionalSubjectFilter.OptionalSubjectId.Should().Be("alice");
    }

    [Fact]
    public void ToProto_WithSubjectRelation()
    {
        var proto = new Filter("document")
            .WithSubjectType("group")
            .WithSubjectRelation("member")
            .ToProto();

        proto.OptionalSubjectFilter.Should().NotBeNull();
        proto.OptionalSubjectFilter.OptionalRelation.Should().NotBeNull();
        proto.OptionalSubjectFilter.OptionalRelation.Relation.Should().Be("member");
    }

    [Fact]
    public void ToProto_SubjectIDWithoutSubjectType_Throws()
    {
        // The wire cannot express a subject ID without a subject type
        // (SubjectFilter.subject_type is required) -- silently dropping the
        // constraint instead would widen a filter like
        // new Filter("document").WithSubjectID("alice") into "every subject
        // on every document," which a caller using it with
        // DeleteRelationshipsAsync would not expect. Must throw naming the
        // missing field instead of silently building an unconstrained filter.
        var act = () => new Filter("document").WithSubjectID("alice").ToProto();

        var thrown = act.Should().Throw<InvalidArgumentException>().Which;
        thrown.Message.Should().Contain(nameof(Filter.SubjectID));
        thrown.Message.Should().Contain(nameof(Filter.SubjectType));
    }

    [Fact]
    public void ToProto_SubjectRelationWithoutSubjectType_Throws()
    {
        var act = () => new Filter("document").WithSubjectRelation("member").ToProto();

        var thrown = act.Should().Throw<InvalidArgumentException>().Which;
        thrown.Message.Should().Contain(nameof(Filter.SubjectRelation));
        thrown.Message.Should().Contain(nameof(Filter.SubjectType));
    }

    [Fact]
    public void ToProto_SubjectTypeAndID_DoesNotThrowAndSetsSubjectFilter()
    {
        // Companion to the two throw cases above -- proves the valid
        // combination (SubjectType supplied alongside SubjectID) still
        // works and is not accidentally caught by the new guard.
        var proto = new Filter("document").WithSubjectType("user").WithSubjectID("alice").ToProto();

        proto.OptionalSubjectFilter.Should().NotBeNull();
        proto.OptionalSubjectFilter.SubjectType.Should().Be("user");
        proto.OptionalSubjectFilter.OptionalSubjectId.Should().Be("alice");
    }

    [Fact]
    public void Filter_IsImmutableRecord()
    {
        var a = new Filter("document").WithResourceID("doc1");
        var b = new Filter("document").WithResourceID("doc1");

        a.Should().Be(b);
    }
}
