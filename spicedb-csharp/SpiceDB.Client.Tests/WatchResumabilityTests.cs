// A watch stream that dies cannot be correctly resumed unless the client
// surfaces changes_through (proto: "This token can be used in a subsequent
// WatchRequest to resume watching from this point"), and cannot survive an
// idle-timeout proxy unless the client can request
// WATCH_KIND_INCLUDE_CHECKPOINTS. These tests exercise both.

using Authzed.Api.V1;
using FluentAssertions;
using Grpc.Core;
using Moq;
using Xunit;
using static SpiceDB.Client.Tests.StreamingTestHelpers;

namespace SpiceDB.Client.Tests;

public class WatchResumabilityTests
{
    [Fact]
    public async Task UpdatesAsync_WatchEvent_ExposesUsableResumeToken()
    {
        var mockWatch = new Mock<WatchService.WatchServiceClient>();
        mockWatch
            .Setup(c => c.Watch(
                It.IsAny<WatchRequest>(),
                It.IsAny<Metadata>(),
                It.IsAny<DateTime?>(),
                It.IsAny<CancellationToken>()))
            .Returns(MakeServerStreamingCall(new FakeStreamReader<WatchResponse>(
            [
                new WatchResponse { ChangesThrough = new ZedToken { Token = "resume-me" } },
            ])));

        await using var client = NewClient(watch: mockWatch.Object);

        var events = new List<WatchEvent>();
        await foreach (var e in client.UpdatesAsync())
            events.Add(e);

        events.Should().HaveCount(1);
        events[0].ChangesThrough.Should().Be("resume-me");
    }

    [Fact]
    public async Task UpdatesAsync_WithoutIncludeCheckpoints_RequestsNoUpdateKinds()
    {
        var captured = new List<WatchRequest>();
        var mockWatch = new Mock<WatchService.WatchServiceClient>();
        mockWatch
            .Setup(c => c.Watch(
                It.IsAny<WatchRequest>(),
                It.IsAny<Metadata>(),
                It.IsAny<DateTime?>(),
                It.IsAny<CancellationToken>()))
            .Callback<WatchRequest, Metadata, DateTime?, CancellationToken>((req, _, _, _) => captured.Add(req))
            .Returns(MakeServerStreamingCall(new FakeStreamReader<WatchResponse>([])));

        await using var client = NewClient(watch: mockWatch.Object);

        await foreach (var _ in client.UpdatesAsync())
        {
        }

        captured.Should().HaveCount(1);
        captured[0].OptionalUpdateKinds.Should().BeEmpty();
    }

    [Fact]
    public async Task UpdatesAsync_IncludeCheckpoints_ReachesTheWire()
    {
        var captured = new List<WatchRequest>();
        var mockWatch = new Mock<WatchService.WatchServiceClient>();
        mockWatch
            .Setup(c => c.Watch(
                It.IsAny<WatchRequest>(),
                It.IsAny<Metadata>(),
                It.IsAny<DateTime?>(),
                It.IsAny<CancellationToken>()))
            .Callback<WatchRequest, Metadata, DateTime?, CancellationToken>((req, _, _, _) => captured.Add(req))
            .Returns(MakeServerStreamingCall(new FakeStreamReader<WatchResponse>([])));

        await using var client = NewClient(watch: mockWatch.Object);

        await foreach (var _ in client.UpdatesAsync(includeCheckpoints: true))
        {
        }

        captured.Should().HaveCount(1);
        captured[0].OptionalUpdateKinds.Should().Contain(WatchKind.IncludeCheckpoints);
        // Requesting checkpoints must not silently drop relationship updates --
        // OptionalUpdateKinds is empty-means-default, so a non-empty list is
        // the exact set requested.
        captured[0].OptionalUpdateKinds.Should().Contain(WatchKind.IncludeRelationshipUpdates);
    }

    [Fact]
    public async Task UpdatesAsync_CheckpointEvent_IsDistinguishableFromUpdateEvent()
    {
        var mockWatch = new Mock<WatchService.WatchServiceClient>();
        mockWatch
            .Setup(c => c.Watch(
                It.IsAny<WatchRequest>(),
                It.IsAny<Metadata>(),
                It.IsAny<DateTime?>(),
                It.IsAny<CancellationToken>()))
            .Returns(MakeServerStreamingCall(new FakeStreamReader<WatchResponse>(
            [
                new WatchResponse
                {
                    ChangesThrough = new ZedToken { Token = "checkpoint-rev" },
                    IsCheckpoint = true,
                },
                new WatchResponse
                {
                    ChangesThrough = new ZedToken { Token = "update-rev" },
                    Updates =
                    {
                        new Authzed.Api.V1.RelationshipUpdate
                        {
                            Operation = Authzed.Api.V1.RelationshipUpdate.Types.Operation.Touch,
                            Relationship = RelProto("doc1"),
                        },
                    },
                },
            ])));

        await using var client = NewClient(watch: mockWatch.Object);

        var events = new List<WatchEvent>();
        await foreach (var e in client.UpdatesAsync(includeCheckpoints: true))
            events.Add(e);

        events.Should().HaveCount(2);

        events[0].IsCheckpoint.Should().BeTrue();
        events[0].Updates.Should().BeEmpty();
        events[0].ChangesThrough.Should().Be("checkpoint-rev");

        events[1].IsCheckpoint.Should().BeFalse();
        events[1].Updates.Should().HaveCount(1);
        events[1].ChangesThrough.Should().Be("update-rev");
    }

    // ── helpers ──────────────────────────────────────────────────────────────

    private static Authzed.Api.V1.Relationship RelProto(string resourceId) => new()
    {
        Resource = new ObjectReference { ObjectType = "document", ObjectId = resourceId },
        Relation = "viewer",
        Subject = new SubjectReference { Object = new ObjectReference { ObjectType = "user", ObjectId = "alice" } },
    };

    private static SpiceDBClient NewClient(WatchService.WatchServiceClient watch) =>
        new(
            new Mock<PermissionsService.PermissionsServiceClient>().Object,
            new Mock<SchemaService.SchemaServiceClient>().Object,
            watch,
            new Mock<ExperimentalService.ExperimentalServiceClient>().Object);
}
