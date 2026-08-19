// Tests for the native CheckResult type (and Permissionship's fourth value,
// NoPermission) and the internal proto -> native mappers
// (SpiceDBClient.ToCheckPermissionship/ToCheckResult) that back
// CheckPermissionAsync/CheckPermissionsAsync. These replace bare bool
// returns — critically, ensuring CONDITIONAL_PERMISSION is never treated as
// a grant (root DESIGN.md, "RULE: Only an unconditional grant is true") and
// that a per-item CheckBulkPermissions error surfaces as its specific typed
// SpiceDBException rather than the base type.

using Authzed.Api.V1;
using FluentAssertions;
using Grpc.Core;
using Moq;
using SpiceDB.Client;
using Xunit;
using static SpiceDB.Client.Tests.StreamingTestHelpers;

namespace SpiceDB.Client.Tests;

public class CheckResultTests
{
    // ── T1: HasPermission is a single equality comparison, true only for
    //        HasPermission, parametrized over all four enum values ─────────

    [Theory]
    [InlineData(Permissionship.Unspecified, false)]
    [InlineData(Permissionship.NoPermission, false)]
    [InlineData(Permissionship.HasPermission, true)]
    [InlineData(Permissionship.ConditionalPermission, false)]
    public void HasPermission_TrueOnlyForHasPermission(Permissionship permissionship, bool expected)
    {
        var result = new CheckResult { Permissionship = permissionship };
        result.HasPermission.Should().Be(expected);
    }

    // ── ToCheckPermissionship: proto CheckPermissionResponse.Types.Permissionship
    //    has a NoPermission value that LookupPermissionship does not ───────

    [Theory]
    [InlineData(CheckPermissionResponse.Types.Permissionship.Unspecified, Permissionship.Unspecified)]
    [InlineData(CheckPermissionResponse.Types.Permissionship.NoPermission, Permissionship.NoPermission)]
    [InlineData(CheckPermissionResponse.Types.Permissionship.HasPermission, Permissionship.HasPermission)]
    [InlineData(CheckPermissionResponse.Types.Permissionship.ConditionalPermission, Permissionship.ConditionalPermission)]
    public void ToCheckPermissionship_MapsKnownValues(
        CheckPermissionResponse.Types.Permissionship proto, Permissionship expected)
    {
        SpiceDBClient.ToCheckPermissionship(proto).Should().Be(expected);
    }

    [Fact]
    public void ToCheckPermissionship_MapsUnrecognizedValueToUnspecified()
    {
        SpiceDBClient.ToCheckPermissionship((CheckPermissionResponse.Types.Permissionship)99)
            .Should().Be(Permissionship.Unspecified);
    }

    // ── T2: MissingContext carries the server's missing_required_context
    //        contents, asserted by value ─────────────────────────────────

    [Fact]
    public void ToCheckResult_MapsMissingContextByValue()
    {
        var item = new CheckBulkPermissionsResponseItem
        {
            Permissionship = CheckPermissionResponse.Types.Permissionship.ConditionalPermission,
            PartialCaveatInfo = new Authzed.Api.V1.PartialCaveatInfo
            {
                MissingRequiredContext = { "region", "now" },
            },
        };

        var result = SpiceDBClient.ToCheckResult(item, "tok1");

        result.MissingContext.Should().Equal("region", "now");
    }

    [Fact]
    public void ToCheckResult_NoCaveatInfo_MissingContextIsEmpty()
    {
        var item = new CheckBulkPermissionsResponseItem
        {
            Permissionship = CheckPermissionResponse.Types.Permissionship.HasPermission,
        };

        var result = SpiceDBClient.ToCheckResult(item, "tok1");

        result.MissingContext.Should().BeEmpty();
    }

    // ── T3: CheckedAt is populated from the response ────────────────────────

    [Fact]
    public void ToCheckResult_MapsCheckedAt()
    {
        var item = new CheckBulkPermissionsResponseItem
        {
            Permissionship = CheckPermissionResponse.Types.Permissionship.HasPermission,
        };

        var result = SpiceDBClient.ToCheckResult(item, "zed-token-123");

        result.CheckedAt.Should().Be("zed-token-123");
    }

    // ── End-to-end: CheckPermissionsAsync/CheckPermissionAsync surface
    //    Permissionship/MissingContext/CheckedAt through the public API ────

    private static SpiceDBClient NewClient(PermissionsService.PermissionsServiceClient permissions) =>
        new(
            permissions,
            new Mock<SchemaService.SchemaServiceClient>().Object,
            new Mock<WatchService.WatchServiceClient>().Object,
            new Mock<ExperimentalService.ExperimentalServiceClient>().Object);

    private static Mock<PermissionsService.PermissionsServiceClient> MockCheckBulk(
        CheckBulkPermissionsResponse response)
    {
        var mock = new Mock<PermissionsService.PermissionsServiceClient>();
        mock.Setup(c => c.CheckBulkPermissionsAsync(
                It.IsAny<CheckBulkPermissionsRequest>(),
                It.IsAny<Metadata>(),
                It.IsAny<DateTime?>(),
                It.IsAny<CancellationToken>()))
            .Returns(MakeUnaryCall(response));
        return mock;
    }

    [Fact]
    public async Task CheckPermissionAsync_SurfacesConditionalPermissionAndMissingContext()
    {
        var resp = new CheckBulkPermissionsResponse
        {
            CheckedAt = new ZedToken { Token = "tok-abc" },
            Pairs =
            {
                new CheckBulkPermissionsPair
                {
                    Item = new CheckBulkPermissionsResponseItem
                    {
                        Permissionship = CheckPermissionResponse.Types.Permissionship.ConditionalPermission,
                        PartialCaveatInfo = new Authzed.Api.V1.PartialCaveatInfo
                        {
                            MissingRequiredContext = { "now" },
                        },
                    },
                },
            },
        };

        var mockPermissions = MockCheckBulk(resp);
        await using var client = NewClient(mockPermissions.Object);

        var rel = Relationship.FromTriple("document", "doc1", "viewer", "user", "alice");
        var result = await client.CheckPermissionAsync(Consistency.Full(), "view", rel);

        result.Permissionship.Should().Be(Permissionship.ConditionalPermission);
        result.HasPermission.Should().BeFalse();
        result.MissingContext.Should().Equal("now");
        result.CheckedAt.Should().Be("tok-abc");
    }

    [Fact]
    public async Task CheckPermissionsAsync_PropagatesResponseLevelCheckedAtToEveryPair()
    {
        // checked_at is response-level, not per-item — the bulk path must
        // propagate that one token to every result.
        var resp = new CheckBulkPermissionsResponse
        {
            CheckedAt = new ZedToken { Token = "shared-token" },
            Pairs =
            {
                new CheckBulkPermissionsPair
                {
                    Item = new CheckBulkPermissionsResponseItem { Permissionship = CheckPermissionResponse.Types.Permissionship.HasPermission },
                },
                new CheckBulkPermissionsPair
                {
                    Item = new CheckBulkPermissionsResponseItem { Permissionship = CheckPermissionResponse.Types.Permissionship.NoPermission },
                },
            },
        };

        var mockPermissions = MockCheckBulk(resp);
        await using var client = NewClient(mockPermissions.Object);

        var rels = new[]
        {
            Relationship.FromTriple("document", "doc1", "viewer", "user", "alice"),
            Relationship.FromTriple("document", "doc2", "viewer", "user", "bob"),
        };

        var results = await client.CheckPermissionsAsync(Consistency.Full(), "view", default, rels);

        results.Should().HaveCount(2);
        results[0].CheckedAt.Should().Be("shared-token");
        results[1].CheckedAt.Should().Be("shared-token");
    }

    // ── Aggregate clause: CheckAny/CheckAll count only HasPermission ────────

    [Fact]
    public async Task CheckAnyAsync_ConditionalDoesNotCountAsGranted()
    {
        var resp = new CheckBulkPermissionsResponse
        {
            CheckedAt = new ZedToken { Token = "tok" },
            Pairs =
            {
                new CheckBulkPermissionsPair
                {
                    Item = new CheckBulkPermissionsResponseItem { Permissionship = CheckPermissionResponse.Types.Permissionship.ConditionalPermission },
                },
                new CheckBulkPermissionsPair
                {
                    Item = new CheckBulkPermissionsResponseItem { Permissionship = CheckPermissionResponse.Types.Permissionship.NoPermission },
                },
            },
        };

        var mockPermissions = MockCheckBulk(resp);
        await using var client = NewClient(mockPermissions.Object);

        var rels = new[]
        {
            Relationship.FromTriple("document", "doc1", "viewer", "user", "alice"),
            Relationship.FromTriple("document", "doc2", "viewer", "user", "bob"),
        };

        var any = await client.CheckAnyAsync(Consistency.Full(), "view", default, rels);
        any.Should().BeFalse();
    }

    [Fact]
    public async Task CheckAllAsync_ConditionalPreventsAllFromBeingTrue()
    {
        var resp = new CheckBulkPermissionsResponse
        {
            CheckedAt = new ZedToken { Token = "tok" },
            Pairs =
            {
                new CheckBulkPermissionsPair
                {
                    Item = new CheckBulkPermissionsResponseItem { Permissionship = CheckPermissionResponse.Types.Permissionship.HasPermission },
                },
                new CheckBulkPermissionsPair
                {
                    Item = new CheckBulkPermissionsResponseItem { Permissionship = CheckPermissionResponse.Types.Permissionship.ConditionalPermission },
                },
            },
        };

        var mockPermissions = MockCheckBulk(resp);
        await using var client = NewClient(mockPermissions.Object);

        var rels = new[]
        {
            Relationship.FromTriple("document", "doc1", "viewer", "user", "alice"),
            Relationship.FromTriple("document", "doc2", "viewer", "user", "bob"),
        };

        var all = await client.CheckAllAsync(Consistency.Full(), "view", default, rels);
        all.Should().BeFalse();
    }

    // ── CheckAll must not be vacuously true on zero relationships: LINQ's
    //    Enumerable.All is vacuously true over an empty sequence, so a caller
    //    gating on CheckAllAsync(cs, "edit", ct, docs.Select(ToRel).ToArray())
    //    would have been silently granted whenever the derived relationships
    //    array came up empty (a filter that matched nothing, an upstream
    //    returning []). Root DESIGN.md: "An aggregate over zero checks is not
    //    a grant." No mock setup on CheckBulkPermissionsAsync is configured
    //    below — CheckAllAsync must never reach the server for zero
    //    relationships (the pre-existing `relationships.Length == 0` early
    //    return in CheckPermissionsCoreAsync already guarantees that; this
    //    guards the boolean CheckAllAsync/CheckAllWithContextAsync return). ──

    [Fact]
    public async Task CheckAllAsync_ZeroRelationships_ReturnsFalse()
    {
        var mockPermissions = new Mock<PermissionsService.PermissionsServiceClient>();
        await using var client = NewClient(mockPermissions.Object);

        var all = await client.CheckAllAsync(Consistency.Full(), "view");
        all.Should().BeFalse();
    }

    [Fact]
    public async Task CheckAllWithContextAsync_ZeroRelationships_ReturnsFalse()
    {
        var mockPermissions = new Mock<PermissionsService.PermissionsServiceClient>();
        await using var client = NewClient(mockPermissions.Object);

        var all = await client.CheckAllWithContextAsync(Consistency.Full(), "view", null);
        all.Should().BeFalse();
    }

    // ── HARD REQUIREMENT: per-item CheckBulkPermissions error surfaces as
    //    its specific typed exception, not the base SpiceDBException ───────

    [Fact]
    public async Task CheckPermissionsAsync_PerItemPermissionDenied_ThrowsPermissionDeniedException()
    {
        var resp = new CheckBulkPermissionsResponse
        {
            Pairs =
            {
                new CheckBulkPermissionsPair
                {
                    Error = new Google.Rpc.Status { Code = (int)StatusCode.PermissionDenied, Message = "nope" },
                },
            },
        };

        var mockPermissions = MockCheckBulk(resp);
        await using var client = NewClient(mockPermissions.Object);

        var rel = Relationship.FromTriple("document", "doc1", "viewer", "user", "alice");
        var act = async () => await client.CheckPermissionAsync(Consistency.Full(), "view", rel);

        // Must be the SPECIFIC typed exception — not merely assignable to
        // SpiceDBException, which the pre-fix base-type throw would also
        // satisfy. A caller string-matching PermissionDeniedException
        // couldn't distinguish it from any other per-item failure before
        // this fix.
        var result = await act.Should().ThrowExactlyAsync<PermissionDeniedException>();
        result.Which.Message.Should().Be("nope");
    }

    [Fact]
    public async Task CheckPermissionsAsync_PerItemNotFound_ThrowsNotFoundException()
    {
        // Proves the mapping isn't hardcoded to one code — different
        // google.rpc.Status codes route to different typed exceptions via
        // the same ErrorMapper switch used by unary calls.
        var resp = new CheckBulkPermissionsResponse
        {
            Pairs =
            {
                new CheckBulkPermissionsPair
                {
                    Error = new Google.Rpc.Status { Code = (int)StatusCode.NotFound, Message = "missing" },
                },
            },
        };

        var mockPermissions = MockCheckBulk(resp);
        await using var client = NewClient(mockPermissions.Object);

        var rel = Relationship.FromTriple("document", "doc1", "viewer", "user", "alice");
        var act = async () => await client.CheckPermissionAsync(Consistency.Full(), "view", rel);

        await act.Should().ThrowExactlyAsync<NotFoundException>();
    }

    // A per-item failure must reach the caller carrying the same structured
    // reason an RPC-level failure does. The per-item google.rpc.Status used to
    // be reduced to a code and a message before mapping, silently dropping the
    // item's own ErrorInfo — a failure mode that shows up as an empty Reason
    // and nothing red. See root DESIGN.md, "RULE: Error mapping must not lose
    // the server's detail".
    [Fact]
    public async Task CheckPermissionsAsync_PerItemError_CarriesItsOwnErrorReason()
    {
        var perItemStatus = new Google.Rpc.Status
        {
            Code = (int)StatusCode.ResourceExhausted,
            Message = "max depth exceeded",
        };
        perItemStatus.Details.Add(Google.Protobuf.WellKnownTypes.Any.Pack(new Google.Rpc.ErrorInfo
        {
            Reason = "ERROR_REASON_MAXIMUM_DEPTH_EXCEEDED",
            Domain = "authzed.com",
            Metadata = { { "maximum_depth_allowed", "50" } },
        }));

        var resp = new CheckBulkPermissionsResponse
        {
            Pairs = { new CheckBulkPermissionsPair { Error = perItemStatus } },
        };

        var mockPermissions = MockCheckBulk(resp);
        await using var client = NewClient(mockPermissions.Object);

        var rel = Relationship.FromTriple("document", "doc1", "viewer", "user", "alice");
        var act = async () => await client.CheckPermissionAsync(Consistency.Full(), "view", rel);

        var result = await act.Should().ThrowExactlyAsync<ResourceExhaustedException>();
        result.Which.Reason.Should().Be("ERROR_REASON_MAXIMUM_DEPTH_EXCEEDED");
        result.Which.ReasonDomain.Should().Be("authzed.com");
        result.Which.ReasonMetadata.Should().Contain("maximum_depth_allowed", "50");
    }

    // ── HARD REQUIREMENT: a response with fewer (or more) pairs than request
    //    items, or a pair whose Response oneof is unset (neither Item nor
    //    Error), must fail loudly with a typed error rather than silently
    //    return a misaligned CheckResult[]. The proto guarantees pairs are
    //    returned in request order but says nothing about count, so a short
    //    response would otherwise silently desync results[i] from
    //    relationships[i] for every item after the gap — one resource's
    //    answer attributed to another. ─────────────────────────────────────

    [Fact]
    public async Task CheckPermissionsAsync_FewerPairsThanRequestItems_ThrowsSpiceDBException()
    {
        // Two relationships requested, only one pair returned.
        var resp = new CheckBulkPermissionsResponse
        {
            Pairs =
            {
                new CheckBulkPermissionsPair
                {
                    Item = new CheckBulkPermissionsResponseItem { Permissionship = CheckPermissionResponse.Types.Permissionship.HasPermission },
                },
            },
        };

        var mockPermissions = MockCheckBulk(resp);
        await using var client = NewClient(mockPermissions.Object);

        var rel1 = Relationship.FromTriple("document", "doc1", "viewer", "user", "alice");
        var rel2 = Relationship.FromTriple("document", "doc2", "viewer", "user", "bob");
        var act = async () => await client.CheckPermissionsAsync(Consistency.Full(), "view", default, rel1, rel2);

        var result = await act.Should().ThrowExactlyAsync<SpiceDBException>();
        result.Which.Message.Should().Contain("1").And.Contain("2");
    }

    [Fact]
    public async Task CheckPermissionsAsync_MalformedPair_ThrowsInsteadOfShrinkingResults()
    {
        // Neither Item nor Error set on the pair's Response oneof — the proto
        // schema guarantees a well-behaved server never sends this, but
        // nothing on the wire prevents it.
        var resp = new CheckBulkPermissionsResponse
        {
            Pairs = { new CheckBulkPermissionsPair() },
        };

        var mockPermissions = MockCheckBulk(resp);
        await using var client = NewClient(mockPermissions.Object);

        var rel = Relationship.FromTriple("document", "doc1", "viewer", "user", "alice");
        var act = async () => await client.CheckPermissionAsync(Consistency.Full(), "view", rel);

        var result = await act.Should().ThrowExactlyAsync<SpiceDBException>();
        result.Which.Message.Should().Contain("check item 0");
    }

    // ── LookupResource/LookupSubject gain LookedUpAt ────────────────────────

    [Fact]
    public void LookupResource_DefaultsLookedUpAtToEmpty()
    {
        new LookupResource().LookedUpAt.Should().BeEmpty();
    }

    [Fact]
    public async Task LookupResourcesAsync_SurfacesLookedUpAt()
    {
        var resp = new LookupResourcesResponse
        {
            ResourceObjectId = "doc1",
            Permissionship = LookupPermissionship.HasPermission,
            LookedUpAt = new ZedToken { Token = "lookup-tok" },
        };

        var mockPermissions = new Mock<PermissionsService.PermissionsServiceClient>();
        mockPermissions
            .Setup(c => c.LookupResources(
                It.IsAny<LookupResourcesRequest>(),
                It.IsAny<Metadata>(),
                It.IsAny<DateTime?>(),
                It.IsAny<CancellationToken>()))
            .Returns(MakeServerStreamingCall(new FakeStreamReader<LookupResourcesResponse>([resp])));

        await using var client = NewClient(mockPermissions.Object);

        var results = new List<LookupResource>();
        await foreach (var result in client.LookupResourcesAsync(
            Consistency.Full(), "document", "view", "user", "alice"))
        {
            results.Add(result);
        }

        results.Should().ContainSingle();
        results[0].LookedUpAt.Should().Be("lookup-tok");
    }
}
