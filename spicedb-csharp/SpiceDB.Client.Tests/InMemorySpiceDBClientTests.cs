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

    private static string[] Tuples(int n) => Enumerable.Range(0, n).Select(i => $"document:d{i}#viewer@user:x").ToArray();

    [Fact]
    public async Task FailedImportBatchAppliesNothing()
    {
        var store = new InMemorySpiceDBClient();
        store.SetFaultHook(f => f is { Stage: FaultStage.ImportBatch, Item: 2 } && f.Call.Operation == SpiceDBOperation.ImportRelationships
            ? InMemorySpiceDBClient.ServerError(StatusCode.AlreadyExists, "duplicate relationship") : null);
        var error = await Assert.ThrowsAsync<AlreadyExistsException>(() => store.ImportRelationshipsAsync(Async(Tuples(10)), new ImportOptions { BatchSize = 3 }));
        (error.Message, error.ServerMessage).Should().Be(("duplicate relationship", "duplicate relationship"));
        store.Relationships.Should().BeEmpty();

        // Batches follow the call's batch size: 10 items in batches of 3 are batches 0..3.
        var batches = new List<int>();
        store.SetFaultHook(f => { if (f.Stage == FaultStage.ImportBatch) batches.Add(f.Item); return null; });
        await store.ImportRelationshipsAsync(Async(Tuples(10)), new ImportOptions { BatchSize = 3 });
        batches.Should().Equal(0, 1, 2, 3);
        store.Relationships.Should().HaveCount(10);
    }

    [Fact]
    public async Task FailedImportCommitAppliesNothing()
    {
        var store = new InMemorySpiceDBClient();
        store.SetFaultHook(f => f.Stage == FaultStage.ImportCommit ? InMemorySpiceDBClient.ServerError(StatusCode.Unavailable, "lost") : null);
        await Assert.ThrowsAsync<UnavailableException>(() => store.ImportRelationshipsAsync(Async(Tuples(4))));
        store.Relationships.Should().BeEmpty();
    }

    [Fact]
    public async Task ExportFailsAfterKItems()
    {
        var store = new InMemorySpiceDBClient();
        await store.ImportRelationshipsAsync(Async(Tuples(5)));
        store.SetFaultHook(f => f is { Stage: FaultStage.Item, Item: 3 } && f.Call.Operation == SpiceDBOperation.ExportRelationships
            ? InMemorySpiceDBClient.ServerError(StatusCode.Unavailable, "stream reset") : null);
        var got = 0;
        await Assert.ThrowsAsync<UnavailableException>(async () => { await foreach (var _ in store.ExportRelationshipsAsync(Consistency.Full(), new Filter("document"))) got++; });
        got.Should().Be(3);

        // Reads fail the same way; item indexes count from the resume point.
        store.SetFaultHook(f => f is { Stage: FaultStage.Item, Item: 1 } ? InMemorySpiceDBClient.ServerError(StatusCode.Unavailable, "stream reset") : null);
        var read = new List<string>();
        await Assert.ThrowsAsync<UnavailableException>(async () => { await foreach (var r in store.ReadRelationshipsAsync(Consistency.Full(), new Filter("document"), new ReadOptions { ResumeCursor = "2" })) read.Add(r.Relationship.Resource.ObjectId); });
        read.Should().Equal("d2");
    }

    [Fact]
    public async Task FailNextAffectsExactlyOneCallOfOneOperation()
    {
        var store = new InMemorySpiceDBClient();
        var check = new PermissionCheck(new ObjectRef("document", "d0"), "view", Alice, Consistency.Full());
        var touch = new Transaction(); touch.Touch(Relationship.FromTuple("document:d0#viewer@user:alice"));
        store.FailNext(SpiceDBOperation.Check, InMemorySpiceDBClient.ServerError(StatusCode.Unavailable, "down"));
        await store.WriteAsync(touch); // another operation is unaffected
        await Assert.ThrowsAsync<UnavailableException>(() => store.CheckAsync(check));
        (await store.CheckAsync(check)).HasPermission.Should().BeFalse();

        // A failed write applies nothing.
        store.FailNext(SpiceDBOperation.Write, InMemorySpiceDBClient.ServerError(StatusCode.Unavailable, "down"));
        var other = new Transaction(); other.Touch(Relationship.FromTuple("document:d1#viewer@user:alice"));
        await Assert.ThrowsAsync<UnavailableException>(() => store.WriteAsync(other));
        store.Relationships.Should().ContainSingle();

        // A server error carries SpiceDB's ErrorInfo reason when given one.
        var shaped = InMemorySpiceDBClient.ServerError(StatusCode.FailedPrecondition, "unable to satisfy write precondition", "ERROR_REASON_WRITE_OR_DELETE_PRECONDITION_FAILURE");
        shaped.Should().BeOfType<FailedPreconditionException>().Which.Reason.Should().Be("ERROR_REASON_WRITE_OR_DELETE_PRECONDITION_FAILURE");
    }

    [Fact]
    public async Task RecordsCallsWithArgumentsAndOptions()
    {
        var store = new InMemorySpiceDBClient();
        await foreach (var _ in store.ReadRelationshipsAsync(Consistency.Full(), new Filter("document"), new ReadOptions { ResumeCursor = "2", Limit = 5 })) { }
        await foreach (var _ in store.ReadRelationshipsAsync(Consistency.Full(), new Filter("folder"))) { }
        await store.DeleteAllRelationshipsNonAtomicAsync(new Filter("document"), new DeleteAllOptions { PageSize = 1 });

        var reads = store.CallsOf(SpiceDBOperation.ReadRelationships);
        reads.Select(c => c.Index).Should().Equal(0, 1);
        reads[0].OptionsAs<ReadOptions>()!.ResumeCursor.Should().Be("2");
        reads[0].OptionsAs<ReadOptions>()!.Limit.Should().Be(5);
        reads[1].Options.Should().BeNull();
        reads[1].Argument<Filter>()!.ResourceType.Should().Be("folder");
        store.Calls.Select(c => c.Operation).Should().Equal(SpiceDBOperation.ReadRelationships, SpiceDBOperation.ReadRelationships, SpiceDBOperation.DeleteAllRelationshipsNonAtomic);
    }

    private static async IAsyncEnumerable<Relationship> Async(params string[] tuples)
    {
        foreach (var t in tuples) { await Task.Yield(); yield return Relationship.FromTuple(t); }
    }
}
