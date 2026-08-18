// Retry safety, per root DESIGN.md "RULE: Automatic retry is for idempotent
// operations only":
//
//   - Reads (e.g. ReadSchemaAsync) retry on a transient error.
//   - Mutations (e.g. WriteAsync/WriteRelationships) are attempted exactly
//     once, even on a retryable error -- a WriteRelationships carrying
//     OPERATION_CREATE or preconditions is not idempotent, and retrying a
//     lost response would surface ALREADY_EXISTS/FAILED_PRECONDITION for a
//     write that in fact succeeded.
//   - RESOURCE_EXHAUSTED is never retried, on either a read or a mutation.
//
// See ErrorsTests.cs for the inverted TransientCodes/IsTransient coverage of
// the RESOURCE_EXHAUSTED half of this guarantee, and
// StreamingEstablishmentRetryTests.cs for the same guarantees on streaming
// RPC establishment.

using Authzed.Api.V1;
using FluentAssertions;
using Grpc.Core;
using Moq;
using Xunit;
using static SpiceDB.Client.Tests.StreamingTestHelpers;

namespace SpiceDB.Client.Tests;

public class UnaryRetrySafetyTests
{
    private static SpiceDBClient NewClient(
        PermissionsService.PermissionsServiceClient? permissions = null,
        SchemaService.SchemaServiceClient? schema = null) =>
        new(
            permissions ?? new Mock<PermissionsService.PermissionsServiceClient>().Object,
            schema ?? new Mock<SchemaService.SchemaServiceClient>().Object,
            new Mock<WatchService.WatchServiceClient>().Object,
            new Mock<ExperimentalService.ExperimentalServiceClient>().Object);

    private static Relationship SampleRelationship() =>
        Relationship.FromTriple("document", "readme", "viewer", "user", "jimmy");

    // ── Reads retry ──────────────────────────────────────────────────────

    [Fact]
    public async Task ReadSchemaAsync_RetriesOnTransientError_ThenSucceeds()
    {
        var callCount = 0;
        var mockSchema = new Mock<SchemaService.SchemaServiceClient>();
        mockSchema
            .Setup(c => c.ReadSchemaAsync(
                It.IsAny<ReadSchemaRequest>(),
                It.IsAny<Metadata>(),
                It.IsAny<DateTime?>(),
                It.IsAny<CancellationToken>()))
            .Returns(() =>
            {
                callCount++;
                if (callCount == 1)
                    throw UnavailableError();
                return MakeUnaryCall(new ReadSchemaResponse { SchemaText = "definition user {}" });
            });

        await using var client = NewClient(schema: mockSchema.Object);

        var (schema, _) = await client.ReadSchemaAsync();

        schema.Should().Be("definition user {}");
        callCount.Should().Be(2, "a transient error on a read must be retried");
    }

    [Fact]
    public async Task ReadSchemaAsync_NeverRetriesResourceExhausted()
    {
        var callCount = 0;
        var mockSchema = new Mock<SchemaService.SchemaServiceClient>();
        mockSchema
            .Setup(c => c.ReadSchemaAsync(
                It.IsAny<ReadSchemaRequest>(),
                It.IsAny<Metadata>(),
                It.IsAny<DateTime?>(),
                It.IsAny<CancellationToken>()))
            .Returns(() =>
            {
                callCount++;
                throw new RpcException(new Status(StatusCode.ResourceExhausted, "quota"));
            });

        await using var client = NewClient(schema: mockSchema.Object);

        var act = async () => await client.ReadSchemaAsync();

        await act.Should().ThrowAsync<ResourceExhaustedException>();
        callCount.Should().Be(1, "RESOURCE_EXHAUSTED must never be retried");
    }

    // ── Mutations do not retry ───────────────────────────────────────────

    [Fact]
    public async Task WriteAsync_AttemptsExactlyOnce_OnRetryableError()
    {
        var callCount = 0;
        var mockPermissions = new Mock<PermissionsService.PermissionsServiceClient>();
        mockPermissions
            .Setup(c => c.WriteRelationshipsAsync(
                It.IsAny<WriteRelationshipsRequest>(),
                It.IsAny<Metadata>(),
                It.IsAny<DateTime?>(),
                It.IsAny<CancellationToken>()))
            .Returns(() =>
            {
                callCount++;
                throw UnavailableError();
            });

        await using var client = NewClient(permissions: mockPermissions.Object);
        var txn = new Transaction();
        txn.Create(SampleRelationship());

        var act = async () => await client.WriteAsync(txn);

        await act.Should().ThrowAsync<UnavailableException>();
        callCount.Should().Be(1, "a mutation must be attempted exactly once, even on a retryable error");
    }

    [Fact]
    public async Task WriteAsync_NeverRetriesResourceExhausted()
    {
        var callCount = 0;
        var mockPermissions = new Mock<PermissionsService.PermissionsServiceClient>();
        mockPermissions
            .Setup(c => c.WriteRelationshipsAsync(
                It.IsAny<WriteRelationshipsRequest>(),
                It.IsAny<Metadata>(),
                It.IsAny<DateTime?>(),
                It.IsAny<CancellationToken>()))
            .Returns(() =>
            {
                callCount++;
                throw new RpcException(new Status(StatusCode.ResourceExhausted, "quota"));
            });

        await using var client = NewClient(permissions: mockPermissions.Object);
        var txn = new Transaction();
        txn.Create(SampleRelationship());

        var act = async () => await client.WriteAsync(txn);

        await act.Should().ThrowAsync<ResourceExhaustedException>();
        callCount.Should().Be(1);
    }

    // ── Backoff jitter ────────────────────────────────────────────────────

    [Fact]
    public void JitteredDelay_VariesBetweenCalls()
    {
        var jitteredDelay = typeof(SpiceDBClient).GetMethod(
            "JitteredDelay",
            System.Reflection.BindingFlags.NonPublic | System.Reflection.BindingFlags.Static)!;
        var cap = TimeSpan.FromMilliseconds(400);

        var seen = Enumerable.Range(0, 50)
            .Select(_ => (TimeSpan)jitteredDelay.Invoke(null, [cap])!)
            .ToList();

        seen.Distinct().Count().Should().BeGreaterThan(1, "backoff should vary between calls");
        seen.Should().OnlyContain(v => v >= TimeSpan.Zero && v <= cap);
    }
}
