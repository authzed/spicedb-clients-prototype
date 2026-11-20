using Authzed.Api.V1;
using FluentAssertions;
using Grpc.Core;
using Moq;
using SpiceDB.Client.Testing;
using Xunit;
using static SpiceDB.Client.Tests.StreamingTestHelpers;
using Consistency = SpiceDB.Client.Consistency;
using Relationship = SpiceDB.Client.Relationship;

namespace SpiceDB.Client.Tests;

/// <summary>
/// LookupResources asks for batched responses and still yields one resource at a time: request
/// batch sizes, both wire forms, result-counted paging, batch-end cursors, limit alignment,
/// malformed batch results and the in-memory double's matching behavior.
/// </summary>
public class LookupBatchingTests
{
    private static readonly SubjectRef Alice = new ObjectRef("user", "alice");
    private const LookupPermissionship Has = LookupPermissionship.HasPermission;
    private const LookupPermissionship Conditional = LookupPermissionship.ConditionalPermission;

    /// <summary>
    /// A server holding one result per permissionship, named doc0, doc1, ... Cursors are the
    /// decimal index to resume at. It batches as SpiceDB does (a batch every batch-size results
    /// and a final partial batch at the end of the stream), or answers one resource per message
    /// when the request asks for no batching or <paramref name="batches"/> is false.
    /// </summary>
    private static (SpiceDBClient Client, List<LookupResourcesRequest> Requests) Server(LookupPermissionship[] values, bool batches = true)
    {
        var requests = new List<LookupResourcesRequest>();
        var mock = new Mock<PermissionsService.PermissionsServiceClient>();
        mock.Setup(c => c.LookupResources(It.IsAny<LookupResourcesRequest>(), It.IsAny<Metadata>(), It.IsAny<DateTime?>(), It.IsAny<CancellationToken>()))
            .Returns((LookupResourcesRequest request, Metadata _, DateTime? _, CancellationToken _) =>
            {
                requests.Add(request.Clone());
                var start = request.OptionalCursor is null ? 0 : int.Parse(request.OptionalCursor.Token);
                var end = request.OptionalLimit == 0 ? values.Length : Math.Min(values.Length, start + (int)request.OptionalLimit);
                var size = batches ? (int)request.OptionalResponseBatchSize : 0;
                var responses = new List<LookupResourcesResponse>();
                for (var i = start; i < end; i += Math.Max(size, 1))
                {
                    if (size == 0)
                    {
                        responses.Add(new() { ResourceObjectId = "doc" + i, Permissionship = values[i], AfterResultCursor = new() { Token = (i + 1).ToString() }, LookedUpAt = new() { Token = "revision" } });
                        continue;
                    }
                    var batchEnd = Math.Min(end, i + size);
                    var batch = new LookupResourcesBatch { AfterCursor = new() { Token = batchEnd.ToString() } };
                    for (var j = i; j < batchEnd; j++)
                    {
                        var result = new LookupResourcesResult { ResourceObjectId = "doc" + j, Permissionship = values[j] };
                        if (values[j] == Conditional) result.PartialCaveatInfo = new() { MissingRequiredContext = { "ip" } };
                        batch.Results.Add(result);
                    }
                    responses.Add(new() { Batched = batch, LookedUpAt = new() { Token = "revision" } });
                }
                return MakeServerStreamingCall(new FakeStreamReader<LookupResourcesResponse>(responses.ToArray()));
            });
        return (Client(mock), requests);
    }

    private static SpiceDBClient Client(Mock<PermissionsService.PermissionsServiceClient> permissions) =>
        new(permissions.Object, new Mock<SchemaService.SchemaServiceClient>().Object, new Mock<WatchService.WatchServiceClient>().Object, new Mock<ExperimentalService.ExperimentalServiceClient>().Object);

    private static SpiceDBClient Scripted(params LookupResourcesResponse[] responses)
    {
        var mock = new Mock<PermissionsService.PermissionsServiceClient>();
        mock.Setup(c => c.LookupResources(It.IsAny<LookupResourcesRequest>(), It.IsAny<Metadata>(), It.IsAny<DateTime?>(), It.IsAny<CancellationToken>()))
            .Returns(() => MakeServerStreamingCall(new FakeStreamReader<LookupResourcesResponse>(responses)));
        return Client(mock);
    }

    private static LookupPermissionship[] Granted(int count) => Enumerable.Repeat(Has, count).ToArray();

    private static async Task<List<LookupResource>> Lookup(ISpiceDBClient client, LookupOptions? options = null)
    {
        var results = new List<LookupResource>();
        await foreach (var r in client.LookupResourcesAsync(Consistency.Full(), "document", "view", Alice, options)) results.Add(r);
        return results;
    }

    private static string[] Ids(int from, int to) => Enumerable.Range(from, to - from).Select(i => "doc" + i).ToArray();

    [Fact]
    public async Task RequestCarriesTheDefaultExplicitOrNoBatchSize()
    {
        var (client, requests) = Server([]);
        await using var _ = client;

        await Lookup(client);
        await Lookup(client, new LookupOptions { PageSize = 5000 });
        await Lookup(client, new LookupOptions { PageSize = 20, ResponseBatchSize = 7 });
        await Lookup(client, new LookupOptions { ResponseBatchSize = 5000 });
        await Lookup(client, new LookupOptions { Batching = LookupBatching.Disabled });
        await Lookup(client, new LookupOptions { Batching = LookupBatching.Disabled, ResponseBatchSize = 7 });
        await Lookup(client, new LookupOptions { Paging = Paging.SingleRequest });
        await Lookup(client, new LookupOptions { Paging = Paging.SingleRequest, Limit = 40 });
        await Lookup(client, new LookupOptions { Paging = Paging.SingleRequest, Limit = 4000 });

        requests.Select(r => r.OptionalResponseBatchSize).Should().Equal(
            [512u, 1000u, 7u, 5000u, 0u, 0u, 1000u, 40u, 1000u],
            "the default is the page size (in single-request mode the limit) capped at 1,000; an explicit size is sent as given, never clamped");
        requests.Select(r => r.OptionalLimit).Should().Equal([512u, 5000u, 20u, 512u, 512u, 512u, 0u, 40u, 4000u], "the wire limit is unchanged by batching");
    }

    [Theory]
    [InlineData(0)]
    [InlineData(-1)]
    public async Task NonPositiveBatchSizeIsALocalError(int size)
    {
        var (client, requests) = Server(Granted(3));
        await using var _ = client;
        // Rejected even when batching is disabled, like PageSize under single-request paging.
        foreach (var batching in new[] { LookupBatching.Automatic, LookupBatching.Disabled })
        {
            var error = await Assert.ThrowsAsync<InvalidArgumentException>(() => Lookup(client, new LookupOptions { ResponseBatchSize = size, Batching = batching }));
            error.Message.Should().Be("ResponseBatchSize must be positive.");
            await Assert.ThrowsAsync<InvalidArgumentException>(() => Lookup(new InMemorySpiceDBClient(), new LookupOptions { ResponseBatchSize = size, Batching = batching }));
        }
        requests.Should().BeEmpty();
        Assert.Throws<InvalidArgumentException>(() => new LookupOptions { Batching = (LookupBatching)9 });
    }

    [Fact]
    public async Task BatchingOptionsAreRejectedOutsideLookupResources()
    {
        var (client, _) = Server([]);
        await using var _1 = client;
        foreach (var options in new[] { new LookupOptions { ResponseBatchSize = 10 }, new LookupOptions { Batching = LookupBatching.Disabled } })
        {
            foreach (var target in new ISpiceDBClient[] { client, new InMemorySpiceDBClient() })
                await Assert.ThrowsAsync<InvalidArgumentException>(async () => { await foreach (var _ in target.LookupSubjectsAsync(Consistency.Full(), new ObjectRef("document", "1"), "view", "user", options)) { } });
            await Assert.ThrowsAsync<InvalidArgumentException>(() => client.ExperimentalRoaringLookupResourcesAsync(Consistency.Full(), "document", "view", Alice, options));
        }
    }

    [Fact]
    public async Task BatchedStreamYieldsEveryResultInOrderWithBatchEndCursors()
    {
        var (client, requests) = Server([Has, Conditional, Has, Has, Has, Conditional, Has]);
        await using var _ = client;

        var results = await Lookup(client, new LookupOptions { ResponseBatchSize = 3 });

        results.Select(r => r.ResourceId).Should().Equal(Ids(0, 7));
        results.Select(r => r.Permissionship).Should().Equal(Permissionship.HasPermission, Permissionship.ConditionalPermission, Permissionship.HasPermission,
            Permissionship.HasPermission, Permissionship.HasPermission, Permissionship.ConditionalPermission, Permissionship.HasPermission);
        results[1].PartialCaveat!.MissingRequiredContext.Should().Equal("ip");
        results[0].PartialCaveat.Should().BeNull();
        results.Should().OnlyContain(r => r.LookedUpAt.Value == "revision");
        results.Select(r => r.AfterResultCursor).Should().Equal([null, null, "3", null, null, "6", "7"], "only the last item of each batch carries the batch's cursor");
        requests.Should().ContainSingle("7 results are fewer than a page");
    }

    [Fact]
    public async Task PagingOverBatchesCountsResultsAndSendsTheBatchCursor()
    {
        var (client, requests) = Server(Granted(10));
        await using var _ = client;

        var results = await Lookup(client, new LookupOptions { PageSize = 4, ResponseBatchSize = 3 });

        results.Select(r => r.ResourceId).Should().Equal(Ids(0, 10));
        // Each page is two messages (3 + 1 results); only the result count reaches the page size.
        results.Select(r => r.AfterResultCursor).Should().Equal([null, null, "3", "4", null, null, "7", "8", null, "10"]);
        requests.Select(r => r.OptionalCursor?.Token).Should().Equal([null, "4", "8"], "the next page resumes from the last batch's cursor; the short third page ends the read");
        requests.Should().OnlyContain(r => r.OptionalLimit == 4 && r.OptionalResponseBatchSize == 3);
    }

    [Fact]
    public async Task FullLastPageFetchesOneMoreEmptyPage()
    {
        var (client, requests) = Server(Granted(8));
        await using var _ = client;
        (await Lookup(client, new LookupOptions { PageSize = 4 })).Should().HaveCount(8);
        requests.Select(r => r.OptionalCursor?.Token).Should().Equal(null, "4", "8");
    }

    [Fact]
    public async Task ServerThatIgnoresBatchingStillWorks()
    {
        var (client, requests) = Server(Granted(5), batches: false);
        await using var _ = client;

        var results = await Lookup(client, new LookupOptions { PageSize = 2 });

        results.Select(r => r.ResourceId).Should().Equal(Ids(0, 5));
        results.Select(r => r.AfterResultCursor).Should().Equal("1", "2", "3", "4", "5");
        requests.Should().HaveCount(3).And.OnlyContain(r => r.OptionalResponseBatchSize == 2);
    }

    [Fact]
    public async Task FilterMovesTheCursorToTheLastKeptItemOfABatch()
    {
        // Batches of 3: [has, has, cond] [cond, cond, cond] [has, cond, has] [cond]
        var (client, requests) = Server([Has, Has, Conditional, Conditional, Conditional, Conditional, Has, Conditional, Has, Conditional]);
        await using var _ = client;

        var results = await Lookup(client, new LookupOptions { PageSize = 6, ResponseBatchSize = 3, PermissionshipFilter = LookupPermissionshipFilter.HasPermissionOnly });

        results.Select(r => (r.ResourceId, r.AfterResultCursor)).Should().Equal(
            ("doc0", null), ("doc1", "3"), ("doc6", null), ("doc8", "9"));
        requests.Select(r => r.OptionalCursor?.Token).Should().Equal([null, "6"], "a batch the filter drops entirely still advances paging");
    }

    [Theory]
    [InlineData(10, 4, 10)] // limit 10, page 4: pages of 4, 4, then 2 remaining -> batch of 2
    [InlineData(3, 512, 3)]
    [InlineData(7, 3, 7)]
    [InlineData(12, 512, 12)]
    public async Task LimitedCallEndsOnACursor(int limit, int pageSize, int expected)
    {
        var (client, _) = Server(Granted(50));
        await using var _1 = client;

        var first = await Lookup(client, new LookupOptions { PageSize = pageSize, Limit = limit });

        first.Select(r => r.ResourceId).Should().Equal(Ids(0, expected));
        first[^1].AfterResultCursor.Should().Be(expected.ToString());
        var rest = await Lookup(client, new LookupOptions { PageSize = pageSize, ResumeCursor = first[^1].AfterResultCursor });
        rest.Select(r => r.ResourceId).Should().Equal(Ids(expected, 50));
    }

    [Fact]
    public async Task PerPageBatchSizeFollowsTheRemainingLimit()
    {
        var (client, requests) = Server(Granted(50));
        await using var _ = client;

        // remaining < batch size: the batch shrinks to the remaining count.
        (await Lookup(client, new LookupOptions { PageSize = 4, Limit = 10 }))[^1].AfterResultCursor.Should().Be("10");
        requests.Select(r => r.OptionalResponseBatchSize).Should().Equal(4u, 4u, 2u);
        requests.Should().OnlyContain(r => r.OptionalLimit == 4, "the wire limit stays the page size");

        // remaining a multiple of the batch size: unchanged.
        requests.Clear();
        (await Lookup(client, new LookupOptions { PageSize = 8, ResponseBatchSize = 2, Limit = 12 }))[^1].AfterResultCursor.Should().Be("12");
        requests.Select(r => r.OptionalResponseBatchSize).Should().Equal(2u, 2u);

        // neither: that page is not batched, so every item on it has a cursor.
        requests.Clear();
        var unbatched = await Lookup(client, new LookupOptions { PageSize = 8, ResponseBatchSize = 3, Limit = 13 });
        requests.Select(r => r.OptionalResponseBatchSize).Should().Equal(3u, 0u);
        unbatched.Skip(8).Select(r => r.AfterResultCursor).Should().Equal("9", "10", "11", "12", "13");

        // a limit with the unconditional-only filter: never batched.
        requests.Clear();
        var grants = await Lookup(client, new LookupOptions { PageSize = 4, Limit = 6, PermissionshipFilter = LookupPermissionshipFilter.HasPermissionOnly });
        requests.Should().HaveCount(2).And.OnlyContain(r => r.OptionalResponseBatchSize == 0);
        grants.Should().OnlyContain(r => r.AfterResultCursor != null);

        // the filter without a limit stays batched.
        requests.Clear();
        await Lookup(client, new LookupOptions { PageSize = 100, PermissionshipFilter = LookupPermissionshipFilter.HasPermissionOnly });
        requests.Should().ContainSingle().Which.OptionalResponseBatchSize.Should().Be(100u);
    }

    [Theory]
    // batch, single, page, remaining, unconditionalOnly -> expected
    [InlineData(512, false, 512, null, false, 512)]
    [InlineData(512, false, 512, null, true, 512)]
    [InlineData(0, false, 512, 5, false, 0)]
    [InlineData(512, false, 512, 5, false, 5)]
    [InlineData(4, false, 8, 3, false, 3)]
    [InlineData(2, false, 8, 6, false, 2)]
    [InlineData(4, false, 8, 6, false, 0)]
    [InlineData(4, false, 8, 8, false, 4)]
    [InlineData(3, false, 8, 20, false, 3)]
    [InlineData(4, false, 8, 3, true, 0)]
    [InlineData(4, false, 8, 100, true, 0)]
    [InlineData(1000, true, 512, 5000, false, 1000)]
    [InlineData(7, true, 512, 5, true, 7)]
    public void ForPageAlignsTheLastBatchWithTheLimit(int batch, bool single, int page, int? remaining, bool unconditionalOnly, int expected) =>
        LookupBatch.ForPage(batch, single, page, remaining, unconditionalOnly).Should().Be(expected);

    [Fact]
    public async Task EmptyBatchYieldsNothing()
    {
        var full = new LookupResourcesBatch { AfterCursor = new() { Token = "c1" } };
        full.Results.Add(new LookupResourcesResult { ResourceObjectId = "doc0", Permissionship = Has });
        await using var client = Scripted(new() { Batched = new(), LookedUpAt = new() { Token = "revision" } }, new() { Batched = full, LookedUpAt = new() { Token = "revision" } });
        (await Lookup(client)).Select(r => (r.ResourceId, r.AfterResultCursor)).Should().Equal(("doc0", "c1"));
    }

    [Theory]
    [InlineData(false, "missing resource object id")]
    [InlineData(true, "permissionship is unset")]
    public async Task MalformedBatchResultFailsWithInternal(bool unsetPermissionship, string message)
    {
        var batch = new LookupResourcesBatch { AfterCursor = new() { Token = "c2" } };
        batch.Results.Add(new LookupResourcesResult { ResourceObjectId = "doc0", Permissionship = Has });
        batch.Results.Add(unsetPermissionship ? new LookupResourcesResult { ResourceObjectId = "doc1" } : new LookupResourcesResult { Permissionship = Has });

        foreach (var filter in new[] { LookupPermissionshipFilter.All, LookupPermissionshipFilter.HasPermissionOnly })
        {
            await using var client = Scripted(new LookupResourcesResponse { Batched = batch, LookedUpAt = new() { Token = "revision" } });
            var results = new List<LookupResource>();
            var error = await Assert.ThrowsAsync<SpiceDBException>(async () =>
            {
                await foreach (var r in client.LookupResourcesAsync(Consistency.Full(), "document", "view", Alice, new LookupOptions { PermissionshipFilter = filter })) results.Add(r);
            });
            error.StatusCode.Should().Be(StatusCode.Internal);
            error.Message.Should().Contain("client detected malformed response from server").And.Contain(message);
            results.Select(r => r.ResourceId).Should().Equal(["doc0"], "the good result is yielded and the bad one fails the lookup, never skipped");
        }
    }

    [Fact]
    public async Task UnknownPermissionshipInABatchMapsToUnspecified()
    {
        var batch = new LookupResourcesBatch { AfterCursor = new() { Token = "c1" } };
        batch.Results.Add(new LookupResourcesResult { ResourceObjectId = "doc0", Permissionship = (LookupPermissionship)99 });
        await using var client = Scripted(new LookupResourcesResponse { Batched = batch });
        (await Lookup(client)).Single().Permissionship.Should().Be(Permissionship.Unspecified);
    }

    private static async Task<InMemorySpiceDBClient> Double(int documents, params int[] caveated)
    {
        var store = new InMemorySpiceDBClient();
        var txn = new Transaction();
        for (var i = 0; i < documents; i++)
            txn.Touch(Relationship.FromTuple($"document:doc{i}#view@user:alice" + (caveated.Contains(i) ? "[only_weekdays]" : "")));
        await store.WriteAsync(txn);
        return store;
    }

    [Fact]
    public async Task DoubleBatchesLikeAServerAndResumesFromBatchEnds()
    {
        var store = await Double(10);
        var options = new LookupOptions { PageSize = 4, ResponseBatchSize = 3 };

        var all = await Lookup(store, options);

        all.Select(r => r.ResourceId).Should().Equal(Ids(0, 10));
        string? At(int n) => InMemorySpiceDBClient.CursorAfter(n);
        all.Select(r => r.AfterResultCursor).Should().Equal([null, null, At(3), At(4), null, null, At(7), At(8), null, At(10)], "the same pages and batches as the client test against a batching server");

        var rest = await Lookup(store, new LookupOptions { PageSize = 4, ResponseBatchSize = 3, ResumeCursor = all[2].AfterResultCursor });
        rest.Select(r => r.ResourceId).Should().Equal(Ids(3, 10));

        // The default batch is the page: one cursor, on the last item.
        (await Lookup(store)).Select(r => r.AfterResultCursor).Should().Equal([null, null, null, null, null, null, null, null, null, At(10)]);
    }

    [Fact]
    public async Task DoubleWithBatchingDisabledPutsACursorOnEveryItem()
    {
        var store = await Double(5);
        var all = await Lookup(store, new LookupOptions { Batching = LookupBatching.Disabled });
        all.Select(r => r.AfterResultCursor).Should().Equal(Enumerable.Range(1, 5).Select(InMemorySpiceDBClient.CursorAfter));
        (await Lookup(store, new LookupOptions { Batching = LookupBatching.Disabled, ResumeCursor = all[1].AfterResultCursor })).Select(r => r.ResourceId).Should().Equal(Ids(2, 5));
    }

    [Fact]
    public async Task DoubleAlignsLimitsAndFiltersLikeTheClient()
    {
        var store = await Double(10, 2, 3, 4, 5, 7, 9);

        // A limited call ends on a cursor, and resuming from it returns the rest exactly.
        var first = await Lookup(store, new LookupOptions { PageSize = 4, Limit = 6 });
        first.Select(r => r.AfterResultCursor).Should().Equal([null, null, null, InMemorySpiceDBClient.CursorAfter(4), null, InMemorySpiceDBClient.CursorAfter(6)]);
        (await Lookup(store, new LookupOptions { PageSize = 4, ResumeCursor = first[^1].AfterResultCursor })).Select(r => r.ResourceId).Should().Equal(Ids(6, 10));

        // Batches of 3: [has, has, cond] [cond, cond, cond] [has, cond, has] [cond]. The cursor is
        // the batch's, on the last item the filter keeps.
        var grants = await Lookup(store, new LookupOptions { PageSize = 6, ResponseBatchSize = 3, PermissionshipFilter = LookupPermissionshipFilter.HasPermissionOnly });
        grants.Select(r => (r.ResourceId, r.AfterResultCursor)).Should().Equal(
            ("doc0", null), ("doc1", InMemorySpiceDBClient.CursorAfter(3)), ("doc6", null), ("doc8", InMemorySpiceDBClient.CursorAfter(9)));

        // A limit with the filter is not batched: every yielded item has its own cursor.
        var limited = await Lookup(store, new LookupOptions { Limit = 3, PermissionshipFilter = LookupPermissionshipFilter.HasPermissionOnly });
        limited.Select(r => (r.ResourceId, r.AfterResultCursor)).Should().Equal(
            ("doc0", InMemorySpiceDBClient.CursorAfter(1)), ("doc1", InMemorySpiceDBClient.CursorAfter(2)), ("doc6", InMemorySpiceDBClient.CursorAfter(7)));
    }
}
