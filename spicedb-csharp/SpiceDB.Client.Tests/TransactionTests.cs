using FluentAssertions;
using SpiceDB.Client;
using Xunit;

namespace SpiceDB.Client.Tests;

public class TransactionTests
{
    private static Relationship TestRel(string id = "doc1") =>
        Relationship.FromTriple("document", id, "viewer", "user", "alice");

    [Fact]
    public void Create_AddsCreateOperation()
    {
        var txn = new Transaction();
        txn.Create(TestRel());

        txn.V1Updates.Should().HaveCount(1);
        txn.V1Updates[0].Operation.Should().Be(
            Authzed.Api.V1.RelationshipUpdate.Types.Operation.Create);
        txn.V1Updates[0].Relationship.Resource.ObjectType.Should().Be("document");
    }

    [Fact]
    public void Touch_AddsTouchOperation()
    {
        var txn = new Transaction();
        txn.Touch(TestRel());

        txn.V1Updates.Should().HaveCount(1);
        txn.V1Updates[0].Operation.Should().Be(
            Authzed.Api.V1.RelationshipUpdate.Types.Operation.Touch);
    }

    [Fact]
    public void Delete_AddsDeleteOperation()
    {
        var txn = new Transaction();
        txn.Delete(TestRel());

        txn.V1Updates.Should().HaveCount(1);
        txn.V1Updates[0].Operation.Should().Be(
            Authzed.Api.V1.RelationshipUpdate.Types.Operation.Delete);
    }

    [Fact]
    public void MultipleOperations_PreservesOrder()
    {
        var txn = new Transaction();
        txn.Create(TestRel("doc1"));
        txn.Touch(TestRel("doc2"));
        txn.Delete(TestRel("doc3"));

        txn.V1Updates.Should().HaveCount(3);
        txn.V1Updates[0].Operation.Should().Be(
            Authzed.Api.V1.RelationshipUpdate.Types.Operation.Create);
        txn.V1Updates[1].Operation.Should().Be(
            Authzed.Api.V1.RelationshipUpdate.Types.Operation.Touch);
        txn.V1Updates[2].Operation.Should().Be(
            Authzed.Api.V1.RelationshipUpdate.Types.Operation.Delete);

        txn.V1Updates[0].Relationship.Resource.ObjectId.Should().Be("doc1");
        txn.V1Updates[1].Relationship.Resource.ObjectId.Should().Be("doc2");
        txn.V1Updates[2].Relationship.Resource.ObjectId.Should().Be("doc3");
    }

    [Fact]
    public void MustNotMatch_AddsPrecondition()
    {
        var txn = new Transaction();
        var filter = new Filter("document").WithResourceID("doc1");
        txn.MustNotMatch(filter);

        txn.Preconditions.Should().HaveCount(1);
        txn.Preconditions[0].Operation.Should().Be(
            Authzed.Api.V1.Precondition.Types.Operation.MustNotMatch);
        txn.Preconditions[0].Filter.ResourceType.Should().Be("document");
    }

    [Fact]
    public void MustMatch_AddsPrecondition()
    {
        var txn = new Transaction();
        var filter = new Filter("document").WithResourceID("doc1");
        txn.MustMatch(filter);

        txn.Preconditions.Should().HaveCount(1);
        txn.Preconditions[0].Operation.Should().Be(
            Authzed.Api.V1.Precondition.Types.Operation.MustMatch);
    }

    [Fact]
    public void Create_ThrowsOnNull()
    {
        var txn = new Transaction();
        var act = () => txn.Create(null!);
        act.Should().Throw<ArgumentNullException>();
    }

    [Fact]
    public void Touch_ThrowsOnNull()
    {
        var txn = new Transaction();
        var act = () => txn.Touch(null!);
        act.Should().Throw<ArgumentNullException>();
    }

    [Fact]
    public void Delete_ThrowsOnNull()
    {
        var txn = new Transaction();
        var act = () => txn.Delete(null!);
        act.Should().Throw<ArgumentNullException>();
    }

    [Fact]
    public void MustNotMatch_ThrowsOnNull()
    {
        var txn = new Transaction();
        var act = () => txn.MustNotMatch(null!);
        act.Should().Throw<ArgumentNullException>();
    }

    [Fact]
    public void MustMatch_ThrowsOnNull()
    {
        var txn = new Transaction();
        var act = () => txn.MustMatch(null!);
        act.Should().Throw<ArgumentNullException>();
    }

    [Fact]
    public void EmptyTransaction_HasNoUpdatesOrPreconditions()
    {
        var txn = new Transaction();

        txn.V1Updates.Should().BeEmpty();
        txn.Preconditions.Should().BeEmpty();
    }

    [Fact]
    public void MixedOperationsAndPreconditions()
    {
        var txn = new Transaction();
        txn.Create(TestRel("doc1"));
        txn.MustNotMatch(new Filter("document").WithResourceID("doc1").WithRelation("viewer"));
        txn.Touch(TestRel("doc2"));
        txn.MustMatch(new Filter("document").WithResourceID("doc2"));

        txn.V1Updates.Should().HaveCount(2);
        txn.Preconditions.Should().HaveCount(2);
    }
}
