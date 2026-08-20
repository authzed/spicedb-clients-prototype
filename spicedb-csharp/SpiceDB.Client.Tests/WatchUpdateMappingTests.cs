// Proves that UpdateFromProto maps an unrecognized watch
// RelationshipUpdate.Operation — OPERATION_UNSPECIFIED, or any future wire
// value added after this client shipped — to UpdateOperation.Unspecified,
// never to UpdateOperation.Touch.
//
// Before this fix the mapper's `_ =>` arm returned Touch, so any operation the
// client could not interpret was reported as a write. A cache or index mirror
// consuming the watch stream would upsert a relationship on an update it did
// not understand — one that may in fact have been a delete.
//
// Root DESIGN.md, "RULE: A conversion that cannot preserve meaning must fail",
// clause 2: server-supplied values the client does not recognise MUST NOT
// raise, and MUST map to the safe, non-permissive default — never a grant, and
// never a write. This matches ToTreeOperation and both permissionship mappers
// in SpiceDBClient.cs, which already degrade to their Unspecified value.

using Authzed.Api.V1;
using FluentAssertions;
using Grpc.Core;
using Moq;
using Xunit;

namespace SpiceDB.Client.Tests;

public class WatchUpdateMappingTests
{
    [Theory]
    // OPERATION_UNSPECIFIED, explicitly on the wire.
    [InlineData(0)]
    // A discriminant no version of this client knows about, standing in for an
    // operation added to the proto after this client shipped.
    [InlineData(9999)]
    public async Task UpdatesAsync_UnrecognizedOperation_MapsToUnspecifiedNotTouch(int rawOperation)
    {
        var script = new StreamCallScript<WatchResponse>()
            .Then([new WatchResponse
            {
                Updates =
                {
                    new Authzed.Api.V1.RelationshipUpdate
                    {
                        Operation = (Authzed.Api.V1.RelationshipUpdate.Types.Operation)rawOperation,
                        Relationship = RelProto("doc1"),
                    },
                },
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
        await foreach (var e in client.UpdatesAsync())
            events.Add(e);

        events.Should().HaveCount(1);
        var got = events[0].Updates;
        got.Should().HaveCount(1);
        got[0].Operation.Should().Be(UpdateOperation.Unspecified);
        got[0].Operation.Should().NotBe(
            UpdateOperation.Touch,
            "an operation the client cannot interpret must never be reported as a write");
        got[0].Relationship.ResourceID.Should().Be("doc1");
    }

    [Fact]
    public async Task UpdatesAsync_RecognizedOperations_StillMapToThemselves()
    {
        var script = new StreamCallScript<WatchResponse>()
            .Then([new WatchResponse
            {
                Updates =
                {
                    new Authzed.Api.V1.RelationshipUpdate
                    {
                        Operation = Authzed.Api.V1.RelationshipUpdate.Types.Operation.Create,
                        Relationship = RelProto("doc1"),
                    },
                    new Authzed.Api.V1.RelationshipUpdate
                    {
                        Operation = Authzed.Api.V1.RelationshipUpdate.Types.Operation.Touch,
                        Relationship = RelProto("doc2"),
                    },
                    new Authzed.Api.V1.RelationshipUpdate
                    {
                        Operation = Authzed.Api.V1.RelationshipUpdate.Types.Operation.Delete,
                        Relationship = RelProto("doc3"),
                    },
                },
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
        await foreach (var e in client.UpdatesAsync())
            events.Add(e);

        events.Should().HaveCount(1);
        var got = events[0].Updates;

        got.Select(u => u.Operation).Should().Equal(
            UpdateOperation.Create,
            UpdateOperation.Touch,
            UpdateOperation.Delete);
        got.Select(u => u.Relationship.ResourceID).Should().Equal("doc1", "doc2", "doc3");
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
