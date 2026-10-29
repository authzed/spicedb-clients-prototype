using Authzed.Api.V1;
using FluentAssertions;
using Google.Protobuf;
using Google.Rpc;
using Grpc.Core;
using Moq;
using SpiceDB.Client.Protocol;
using Xunit;
using static SpiceDB.Client.Tests.StreamingTestHelpers;
using Consistency = SpiceDB.Client.Consistency;
using GrpcStatus = Grpc.Core.Status;
using Relationship = SpiceDB.Client.Relationship;

namespace SpiceDB.Client.Tests;

/// <summary>Per-call retry overrides, empty writes, single-request paging, wrapper statuses and bulk-check conversions.</summary>
public class PerCallRetryAndPagingTests
{
    private static readonly RetryPolicy RetryOnce = new() { MaxRetries = 1, RetryableCodes = [StatusCode.Unavailable], InitialBackoff = TimeSpan.FromMilliseconds(1) };

    private static SpiceDBClient Client(PermissionsService.PermissionsServiceClient? permissions = null, SchemaService.SchemaServiceClient? schema = null, WatchService.WatchServiceClient? watch = null, ExperimentalService.ExperimentalServiceClient? experimental = null, ClientOptions? options = null) =>
        new(permissions ?? new Mock<PermissionsService.PermissionsServiceClient>().Object, schema ?? new Mock<SchemaService.SchemaServiceClient>().Object,
            watch ?? new Mock<WatchService.WatchServiceClient>().Object, experimental ?? new Mock<ExperimentalService.ExperimentalServiceClient>().Object, null, options);

    private static AsyncUnaryCall<T> Failing<T>(StatusCode code, string message = "transient") =>
        new(Task.FromException<T>(new RpcException(new GrpcStatus(code, message))), Task.FromResult(new Metadata()), () => new GrpcStatus(code, message), () => new Metadata(), () => { });

    /// <summary>A permissions service whose first write fails with Unavailable (when <paramref name="failFirst"/>) and later writes succeed.</summary>
    private static (Mock<PermissionsService.PermissionsServiceClient> Mock, Func<int> Writes) Writes(bool failFirst)
    {
        var writes = 0;
        var permissions = new Mock<PermissionsService.PermissionsServiceClient>();
        permissions.Setup(p => p.WriteRelationshipsAsync(It.IsAny<WriteRelationshipsRequest>(), It.IsAny<Metadata>(), It.IsAny<DateTime?>(), It.IsAny<CancellationToken>()))
            .Returns(() => ++writes == 1 && failFirst ? Failing<WriteRelationshipsResponse>(StatusCode.Unavailable) : MakeUnaryCall(new WriteRelationshipsResponse { WrittenAt = new() { Token = "w" } }));
        return (permissions, () => writes);
    }

    private static Transaction Touch()
    {
        var txn = new Transaction();
        txn.Touch(Relationship.FromTuple("document:a#viewer@user:alice"));
        return txn;
    }

    [Fact]
    public async Task MutationsAreSingleAttemptByDefault()
    {
        var (permissions, writes) = Writes(failFirst: true);
        await using var client = Client(permissions.Object, options: new ClientOptions { RetryPolicy = RetryOnce });
        await Assert.ThrowsAsync<UnavailableException>(() => client.WriteAsync(Touch()));
        writes().Should().Be(1);
    }

    [Fact]
    public async Task TouchOnlyWriteMayOptInPerCall()
    {
        var (permissions, writes) = Writes(failFirst: true);
        await using var client = Client(permissions.Object);
        (await client.WriteAsync(Touch(), new WriteOptions { RetryPolicy = RetryOnce })).Revision.Value.Should().Be("w");
        writes().Should().Be(2);
    }

    [Fact]
    public async Task NonIdempotentMutationsRejectTheOptInBeforeAnyRequest()
    {
        var (permissions, writes) = Writes(failFirst: false);
        var experimental = new Mock<ExperimentalService.ExperimentalServiceClient>(MockBehavior.Strict);
        await using var client = Client(permissions.Object, experimental: experimental.Object);
        var create = new Transaction(); create.Create(Relationship.FromTuple("document:a#viewer@user:alice"));
        var deletes = new Transaction(); deletes.Delete(Relationship.FromTuple("document:a#viewer@user:alice"));
        var guarded = Touch(); guarded.MustMatch(new Filter("document"));
        foreach (var txn in new[] { create, deletes, guarded })
            await Assert.ThrowsAsync<InvalidArgumentException>(() => client.WriteAsync(txn, new WriteOptions { RetryPolicy = RetryOnce }));
        writes().Should().Be(0);

        await Assert.ThrowsAsync<InvalidArgumentException>(() => client.DeleteRelationshipsAsync(new Filter("document"), 1, new DeleteOptions { RetryPolicy = RetryOnce }));
        await Assert.ThrowsAsync<InvalidArgumentException>(() => client.DeleteAllRelationshipsNonAtomicAsync(new Filter("document"), new DeleteAllOptions { RetryPolicy = RetryOnce }));
        await Assert.ThrowsAsync<InvalidArgumentException>(() => client.ImportRelationshipsAsync(AsyncEnumerable(Relationship.FromTuple("document:a#viewer@user:alice")), new ImportOptions { RetryPolicy = RetryOnce }));
        await Assert.ThrowsAsync<InvalidArgumentException>(() => client.ExperimentalRegisterRelationshipCounterAsync("c", new Filter("document"), new CallOptions { RetryPolicy = RetryOnce }));
        await Assert.ThrowsAsync<InvalidArgumentException>(() => client.ExperimentalUnregisterRelationshipCounterAsync("c", new CallOptions { RetryPolicy = RetryOnce }));
        permissions.Verify(p => p.DeleteRelationshipsAsync(It.IsAny<DeleteRelationshipsRequest>(), It.IsAny<Metadata>(), It.IsAny<DateTime?>(), It.IsAny<CancellationToken>()), Times.Never);
        permissions.Verify(p => p.ImportBulkRelationships(It.IsAny<Metadata>(), It.IsAny<DateTime?>(), It.IsAny<CancellationToken>()), Times.Never);

        // A zero-retry policy asks for nothing a mutation cannot honor.
        (await client.WriteAsync(create, new WriteOptions { RetryPolicy = RetryPolicy.None })).Revision.Value.Should().Be("w");
    }

    [Fact]
    public async Task SchemaWriteMayOptInPerCall()
    {
        var calls = 0;
        var schema = new Mock<SchemaService.SchemaServiceClient>();
        schema.Setup(s => s.WriteSchemaAsync(It.IsAny<WriteSchemaRequest>(), It.IsAny<Metadata>(), It.IsAny<DateTime?>(), It.IsAny<CancellationToken>()))
            .Returns(() => ++calls == 1 ? Failing<WriteSchemaResponse>(StatusCode.Unavailable) : MakeUnaryCall(new WriteSchemaResponse { WrittenAt = new() { Token = "s" } }));
        await using var client = Client(schema: schema.Object);
        (await client.WriteSchemaAsync("definition user {}", new CallOptions { RetryPolicy = RetryOnce })).Value.Should().Be("s");
        calls.Should().Be(2);
    }

    [Fact]
    public async Task PerCallPolicyExemptsOneStreamAndOverridesClientWideDisabling()
    {
        var script = new StreamCallScript<WatchResponse>().Then([], UnavailableError()).Then([new WatchResponse { ChangesThrough = new() { Token = "t" } }]);
        var watch = new Mock<WatchService.WatchServiceClient>();
        watch.Setup(w => w.Watch(It.IsAny<WatchRequest>(), It.IsAny<Metadata>(), It.IsAny<DateTime?>(), It.IsAny<CancellationToken>())).Returns(() => script.Next());
        var retrying = new RetryPolicy { MaxRetries = 3, RetryableCodes = [StatusCode.Unavailable], InitialBackoff = TimeSpan.FromMilliseconds(1) };

        await using (var client = Client(watch: watch.Object, options: new ClientOptions { RetryPolicy = retrying }))
            await Assert.ThrowsAsync<UnavailableException>(async () => { await foreach (var _ in client.UpdatesAsync([], null, new WatchOptions { RetryPolicy = RetryPolicy.None })) { } });
        script.CallCount.Should().Be(1, "the exempted watch is opened once");

        // The opposite direction: client-wide stream retries are off, one call opts back in.
        script = new StreamCallScript<WatchResponse>().Then([], UnavailableError()).Then([new WatchResponse { ChangesThrough = new() { Token = "t" } }]);
        await using (var client = Client(watch: watch.Object, options: new ClientOptions { StreamRetries = StreamRetries.Disabled, Retries = Retries.Disabled }))
        {
            await foreach (var e in client.UpdatesAsync([], null, new WatchOptions { RetryPolicy = retrying }))
            {
                e.ChangesThrough.Value.Should().Be("t");
                break;
            }
        }
        script.CallCount.Should().Be(2);
    }

    [Fact]
    public async Task InvalidPerCallPolicyIsRejected()
    {
        await using var client = Client();
        await Assert.ThrowsAsync<InvalidArgumentException>(() => client.ReadSchemaAsync(new CallOptions { RetryPolicy = new RetryPolicy { MaxRetries = -1 } }));
    }

    [Fact]
    public async Task EmptyWriteIsSentToTheServer()
    {
        var (permissions, writes) = Writes(failFirst: false);
        await using var client = Client(permissions.Object);
        (await client.WriteAsync(new Transaction())).Revision.Value.Should().Be("w");
        writes().Should().Be(1);
    }

    [Fact]
    public async Task SingleRequestPagingSendsExactlyOneRequest()
    {
        var lookups = new List<LookupResourcesRequest>();
        var reads = new List<ReadRelationshipsRequest>();
        var permissions = new Mock<PermissionsService.PermissionsServiceClient>();
        permissions.Setup(p => p.LookupResources(It.IsAny<LookupResourcesRequest>(), It.IsAny<Metadata>(), It.IsAny<DateTime?>(), It.IsAny<CancellationToken>()))
            .Returns((LookupResourcesRequest r, Metadata _, DateTime? _, CancellationToken _) =>
            {
                lookups.Add(r.Clone());
                return MakeServerStreamingCall(new FakeStreamReader<LookupResourcesResponse>(Enumerable.Range(0, 3).Select(i => new LookupResourcesResponse
                { ResourceObjectId = i.ToString(), Permissionship = LookupPermissionship.HasPermission, AfterResultCursor = new() { Token = "c" + i } }).ToArray()));
            });
        permissions.Setup(p => p.ReadRelationships(It.IsAny<ReadRelationshipsRequest>(), It.IsAny<Metadata>(), It.IsAny<DateTime?>(), It.IsAny<CancellationToken>()))
            .Returns((ReadRelationshipsRequest r, Metadata _, DateTime? _, CancellationToken _) =>
            {
                reads.Add(r.Clone());
                return MakeServerStreamingCall(new FakeStreamReader<ReadRelationshipsResponse>(Enumerable.Range(0, 2).Select(i => new ReadRelationshipsResponse
                { Relationship = Relationship.FromTuple($"document:{i}#viewer@user:a").ToProto(), AfterResultCursor = new() { Token = "r" + i } }).ToArray()));
            });
        await using var client = Client(permissions.Object);
        var subject = SubjectRef.Direct(new ObjectRef("user", "a"));

        var cursors = new List<string?>();
        // PageSize 1 would make automatic paging fetch again after every item; single-request ignores it.
        await foreach (var r in client.LookupResourcesAsync(Consistency.Full(), "document", "view", subject, new LookupOptions { Paging = Paging.SingleRequest, PageSize = 1 })) cursors.Add(r.AfterResultCursor);
        cursors.Should().Equal("c0", "c1", "c2");
        lookups.Should().ContainSingle().Which.OptionalLimit.Should().Be(0u, "no Limit means unbounded");

        await foreach (var _ in client.LookupResourcesAsync(Consistency.Full(), "document", "view", subject, new LookupOptions { Paging = Paging.SingleRequest, Limit = 7 })) { }
        lookups[1].OptionalLimit.Should().Be(7u);

        var read = new List<ReadRelationship>();
        await foreach (var r in client.ReadRelationshipsAsync(Consistency.Full(), new Filter("document"), new ReadOptions { Paging = Paging.SingleRequest, PageSize = 2, Limit = 2 })) read.Add(r);
        read.Should().HaveCount(2);
        reads.Should().ContainSingle("a full page does not trigger a follow-up request").Which.OptionalLimit.Should().Be(2u);

        await Assert.ThrowsAsync<InvalidArgumentException>(async () => { await foreach (var _ in client.LookupSubjectsAsync(Consistency.Full(), new ObjectRef("document", "1"), "view", "user", new LookupOptions { Paging = Paging.SingleRequest })) { } });
        Assert.Throws<InvalidArgumentException>(() => new ReadOptions { Paging = (Paging)9 });
    }

    [Theory]
    [InlineData(null)]
    [InlineData("")]
    [InlineData("same")]
    public async Task FullPageWithoutANewCursorFailsInsteadOfRepeatingThePage(string? pageCursor)
    {
        // Every page is full and ends on pageCursor; "same" is also the cursor the caller resumes
        // from, so the first page already fails to advance. Requests are capped so a regression
        // fails rather than hangs.
        int reads = 0, lookups = 0, exports = 0;
        Cursor? Next() => pageCursor is null ? null : new() { Token = pageCursor };
        void Cap(int calls) { if (calls > 5) throw new InvalidOperationException("the same page was requested repeatedly"); }
        var permissions = new Mock<PermissionsService.PermissionsServiceClient>();
        permissions.Setup(p => p.ReadRelationships(It.IsAny<ReadRelationshipsRequest>(), It.IsAny<Metadata>(), It.IsAny<DateTime?>(), It.IsAny<CancellationToken>()))
            .Returns(() =>
            {
                Cap(++reads);
                return MakeServerStreamingCall(new FakeStreamReader<ReadRelationshipsResponse>(Enumerable.Range(0, 2).Select(i => new ReadRelationshipsResponse
                { Relationship = Relationship.FromTuple($"document:{i}#viewer@user:a").ToProto(), AfterResultCursor = Next() }).ToArray()));
            });
        permissions.Setup(p => p.LookupResources(It.IsAny<LookupResourcesRequest>(), It.IsAny<Metadata>(), It.IsAny<DateTime?>(), It.IsAny<CancellationToken>()))
            .Returns(() =>
            {
                Cap(++lookups);
                return MakeServerStreamingCall(new FakeStreamReader<LookupResourcesResponse>(Enumerable.Range(0, 2).Select(i => new LookupResourcesResponse
                { ResourceObjectId = i.ToString(), Permissionship = LookupPermissionship.HasPermission, AfterResultCursor = Next() }).ToArray()));
            });
        permissions.Setup(p => p.ExportBulkRelationships(It.IsAny<ExportBulkRelationshipsRequest>(), It.IsAny<Metadata>(), It.IsAny<DateTime?>(), It.IsAny<CancellationToken>()))
            .Returns(() =>
            {
                Cap(++exports);
                var response = new ExportBulkRelationshipsResponse { AfterResultCursor = Next() };
                response.Relationships.Add(Enumerable.Range(0, 2).Select(i => Relationship.FromTuple($"document:{i}#viewer@user:a").ToProto()));
                return MakeServerStreamingCall(new FakeStreamReader<ExportBulkRelationshipsResponse>([response]));
            });
        await using var client = Client(permissions.Object);
        var subject = SubjectRef.Direct(new ObjectRef("user", "a"));
        var resume = pageCursor == "same" ? "same" : null;

        var read = await Assert.ThrowsAsync<SpiceDBException>(async () => { await foreach (var _ in client.ReadRelationshipsAsync(Consistency.Full(), new Filter("document"), new ReadOptions { PageSize = 2, ResumeCursor = resume })) { } });
        var lookup = await Assert.ThrowsAsync<SpiceDBException>(async () => { await foreach (var _ in client.LookupResourcesAsync(Consistency.Full(), "document", "view", subject, new LookupOptions { PageSize = 2, ResumeCursor = resume })) { } });
        var export = await Assert.ThrowsAsync<SpiceDBException>(async () => { await foreach (var _ in client.ExportRelationshipsAsync(Consistency.Full(), new Filter("document"), new ExportOptions { PageSize = 2, ResumeCursor = resume })) { } });

        foreach (var error in new[] { read, lookup, export })
        {
            error.StatusCode.Should().Be(StatusCode.Internal);
            error.Message.Should().Contain("cursor did not advance");
        }
        (reads, lookups, exports).Should().Be((1, 1, 1));
    }

    [Fact]
    public async Task FullPageWithAnAdvancingCursorFetchesTheNextPage()
    {
        var sent = new List<string?>();
        var permissions = new Mock<PermissionsService.PermissionsServiceClient>();
        permissions.Setup(p => p.ReadRelationships(It.IsAny<ReadRelationshipsRequest>(), It.IsAny<Metadata>(), It.IsAny<DateTime?>(), It.IsAny<CancellationToken>()))
            .Returns((ReadRelationshipsRequest r, Metadata _, DateTime? _, CancellationToken _) =>
            {
                sent.Add(r.OptionalCursor?.Token);
                var rows = sent.Count == 1 ? 2 : 1;
                return MakeServerStreamingCall(new FakeStreamReader<ReadRelationshipsResponse>(Enumerable.Range(0, rows).Select(i => new ReadRelationshipsResponse
                { Relationship = Relationship.FromTuple($"document:{sent.Count}{i}#viewer@user:a").ToProto(), AfterResultCursor = new() { Token = $"p{sent.Count}" } }).ToArray()));
            });
        await using var client = Client(permissions.Object);

        var count = 0;
        await foreach (var _ in client.ReadRelationshipsAsync(Consistency.Full(), new Filter("document"), new ReadOptions { PageSize = 2 })) count++;
        count.Should().Be(3);
        sent.Should().Equal(null, "p1");
    }

    [Fact]
    public async Task ItemAndProgressErrorsReportTheServerStatusOnce()
    {
        var permissions = new Mock<PermissionsService.PermissionsServiceClient>();
        permissions.Setup(p => p.CheckBulkPermissionsAsync(It.IsAny<CheckBulkPermissionsRequest>(), It.IsAny<Metadata>(), It.IsAny<DateTime?>(), It.IsAny<CancellationToken>()))
            .Returns((CheckBulkPermissionsRequest request, Metadata _, DateTime? _, CancellationToken _) => MakeUnaryCall(new CheckBulkPermissionsResponse
            { CheckedAt = new() { Token = "rev" }, Pairs = { new CheckBulkPermissionsPair { Request = request.Items[0], Error = new Google.Rpc.Status { Code = (int)StatusCode.FailedPrecondition, Message = "no such permission" } } } }));
        var deletes = 0;
        permissions.Setup(p => p.DeleteRelationshipsAsync(It.IsAny<DeleteRelationshipsRequest>(), It.IsAny<Metadata>(), It.IsAny<DateTime?>(), It.IsAny<CancellationToken>()))
            .Returns(() => ++deletes == 1
                ? MakeUnaryCall(new DeleteRelationshipsResponse { DeletedAt = new() { Token = "d" }, RelationshipsDeletedCount = 1, DeletionProgress = DeleteRelationshipsResponse.Types.DeletionProgress.Partial })
                : Failing<DeleteRelationshipsResponse>(StatusCode.Unavailable, "gone"));
        await using var client = Client(permissions.Object);
        var check = new PermissionCheck(new ObjectRef("document", "1"), "view", SubjectRef.Direct(new ObjectRef("user", "a")), Consistency.Full());

        var item = await Assert.ThrowsAsync<FailedPreconditionException>(() => client.CheckBulkAsync([check]));
        item.InputIndex.Should().Be(0);
        (item.Message, item.ServerMessage, item.Status.Detail, item.ToRpcException().Status.Detail).Should().Be(("no such permission", "no such permission", "no such permission", "no such permission"));
        item.Status.StatusCode.Should().Be(StatusCode.FailedPrecondition);

        var progress = await Assert.ThrowsAsync<UnavailableException>(() => client.DeleteAllRelationshipsNonAtomicAsync(new Filter("document")));
        progress.GetDeleteProgress()!.AcknowledgedCount.Should().Be(1);
        (progress.Message, progress.Status.Detail).Should().Be(("gone", "gone"));
        progress.Status.StatusCode.Should().Be(StatusCode.Unavailable);
    }

    [Fact]
    public async Task CheckBulkConversionRoundTripsTheServersResponse()
    {
        var info = new ErrorInfo { Reason = "ERROR_REASON_UNKNOWN_RELATION_OR_PERMISSION", Domain = "authzed.com" };
        var permissions = new Mock<PermissionsService.PermissionsServiceClient>();
        permissions.Setup(p => p.CheckBulkPermissionsAsync(It.IsAny<CheckBulkPermissionsRequest>(), It.IsAny<Metadata>(), It.IsAny<DateTime?>(), It.IsAny<CancellationToken>()))
            .Returns((CheckBulkPermissionsRequest request, Metadata _, DateTime? _, CancellationToken _) => MakeUnaryCall(ServerResponse(request, info)));
        await using var client = Client(permissions.Object);
        PermissionCheck Check(string id, IReadOnlyDictionary<string, object?>? context = null) =>
            new(new ObjectRef("document", id), "view", SubjectRef.Direct(new ObjectRef("user", "a")), Consistency.Full(), context);
        var requests = new[] { Check("1"), Check("broken"), Check("3", new Dictionary<string, object?> { ["cidr"] = "10.0.0.0/8" }) };
        var options = new CheckOptions { ItemErrors = BulkItemErrors.Returned, Context = new Dictionary<string, object?> { ["region"] = "us" } };
        var results = await client.CheckBulkAsync(requests, options);

        var sent = new CheckBulkPermissionsRequest();
        sent.Items.AddRange(requests.Select(r => ProtoConvert.ToCheckBulkRequestItem(r, options)));
        ProtoConvert.ToCheckBulkResponse(requests, results, options).Should().Be(ServerResponse(sent, info));
        Assert.Throws<InvalidArgumentException>(() => ProtoConvert.ToCheckBulkResponse(requests[..1], results));
    }

    private static CheckBulkPermissionsResponse ServerResponse(CheckBulkPermissionsRequest request, ErrorInfo info)
    {
        var response = new CheckBulkPermissionsResponse { CheckedAt = new() { Token = "bulk-rev" } };
        foreach (var item in request.Items)
        {
            var pair = new CheckBulkPermissionsPair { Request = item };
            if (item.Resource.ObjectId == "broken")
            {
                pair.Error = new Google.Rpc.Status { Code = (int)StatusCode.FailedPrecondition, Message = "permission view not found" };
                pair.Error.Details.Add(Google.Protobuf.WellKnownTypes.Any.Pack(info));
            }
            else pair.Item = new() { Permissionship = CheckPermissionResponse.Types.Permissionship.ConditionalPermission, PartialCaveatInfo = new() { MissingRequiredContext = { "cidr" } } };
            response.Pairs.Add(pair);
        }
        return response;
    }

    [Fact]
    public void LookupSubjectConversionFillsTheDeprecatedFieldsTheServerSends()
    {
#pragma warning disable CS0612 // as SpiceDB sends it: the deprecated top-level fields mirror Subject
        var wire = new LookupSubjectsResponse
        {
            LookedUpAt = new() { Token = "rev" },
            Subject = new() { SubjectObjectId = "*", Permissionship = LookupPermissionship.ConditionalPermission, PartialCaveatInfo = new() { MissingRequiredContext = { "k" } } },
            SubjectObjectId = "*",
            Permissionship = LookupPermissionship.ConditionalPermission,
            PartialCaveatInfo = new() { MissingRequiredContext = { "k" } },
            ExcludedSubjects = { new Authzed.Api.V1.ResolvedSubject { SubjectObjectId = "bob", Permissionship = LookupPermissionship.HasPermission } },
            ExcludedSubjectIds = { "bob" },
        };
#pragma warning restore CS0612
        ProtoConvert.ToProto(ProtoConvert.FromProto(wire)).Should().Be(wire);
    }

    private static async IAsyncEnumerable<T> AsyncEnumerable<T>(params T[] items)
    {
        foreach (var item in items) { await Task.Yield(); yield return item; }
    }
}
