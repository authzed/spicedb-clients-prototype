// Tests for DeleteRelationshipsAsync's optional preconditions (mustMatch/
// mustNotMatch) and limit, which reach the proto's `optional_preconditions`
// and `optional_limit` fields. Mirrors spicedb-go's `WithDeleteMustMatch`/
// `WithDeleteMustNotMatch`/`WithDeleteLimit` (client/relationships.go) —
// preconditions are re-sent on every page of the auto-paging loop, and
// `limit` overrides the default 10,000-item page size while auto-paging
// keeps working (partial deletions stay allowed, same as the no-options
// default). Uses the Moq'd gRPC client seam also used by
// StreamingErrorMappingTests/LookupResultTests.

using Authzed.Api.V1;
using FluentAssertions;
using Grpc.Core;
using Moq;
using Xunit;
using static SpiceDB.Client.Tests.StreamingTestHelpers;

namespace SpiceDB.Client.Tests;

public class DeleteRelationshipsTests
{
    private static DeleteRelationshipsResponse CompleteResponse(string token = "rev1") => new()
    {
        DeletedAt = new ZedToken { Token = token },
        DeletionProgress = DeleteRelationshipsResponse.Types.DeletionProgress.Complete,
    };

    private static DeleteRelationshipsResponse PartialResponse(string token = "rev-partial") => new()
    {
        DeletedAt = new ZedToken { Token = token },
        DeletionProgress = DeleteRelationshipsResponse.Types.DeletionProgress.Partial,
    };

    private static SpiceDBClient NewClient(PermissionsService.PermissionsServiceClient permissions) =>
        new(
            permissions,
            new Mock<SchemaService.SchemaServiceClient>().Object,
            new Mock<WatchService.WatchServiceClient>().Object,
            new Mock<ExperimentalService.ExperimentalServiceClient>().Object);

    /// <summary>
    /// Sets up the mock to return the given responses in order (one per call),
    /// capturing every outbound request into <paramref name="captured"/>.
    /// </summary>
    private static Mock<PermissionsService.PermissionsServiceClient> MockThatCaptures(
        List<DeleteRelationshipsRequest> captured,
        params DeleteRelationshipsResponse[] responses)
    {
        var queue = new Queue<DeleteRelationshipsResponse>(responses);
        var mock = new Mock<PermissionsService.PermissionsServiceClient>();
        mock.Setup(c => c.DeleteRelationshipsAsync(
                It.IsAny<DeleteRelationshipsRequest>(),
                It.IsAny<Metadata>(),
                It.IsAny<DateTime?>(),
                It.IsAny<CancellationToken>()))
            .Returns((DeleteRelationshipsRequest req, Metadata _, DateTime? _, CancellationToken _) =>
            {
                captured.Add(req);
                return MakeUnaryCall(queue.Dequeue());
            });
        return mock;
    }

    // ── Default behavior (no options) is unchanged ──────────────────────────

    [Fact]
    public async Task NoOptions_PreservesDefaultBehavior()
    {
        var captured = new List<DeleteRelationshipsRequest>();
        var mock = MockThatCaptures(captured, CompleteResponse("rev1"));
        await using var client = NewClient(mock.Object);

        var revision = await client.DeleteRelationshipsAsync(new Filter("document"));

        revision.Should().Be("rev1");
        captured.Should().HaveCount(1);
        captured[0].OptionalPreconditions.Should().BeEmpty();
        captured[0].OptionalLimit.Should().Be(10_000u);
        captured[0].OptionalAllowPartialDeletions.Should().BeTrue();
    }

    // ── mustMatch / mustNotMatch build preconditions ────────────────────────

    [Fact]
    public async Task MustMatchAndMustNotMatch_BuildPreconditionsInOrder()
    {
        var captured = new List<DeleteRelationshipsRequest>();
        var mock = MockThatCaptures(captured, CompleteResponse());
        await using var client = NewClient(mock.Object);

        var guard = new Filter("document").WithResourceID("doc1");
        var forbidden = new Filter("document").WithResourceID("doc2");

        await client.DeleteRelationshipsAsync(
            new Filter("document"),
            mustMatch: [guard],
            mustNotMatch: [forbidden]);

        captured.Should().HaveCount(1);
        var preconditions = captured[0].OptionalPreconditions;
        preconditions.Should().HaveCount(2);

        preconditions[0].Operation.Should().Be(Precondition.Types.Operation.MustMatch);
        preconditions[0].Filter.OptionalResourceId.Should().Be("doc1");

        preconditions[1].Operation.Should().Be(Precondition.Types.Operation.MustNotMatch);
        preconditions[1].Filter.OptionalResourceId.Should().Be("doc2");
    }

    [Fact]
    public async Task MustMatchOnly_BuildsSingleMustMatchPrecondition()
    {
        var captured = new List<DeleteRelationshipsRequest>();
        var mock = MockThatCaptures(captured, CompleteResponse());
        await using var client = NewClient(mock.Object);

        await client.DeleteRelationshipsAsync(
            new Filter("document"),
            mustMatch: [new Filter("document").WithResourceID("doc1")]);

        captured[0].OptionalPreconditions.Should().ContainSingle();
        captured[0].OptionalPreconditions[0].Operation.Should().Be(Precondition.Types.Operation.MustMatch);
    }

    // ── limit sets OptionalLimit and keeps partial deletions allowed ───────

    [Fact]
    public async Task Limit_OverridesOptionalLimit_AndAllowsPartialDeletions()
    {
        var captured = new List<DeleteRelationshipsRequest>();
        var mock = MockThatCaptures(captured, CompleteResponse());
        await using var client = NewClient(mock.Object);

        await client.DeleteRelationshipsAsync(new Filter("document"), limit: 5);

        captured.Should().HaveCount(1);
        captured[0].OptionalLimit.Should().Be(5u);
        captured[0].OptionalAllowPartialDeletions.Should().BeTrue();
    }

    // ── Preconditions and limit are threaded into every page ────────────────

    [Fact]
    public async Task MultiPageDelete_ThreadsPreconditionsAndLimitOnEveryPage()
    {
        var captured = new List<DeleteRelationshipsRequest>();
        var mock = MockThatCaptures(captured, PartialResponse("rev-p1"), CompleteResponse("rev-final"));
        await using var client = NewClient(mock.Object);

        var guard = new Filter("document").WithResourceID("doc1");

        var revision = await client.DeleteRelationshipsAsync(
            new Filter("document"),
            mustMatch: [guard],
            limit: 2);

        revision.Should().Be("rev-final");
        captured.Should().HaveCount(2);
        foreach (var req in captured)
        {
            req.OptionalLimit.Should().Be(2u);
            req.OptionalAllowPartialDeletions.Should().BeTrue();
            req.OptionalPreconditions.Should().ContainSingle();
            req.OptionalPreconditions[0].Operation.Should().Be(Precondition.Types.Operation.MustMatch);
        }
    }

    // ── null filter still throws ─────────────────────────────────────────

    [Fact]
    public async Task NullFilter_Throws()
    {
        var mock = new Mock<PermissionsService.PermissionsServiceClient>();
        await using var client = NewClient(mock.Object);

        var act = async () => await client.DeleteRelationshipsAsync(null!);

        await act.Should().ThrowAsync<ArgumentNullException>();
    }
}
