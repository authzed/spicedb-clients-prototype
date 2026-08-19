// Bulk-check chunking.
//
// SpiceDB rejects a CheckBulkPermissions request carrying more items than
// maxBulkCheckCount — 10,000, a hard-coded const in
// internal/services/v1/bulkcheck.go with no flag to raise or lower it — with
// ERROR_REASON_TOO_MANY_CHECKS_IN_REQUEST. Nothing in the proto enforces this
// (CheckBulkPermissionsRequest.items carries only a per-item `required` rule,
// not a collection-size rule), so the client is what has to split large
// inputs.

using Authzed.Api.V1;
using FluentAssertions;
using Grpc.Core;
using Moq;
using SpiceDB.Client;
using Xunit;
using static SpiceDB.Client.Tests.StreamingTestHelpers;

namespace SpiceDB.Client.Tests;

public class CheckChunkingTests
{
    /// <summary>Mirrors the client's own DefaultCheckBatchSize, which is private.</summary>
    private const int CheckBatchSize = 1_000;

    private const int Total = CheckBatchSize * 2 + 7;

    private static SpiceDBClient NewClient(PermissionsService.PermissionsServiceClient permissions) =>
        new(
            permissions,
            new Mock<SchemaService.SchemaServiceClient>().Object,
            new Mock<WatchService.WatchServiceClient>().Object,
            new Mock<ExperimentalService.ExperimentalServiceClient>().Object);

    /// <summary>
    /// Records the item count of every CheckBulkPermissions request and answers
    /// each one, echoing the item's resource ID back through
    /// MissingRequiredContext so a caller can prove which request item each
    /// result came from — and therefore that concatenating chunk responses
    /// preserved input order.
    /// </summary>
    private sealed class Recorder
    {
        public List<int> Sizes { get; } = [];

        /// <summary>When >= 0, the request at that index returns one fewer pair than asked.</summary>
        public int ShortAtRequest { get; init; } = -1;

        /// <summary>
        /// When >= 0, the pair at that ABSOLUTE index — counted across every request, the way
        /// the caller counts — leaves its <c>Response</c> oneof unset. That is the guard whose
        /// message carries an index in this client: a per-item <c>Error</c> is routed straight
        /// through <c>ErrorMapper</c> and never gains a "check item N" prefix here.
        /// </summary>
        public int MalformedAtAbsolute { get; init; } = -1;

        public Mock<PermissionsService.PermissionsServiceClient> Mock()
        {
            var mock = new Mock<PermissionsService.PermissionsServiceClient>();
            mock.Setup(c => c.CheckBulkPermissionsAsync(
                    It.IsAny<CheckBulkPermissionsRequest>(),
                    It.IsAny<Metadata>(),
                    It.IsAny<DateTime?>(),
                    It.IsAny<CancellationToken>()))
                .Returns((CheckBulkPermissionsRequest req, Metadata _, DateTime? _, CancellationToken _) =>
                    MakeUnaryCall(Respond(req)));
            return mock;
        }

        private CheckBulkPermissionsResponse Respond(CheckBulkPermissionsRequest req)
        {
            var index = Sizes.Count;
            var numberBase = Sizes.Sum();
            Sizes.Add(req.Items.Count);

            var items = (ShortAtRequest == index && req.Items.Count > 0
                ? req.Items.Take(req.Items.Count - 1)
                : req.Items).ToList();

            var resp = new CheckBulkPermissionsResponse { CheckedAt = new ZedToken { Token = "tok" } };
            for (var i = 0; i < items.Count; i++)
            {
                if (MalformedAtAbsolute == numberBase + i)
                {
                    // Neither Item nor Error set — the oneof left empty.
                    resp.Pairs.Add(new CheckBulkPermissionsPair());
                    continue;
                }
                resp.Pairs.Add(new CheckBulkPermissionsPair
                {
                    Item = new CheckBulkPermissionsResponseItem
                    {
                        Permissionship = CheckPermissionResponse.Types.Permissionship.HasPermission,
                        PartialCaveatInfo = new Authzed.Api.V1.PartialCaveatInfo
                        {
                            MissingRequiredContext = { items[i].Resource.ObjectId },
                        },
                    },
                });
            }
            return resp;
        }
    }

    /// <summary>n relationships whose resource IDs are their zero-padded index.</summary>
    private static Relationship[] NumberedRels(int n) =>
        [.. Enumerable.Range(0, n).Select(i =>
            Relationship.FromTriple("document", i.ToString("D5"), "view", "user", "alice"))];

    [Fact]
    public async Task CheckPermissionsAsync_OversizedInput_IsSplitIntoChunks()
    {
        var recorder = new Recorder();
        await using var client = NewClient(recorder.Mock().Object);

        var results = await client.CheckPermissionsAsync(
            Consistency.Full(), "view", default, NumberedRels(Total));

        results.Should().HaveCount(Total);
        recorder.Sizes.Should().Equal(CheckBatchSize, CheckBatchSize, 7);
    }

    [Fact]
    public async Task CheckPermissionsAsync_ChunkedResults_StayInInputOrder()
    {
        // The echo carries each item's own resource ID, so a reordering — or a
        // chunk's results landing under the wrong offset — is visible on every
        // one of the 2,007 results, not just at the seams.
        var recorder = new Recorder();
        await using var client = NewClient(recorder.Mock().Object);

        var results = await client.CheckPermissionsAsync(
            Consistency.Full(), "view", default, NumberedRels(Total));

        results.Select(r => r.MissingContext[0])
            .Should().Equal(Enumerable.Range(0, Total).Select(i => i.ToString("D5")));
    }

    [Theory]
    [InlineData(1)]
    [InlineData(999)]
    [InlineData(CheckBatchSize)]
    public async Task CheckPermissionsAsync_UnderChunkSize_SendsExactlyOneRequest(int n)
    {
        // The common case must not regress into a loop with per-chunk overhead.
        var recorder = new Recorder();
        await using var client = NewClient(recorder.Mock().Object);

        var results = await client.CheckPermissionsAsync(
            Consistency.Full(), "view", default, NumberedRels(n));

        results.Should().HaveCount(n);
        recorder.Sizes.Should().Equal(n);
    }

    [Fact]
    public async Task CheckPermissionsAsync_EmptyInput_SendsNoRequest()
    {
        // Zero relationships costs zero round trips — not one request carrying
        // an empty item list — and returns an empty array rather than throwing.
        var recorder = new Recorder();
        await using var client = NewClient(recorder.Mock().Object);

        var results = await client.CheckPermissionsAsync(Consistency.Full(), "view");

        results.Should().BeEmpty();
        recorder.Sizes.Should().BeEmpty();
    }

    [Fact]
    public async Task CheckPermissionsAsync_LengthGuard_FiresOnALaterChunk()
    {
        // The pair-count guard is evaluated per chunk, not once against the
        // caller's total: the first chunk answers in full, the second returns
        // 999 pairs for 1,000 items. Without a per-chunk guard the shortfall
        // would silently desync every result from the second chunk onward.
        var recorder = new Recorder { ShortAtRequest = 1 };
        await using var client = NewClient(recorder.Mock().Object);

        var act = async () => await client.CheckPermissionsAsync(
            Consistency.Full(), "view", default, NumberedRels(Total));

        (await act.Should().ThrowAsync<SpiceDBException>())
            .WithMessage("*999 pair(s) for 1000 request item(s)*");
        recorder.Sizes.Should().Equal(
            CheckBatchSize,
            CheckBatchSize);
    }

    [Fact]
    public async Task CheckPermissionsAsync_MalformedPair_ReportsTheCallersAbsoluteIndex()
    {
        // Chunking made every "check item N" message chunk-relative: a failure
        // at relationship 1003 read as "check item 3", so a caller who logs or
        // parses it acts on relationship 3 — one resource's answer attributed
        // to another, the same failure family the pair-count guard exists to
        // prevent, relocated into the diagnostic.
        const int failing = CheckBatchSize + 3;
        var recorder = new Recorder { MalformedAtAbsolute = failing };
        await using var client = NewClient(recorder.Mock().Object);

        var act = async () => await client.CheckPermissionsAsync(
            Consistency.Full(), "view", default, NumberedRels(CheckBatchSize * 2));

        var thrown = await act.Should().ThrowAsync<SpiceDBException>();
        thrown.Which.Message.Should().Contain($"check item {failing}:");
        thrown.Which.Message.Should().NotContain("check item 3:");
    }
}
