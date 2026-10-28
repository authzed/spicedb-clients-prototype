using FluentAssertions;
using Grpc.Core;
using SpiceDB.Client.Testing;
using Xunit;

namespace SpiceDB.Client.Tests;

public class InMemorySpiceDBClientTests
{
    private static readonly SubjectRef Alice = SubjectRef.Direct(new ObjectRef("user", "alice"));

    [Fact]
    public void SharesTheRealClientsInterface()
    {
        typeof(ISpiceDBRelationshipClient).IsAssignableFrom(typeof(SpiceDBClient)).Should().BeTrue();
        typeof(ISpiceDBRelationshipClient).IsAssignableFrom(typeof(InMemorySpiceDBClient)).Should().BeTrue();
    }

    [Fact]
    public async Task WritesApplyPreconditionsAndCreateConflictsAtomically()
    {
        ISpiceDBRelationshipClient store = new InMemorySpiceDBClient();
        var txn = new Transaction();
        txn.Create(Relationship.FromTuple("document:1#viewer@user:alice"));
        txn.Touch(Relationship.FromTuple("document:2#viewer@user:alice"));
        var first = await store.WriteAsync(txn);

        var conflict = new Transaction();
        conflict.Touch(Relationship.FromTuple("document:3#viewer@user:alice"));
        conflict.Create(Relationship.FromTuple("document:1#viewer@user:alice"));
        var error = await Assert.ThrowsAsync<AlreadyExistsException>(() => store.WriteAsync(conflict));
        error.StatusCode.Should().Be(StatusCode.AlreadyExists);

        var guarded = new Transaction();
        guarded.MustNotMatch(new Filter("document").WithResourceId("1"));
        guarded.Delete(Relationship.FromTuple("document:2#viewer@user:alice"));
        await Assert.ThrowsAsync<FailedPreconditionException>(() => store.WriteAsync(guarded));

        var read = new List<ReadRelationship>();
        await foreach (var r in store.ReadRelationshipsAsync(Consistency.AtLeast(first.Revision), new Filter("document"))) read.Add(r);
        read.Select(r => r.Relationship.ToString()).Should().Equal("document:1#viewer@user:alice", "document:2#viewer@user:alice");

        var allowed = new Transaction();
        allowed.MustMatch(new Filter("document").WithResourceId("1"));
        allowed.Delete(Relationship.FromTuple("document:1#viewer@user:alice"));
        (await store.WriteAsync(allowed)).Revision.Should().NotBe(first.Revision);
    }

    [Fact]
    public async Task ReadsAndDeletesUseFilterSemantics()
    {
        var store = new InMemorySpiceDBClient();
        var txn = new Transaction();
        foreach (var t in new[] { "document:1#viewer@user:alice", "document:2#viewer@team:t#member", "document:3#viewer@user:bob", "folder:1#viewer@user:alice" })
            txn.Touch(Relationship.FromTuple(t));
        await store.WriteAsync(txn);

        var direct = new List<ReadRelationship>();
        await foreach (var r in store.ReadRelationshipsAsync(Consistency.Full(), new Filter("document").WithSubjectType("team").WithSubjectRelation(SubjectRelationFilter.Direct))) direct.Add(r);
        direct.Should().BeEmpty();

        var page = new List<ReadRelationship>();
        await foreach (var r in store.ReadRelationshipsAsync(Consistency.Full(), new Filter("document"), new ReadOptions { Limit = 1 })) page.Add(r);
        var rest = new List<ReadRelationship>();
        await foreach (var r in store.ReadRelationshipsAsync(Consistency.Full(), new Filter("document"), new ReadOptions { ResumeCursor = page[0].AfterResultCursor })) rest.Add(r);
        rest.Select(r => r.Relationship.ResourceId).Should().Equal("2", "3");

        await Assert.ThrowsAsync<FailedPreconditionException>(() => store.DeleteRelationshipsAsync(new Filter("document"), 1, new DeleteOptions { Partial = PartialDeletion.Disallowed }));
        var partial = await store.DeleteRelationshipsAsync(new Filter("document"), 1);
        partial.Completed.Should().BeFalse();
        var all = await store.DeleteAllRelationshipsNonAtomicAsync(new Filter("document"), new DeleteAllOptions { PageSize = 1 });
        all.DeletedCount.Should().Be(2);
        store.Relationships.Select(r => r.ToString()).Should().Equal("folder:1#viewer@user:alice");
    }

    [Fact]
    public async Task ChecksReturnRegisteredResultsAndErrors()
    {
        var store = new InMemorySpiceDBClient();
        var doc = new ObjectRef("document", "1");
        store.SetCheckResult(doc, "view", Alice, Permissionship.HasPermission);
        store.SetCheckError(doc, "edit", Alice, SpiceDBException.Create(StatusCode.FailedPrecondition, "depth"));
        PermissionCheck Check(string permission) => new(doc, permission, Alice, Consistency.Full());

        (await store.CheckAsync(Check("view"))).HasPermission.Should().BeTrue();
        (await store.CheckAsync(Check("admin"))).Permissionship.Should().Be(Permissionship.NoPermission);
        await Assert.ThrowsAsync<FailedPreconditionException>(() => store.CheckAsync(Check("edit")));
        var results = await store.CheckBulkAsync([Check("view"), Check("edit")], new CheckOptions { ItemErrors = BulkItemErrors.Returned });
        results[1].Error.Should().BeOfType<FailedPreconditionException>().Which.InputIndex.Should().Be(1);
        (await Assert.ThrowsAsync<FailedPreconditionException>(() => store.CheckBulkAsync([Check("view"), Check("edit")]))).InputIndex.Should().Be(1);
    }
}
