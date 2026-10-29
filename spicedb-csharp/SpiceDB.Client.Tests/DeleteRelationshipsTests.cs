// Tests for DeleteRelationshipsAsync's optional preconditions (mustMatch/
// mustNotMatch) and limit, which reach the proto's `optional_preconditions`
// and `optional_limit` fields. Mirrors spicedb-go's `WithDeleteMustMatch`/
// `WithDeleteMustNotMatch`/`WithDeleteLimit` (client/relationships.go) —
// preconditions are re-sent on every page of the auto-paging loop, and
// `limit` overrides the default 1,000-item page size while auto-paging
// keeps working (partial deletions stay allowed, same as the no-options
// default). Uses the Moq'd gRPC client seam also used by
// StreamingErrorMappingTests/LookupResultTests.
//
// Also covers DeleteRelationshipsWithOptionsAsync/DeleteAllOptions
// (same knobs plus `Cursor`, which reaches `optional_cursor`) and the
// auto-paging loop's transparent use of `after_result_cursor` between pages.

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
        DeletedAt = new Authzed.Api.V1.ZedToken { Token = token },
        DeletionProgress = DeleteRelationshipsResponse.Types.DeletionProgress.Complete,
    };

    private static DeleteRelationshipsResponse PartialResponse(string token = "rev-partial") => new()
    {
        DeletedAt = new Authzed.Api.V1.ZedToken { Token = token },
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

    [Theory]
    [InlineData(null)]
    [InlineData("")]
    public async Task NonAtomicDeletionOmitsAbsentOrEmptyResponseCursors(string? cursor)
    {
        var captured = new List<DeleteRelationshipsRequest>();
        var partial = PartialResponse();
        var complete = CompleteResponse();
        partial.RelationshipsDeletedCount = 2;
        complete.RelationshipsDeletedCount = 1;
        if (cursor is not null) { partial.AfterResultCursor = new Cursor { Token = cursor }; complete.AfterResultCursor = new Cursor { Token = cursor }; }
        var mock = MockThatCaptures(captured, partial, complete);
        await using var client = NewClient(mock.Object);
        var result = await client.DeleteAllRelationshipsNonAtomicAsync(new Filter("document"), new DeleteAllOptions { ResumeCursor = "" });
        captured.Should().HaveCount(2);
        captured.Should().OnlyContain(request => request.OptionalCursor == null);
        result.ResumeCursor.Should().Be(cursor);
        result.DeletedCount.Should().Be(3);
    }

    // ── Default behavior (no options) is unchanged ──────────────────────────

    [Fact]
    public async Task NoOptions_PreservesDefaultBehavior()
    {
        var captured = new List<DeleteRelationshipsRequest>();
        var mock = MockThatCaptures(captured, CompleteResponse("rev1"));
        await using var client = NewClient(mock.Object);

        var revision = await client.DeleteAllRelationshipsNonAtomicAsync(new Filter("document"), null, default);

        revision.Revision.Value.Should().Be("rev1");
        captured.Should().HaveCount(1);
        captured[0].OptionalPreconditions.Should().BeEmpty();
        captured[0].OptionalLimit.Should().Be(1_000u);
        captured[0].OptionalAllowPartialDeletions.Should().BeTrue();
    }

    // ── mustMatch / mustNotMatch build preconditions ────────────────────────

    [Fact]
    public async Task MustMatchAndMustNotMatch_BuildPreconditionsInOrder()
    {
        var captured = new List<DeleteRelationshipsRequest>();
        var mock = MockThatCaptures(captured, CompleteResponse());
        await using var client = NewClient(mock.Object);

        var guard = new Filter("document").WithResourceId("doc1");
        var forbidden = new Filter("document").WithResourceId("doc2");

        await client.DeleteAllRelationshipsNonAtomicAsync(new Filter("document"), new DeleteAllOptions { MustMatch = [guard], MustNotMatch = [forbidden] }, default);

        captured.Should().HaveCount(1);
        var preconditions = captured[0].OptionalPreconditions;
        preconditions.Should().HaveCount(2);

        preconditions[0].Operation.Should().Be(Authzed.Api.V1.Precondition.Types.Operation.MustMatch);
        preconditions[0].Filter.OptionalResourceId.Should().Be("doc1");

        preconditions[1].Operation.Should().Be(Authzed.Api.V1.Precondition.Types.Operation.MustNotMatch);
        preconditions[1].Filter.OptionalResourceId.Should().Be("doc2");
    }

    [Fact]
    public async Task MustMatchOnly_BuildsSingleMustMatchPrecondition()
    {
        var captured = new List<DeleteRelationshipsRequest>();
        var mock = MockThatCaptures(captured, CompleteResponse());
        await using var client = NewClient(mock.Object);

        await client.DeleteAllRelationshipsNonAtomicAsync(new Filter("document"), new DeleteAllOptions { MustMatch = [new Filter("document").WithResourceId("doc1")] }, default);

        captured[0].OptionalPreconditions.Should().ContainSingle();
        captured[0].OptionalPreconditions[0].Operation.Should().Be(Authzed.Api.V1.Precondition.Types.Operation.MustMatch);
    }

    // ── limit sets OptionalLimit and keeps partial deletions allowed ───────

    [Fact]
    public async Task Limit_OverridesOptionalLimit_AndAllowsPartialDeletions()
    {
        var captured = new List<DeleteRelationshipsRequest>();
        var mock = MockThatCaptures(captured, CompleteResponse());
        await using var client = NewClient(mock.Object);

        await client.DeleteAllRelationshipsNonAtomicAsync(new Filter("document"), new DeleteAllOptions { PageSize = 5 }, default);

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

        var guard = new Filter("document").WithResourceId("doc1");

        var revision = await client.DeleteAllRelationshipsNonAtomicAsync(new Filter("document"), new DeleteAllOptions { MustMatch = [guard], PageSize = 2 }, default);

        revision.Revision.Value.Should().Be("rev-final");
        captured.Should().HaveCount(2);
        foreach (var req in captured)
        {
            req.OptionalLimit.Should().Be(2u);
            req.OptionalAllowPartialDeletions.Should().BeTrue();
            req.OptionalPreconditions.Should().ContainSingle();
            req.OptionalPreconditions[0].Operation.Should().Be(Authzed.Api.V1.Precondition.Types.Operation.MustMatch);
        }
    }

    // ── null filter still throws ─────────────────────────────────────────

    [Fact]
    public async Task NullFilter_Throws()
    {
        var mock = new Mock<PermissionsService.PermissionsServiceClient>();
        await using var client = NewClient(mock.Object);

        var act = async () => await client.DeleteAllRelationshipsNonAtomicAsync(null!, null, default);

        await act.Should().ThrowAsync<InvalidArgumentException>();
    }

    // ── DeleteRelationshipsWithOptionsAsync mirrors the plain overload ─────

    [Fact]
    public async Task WithOptions_NullOptions_PreservesDefaultBehavior()
    {
        var captured = new List<DeleteRelationshipsRequest>();
        var mock = MockThatCaptures(captured, CompleteResponse("rev1"));
        await using var client = NewClient(mock.Object);

        var revision = await client.DeleteAllRelationshipsNonAtomicAsync(new Filter("document"), null!, default);

        revision.Revision.Value.Should().Be("rev1");
        captured.Should().HaveCount(1);
        captured[0].OptionalPreconditions.Should().BeEmpty();
        captured[0].OptionalLimit.Should().Be(1_000u);
        captured[0].OptionalAllowPartialDeletions.Should().BeTrue();
        captured[0].OptionalCursor.Should().BeNull();
    }

    [Fact]
    public async Task WithOptions_MustMatchMustNotMatchAndLimit_BehaveLikePlainOverload()
    {
        var captured = new List<DeleteRelationshipsRequest>();
        var mock = MockThatCaptures(captured, CompleteResponse());
        await using var client = NewClient(mock.Object);

        var guard = new Filter("document").WithResourceId("doc1");
        var forbidden = new Filter("document").WithResourceId("doc2");

        await client.DeleteAllRelationshipsNonAtomicAsync(new Filter("document"), new DeleteAllOptions
        {
            MustMatch = [guard],
            MustNotMatch = [forbidden],
            PageSize = 5,
        }, default);

        captured.Should().HaveCount(1);
        captured[0].OptionalLimit.Should().Be(5u);
        var preconditions = captured[0].OptionalPreconditions;
        preconditions.Should().HaveCount(2);
        preconditions[0].Operation.Should().Be(Authzed.Api.V1.Precondition.Types.Operation.MustMatch);
        preconditions[1].Operation.Should().Be(Authzed.Api.V1.Precondition.Types.Operation.MustNotMatch);
    }

    // ── Cursor option seeds the first page; AfterResultCursor threads pages ─

    [Fact]
    public async Task WithOptions_Cursor_SeedsFirstRequestsOptionalCursor()
    {
        var captured = new List<DeleteRelationshipsRequest>();
        var mock = MockThatCaptures(captured, CompleteResponse());
        await using var client = NewClient(mock.Object);

        await client.DeleteAllRelationshipsNonAtomicAsync(new Filter("document"), new DeleteAllOptions { ResumeCursor = "resume-token" }, default);

        captured.Should().HaveCount(1);
        captured[0].OptionalCursor.Should().NotBeNull();
        captured[0].OptionalCursor.Token.Should().Be("resume-token");
    }

    [Fact]
    public async Task MultiPageDelete_ThreadsAfterResultCursorIntoNextPagesOptionalCursor()
    {
        var captured = new List<DeleteRelationshipsRequest>();
        var partial = PartialResponse("rev-p1");
        partial.AfterResultCursor = new Cursor { Token = "server-issued-cursor" };
        var mock = MockThatCaptures(captured, partial, CompleteResponse("rev-final"));
        await using var client = NewClient(mock.Object);

        var revision = await client.DeleteAllRelationshipsNonAtomicAsync(new Filter("document"), new DeleteAllOptions { PageSize = 2 }, default);

        revision.Revision.Value.Should().Be("rev-final");
        captured.Should().HaveCount(2);
        captured[0].OptionalCursor.Should().BeNull();
        captured[1].OptionalCursor.Should().NotBeNull();
        captured[1].OptionalCursor.Token.Should().Be("server-issued-cursor");
    }

    // ── null filter still throws on the WithOptions overload ───────────────

    [Fact]
    public async Task WithOptions_NullFilter_Throws()
    {
        var mock = new Mock<PermissionsService.PermissionsServiceClient>();
        await using var client = NewClient(mock.Object);

        var act = async () => await client.DeleteAllRelationshipsNonAtomicAsync(null!, new DeleteAllOptions(), default);

        await act.Should().ThrowAsync<InvalidArgumentException>();
    }
}
