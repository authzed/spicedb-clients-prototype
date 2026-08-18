// Proves that RpcExceptions raised while iterating a streaming/bulk RPC are
// mapped to typed SpiceDBException subtypes at the `await foreach` consumer,
// matching the mapping already done for unary calls via RetryAsync.
// (Audit CI-2.)

using Authzed.Api.V1;
using FluentAssertions;
using Grpc.Core;
using Moq;
using Xunit;
using static SpiceDB.Client.Tests.StreamingTestHelpers;

namespace SpiceDB.Client.Tests;

public class StreamingErrorMappingTests
{
    // ── ReadRelationshipsAsync (server-streaming, paged) ────────────────────

    [Fact]
    public async Task ReadRelationshipsAsync_StreamThrowsNotFound_SurfacesTypedException()
    {
        var mockPermissions = new Mock<PermissionsService.PermissionsServiceClient>();
        mockPermissions
            .Setup(c => c.ReadRelationships(
                It.IsAny<ReadRelationshipsRequest>(),
                It.IsAny<Metadata>(),
                It.IsAny<DateTime?>(),
                It.IsAny<CancellationToken>()))
            .Returns(MakeServerStreamingCall(
                new FakeStreamReader<ReadRelationshipsResponse>([], NotFoundError())));

        await using var client = NewClient(permissions: mockPermissions.Object);

        var act = async () =>
        {
            await foreach (var _ in client.ReadRelationshipsAsync(
                Consistency.Full(), new Filter("document")))
            {
            }
        };

        (await act.Should().ThrowAsync<NotFoundException>()).Which.InnerException.Should().BeOfType<RpcException>();
    }

    [Fact]
    public async Task ReadRelationshipsAsync_StreamThrowsAfterItems_SurfacesTypedException()
    {
        var firstItem = new ReadRelationshipsResponse
        {
            Relationship = new Authzed.Api.V1.Relationship
            {
                Resource = new ObjectReference { ObjectType = "document", ObjectId = "doc1" },
                Relation = "viewer",
                Subject = new SubjectReference { Object = new ObjectReference { ObjectType = "user", ObjectId = "alice" } },
            },
            AfterResultCursor = new Cursor { Token = "cursor1" },
        };

        var mockPermissions = new Mock<PermissionsService.PermissionsServiceClient>();
        mockPermissions
            .Setup(c => c.ReadRelationships(
                It.IsAny<ReadRelationshipsRequest>(),
                It.IsAny<Metadata>(),
                It.IsAny<DateTime?>(),
                It.IsAny<CancellationToken>()))
            .Returns(MakeServerStreamingCall(
                new FakeStreamReader<ReadRelationshipsResponse>([firstItem], NotFoundError())));

        await using var client = NewClient(permissions: mockPermissions.Object);

        var seen = 0;
        var act = async () =>
        {
            await foreach (var _ in client.ReadRelationshipsAsync(
                Consistency.Full(), new Filter("document")))
            {
                seen++;
            }
        };

        await act.Should().ThrowAsync<NotFoundException>();
        seen.Should().Be(1); // the item before the error was still yielded
    }

    // ── UpdatesAsync / Watch (server-streaming, single call) ────────────────

    [Fact]
    public async Task UpdatesAsync_StreamThrowsNotFound_SurfacesTypedException()
    {
        var mockWatch = new Mock<WatchService.WatchServiceClient>();
        mockWatch
            .Setup(c => c.Watch(
                It.IsAny<WatchRequest>(),
                It.IsAny<Metadata>(),
                It.IsAny<DateTime?>(),
                It.IsAny<CancellationToken>()))
            .Returns(MakeServerStreamingCall(
                new FakeStreamReader<WatchResponse>([], NotFoundError())));

        await using var client = NewClient(watch: mockWatch.Object);

        var act = async () =>
        {
            await foreach (var _ in client.UpdatesAsync())
            {
            }
        };

        await act.Should().ThrowAsync<NotFoundException>();
    }

    // ── LookupResourcesAsync (server-streaming, paged) ───────────────────────

    [Fact]
    public async Task LookupResourcesAsync_StreamThrowsNotFound_SurfacesTypedException()
    {
        var mockPermissions = new Mock<PermissionsService.PermissionsServiceClient>();
        mockPermissions
            .Setup(c => c.LookupResources(
                It.IsAny<LookupResourcesRequest>(),
                It.IsAny<Metadata>(),
                It.IsAny<DateTime?>(),
                It.IsAny<CancellationToken>()))
            .Returns(MakeServerStreamingCall(
                new FakeStreamReader<LookupResourcesResponse>([], NotFoundError())));

        await using var client = NewClient(permissions: mockPermissions.Object);

        var act = async () =>
        {
            await foreach (var _ in client.LookupResourcesAsync(
                Consistency.Full(), "document", "view", "user", "alice"))
            {
            }
        };

        await act.Should().ThrowAsync<NotFoundException>();
    }

    // ── LookupSubjectsAsync (server-streaming, single call) ──────────────────

    [Fact]
    public async Task LookupSubjectsAsync_StreamThrowsNotFound_SurfacesTypedException()
    {
        var mockPermissions = new Mock<PermissionsService.PermissionsServiceClient>();
        mockPermissions
            .Setup(c => c.LookupSubjects(
                It.IsAny<LookupSubjectsRequest>(),
                It.IsAny<Metadata>(),
                It.IsAny<DateTime?>(),
                It.IsAny<CancellationToken>()))
            .Returns(MakeServerStreamingCall(
                new FakeStreamReader<LookupSubjectsResponse>([], NotFoundError())));

        await using var client = NewClient(permissions: mockPermissions.Object);

        var act = async () =>
        {
            await foreach (var _ in client.LookupSubjectsAsync(
                Consistency.Full(), "document", "doc1", "view", "user"))
            {
            }
        };

        await act.Should().ThrowAsync<NotFoundException>();
    }

    // ── ExportRelationshipsAsync (server-streaming, paged) ───────────────────

    [Fact]
    public async Task ExportRelationshipsAsync_StreamThrowsNotFound_SurfacesTypedException()
    {
        var mockPermissions = new Mock<PermissionsService.PermissionsServiceClient>();
        mockPermissions
            .Setup(c => c.ExportBulkRelationships(
                It.IsAny<ExportBulkRelationshipsRequest>(),
                It.IsAny<Metadata>(),
                It.IsAny<DateTime?>(),
                It.IsAny<CancellationToken>()))
            .Returns(MakeServerStreamingCall(
                new FakeStreamReader<ExportBulkRelationshipsResponse>([], NotFoundError())));

        await using var client = NewClient(permissions: mockPermissions.Object);

        var act = async () =>
        {
            await foreach (var _ in client.ExportRelationshipsAsync(Consistency.Full()))
            {
            }
        };

        await act.Should().ThrowAsync<NotFoundException>();
    }

    // ── ImportRelationshipsAsync (client-streaming) ──────────────────────────

    [Fact]
    public async Task ImportRelationshipsAsync_SendThrowsNotFound_SurfacesTypedException()
    {
        var mockPermissions = new Mock<PermissionsService.PermissionsServiceClient>();
        mockPermissions
            .Setup(c => c.ImportBulkRelationships(
                It.IsAny<Metadata>(),
                It.IsAny<DateTime?>(),
                It.IsAny<CancellationToken>()))
            .Returns(MakeClientStreamingCall(
                new ThrowingClientStreamWriter<ImportBulkRelationshipsRequest>(NotFoundError()),
                Task.FromException<ImportBulkRelationshipsResponse>(NotFoundError())));

        await using var client = NewClient(permissions: mockPermissions.Object);

        var act = async () => await client.ImportRelationshipsAsync(OneRelationship());

        await act.Should().ThrowAsync<NotFoundException>();
    }

    // ── helpers ──────────────────────────────────────────────────────────────

    private static async IAsyncEnumerable<Relationship> OneRelationship()
    {
        yield return Relationship.FromTriple("document", "doc1", "viewer", "user", "alice");
        await Task.CompletedTask;
    }

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
