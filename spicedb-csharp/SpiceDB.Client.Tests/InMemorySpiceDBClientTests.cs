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
        typeof(ISpiceDBSchemaClient).IsAssignableFrom(typeof(SpiceDBClient)).Should().BeTrue();
        typeof(ISpiceDBSchemaClient).IsAssignableFrom(typeof(InMemorySpiceDBClient)).Should().BeTrue();
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

        // SpiceDB rejects an all-or-nothing delete over its limit with INVALID_ARGUMENT.
        var tooMany = await Assert.ThrowsAsync<InvalidArgumentException>(() => store.DeleteRelationshipsAsync(new Filter("document"), 1, new DeleteOptions { Partial = PartialDeletion.Disallowed }));
        tooMany.Reason.Should().Be("ERROR_REASON_TOO_MANY_RELATIONSHIPS_FOR_TRANSACTIONAL_DELETE");
        tooMany.ServerMessage.Should().Be("found more than 1 relationships to be deleted and partial deletion was not requested");
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

    [Fact]
    public async Task ErrorsMatchTheRealClientsShape()
    {
        var store = new InMemorySpiceDBClient();
        var txn = new Transaction(); txn.Touch(Relationship.FromTuple("document:1#viewer@user:alice"));
        await store.WriteAsync(txn);

        var guarded = new Transaction(); guarded.MustMatch(new Filter("document").WithRelation("owner"));
        var failed = await Assert.ThrowsAsync<FailedPreconditionException>(() => store.WriteAsync(guarded));
        failed.ServerMessage.Should().StartWith("unable to satisfy write precondition `").And.Be(failed.Message);
        failed.Reason.Should().Be("ERROR_REASON_WRITE_OR_DELETE_PRECONDITION_FAILURE");
        failed.Status.StatusCode.Should().Be(StatusCode.FailedPrecondition);

        var create = new Transaction(); create.Create(Relationship.FromTuple("document:1#viewer@user:alice"));
        var conflict = await Assert.ThrowsAsync<AlreadyExistsException>(() => store.WriteAsync(create));
        conflict.ServerMessage.Should().StartWith("could not CREATE relationship `document:1#viewer@user:alice`, as it already existed");
        conflict.Reason.Should().Be("ERROR_REASON_ATTEMPT_TO_RECREATE_RELATIONSHIP");

        // Local validation looks like the client's local validation: no server message.
        var local = await Assert.ThrowsAsync<InvalidArgumentException>(async () => { await foreach (var _ in store.ReadRelationshipsAsync(Consistency.Full(), new Filter("document"), new ReadOptions { Limit = 0 })) { } });
        local.ServerMessage.Should().BeNull();
        local.Message.Should().Be("Limit must be positive.");

        // The same per-call retry rules as the real client.
        var retry = new RetryPolicy { MaxRetries = 1, InitialBackoff = TimeSpan.FromMilliseconds(1) };
        await Assert.ThrowsAsync<InvalidArgumentException>(() => store.WriteAsync(create, new WriteOptions { RetryPolicy = retry }));
        await Assert.ThrowsAsync<InvalidArgumentException>(() => store.DeleteRelationshipsAsync(new Filter("document"), 1, new DeleteOptions { RetryPolicy = retry }));
        await store.WriteAsync(txn, new WriteOptions { RetryPolicy = retry });

        // An empty write (the guarded one above has no updates) checks preconditions and returns the current revision.
        guarded.Count.Should().Be(0);
        var before = store.Revision;
        (await store.WriteAsync(new Transaction())).Revision.Should().Be(before);
        store.Revision.Should().Be(before);
    }

    [Fact]
    public async Task ImportsExportsAndSchemaOperations()
    {
        var store = new InMemorySpiceDBClient();
        (await store.ImportRelationshipsAsync(Async("doc:a#viewer@user:x", "doc:b#viewer@user:x", "doc:c#viewer@user:x", "folder:f#viewer@user:x"))).Should().Be(4ul);
        var duplicate = await Assert.ThrowsAsync<AlreadyExistsException>(() => store.ImportRelationshipsAsync(Async("doc:z#viewer@user:x", "doc:a#viewer@user:x")));
        duplicate.Reason.Should().Be("ERROR_REASON_ATTEMPT_TO_RECREATE_RELATIONSHIP");
        store.Relationships.Should().HaveCount(4, "a failed import adds nothing");

        var first = new List<ExportedRelationship>();
        await foreach (var r in store.ExportRelationshipsAsync(Consistency.Full(), new Filter("doc"), new ExportOptions { Limit = 2 })) first.Add(r);
        first.Should().HaveCount(2);
        var rest = new List<string>();
        await foreach (var r in store.ExportRelationshipsAsync(Consistency.Full(), new Filter("doc"), new ExportOptions { ResumeCursor = first[1].AfterResultCursor })) rest.Add(r.Relationship.ResourceId);
        rest.Should().Equal("c");
        var folders = new List<string>();
        await foreach (var r in store.ExportRelationshipsAsync(Consistency.Full(), new Filter("folder"))) folders.Add(r.Relationship.ToString());
        folders.Should().Equal("folder:f#viewer@user:x");
        await Assert.ThrowsAsync<InvalidArgumentException>(async () => { await foreach (var _ in store.ReadRelationshipsAsync(Consistency.Full(), new Filter("doc"), new ReadOptions { ResumeCursor = "nope" })) { } });

        var missing = await Assert.ThrowsAsync<NotFoundException>(() => store.ReadSchemaAsync());
        missing.ServerMessage.Should().Be("no schema has been defined; please call WriteSchema to start");
        (await store.ReflectSchemaAsync(Consistency.MinLatency())).Definitions.Should().BeEmpty("SpiceDB reflects an empty schema as empty");
        var written = await store.WriteSchemaAsync("definition user {}");
        var schema = await store.ReadSchemaAsync();
        (schema.Schema, schema.Revision).Should().Be(("definition user {}", written));
        store.SetReflectSchemaResult(new ReflectSchemaResult { Definitions = [new SchemaDefinition { Name = "user" }] });
        var reflected = await store.ReflectSchemaAsync(Consistency.MinLatency());
        reflected.Definitions.Single().Name.Should().Be("user");
        reflected.Revision.Should().Be(store.Revision);
        await Assert.ThrowsAsync<InvalidArgumentException>(() => store.ReflectSchemaAsync(null!));
    }

    [Fact]
    public async Task FillsResponseMetadataSinks()
    {
        var store = new InMemorySpiceDBClient();
        store.SetResponseMetadata(new Metadata { { "io.spicedb.respmeta.version", "v1.99" } }, new Metadata { { "t", "1" } });
        var sink = new ResponseMetadata();
        await store.WriteSchemaAsync("definition user {}", new CallOptions { Transport = new TransportOptions { ResponseMetadata = sink } });
        sink.Headers!.GetValue("io.spicedb.respmeta.version").Should().Be("v1.99");
        sink.Trailers!.GetValue("t").Should().Be("1");
    }

    private static async IAsyncEnumerable<Relationship> Async(params string[] tuples)
    {
        foreach (var t in tuples) { await Task.Yield(); yield return Relationship.FromTuple(t); }
    }
}
