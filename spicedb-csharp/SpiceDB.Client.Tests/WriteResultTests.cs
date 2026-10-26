using Authzed.Api.V1;
using FluentAssertions;
using Grpc.Core;
using Moq;
using Xunit;
using static SpiceDB.Client.Tests.StreamingTestHelpers;

namespace SpiceDB.Client.Tests;

public class WriteResultTests
{
    [Theory]
    [InlineData(null)]
    [InlineData("")]
    [InlineData("opaque-revision")]
    public async Task RelationshipWritesReturnNativeResultPreservingRevisionMapping(string? revision)
    {
        var permissions = new Mock<PermissionsService.PermissionsServiceClient>();
        var response = new WriteRelationshipsResponse();
        if (revision is not null) response.WrittenAt = new Authzed.Api.V1.ZedToken { Token = revision };
        permissions.Setup(c => c.WriteRelationshipsAsync(It.IsAny<WriteRelationshipsRequest>(), It.IsAny<Metadata>(), It.IsAny<DateTime?>(), It.IsAny<CancellationToken>()))
            .Returns(MakeUnaryCall(response));
        await using var client = new SpiceDBClient(permissions.Object, new Mock<SchemaService.SchemaServiceClient>().Object, new Mock<WatchService.WatchServiceClient>().Object, new Mock<ExperimentalService.ExperimentalServiceClient>().Object);
        WriteResult result = await client.WriteAsync(new Transaction());
        result.Revision.Value.Should().Be(revision ?? "");
        result.Should().Be(new WriteResult(new ZedToken(revision ?? "")));
    }
}
