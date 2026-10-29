// Proves that the 5 streaming methods retry stream/page ESTABLISHMENT on
// transient errors (mirroring RetryAsync's transient predicate + backoff/
// MaxRetryAttempts budget), but NEVER retry once any item has been yielded
// from the current stream/page — retrying after a yield would replay
// already-delivered items to the caller. (Task RB4-C#.)
//
// Each method gets two tests:
//   - "...RetriesEstablishment..." — the stream fails transiently on first
//     open (before anything is yielded), then succeeds on retry. Asserts
//     items are delivered AND that the underlying stub was called twice
//     (proving a retry actually happened, not just that the call succeeded).
//   - "...AfterYield_IsNotRetried" — the stream yields an item, then fails
//     transiently. Asserts the typed exception surfaces (mapped, not
//     retried) and that the stub was called exactly once (no replay).

using Authzed.Api.V1;
using FluentAssertions;
using Grpc.Core;
using Moq;
using Xunit;
using static SpiceDB.Client.Tests.StreamingTestHelpers;

namespace SpiceDB.Client.Tests;

public class StreamingEstablishmentRetryTests
{
    // ── ReadRelationshipsAsync (server-streaming, paged) ────────────────────

    [Fact]
    public async Task ReadRelationshipsAsync_RetriesEstablishment_OnFirstOpenTransientError()
    {
        var script = new StreamCallScript<ReadRelationshipsResponse>()
            .Then([], UnavailableError())
            .Then([RelResponse("doc1", "cursor1")]);

        var mockPermissions = new Mock<PermissionsService.PermissionsServiceClient>();
        mockPermissions
            .Setup(c => c.ReadRelationships(
                It.IsAny<ReadRelationshipsRequest>(),
                It.IsAny<Metadata>(),
                It.IsAny<DateTime?>(),
                It.IsAny<CancellationToken>()))
            .Returns(() => script.Next());

        await using var client = NewClient(permissions: mockPermissions.Object);

        var got = new List<Relationship>();
        await foreach (var r in client.ReadRelationshipsAsync(Consistency.Full(), new Filter("document"), null, default))
            got.Add(r.Relationship);

        got.Select(r => r.Resource.ObjectId).Should().Equal("doc1");
        script.CallCount.Should().Be(2); // one failed open + one successful open
    }

    [Fact]
    public async Task ReadRelationshipsAsync_TransientErrorAfterYield_IsNotRetried()
    {
        var script = new StreamCallScript<ReadRelationshipsResponse>()
            .Then([RelResponse("doc1", "cursor1")], UnavailableError());

        var mockPermissions = new Mock<PermissionsService.PermissionsServiceClient>();
        mockPermissions
            .Setup(c => c.ReadRelationships(
                It.IsAny<ReadRelationshipsRequest>(),
                It.IsAny<Metadata>(),
                It.IsAny<DateTime?>(),
                It.IsAny<CancellationToken>()))
            .Returns(() => script.Next());

        await using var client = NewClient(permissions: mockPermissions.Object);

        var got = new List<Relationship>();
        var act = async () =>
        {
            await foreach (var r in client.ReadRelationshipsAsync(Consistency.Full(), new Filter("document"), null, default))
                got.Add(r.Relationship);
        };

        await act.Should().ThrowAsync<UnavailableException>();
        got.Select(r => r.Resource.ObjectId).Should().Equal("doc1"); // the pre-error item was still yielded
        script.CallCount.Should().Be(1); // no re-open — a retry here would have replayed doc1
    }

    [Fact]
    public async Task ReadRelationshipsAsync_ExhaustsRetryBudget_ThenSurfacesTypedException()
    {
        // MaxRetryAttempts == 3 -> 4 total attempts before giving up.
        var script = new StreamCallScript<ReadRelationshipsResponse>()
            .Then([], UnavailableError())
            .Then([], UnavailableError())
            .Then([], UnavailableError())
            .Then([], UnavailableError());

        var mockPermissions = new Mock<PermissionsService.PermissionsServiceClient>();
        mockPermissions
            .Setup(c => c.ReadRelationships(
                It.IsAny<ReadRelationshipsRequest>(),
                It.IsAny<Metadata>(),
                It.IsAny<DateTime?>(),
                It.IsAny<CancellationToken>()))
            .Returns(() => script.Next());

        await using var client = NewClient(permissions: mockPermissions.Object);

        var act = async () =>
        {
            await foreach (var _ in client.ReadRelationshipsAsync(Consistency.Full(), new Filter("document"), null, default))
            {
            }
        };

        await act.Should().ThrowAsync<UnavailableException>();
        script.CallCount.Should().Be(4);
    }

    // ── LookupResourcesAsync (server-streaming, paged) ───────────────────────

    [Fact]
    public async Task LookupResourcesAsync_RetriesEstablishment_OnFirstOpenTransientError()
    {
        var script = new StreamCallScript<LookupResourcesResponse>()
            .Then([], UnavailableError())
            .Then([new LookupResourcesResponse
            {
                ResourceObjectId = "doc1",
                Permissionship = LookupPermissionship.HasPermission,
                AfterResultCursor = new Cursor { Token = "cursor1" },
            }]);

        var mockPermissions = new Mock<PermissionsService.PermissionsServiceClient>();
        mockPermissions
            .Setup(c => c.LookupResources(
                It.IsAny<LookupResourcesRequest>(),
                It.IsAny<Metadata>(),
                It.IsAny<DateTime?>(),
                It.IsAny<CancellationToken>()))
            .Returns(() => script.Next());

        await using var client = NewClient(permissions: mockPermissions.Object);

        var got = new List<LookupResource>();
        await foreach (var r in client.LookupResourcesAsync(Consistency.Full(), "document", "view", new SubjectRef(new ObjectRef("user", "alice")), null, default))
            got.Add(r);

        got.Select(r => r.ResourceID).Should().Equal("doc1");
        script.CallCount.Should().Be(2);
    }

    [Fact]
    public async Task LookupResourcesAsync_TransientErrorAfterYield_IsNotRetried()
    {
        var script = new StreamCallScript<LookupResourcesResponse>()
            .Then(
                [new LookupResourcesResponse
                {
                    ResourceObjectId = "doc1",
                    Permissionship = LookupPermissionship.HasPermission,
                    AfterResultCursor = new Cursor { Token = "cursor1" },
                }],
                UnavailableError());

        var mockPermissions = new Mock<PermissionsService.PermissionsServiceClient>();
        mockPermissions
            .Setup(c => c.LookupResources(
                It.IsAny<LookupResourcesRequest>(),
                It.IsAny<Metadata>(),
                It.IsAny<DateTime?>(),
                It.IsAny<CancellationToken>()))
            .Returns(() => script.Next());

        await using var client = NewClient(permissions: mockPermissions.Object);

        var got = new List<LookupResource>();
        var act = async () =>
        {
            await foreach (var r in client.LookupResourcesAsync(Consistency.Full(), "document", "view", new SubjectRef(new ObjectRef("user", "alice")), null, default))
                got.Add(r);
        };

        await act.Should().ThrowAsync<UnavailableException>();
        got.Select(r => r.ResourceID).Should().Equal("doc1");
        script.CallCount.Should().Be(1);
    }

    // ── LookupSubjectsAsync (server-streaming, single call) ──────────────────

    [Fact]
    public async Task LookupSubjectsAsync_RetriesEstablishment_OnFirstOpenTransientError()
    {
        var script = new StreamCallScript<LookupSubjectsResponse>()
            .Then([], UnavailableError())
            .Then([new LookupSubjectsResponse
            {
                Subject = new Authzed.Api.V1.ResolvedSubject { SubjectObjectId = "alice", Permissionship = LookupPermissionship.HasPermission },
            }]);

        var mockPermissions = new Mock<PermissionsService.PermissionsServiceClient>();
        mockPermissions
            .Setup(c => c.LookupSubjects(
                It.IsAny<LookupSubjectsRequest>(),
                It.IsAny<Metadata>(),
                It.IsAny<DateTime?>(),
                It.IsAny<CancellationToken>()))
            .Returns(() => script.Next());

        await using var client = NewClient(permissions: mockPermissions.Object);

        var got = new List<LookupSubject>();
        await foreach (var s in client.LookupSubjectsAsync(Consistency.Full(), new ObjectRef("document", "doc1"), "view", "user", null, default))
            got.Add(s);

        got.Select(s => s.Subject.SubjectID).Should().Equal("alice");
        script.CallCount.Should().Be(2);
    }

    [Fact]
    public async Task LookupSubjectsAsync_TransientErrorAfterYield_IsNotRetried()
    {
        var script = new StreamCallScript<LookupSubjectsResponse>()
            .Then(
                [new LookupSubjectsResponse
                {
                    Subject = new Authzed.Api.V1.ResolvedSubject { SubjectObjectId = "alice", Permissionship = LookupPermissionship.HasPermission },
                }],
                UnavailableError());

        var mockPermissions = new Mock<PermissionsService.PermissionsServiceClient>();
        mockPermissions
            .Setup(c => c.LookupSubjects(
                It.IsAny<LookupSubjectsRequest>(),
                It.IsAny<Metadata>(),
                It.IsAny<DateTime?>(),
                It.IsAny<CancellationToken>()))
            .Returns(() => script.Next());

        await using var client = NewClient(permissions: mockPermissions.Object);

        var got = new List<LookupSubject>();
        var act = async () =>
        {
            await foreach (var s in client.LookupSubjectsAsync(Consistency.Full(), new ObjectRef("document", "doc1"), "view", "user", null, default))
                got.Add(s);
        };

        await act.Should().ThrowAsync<UnavailableException>();
        got.Select(s => s.Subject.SubjectID).Should().Equal("alice");
        script.CallCount.Should().Be(1);
    }

    // ── ExportRelationshipsAsync (server-streaming, paged) ───────────────────

    [Fact]
    public async Task ExportRelationshipsAsync_RetriesEstablishment_OnFirstOpenTransientError()
    {
        var script = new StreamCallScript<ExportBulkRelationshipsResponse>()
            .Then([], UnavailableError())
            .Then([new ExportBulkRelationshipsResponse
            {
                Relationships = { RelProto("doc1") },
                AfterResultCursor = new Cursor { Token = "cursor1" },
            }]);

        var mockPermissions = new Mock<PermissionsService.PermissionsServiceClient>();
        mockPermissions
            .Setup(c => c.ExportBulkRelationships(
                It.IsAny<ExportBulkRelationshipsRequest>(),
                It.IsAny<Metadata>(),
                It.IsAny<DateTime?>(),
                It.IsAny<CancellationToken>()))
            .Returns(() => script.Next());

        await using var client = NewClient(permissions: mockPermissions.Object);

        var got = new List<Relationship>();
        await foreach (var r in client.ExportRelationshipsAsync(Consistency.Full(), new Filter("document"), null, default))
            got.Add(r.Relationship);

        got.Select(r => r.Resource.ObjectId).Should().Equal("doc1");
        script.CallCount.Should().Be(2);
    }

    [Fact]
    public async Task ExportRelationshipsAsync_TransientErrorAfterYield_IsNotRetried()
    {
        var script = new StreamCallScript<ExportBulkRelationshipsResponse>()
            .Then(
                [new ExportBulkRelationshipsResponse
                {
                    Relationships = { RelProto("doc1") },
                    AfterResultCursor = new Cursor { Token = "cursor1" },
                }],
                UnavailableError());

        var mockPermissions = new Mock<PermissionsService.PermissionsServiceClient>();
        mockPermissions
            .Setup(c => c.ExportBulkRelationships(
                It.IsAny<ExportBulkRelationshipsRequest>(),
                It.IsAny<Metadata>(),
                It.IsAny<DateTime?>(),
                It.IsAny<CancellationToken>()))
            .Returns(() => script.Next());

        await using var client = NewClient(permissions: mockPermissions.Object);

        var got = new List<Relationship>();
        var act = async () =>
        {
            await foreach (var r in client.ExportRelationshipsAsync(Consistency.Full(), new Filter("document"), null, default))
                got.Add(r.Relationship);
        };

        await act.Should().ThrowAsync<UnavailableException>();
        got.Select(r => r.Resource.ObjectId).Should().Equal("doc1");
        script.CallCount.Should().Be(1);
    }

    // ── UpdatesAsync / Watch (server-streaming, single call) ─────────────────

    [Fact]
    public async Task UpdatesAsync_ClientWidePolicyNeverReopensAWatch()
    {
        // A reopened watch without a start revision would begin at a later revision and skip
        // the changes in between, so the default client surfaces the failure instead.
        var script = new StreamCallScript<WatchResponse>()
            .Then([], UnavailableError())
            .Then([new WatchResponse { ChangesThrough = new() { Token = "t" } }]);
        var mockWatch = new Mock<WatchService.WatchServiceClient>();
        mockWatch
            .Setup(c => c.Watch(It.IsAny<WatchRequest>(), It.IsAny<Metadata>(), It.IsAny<DateTime?>(), It.IsAny<CancellationToken>()))
            .Returns(() => script.Next());

        await using var client = NewClient(watch: mockWatch.Object);
        var act = async () => { await foreach (var _ in client.UpdatesAsync([], null)) { } };

        await act.Should().ThrowAsync<UnavailableException>();
        script.CallCount.Should().Be(1);
    }

    [Fact]
    public async Task UpdatesAsync_PerCallPolicyReopensBeforeTheFirstEvent()
    {
        var script = new StreamCallScript<WatchResponse>()
            .Then([], UnavailableError())
            .Then([new WatchResponse
            {
                Updates = { new Authzed.Api.V1.RelationshipUpdate { Operation = Authzed.Api.V1.RelationshipUpdate.Types.Operation.Touch, Relationship = RelProto("doc1") } },
            }]);

        var mockWatch = new Mock<WatchService.WatchServiceClient>();
        mockWatch
            .Setup(c => c.Watch(
                It.IsAny<WatchRequest>(),
                It.IsAny<Metadata>(),
                It.IsAny<DateTime?>(),
                It.IsAny<CancellationToken>()))
            .Returns(() => script.Next());

        await using var client = NewClient(watch: mockWatch.Object);

        var events = new List<WatchEvent>();
        await foreach (var e in client.UpdatesAsync([], null, new WatchOptions { Content = WatchContent.Relationships, RetryPolicy = new RetryPolicy { MaxRetries = 1, RetryableCodes = [StatusCode.Unavailable], InitialBackoff = TimeSpan.FromMilliseconds(1) } }, default))
            events.Add(e);

        var got = events.SelectMany(e => e.Updates).ToList();
        got.Select(u => u.Relationship.Resource.ObjectId).Should().Equal("doc1");
        script.CallCount.Should().Be(2);
    }

    [Fact]
    public async Task UpdatesAsync_TransientErrorAfterYield_IsNotRetried()
    {
        var script = new StreamCallScript<WatchResponse>()
            .Then(
                [new WatchResponse
                {
                    Updates = { new Authzed.Api.V1.RelationshipUpdate { Operation = Authzed.Api.V1.RelationshipUpdate.Types.Operation.Touch, Relationship = RelProto("doc1") } },
                }],
                UnavailableError());

        var mockWatch = new Mock<WatchService.WatchServiceClient>();
        mockWatch
            .Setup(c => c.Watch(
                It.IsAny<WatchRequest>(),
                It.IsAny<Metadata>(),
                It.IsAny<DateTime?>(),
                It.IsAny<CancellationToken>()))
            .Returns(() => script.Next());

        await using var client = NewClient(watch: mockWatch.Object);

        var events = new List<WatchEvent>();
        var act = async () =>
        {
            await foreach (var e in client.UpdatesAsync([], null, new WatchOptions { Content = WatchContent.Relationships }, default))
                events.Add(e);
        };

        await act.Should().ThrowAsync<UnavailableException>();
        var got = events.SelectMany(e => e.Updates).ToList();
        got.Select(u => u.Relationship.Resource.ObjectId).Should().Equal("doc1");
        script.CallCount.Should().Be(1);
    }

    // ── helpers ──────────────────────────────────────────────────────────────

    private static Authzed.Api.V1.Relationship RelProto(string resourceId) => new()
    {
        Resource = new ObjectReference { ObjectType = "document", ObjectId = resourceId },
        Relation = "viewer",
        Subject = new SubjectReference { Object = new ObjectReference { ObjectType = "user", ObjectId = "alice" } },
    };

    private static ReadRelationshipsResponse RelResponse(string resourceId, string cursorToken) => new()
    {
        Relationship = RelProto(resourceId),
        AfterResultCursor = new Cursor { Token = cursorToken },
    };

    private static SpiceDBClient NewClient(
        PermissionsService.PermissionsServiceClient? permissions = null,
        SchemaService.SchemaServiceClient? schema = null,
        WatchService.WatchServiceClient? watch = null,
        ExperimentalService.ExperimentalServiceClient? experimental = null) =>
        new(
            permissions ?? new Mock<PermissionsService.PermissionsServiceClient>().Object,
            schema ?? new Mock<SchemaService.SchemaServiceClient>().Object,
            watch ?? new Mock<WatchService.WatchServiceClient>().Object,
            experimental ?? new Mock<ExperimentalService.ExperimentalServiceClient>().Object);
}
