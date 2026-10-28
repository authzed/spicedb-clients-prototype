using System.Net;
using System.Text.Json;
using Authzed.Api.V1;
using FluentAssertions;
using Google.Protobuf;
using Google.Rpc;
using Grpc.Core;
using Grpc.Net.Client;
using Microsoft.AspNetCore.Builder;
using Microsoft.AspNetCore.Hosting;
using Microsoft.AspNetCore.Hosting.Server;
using Microsoft.AspNetCore.Hosting.Server.Features;
using Microsoft.AspNetCore.Server.Kestrel.Core;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.Logging;
using Moq;
using SpiceDB.Client.Protocol;
using Xunit;
using static SpiceDB.Client.Tests.StreamingTestHelpers;
using GrpcStatus = Grpc.Core.Status;
using Relationship = SpiceDB.Client.Relationship;
using RelationshipUpdate = SpiceDB.Client.RelationshipUpdate;
using Precondition = SpiceDB.Client.Precondition;
using ZedToken = SpiceDB.Client.ZedToken;
using Consistency = SpiceDB.Client.Consistency;
using LookupSubject = SpiceDB.Client.LookupSubject;

namespace SpiceDB.Client.Tests;

/// <summary>Adoption items N1-N17 that need no server; real loopback gRPC where transport behavior matters.</summary>
public class AdoptionNativeTests
{
    private static SpiceDBClient Client(PermissionsService.PermissionsServiceClient? permissions = null, SchemaService.SchemaServiceClient? schema = null, WatchService.WatchServiceClient? watch = null, ClientOptions? options = null) =>
        new(permissions ?? new Mock<PermissionsService.PermissionsServiceClient>().Object, schema ?? new Mock<SchemaService.SchemaServiceClient>().Object,
            watch ?? new Mock<WatchService.WatchServiceClient>().Object, new Mock<ExperimentalService.ExperimentalServiceClient>().Object, null, options);

    private static AsyncUnaryCall<T> Failing<T>(StatusCode code, string message = "boom") =>
        new(Task.FromException<T>(new RpcException(new GrpcStatus(code, message))), Task.FromResult(new Metadata()), () => new GrpcStatus(code, message), () => new Metadata(), () => { });

    // ── N1/N2/N5: error interop ───────────────────────────────────────────

    [Theory]
    [InlineData(StatusCode.NotFound, typeof(NotFoundException))]
    [InlineData(StatusCode.FailedPrecondition, typeof(FailedPreconditionException))]
    [InlineData(StatusCode.AlreadyExists, typeof(AlreadyExistsException))]
    [InlineData(StatusCode.Internal, typeof(SpiceDBException))]
    [InlineData(StatusCode.Unauthenticated, typeof(UnauthenticatedException))]
    public void CreateBuildsTheTypedErrorWithMatchingStatus(StatusCode code, Type expected)
    {
        var error = SpiceDBException.Create(code, "server said this");
        error.Should().BeOfType(expected);
        error.StatusCode.Should().Be(code);
        error.ServerMessage.Should().Be("server said this");
        error.Status.StatusCode.Should().Be(code);
        error.ToRpcException().StatusCode.Should().Be(code);
        SpiceDBException.CodeOf(error).Should().Be(code);
    }

    [Fact]
    public void LocallyRaisedAndCallerConstructedErrorsReportTheirCategoryCode()
    {
        new InvalidArgumentException("bad").StatusCode.Should().Be(StatusCode.InvalidArgument);
        new InvalidArgumentException("bad").ServerMessage.Should().BeNull();
        new DeadlineExceededException("late").Status.StatusCode.Should().Be(StatusCode.DeadlineExceeded);
        new NotFoundException("gone").ToRpcException().Status.Detail.Should().Be("gone");
        new SpiceDBException("plain").StatusCode.Should().Be(StatusCode.Unknown);
        SpiceDBException.CodeOf(new OperationCanceledException()).Should().Be(StatusCode.Cancelled);
        SpiceDBException.CodeOf(new RpcException(new GrpcStatus(StatusCode.Aborted, "x"))).Should().Be(StatusCode.Aborted);
        SpiceDBException.CodeOf(new InvalidOperationException("wrap", new RpcException(new GrpcStatus(StatusCode.NotFound, "x")))).Should().Be(StatusCode.NotFound);
        SpiceDBException.CodeOf(null).Should().Be(StatusCode.Unknown);
        SpiceDBException.FromRpcException(new RpcException(new GrpcStatus(StatusCode.FailedPrecondition, "x"))).Should().BeOfType<FailedPreconditionException>();
    }

    [Fact]
    public async Task ServerErrorsKeepVerbatimMessageStatusAndDetails()
    {
        var info = new ErrorInfo { Reason = "ERROR_REASON_X", Domain = "authzed.com" };
        var rpcStatus = new Google.Rpc.Status { Code = (int)StatusCode.FailedPrecondition, Message = "precondition failed: exact text" };
        rpcStatus.Details.Add(Google.Protobuf.WellKnownTypes.Any.Pack(info));
        var rpc = rpcStatus.ToRpcException();
        var schema = new Mock<SchemaService.SchemaServiceClient>();
        schema.Setup(s => s.ReadSchemaAsync(It.IsAny<ReadSchemaRequest>(), It.IsAny<Metadata>(), It.IsAny<DateTime?>(), It.IsAny<CancellationToken>()))
            .Returns(new AsyncUnaryCall<ReadSchemaResponse>(Task.FromException<ReadSchemaResponse>(rpc), Task.FromResult(new Metadata()), () => rpc.Status, () => rpc.Trailers, () => { }));
        await using var client = Client(schema: schema.Object);
        var error = await Assert.ThrowsAsync<FailedPreconditionException>(() => client.ReadSchemaAsync());
        error.ServerMessage.Should().Be("precondition failed: exact text");
        error.StatusCode.Should().Be(StatusCode.FailedPrecondition);
        error.Status.Should().Be(rpc.Status);
        error.ToRpcException().GetRpcStatus()!.GetDetail<ErrorInfo>()!.Reason.Should().Be("ERROR_REASON_X");
    }

    // ── N4/N17: observable values, tuples, copy helpers ───────────────────

    [Theory]
    [InlineData("document:readme#viewer@user:alice")]
    [InlineData("document:readme#viewer@team:eng#member")]
    [InlineData("document:readme#viewer@user:*")]
    [InlineData("document:readme#viewer@user:alice[only_name]")]
    [InlineData("document:readme#viewer@user:alice[ip_range:{\"cidr\":\"10.0.0.0/8\",\"list\":[\"a\",\"]\"],\"n\":1.5,\"nested\":{\"b\":true}}]")]
    [InlineData("document:readme#viewer@user:alice[expiration:2030-01-02T03:04:05Z]")]
    [InlineData("document:readme#viewer@user:alice[expiration:2030-01-02T03:04:05.1234567Z]")]
    [InlineData("document:readme#viewer@team:eng#member[ip_range:{\"cidr\":\"x\"}][expiration:2030-01-02T03:04:05Z]")]
    public void TupleSyntaxRoundTrips(string tuple)
    {
        var parsed = Relationship.FromTuple(tuple);
        parsed.ToString().Should().Be(tuple);
        Relationship.FromTuple(parsed.ToString()).ToString().Should().Be(tuple);
    }

    [Fact]
    public void TupleParsingCoversCaveatContextExpirationAndEllipsis()
    {
        var r = Relationship.FromTuple("document:readme#viewer@team:eng#...[ip_range:{\"cidr\":\"]\",\"n\":2}][expiration:2030-01-02T03:04:05.123456789Z]");
        r.SubjectRelation.Should().BeNull();
        r.CaveatName.Should().Be("ip_range");
        r.CaveatContext!["cidr"].Should().Be("]");
        r.CaveatContext!["n"].Should().Be(2.0);
        r.Expiration.Should().Be(new DateTimeOffset(2030, 1, 2, 3, 4, 5, TimeSpan.Zero).AddTicks(1234568), "nanoseconds round to 100ns ticks");
        Assert.Throws<InvalidArgumentException>(() => Relationship.FromTuple("document:a#v@user:b[expiration:nope]"));
        Assert.Throws<InvalidArgumentException>(() => Relationship.FromTuple("document:a#v@user:b[cav:{\"a\":1}"));
        Assert.Throws<InvalidArgumentException>(() => Relationship.FromTuple("document:a#v@user:b[cav:[1]]"));
    }

    [Fact]
    public void ShortAccessorsAndCopyHelpers()
    {
        var r = Relationship.FromTuple("document:readme#viewer@team:eng#member[c]");
        (r.ResourceType, r.ResourceId, r.SubjectType, r.SubjectId, r.SubjectRelation).Should().Be(("document", "readme", "team", "eng", "member"));
        var moved = r.WithResource(new ObjectRef("folder", "x")).WithRelation("owner").WithSubject(SubjectRef.Direct(new ObjectRef("user", "bob")));
        moved.ToString().Should().Be("folder:x#owner@user:bob[c]");
        r.ToString().Should().Be("document:readme#viewer@team:eng#member[c]");
        new ObjectRef("user", "a").ToString().Should().Be("user:a");
        SubjectRef.Named(new ObjectRef("team", "a"), "member").ToString().Should().Be("team:a#member");
    }

    [Fact]
    public void NativeValuesHaveReadableAndStructuredForms()
    {
        var filter = new Filter("document").WithRelation("viewer").WithSubjectType("user").WithSubjectId("alice").WithSubjectRelation(SubjectRelationFilter.Direct);
        filter.ToString().Should().Be("document:*#viewer@user:alice#...");
        new Filter("document").WithResourceIdPrefix("doc-").ToString().Should().Be("document:doc-%*#*@*");
        var txn = new Transaction();
        txn.MustNotMatch(filter);
        txn.Touch(Relationship.FromTuple("document:1#viewer@user:alice"));
        txn.Delete(Relationship.FromTuple("document:2#viewer@user:alice"));
        txn.Count.Should().Be(2);
        txn.ToString().Should().Be("[MUST_NOT_MATCH(document:*#viewer@user:alice#...), TOUCH(document:1#viewer@user:alice), DELETE(document:2#viewer@user:alice)]");
        var json = JsonSerializer.Serialize(new { txn.Updates, txn.Preconditions, filter });
        json.Should().Contain("\"Operation\":\"Touch\"").And.Contain("\"ObjectId\":\"alice\"").And.Contain("\"Kind\":\"Direct\"").And.Contain("\"MustNotMatch\"");
        json.Should().NotContain("{}");
    }

    [Theory]
    [InlineData("document:1#viewer@user:alice", true)]
    [InlineData("document:1#editor@user:alice", false)]
    [InlineData("document:1#viewer@user:bob", false)]
    [InlineData("document:1#viewer@team:t#member", false)]
    [InlineData("folder:1#viewer@user:alice", false)]
    public void FilterMatchesWithServerSemantics(string tuple, bool expected) =>
        new Filter("document").WithRelation("viewer").WithSubjectType("user").WithSubjectId("alice").Matches(Relationship.FromTuple(tuple)).Should().Be(expected);

    [Fact]
    public void FilterMatchesSubjectRelationKindsAndPrefixes()
    {
        var direct = Relationship.FromTuple("document:doc-1#viewer@team:t");
        var named = Relationship.FromTuple("document:doc-1#viewer@team:t#member");
        var any = new Filter("document").WithSubjectType("team");
        any.Matches(direct).Should().BeTrue(); any.Matches(named).Should().BeTrue();
        any.WithSubjectRelation(SubjectRelationFilter.Direct).Matches(named).Should().BeFalse();
        any.WithSubjectRelation(SubjectRelationFilter.Direct).Matches(direct).Should().BeTrue();
        any.WithSubjectRelation(SubjectRelationFilter.Named("member")).Matches(named).Should().BeTrue();
        any.WithSubjectRelation(SubjectRelationFilter.Named("member")).Matches(direct).Should().BeFalse();
        new Filter("document").WithResourceIdPrefix("doc-").Matches(direct).Should().BeTrue();
        new Filter("document").WithResourceIdPrefix("x").Matches(direct).Should().BeFalse();
        named.ToFilter().Matches(named).Should().BeTrue();
        named.ToFilter().Matches(direct).Should().BeFalse();
    }

    // ── N9/N10: retry policy and default deadline ─────────────────────────

    private static (Mock<SchemaService.SchemaServiceClient> Mock, Func<int> Calls, List<DateTime?> Deadlines) FailingSchema(StatusCode code, int failures)
    {
        int calls = 0; var deadlines = new List<DateTime?>();
        var schema = new Mock<SchemaService.SchemaServiceClient>();
        schema.Setup(s => s.ReadSchemaAsync(It.IsAny<ReadSchemaRequest>(), It.IsAny<Metadata>(), It.IsAny<DateTime?>(), It.IsAny<CancellationToken>()))
            .Returns((ReadSchemaRequest _, Metadata _, DateTime? d, CancellationToken _) => { deadlines.Add(d); return ++calls <= failures ? Failing<ReadSchemaResponse>(code) : MakeUnaryCall(new ReadSchemaResponse { SchemaText = "s" }); });
        schema.Setup(s => s.WriteSchemaAsync(It.IsAny<WriteSchemaRequest>(), It.IsAny<Metadata>(), It.IsAny<DateTime?>(), It.IsAny<CancellationToken>()))
            .Returns(() => { calls++; return Failing<WriteSchemaResponse>(code); });
        return (schema, () => calls, deadlines);
    }

    [Fact]
    public async Task RetriesDisabledMakesOneAttempt()
    {
        var (schema, calls, _) = FailingSchema(StatusCode.Unavailable, 1);
        await using var client = Client(schema: schema.Object, options: new ClientOptions { Retries = Retries.Disabled });
        await Assert.ThrowsAsync<UnavailableException>(() => client.ReadSchemaAsync());
        calls().Should().Be(1);
    }

    [Fact]
    public async Task CustomPolicyRetriesOptedInCodesAndReportsEachRetry()
    {
        var (schema, calls, _) = FailingSchema(StatusCode.ResourceExhausted, 2);
        var events = new List<RetryEvent>();
        var policy = new RetryPolicy { MaxRetries = 2, RetryableCodes = [StatusCode.ResourceExhausted], InitialBackoff = TimeSpan.FromMilliseconds(1), OnRetry = events.Add };
        await using var client = Client(schema: schema.Object, options: new ClientOptions { RetryPolicy = policy });
        (await client.ReadSchemaAsync()).Schema.Should().Be("s");
        calls().Should().Be(3);
        events.Select(e => e.Attempt).Should().Equal(1, 2);
        events.Should().OnlyContain(e => e.Operation == "ReadSchemaAsync" && e.Error is ResourceExhaustedException);
    }

    [Fact]
    public async Task NoPolicyMakesAMutationReplay()
    {
        var (schema, calls, _) = FailingSchema(StatusCode.Unavailable, 99);
        var policy = new RetryPolicy { MaxRetries = 5, RetryableCodes = [StatusCode.Unavailable], InitialBackoff = TimeSpan.FromMilliseconds(1) };
        await using var client = Client(schema: schema.Object, options: new ClientOptions { RetryPolicy = policy });
        await Assert.ThrowsAsync<UnavailableException>(() => client.WriteSchemaAsync("definition user {}"));
        calls().Should().Be(1);
    }

    [Fact]
    public async Task StreamRetriesDisabledOpensWatchOnce()
    {
        var script = new StreamCallScript<WatchResponse>().Then([], UnavailableError()).Then([new WatchResponse()]);
        var watch = new Mock<WatchService.WatchServiceClient>();
        watch.Setup(w => w.Watch(It.IsAny<WatchRequest>(), It.IsAny<Metadata>(), It.IsAny<DateTime?>(), It.IsAny<CancellationToken>())).Returns(() => script.Next());
        await using var client = Client(watch: watch.Object, options: new ClientOptions { StreamRetries = StreamRetries.Disabled });
        await Assert.ThrowsAsync<UnavailableException>(async () => { await foreach (var _ in client.UpdatesAsync([], null)) { } });
        script.CallCount.Should().Be(1);
    }

    [Fact]
    public async Task InvalidRetryPolicyAndModesAreRejected()
    {
        Assert.Throws<InvalidArgumentException>(() => SpiceDBClient.CreatePlaintext("localhost:1", "k", new ClientOptions { RetryPolicy = new RetryPolicy { MaxRetries = -1 } }));
        Assert.Throws<InvalidArgumentException>(() => new ClientOptions { Retries = (Retries)7 });
        Assert.Throws<InvalidArgumentException>(() => new ClientOptions { DefaultDeadline = (DefaultDeadline)7 });
        Assert.Throws<InvalidArgumentException>(() => new CheckOptions { ItemErrors = (BulkItemErrors)7 });
        Assert.Throws<InvalidArgumentException>(() => new DeleteOptions { Partial = (PartialDeletion)7 });
        await Task.CompletedTask;
    }

    [Fact]
    public async Task DefaultDeadlineDisabledSendsNoDeadline()
    {
        var (schema, _, deadlines) = FailingSchema(StatusCode.OK, 0);
        await using (var enabled = Client(schema: schema.Object)) await enabled.ReadSchemaAsync();
        await using (var disabled = Client(schema: schema.Object, options: new ClientOptions { DefaultDeadline = DefaultDeadline.Disabled })) await disabled.ReadSchemaAsync();
        await using (var explicitTimeout = Client(schema: schema.Object, options: new ClientOptions { DefaultDeadline = DefaultDeadline.Disabled }))
            await explicitTimeout.ReadSchemaAsync(new CallOptions { Timeout = TimeSpan.FromSeconds(5) });
        deadlines[0].Should().NotBeNull();
        deadlines[1].Should().BeNull();
        deadlines[2].Should().NotBeNull();
    }

    // ── N12: per-item bulk errors ─────────────────────────────────────────

    [Fact]
    public async Task BulkItemErrorsReturnedKeepsEveryItem()
    {
        var permissions = new Mock<PermissionsService.PermissionsServiceClient>();
        permissions.Setup(p => p.CheckBulkPermissionsAsync(It.IsAny<CheckBulkPermissionsRequest>(), It.IsAny<Metadata>(), It.IsAny<DateTime?>(), It.IsAny<CancellationToken>()))
            .Returns((CheckBulkPermissionsRequest request, Metadata _, DateTime? _, CancellationToken _) =>
            {
                var response = new CheckBulkPermissionsResponse { CheckedAt = new() { Token = "rev" } };
                response.Pairs.Add(new CheckBulkPermissionsPair { Request = request.Items[0], Item = new() { Permissionship = CheckPermissionResponse.Types.Permissionship.HasPermission } });
                response.Pairs.Add(new CheckBulkPermissionsPair { Request = request.Items[1], Error = new Google.Rpc.Status { Code = (int)StatusCode.InvalidArgument, Message = "bad item" } });
                return MakeUnaryCall(response);
            });
        await using var client = Client(permissions.Object);
        PermissionCheck Check(string id) => new(new ObjectRef("document", id), "view", SubjectRef.Direct(new ObjectRef("user", "a")), Consistency.Full());
        var results = await client.CheckBulkAsync([Check("1"), Check("2")], new CheckOptions { ItemErrors = BulkItemErrors.Returned });
        results[0].HasPermission.Should().BeTrue();
        results[1].HasPermission.Should().BeFalse();
        results[1].Error.Should().BeOfType<InvalidArgumentException>().Which.InputIndex.Should().Be(1);
        results[1].Error!.ServerMessage.Should().Be("bad item");
        results[1].CheckedAt.Value.Should().Be("rev");
        (await client.CheckAllAsync([Check("1"), Check("2")], new CheckOptions { ItemErrors = BulkItemErrors.Returned })).Should().BeFalse();
        (await Assert.ThrowsAsync<InvalidArgumentException>(() => client.CheckBulkAsync([Check("1"), Check("2")]))).InputIndex.Should().Be(1);
    }

    // ── N16: all-or-nothing delete and delete-all progress ────────────────

    [Fact]
    public async Task AllOrNothingDeleteMayOmitLimitAndDeleteAllReportsProgress()
    {
        var requests = new List<DeleteRelationshipsRequest>();
        var permissions = new Mock<PermissionsService.PermissionsServiceClient>();
        permissions.Setup(p => p.DeleteRelationshipsAsync(It.IsAny<DeleteRelationshipsRequest>(), It.IsAny<Metadata>(), It.IsAny<DateTime?>(), It.IsAny<CancellationToken>()))
            .Returns((DeleteRelationshipsRequest r, Metadata _, DateTime? _, CancellationToken _) =>
            {
                requests.Add(r.Clone());
                var done = requests.Count >= 2 || !r.OptionalAllowPartialDeletions;
                return MakeUnaryCall(new DeleteRelationshipsResponse { DeletedAt = new() { Token = "t" + requests.Count }, RelationshipsDeletedCount = 3, DeletionProgress = done ? DeleteRelationshipsResponse.Types.DeletionProgress.Complete : DeleteRelationshipsResponse.Types.DeletionProgress.Partial, AfterResultCursor = done ? null : new Cursor { Token = "c" } });
            });
        await using var client = Client(permissions.Object);
        await client.DeleteRelationshipsAsync(new Filter("document"), null, new DeleteOptions { Partial = PartialDeletion.Disallowed });
        requests[0].OptionalAllowPartialDeletions.Should().BeFalse();
        requests[0].OptionalLimit.Should().Be(0u);
        await Assert.ThrowsAsync<InvalidArgumentException>(() => client.DeleteRelationshipsAsync(new Filter("document"), null));

        requests.Clear();
        var progress = new List<DeleteResult>();
        var result = await client.DeleteAllRelationshipsNonAtomicAsync(new Filter("document"), new DeleteAllOptions { Progress = new SyncProgress<DeleteResult>(progress.Add) });
        progress.Select(p => p.DeletedCount).Should().Equal(3ul, 6ul);
        progress[0].ResumeCursor.Should().Be("c");
        progress[0].Revision.Value.Should().Be("t1");
        result.DeletedCount.Should().Be(6);
    }

    private sealed class SyncProgress<T>(Action<T> report) : IProgress<T> { public void Report(T value) => report(value); }

    // ── N8: paging state and resume ───────────────────────────────────────

    [Fact]
    public async Task ReadsYieldCursorAndReadAtResumeAndRepeatPageSize()
    {
        var requests = new List<ReadRelationshipsRequest>();
        var permissions = new Mock<PermissionsService.PermissionsServiceClient>();
        permissions.Setup(p => p.ReadRelationships(It.IsAny<ReadRelationshipsRequest>(), It.IsAny<Metadata>(), It.IsAny<DateTime?>(), It.IsAny<CancellationToken>()))
            .Returns((ReadRelationshipsRequest r, Metadata _, DateTime? _, CancellationToken _) =>
            {
                requests.Add(r.Clone());
                var start = r.OptionalCursor is null ? 0 : int.Parse(r.OptionalCursor.Token);
                var items = Enumerable.Range(start, (int)Math.Min(r.OptionalLimit, Math.Max(0, 5 - start))).Select(i => new ReadRelationshipsResponse
                { Relationship = Relationship.FromTuple($"document:{i}#viewer@user:a").ToProto(), AfterResultCursor = new Cursor { Token = (i + 1).ToString() }, ReadAt = new() { Token = "rev" } });
                return MakeServerStreamingCall(new FakeStreamReader<ReadRelationshipsResponse>(items.ToArray()));
            });
        await using var client = Client(permissions.Object);
        var items = new List<ReadRelationship>();
        await foreach (var item in client.ReadRelationshipsAsync(Consistency.MinLatency(), new Filter("document"), new ReadOptions { PageSize = 2, Limit = 3, ResumeCursor = "1" })) items.Add(item);
        items.Select(i => i.Relationship.ResourceId).Should().Equal("1", "2", "3");
        items.Select(i => i.AfterResultCursor).Should().Equal("2", "3", "4");
        items.Should().OnlyContain(i => i.ReadAt.Value == "rev");
        requests.Select(r => r.OptionalCursor?.Token).Should().Equal("1", "3");
        requests.Should().OnlyContain(r => r.OptionalLimit == 2 && r.Consistency.MinimizeLatency);
    }

    // ── N11: exported protocol conversions ────────────────────────────────

    [Fact]
    public void ProtoConvertRoundTripsNativeValues()
    {
        var relationship = Relationship.FromTuple("document:1#viewer@team:t#member[c:{\"a\":1}][expiration:2030-01-02T03:04:05Z]");
        ProtoConvert.FromProto(ProtoConvert.ToProto(relationship)).ToString().Should().Be(relationship.ToString());
        var filter = new Filter("document").WithResourceIdPrefix("d").WithRelation("viewer").WithSubjectType("team").WithSubjectRelation(SubjectRelationFilter.Direct);
        ProtoConvert.FromProto(ProtoConvert.ToProto(filter)).Should().Be(filter);
        var named = filter.WithSubjectRelation(SubjectRelationFilter.Named("member"));
        ProtoConvert.FromProto(ProtoConvert.ToProto(named)).Should().Be(named);
        var precondition = new Precondition(PreconditionOperation.MustNotMatch, filter);
        ProtoConvert.FromProto(ProtoConvert.ToProto(precondition)).Should().Be(precondition);
        var token = new ZedToken("abc");
        ProtoConvert.FromProto(ProtoConvert.ToProto(Consistency.Snapshot(token))).Should().Be(Consistency.Snapshot(token));
        ProtoConvert.FromProto(ProtoConvert.ToProto(Consistency.Full())).Should().Be(Consistency.Full());
        var update = new RelationshipUpdate(UpdateOperation.Delete, relationship);
        ProtoConvert.FromProto(ProtoConvert.ToProto(update)).ToString().Should().Be(update.ToString());
        var check = new PermissionCheck(new ObjectRef("document", "1"), "view", SubjectRef.Direct(new ObjectRef("user", "a")), Consistency.AtLeast(token), new Dictionary<string, object?> { ["k"] = "v" });
        var checkBack = ProtoConvert.FromProto(ProtoConvert.ToProto(check, new CheckOptions { Tracing = CheckTracing.Enabled }));
        (checkBack.Resource, checkBack.Permission, checkBack.Subject, checkBack.Consistency).Should().Be((check.Resource, check.Permission, check.Subject, check.Consistency));
        ProtoConvert.ToProto(check, new CheckOptions { Tracing = CheckTracing.Enabled }).WithTracing.Should().BeTrue();
        var txn = new Transaction(); txn.MustMatch(filter); txn.Create(relationship);
        var write = ProtoConvert.ToProto(txn);
        (write.Updates.Count, write.OptionalPreconditions.Count).Should().Be((1, 1));
    }

    [Fact]
    public void ProtoConvertRoundTripsResultsAndTraces()
    {
        var wire = new CheckPermissionResponse
        {
            CheckedAt = new() { Token = "rev" },
            Permissionship = CheckPermissionResponse.Types.Permissionship.ConditionalPermission,
            PartialCaveatInfo = new() { MissingRequiredContext = { "ip" } },
            DebugTrace = new() { SchemaUsed = "schema", Check = new() { Permission = "view", PermissionType = Authzed.Api.V1.CheckDebugTrace.Types.PermissionType.Permission, Result = Authzed.Api.V1.CheckDebugTrace.Types.Permissionship.HasPermission, Resource = new() { ObjectType = "document", ObjectId = "1" }, Subject = new() { Object = new() { ObjectType = "user", ObjectId = "a" } }, SubProblems = new() { Traces = { new Authzed.Api.V1.CheckDebugTrace { Permission = "viewer", WasCachedResult = false } } }, TraceOperationId = "op", Source = "src" } },
        };
        ProtoConvert.ToProto(ProtoConvert.FromProto(wire)).Should().Be(wire);
        var lookup = new LookupSubjectsResponse { LookedUpAt = new() { Token = "r" }, Subject = new() { SubjectObjectId = "*", Permissionship = Authzed.Api.V1.LookupPermissionship.HasPermission } };
        lookup.ExcludedSubjects.Add(new Authzed.Api.V1.ResolvedSubject { SubjectObjectId = "bob", Permissionship = Authzed.Api.V1.LookupPermissionship.HasPermission });
        ProtoConvert.ToProto(ProtoConvert.FromProto(lookup)).Should().Be(lookup);
        var expand = new ExpandPermissionTreeResponse { ExpandedAt = new() { Token = "r" }, TreeRoot = new() { ExpandedRelation = "view", ExpandedObject = new() { ObjectType = "document", ObjectId = "1" }, Intermediate = new() { Operation = AlgebraicSubjectSet.Types.Operation.Union, Children = { new PermissionRelationshipTree { ExpandedRelation = "viewer", Leaf = new() { Subjects = { new SubjectReference { Object = new() { ObjectType = "user", ObjectId = "a" } } } } } } } } };
        ProtoConvert.ToProto(ProtoConvert.FromProto(expand)).Should().Be(expand);
        var read = new ReadRelationshipsResponse { Relationship = Relationship.FromTuple("document:1#viewer@user:a").ToProto(), ReadAt = new() { Token = "r" }, AfterResultCursor = new() { Token = "c" } };
        ProtoConvert.ToProto(ProtoConvert.FromProto(read)).Should().Be(read);
        ProtoConvert.ToProto(ProtoConvert.FromProto(new WriteRelationshipsResponse { WrittenAt = new() { Token = "w" } })).WrittenAt.Token.Should().Be("w");
    }

    // ── Real loopback gRPC: N6, N7, N14 ───────────────────────────────────

    public sealed class Service : PermissionsService.PermissionsServiceBase
    {
        public Metadata? LastRequestHeaders { get; private set; }
        public int Received { get; private set; }
        public override async Task<ImportBulkRelationshipsResponse> ImportBulkRelationships(IAsyncStreamReader<ImportBulkRelationshipsRequest> request, ServerCallContext context)
        {
            await request.MoveNext(context.CancellationToken);
            Received++;
            // Abort mid-stream the way SpiceDB does for a duplicate relationship.
            throw new RpcException(new GrpcStatus(StatusCode.AlreadyExists, "duplicate relationship"));
        }
        public override async Task<CheckPermissionResponse> CheckPermission(CheckPermissionRequest request, ServerCallContext context)
        {
            LastRequestHeaders = context.RequestHeaders;
            await context.WriteResponseHeadersAsync(new Metadata { { "x-server-version", "v9" } });
            context.ResponseTrailers.Add("x-trailer", "t");
            return new() { CheckedAt = new() { Token = "rev" }, Permissionship = CheckPermissionResponse.Types.Permissionship.HasPermission };
        }
    }

    private static async Task<(WebApplication App, Uri Address, Service Service)> StartServer()
    {
        var service = new Service();
        var builder = WebApplication.CreateBuilder(); builder.Logging.ClearProviders();
        builder.WebHost.ConfigureKestrel(k => k.Listen(IPAddress.Loopback, 0, o => o.Protocols = HttpProtocols.Http2));
        builder.Services.AddGrpc(); builder.Services.AddSingleton(service);
        var app = builder.Build(); app.MapGrpcService<Service>(); await app.StartAsync();
        var address = app.Services.GetRequiredService<IServer>().Features.Get<IServerAddressesFeature>()!.Addresses.Single();
        return (app, new Uri(address), service);
    }

    [Fact]
    public async Task CallInvokerConstructorBorrowsTransportAndCarriesHeadersAndResponseMetadata()
    {
        var (app, address, service) = await StartServer();
        await using var _ = app;
        using var channel = GrpcChannel.ForAddress(address);
        var invoker = channel.CreateCallInvoker();
        var check = new PermissionCheck(new ObjectRef("document", "1"), "view", SubjectRef.Direct(new ObjectRef("user", "a")), Consistency.Full());
        var sink = new ResponseMetadata();
        await using (var client = SpiceDBClient.CreateFromCallInvoker(invoker, new ClientOptions { DefaultDeadline = DefaultDeadline.Disabled }))
        {
            var result = await client.CheckAsync(check, new CheckOptions { Transport = new TransportOptions { Headers = new Metadata { { "x-request-id", "r1" } }, ResponseMetadata = sink } });
            result.HasPermission.Should().BeTrue();
            client.RawProto().Permissions.Should().NotBeNull();
        }
        service.LastRequestHeaders!.GetValue("x-request-id").Should().Be("r1");
        service.LastRequestHeaders!.GetValue("authorization").Should().BeNull("a borrowed invoker carries its own credentials");
        sink.Headers!.GetValue("x-server-version").Should().Be("v9");
        sink.Trailers!.GetValue("x-trailer").Should().Be("t");
        // Disposing the client left the caller's channel usable.
        (await new PermissionsService.PermissionsServiceClient(channel).CheckPermissionAsync(new CheckPermissionRequest())).CheckedAt.Token.Should().Be("rev");
        Assert.Throws<InvalidArgumentException>(() => SpiceDBClient.CreateFromCallInvoker(invoker, new ClientOptions { RemotePlaintextCredentials = RemotePlaintextCredentials.Allow }));
        Assert.Throws<InvalidArgumentException>(() => SpiceDBClient.CreateFromCallInvoker(null!));
    }

    [Fact]
    public async Task ImportReportsTheServersAbortStatusNotTheFailedSend()
    {
        var (app, address, _) = await StartServer();
        await using var __ = app;
        using var channel = GrpcChannel.ForAddress(address);
        await using var client = SpiceDBClient.CreateFromCallInvoker(channel.CreateCallInvoker());
        static async IAsyncEnumerable<Relationship> Many()
        {
            for (var i = 0; i < 5000; i++) { yield return Relationship.FromTuple($"document:{i}#viewer@user:a"); if (i % 100 == 0) await Task.Yield(); }
        }
        var error = await Assert.ThrowsAsync<AlreadyExistsException>(() => client.ImportRelationshipsAsync(Many(), new ImportOptions { BatchSize = 1 }).WaitAsync(TimeSpan.FromSeconds(10)));
        error.ServerMessage.Should().Be("duplicate relationship");
    }
}
