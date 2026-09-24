// Tests for SpiceDBClient.ExperimentalRoaringLookupResourcesAsync -- the
// request built against the materialize-tier RoaringLookupResourcesService,
// and the mapping from ExperimentalRoaringLookupResourcesResponse onto the
// native RoaringLookupResourcesResult (byte[], not the proto ByteString).

using Authzed.Api.Materialize.V0;
using Authzed.Api.V1;
using FluentAssertions;
using Google.Protobuf;
using Grpc.Core;
using Moq;
using SpiceDB.Client;
using Xunit;
using static SpiceDB.Client.Tests.StreamingTestHelpers;

namespace SpiceDB.Client.Tests;

public class RoaringLookupResourcesTests
{
    private static SpiceDBClient NewClient(
        out Mock<RoaringLookupResourcesService.RoaringLookupResourcesServiceClient> mockMaterialize,
        out List<ExperimentalRoaringLookupResourcesRequest> capturedRequests)
    {
        var captured = new List<ExperimentalRoaringLookupResourcesRequest>();
        var mock = new Mock<RoaringLookupResourcesService.RoaringLookupResourcesServiceClient>();
        mock.Setup(c => c.ExperimentalRoaringLookupResourcesAsync(
                It.IsAny<ExperimentalRoaringLookupResourcesRequest>(),
                It.IsAny<Metadata>(),
                It.IsAny<DateTime?>(),
                It.IsAny<CancellationToken>()))
            .Callback<ExperimentalRoaringLookupResourcesRequest, Metadata, DateTime?, CancellationToken>(
                (req, _, _, _) => captured.Add(req))
            .Returns<ExperimentalRoaringLookupResourcesRequest, Metadata, DateTime?, CancellationToken>(
                (_, _, _, _) => MakeUnaryCall(new ExperimentalRoaringLookupResourcesResponse
                {
                    Bitmap = ByteString.CopyFrom([1, 2, 3, 4]),
                    Cardinality = 2,
                    AtRevision = new ZedToken { Token = "tok" },
                }));

        mockMaterialize = mock;
        capturedRequests = captured;

        return new SpiceDBClient(
            new Mock<PermissionsService.PermissionsServiceClient>().Object,
            new Mock<SchemaService.SchemaServiceClient>().Object,
            new Mock<WatchService.WatchServiceClient>().Object,
            new Mock<ExperimentalService.ExperimentalServiceClient>().Object,
            mock.Object);
    }

    [Fact]
    public async Task ExperimentalRoaringLookupResourcesAsync_BuildsRequest()
    {
        var client = NewClient(out _, out var captured);

        await client.ExperimentalRoaringLookupResourcesAsync(
            Consistency.Full(), "document", "view", "user", "alice");

        captured.Should().HaveCount(1);
        captured[0].ResourceObjectType.Should().Be("document");
        captured[0].Permission.Should().Be("view");
        captured[0].Subject.Object.ObjectType.Should().Be("user");
        captured[0].Subject.Object.ObjectId.Should().Be("alice");
    }

    [Fact]
    public async Task ExperimentalRoaringLookupResourcesAsync_MapsResponse()
    {
        var client = NewClient(out _, out _);

        var result = await client.ExperimentalRoaringLookupResourcesAsync(
            Consistency.Full(), "document", "view", "user", "alice");

        result.Bitmap.Should().Equal(1, 2, 3, 4);
        result.Cardinality.Should().Be(2u);
        result.AtRevision.Should().Be("tok");
    }

    [Fact]
    public async Task ExperimentalRoaringLookupResourcesAsync_ThrowsOnNullConsistency()
    {
        var client = NewClient(out _, out _);

        var act = async () => await client.ExperimentalRoaringLookupResourcesAsync(
            null!, "document", "view", "user", "alice");
        await act.Should().ThrowAsync<ArgumentNullException>();
    }
}
