using Authzed.Api.V1;
using Google.Protobuf.WellKnownTypes;
using Grpc.Core;
using Grpc.Net.Client;
using Moq;
using Xunit;
using P = Authzed.Api.V1;
namespace SpiceDB.Client.Tests;

public class ResetBehaviorTests
{
    private static PermissionCheck Request(Consistency? consistency = null, IReadOnlyDictionary<string, object?>? context = null) => new(new("document", "one"), "view", SubjectRef.Direct(new("user", "alice")), consistency ?? Consistency.Full(), context);
    private static SpiceDBClient Client(Mock<PermissionsService.PermissionsServiceClient> permissions) => new(permissions.Object, new Mock<SchemaService.SchemaServiceClient>().Object, new Mock<WatchService.WatchServiceClient>().Object, new Mock<ExperimentalService.ExperimentalServiceClient>().Object);
    private static AsyncUnaryCall<T> Call<T>(T result) => new(Task.FromResult(result), Task.FromResult(new Metadata()), () => Grpc.Core.Status.DefaultSuccess, () => new Metadata(), () => { });
    private static AsyncUnaryCall<T> Call<T>(Task<T> result) => new(result, Task.FromResult(new Metadata()), () => Grpc.Core.Status.DefaultSuccess, () => new Metadata(), () => { });

    [Theory]
    [InlineData("reordered", 0)]
    [InlineData("duplicated", 1)]
    [InlineData("missing", 1)]
    [InlineData("context", 1)]
    [InlineData("permission", 1)]
    [InlineData("subject", 1)]
    public async Task BulkRejectsMismatchedEchoWithAbsoluteIndex(string corruption, int badIndex)
    {
        var calls = 0;
        var mock = new Mock<PermissionsService.PermissionsServiceClient>();
        mock.Setup(x => x.CheckBulkPermissionsAsync(It.IsAny<CheckBulkPermissionsRequest>(), It.IsAny<Metadata>(), It.IsAny<DateTime?>(), It.IsAny<CancellationToken>()))
            .Returns((CheckBulkPermissionsRequest r, Metadata _, DateTime? _, CancellationToken _) =>
            {
                var response = new CheckBulkPermissionsResponse();
                response.Pairs.AddRange(r.Items.Select(item => new CheckBulkPermissionsPair { Request = item.Clone(), Item = new() { Permissionship = CheckPermissionResponse.Types.Permissionship.HasPermission } }));
                if (++calls == 2)
                {
                    switch (corruption)
                    {
                        case "reordered":
                            response.Pairs[0].Request = r.Items[1].Clone();
                            response.Pairs[1].Request = r.Items[0].Clone(); break;
                        case "duplicated": response.Pairs[1].Request = r.Items[0].Clone(); break;
                        case "missing": response.Pairs[1].Request = null; break;
                        case "context": response.Pairs[1].Request.Context.Fields["x"] = Value.ForNumber(99); break;
                        case "permission": response.Pairs[1].Request.Permission = "edit"; break;
                        case "subject": response.Pairs[1].Request.Subject.OptionalRelation = "member"; break;
                    }
                }
                return Call(response);
            });
        await using var client = Client(mock);
        var requests = Enumerable.Range(0, 1002).Select(i => new PermissionCheck(new("document", i.ToString()), "view", SubjectRef.Direct(new("user", "alice")), Consistency.Full(), new Dictionary<string, object?> { ["x"] = i })).ToArray();
        var error = await Assert.ThrowsAsync<SpiceDBException>(() => client.CheckBulkAsync(requests));
        Assert.Equal(1000 + badIndex, error.InputIndex);
        Assert.Contains("request", error.Message);
    }

    [Fact]
    public async Task SingularTracingUsesSingularRpcAndPreservesRecursivePresence()
    {
        var proto = new P.DebugInformation
        {
            SchemaUsed = "definition user {}",
            Check = new P.CheckDebugTrace
            {
                Resource = new() { ObjectType = "document", ObjectId = "one" },
                Permission = "view",
                PermissionType = P.CheckDebugTrace.Types.PermissionType.Permission,
                Subject = new() { Object = new() { ObjectType = "user", ObjectId = "alice" } },
                Result = P.CheckDebugTrace.Types.Permissionship.ConditionalPermission,
                Duration = Duration.FromTimeSpan(TimeSpan.FromMilliseconds(12)),
                TraceOperationId = "operation",
                Source = "node",
                OptionalExpiresAt = Timestamp.FromDateTimeOffset(DateTimeOffset.Parse("2026-10-26T12:00:00Z")),
                CaveatEvaluationInfo = new()
                {
                    Expression = "x > 2",
                    CaveatName = "threshold",
                    Result = CaveatEvalInfo.Types.Result.MissingSomeContext,
                    Context = new Struct { Fields = { ["x"] = Value.ForNull() } },
                    PartialCaveatInfo = new() { MissingRequiredContext = { "x" } }
                },
                SubProblems = new() { Traces = { new P.CheckDebugTrace { WasCachedResult = false, Result = (P.CheckDebugTrace.Types.Permissionship)999, PermissionType = (P.CheckDebugTrace.Types.PermissionType)999 } } },
            },
        };
        var mock = new Mock<PermissionsService.PermissionsServiceClient>();
        mock.Setup(x => x.CheckPermissionAsync(It.IsAny<CheckPermissionRequest>(), It.IsAny<Metadata>(), It.IsAny<DateTime?>(), It.IsAny<CancellationToken>()))
            .Returns((CheckPermissionRequest r, Metadata _, DateTime? d, CancellationToken _) =>
            {
                Assert.True(r.WithTracing); Assert.NotNull(d); Assert.Null(r.Context);
                return Call(new CheckPermissionResponse { Permissionship = CheckPermissionResponse.Types.Permissionship.ConditionalPermission, CheckedAt = new() { Token = "revision" }, DebugTrace = proto, PartialCaveatInfo = new() { MissingRequiredContext = { "x" } } });
            });
        await using var client = Client(mock);
        var result = await client.CheckAsync(Request(), new CheckOptions { Tracing = CheckTracing.Enabled });
        Assert.False(result.HasPermission); Assert.Equal("revision", result.CheckedAt.Value); Assert.Equal(new[] { "x" }, result.MissingContext);
        Assert.Equal("definition user {}", result.DebugTrace!.SchemaUsed);
        var trace = result.DebugTrace.Check!;
        Assert.Equal(TimeSpan.FromMilliseconds(12), trace.Duration); Assert.Equal("operation", trace.TraceOperationId); Assert.Equal("node", trace.Source);
        Assert.Equal(DateTimeOffset.Parse("2026-10-26T12:00:00Z"), trace.Expiration);
        Assert.Equal("threshold", trace.CaveatEvaluation!.CaveatName); Assert.Null(trace.CaveatEvaluation.Context!["x"]);
        Assert.Null(trace.WasCachedResult); Assert.False(trace.SubProblems![0].WasCachedResult);
        Assert.Null(trace.SubProblems[0].SubProblems); Assert.Equal(Permissionship.Unspecified, trace.SubProblems[0].Result);
        Assert.Equal(TracePermissionType.Unspecified, trace.SubProblems[0].PermissionType);
        proto.Check.SubProblems.Traces.Clear(); Assert.Single(trace.SubProblems);
        mock.Verify(x => x.CheckBulkPermissionsAsync(It.IsAny<CheckBulkPermissionsRequest>(), It.IsAny<Metadata>(), It.IsAny<DateTime?>(), It.IsAny<CancellationToken>()), Times.Never);
    }
    [Fact]
    public async Task TracingDefaultsOffAndAbsentTraceRemainsAbsent()
    {
        var mock = new Mock<PermissionsService.PermissionsServiceClient>();
        mock.Setup(x => x.CheckPermissionAsync(It.IsAny<CheckPermissionRequest>(), It.IsAny<Metadata>(), It.IsAny<DateTime?>(), It.IsAny<CancellationToken>()))
            .Returns((CheckPermissionRequest r, Metadata _, DateTime? _, CancellationToken _) => { Assert.False(r.WithTracing); return Call(new CheckPermissionResponse { Permissionship = (CheckPermissionResponse.Types.Permissionship)999 }); });
        await using var client = Client(mock); var result = await client.CheckAsync(Request()); Assert.Null(result.DebugTrace); Assert.False(result.HasPermission);
    }
    [Fact]
    public async Task BulkTracingAndRevisionBelongToEachChunkAndItem()
    {
        int calls = 0;
        var mock = new Mock<PermissionsService.PermissionsServiceClient>();
        mock.Setup(x => x.CheckBulkPermissionsAsync(It.IsAny<CheckBulkPermissionsRequest>(), It.IsAny<Metadata>(), It.IsAny<DateTime?>(), It.IsAny<CancellationToken>()))
            .Returns((CheckBulkPermissionsRequest r, Metadata _, DateTime? _, CancellationToken _) =>
            {
                Assert.True(r.WithTracing); calls++;
                var response = new CheckBulkPermissionsResponse { CheckedAt = new() { Token = calls.ToString() } };
                response.Pairs.AddRange(r.Items.Select((item, index) => new CheckBulkPermissionsPair { Request = item.Clone(), Item = new() { DebugTrace = new() { SchemaUsed = $"{calls}:{index}" } } }));
                return Call(response);
            });
        await using var client = Client(mock);
        var results = await client.CheckBulkAsync(Enumerable.Range(0, 1001).Select(_ => Request()).ToArray(), new CheckOptions { Tracing = CheckTracing.Enabled });
        Assert.Equal(2, calls); Assert.Equal("1", results[999].CheckedAt.Value); Assert.Equal("2", results[1000].CheckedAt.Value); Assert.Equal("2:0", results[1000].DebugTrace!.SchemaUsed);
    }
    [Fact]
    public async Task MixedConsistencyFailsBeforeAnyRpcAndCancelledEmptyChecksCancel()
    {
        var mock = new Mock<PermissionsService.PermissionsServiceClient>(MockBehavior.Strict); await using var client = Client(mock);
        await Assert.ThrowsAsync<InvalidArgumentException>(() => client.CheckBulkAsync([Request(), Request(Consistency.MinLatency())]));
        await Assert.ThrowsAsync<InvalidArgumentException>(() => client.CheckBulkAsync([Request(Consistency.AtLeast(new("a"))), Request(Consistency.AtLeast(new("b")))]));
        using var cts = new CancellationTokenSource(); cts.Cancel();
        var error = await Assert.ThrowsAnyAsync<OperationCanceledException>(() => client.CheckAllAsync([], cancellationToken: cts.Token)); Assert.Equal(cts.Token, error.CancellationToken);
    }
    [Fact]
    public async Task BoundedDeletionNeverLoopsAndDeleteAllRetainsAcknowledgedProgress()
    {
        int calls = 0;
        var mock = new Mock<PermissionsService.PermissionsServiceClient>();
        mock.Setup(x => x.DeleteRelationshipsAsync(It.IsAny<DeleteRelationshipsRequest>(), It.IsAny<Metadata>(), It.IsAny<DateTime?>(), It.IsAny<CancellationToken>()))
            .Returns((DeleteRelationshipsRequest r, Metadata _, DateTime? _, CancellationToken _) =>
            {
                Assert.Equal(2u, r.OptionalLimit); Assert.True(r.OptionalAllowPartialDeletions);
                calls++;
                if (calls == 3) throw new RpcException(new Grpc.Core.Status(StatusCode.Unavailable, "lost response"));
                return Call(new DeleteRelationshipsResponse { RelationshipsDeletedCount = 2, DeletedAt = new() { Token = "ack" }, DeletionProgress = DeleteRelationshipsResponse.Types.DeletionProgress.Partial, AfterResultCursor = new() { Token = "cursor" } });
            });
        await using var client = Client(mock);
        var bounded = await client.DeleteRelationshipsAsync(new Filter("document"), 2);
        Assert.Equal(1, calls); Assert.False(bounded.Completed); Assert.Equal(2ul, bounded.DeletedCount);
        var error = await Assert.ThrowsAsync<UnavailableException>(() => client.DeleteAllRelationshipsNonAtomicAsync(new Filter("document"), new DeleteAllOptions { PageSize = 2 }));
        Assert.Equal(3, calls); Assert.Equal(new DeleteProgress(2, new ZedToken("ack"), "cursor", 1, true), error.DeleteProgress);
    }
    [Fact]
    public async Task CallerCancellationDuringDeleteAllCarriesNativeTokenAndProgress()
    {
        using var cts = new CancellationTokenSource(); int calls = 0;
        var mock = new Mock<PermissionsService.PermissionsServiceClient>();
        mock.Setup(x => x.DeleteRelationshipsAsync(It.IsAny<DeleteRelationshipsRequest>(), It.IsAny<Metadata>(), It.IsAny<DateTime?>(), It.IsAny<CancellationToken>()))
            .Returns((DeleteRelationshipsRequest _, Metadata _, DateTime? _, CancellationToken token) =>
            {
                if (++calls == 2) { cts.Cancel(); return Call(Task.FromCanceled<DeleteRelationshipsResponse>(token)); }
                return Call(new DeleteRelationshipsResponse { RelationshipsDeletedCount = 2, DeletedAt = new() { Token = "ack" }, DeletionProgress = DeleteRelationshipsResponse.Types.DeletionProgress.Partial });
            });
        await using var client = Client(mock);
        var error = await Assert.ThrowsAnyAsync<OperationCanceledException>(() => client.DeleteAllRelationshipsNonAtomicAsync(new Filter("document"), cancellationToken: cts.Token));
        Assert.Equal(cts.Token, error.CancellationToken);
        var progress = Assert.IsType<DeleteProgress>(error.GetDeleteProgress()); Assert.Equal(2ul, progress.AcknowledgedCount); Assert.Null(progress.LastCursor); Assert.True(progress.OutcomeUnknown);
    }
    [Fact]
    public async Task FractionalTimeoutMapsTimerCancellationToDeadlineExceeded()
    {
        var mock = new Mock<PermissionsService.PermissionsServiceClient>();
        mock.Setup(x => x.CheckPermissionAsync(It.IsAny<CheckPermissionRequest>(), It.IsAny<Metadata>(), It.IsAny<DateTime?>(), It.IsAny<CancellationToken>()))
            .Returns((CheckPermissionRequest _, Metadata _, DateTime? _, CancellationToken token) =>
            {
                async Task<CheckPermissionResponse> Respond()
                {
                    await Task.Delay(Timeout.InfiniteTimeSpan, token);
                    return new();
                }
                return Call(Respond());
            });
        await using var client = Client(mock);
        for (var attempt = 0; attempt < 20; attempt++)
            await Assert.ThrowsAsync<DeadlineExceededException>(() => client.CheckAsync(Request(), new CheckOptions { Timeout = TimeSpan.FromMilliseconds(5.9) }));
    }

    [Fact]
    public async Task TotalDeadlineDoesNotRestartAcrossCheckChunks()
    {
        int calls = 0; var deadlines = new List<DateTime>();
        var mock = new Mock<PermissionsService.PermissionsServiceClient>();
        mock.Setup(x => x.CheckBulkPermissionsAsync(It.IsAny<CheckBulkPermissionsRequest>(), It.IsAny<Metadata>(), It.IsAny<DateTime?>(), It.IsAny<CancellationToken>()))
            .Returns((CheckBulkPermissionsRequest r, Metadata _, DateTime? deadline, CancellationToken token) =>
            {
                calls++; deadlines.Add(deadline!.Value);
                async Task<CheckBulkPermissionsResponse> Respond()
                {
                    await Task.Delay(90, token);
                    var response = new CheckBulkPermissionsResponse(); response.Pairs.AddRange(r.Items.Select(item => new CheckBulkPermissionsPair { Request = item.Clone(), Item = new() })); return response;
                }
                return Call(Respond());
            });
        await using var client = Client(mock);
        await Assert.ThrowsAsync<DeadlineExceededException>(() => client.CheckBulkAsync(Enumerable.Range(0, 1001).Select(_ => Request()).ToArray(), new CheckOptions { Timeout = TimeSpan.FromMilliseconds(150) }));
        Assert.Equal(2, calls); Assert.True((deadlines[1] - deadlines[0]).Duration() < TimeSpan.FromMilliseconds(15));
    }
    [Fact]
    public void UnsupportedRoaringOptionsMustFailInsteadOfBeingDiscarded()
    {
        // Checked by the asynchronous companion test below; model snapshots are independent.
        var values = new List<Filter> { new("document") }; var options = new DeleteAllOptions { MustMatch = values }; values.Clear(); Assert.Single(options.MustMatch);
        Assert.Throws<InvalidArgumentException>(() => new CheckOptions { Context = new Dictionary<string, object?> { ["bad"] = double.NaN } });
        var cycle = new List<object?>(); cycle.Add(cycle);
        Assert.Throws<InvalidArgumentException>(() => Request(context: new Dictionary<string, object?> { ["cycle"] = cycle }));
    }
    [Fact]
    public async Task UnsupportedRoaringOptionsAreRejectedBeforeRpc()
    {
        await using var client = Client(new Mock<PermissionsService.PermissionsServiceClient>());
        await Assert.ThrowsAsync<InvalidArgumentException>(() => client.ExperimentalRoaringLookupResourcesAsync(Consistency.Full(), "document", "view", SubjectRef.Direct(new("user", "alice")), new LookupOptions { Debug = LookupDebug.Enabled }));
    }
    [Fact]
    public async Task BorrowedRemotePlaintextChannelNeedsExplicitCredentialOptIn()
    {
        using var channel = GrpcChannel.ForAddress("http://example.test:50051");
        Assert.Throws<InvalidArgumentException>(() => SpiceDBClient.CreateFromChannel(channel, "token"));
        await using var allowed = SpiceDBClient.CreateFromChannel(channel, "token", new ClientOptions { RemotePlaintextCredentials = RemotePlaintextCredentials.Allow });
    }

    [Fact]
    public async Task UnknownDeletionProgressPreservesAcknowledgedResponse()
    {
        var mock = new Mock<PermissionsService.PermissionsServiceClient>();
        mock.Setup(x => x.DeleteRelationshipsAsync(It.IsAny<DeleteRelationshipsRequest>(), It.IsAny<Metadata>(), It.IsAny<DateTime?>(), It.IsAny<CancellationToken>()))
            .Returns(Call(new DeleteRelationshipsResponse { RelationshipsDeletedCount = 3, DeletedAt = new() { Token = "ack" }, DeletionProgress = (DeleteRelationshipsResponse.Types.DeletionProgress)99 }));
        await using var client = Client(mock);
        var error = await Assert.ThrowsAsync<SpiceDBException>(() => client.DeleteAllRelationshipsNonAtomicAsync(new Filter("document")));
        Assert.Equal(new DeleteProgress(3, new ZedToken("ack"), null, 1, false), error.DeleteProgress);
    }
    [Fact]
    public async Task AlreadyCancelledDeleteAllHasZeroKnownProgress()
    {
        using var cts = new CancellationTokenSource(); cts.Cancel();
        await using var client = Client(new Mock<PermissionsService.PermissionsServiceClient>(MockBehavior.Strict));
        var error = await Assert.ThrowsAnyAsync<OperationCanceledException>(() => client.DeleteAllRelationshipsNonAtomicAsync(new Filter("document"), cancellationToken: cts.Token));
        Assert.Equal(new DeleteProgress(0, null, null, 0, false), error.GetDeleteProgress());
    }
    [Fact]
    public async Task CancellationDuringRetryNeverStartsAnotherAttempt()
    {
        using var cts = new CancellationTokenSource(); int calls = 0;
        var mock = new Mock<PermissionsService.PermissionsServiceClient>();
        mock.Setup(x => x.CheckPermissionAsync(It.IsAny<CheckPermissionRequest>(), It.IsAny<Metadata>(), It.IsAny<DateTime?>(), It.IsAny<CancellationToken>()))
            .Returns(() => { calls++; cts.Cancel(); throw new RpcException(new Grpc.Core.Status(StatusCode.Unavailable, "retry")); });
        await using var client = Client(mock);
        var error = await Assert.ThrowsAnyAsync<OperationCanceledException>(() => client.CheckAsync(Request(), cancellationToken: cts.Token));
        Assert.Equal(cts.Token, error.CancellationToken); Assert.Equal(1, calls);
    }
    [Fact]
    public async Task RetriesRetainOriginalDeadline()
    {
        int calls = 0; var deadlines = new List<DateTime>();
        var mock = new Mock<PermissionsService.PermissionsServiceClient>();
        mock.Setup(x => x.CheckPermissionAsync(It.IsAny<CheckPermissionRequest>(), It.IsAny<Metadata>(), It.IsAny<DateTime?>(), It.IsAny<CancellationToken>()))
            .Returns((CheckPermissionRequest _, Metadata _, DateTime? deadline, CancellationToken _) =>
            {
                calls++; deadlines.Add(deadline!.Value);
                if (calls < 3) throw new RpcException(new Grpc.Core.Status(StatusCode.Aborted, "retry"));
                return Call(new CheckPermissionResponse());
            });
        await using var client = Client(mock); await client.CheckAsync(Request());
        Assert.Equal(3, calls); Assert.All(deadlines, d => Assert.True((d - deadlines[0]).Duration() < TimeSpan.FromMilliseconds(15)));
    }
}
