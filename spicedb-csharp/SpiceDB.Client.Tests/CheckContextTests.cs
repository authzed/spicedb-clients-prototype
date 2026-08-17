// Tests for caveat context on the check surface (Task 19 / spec D3b).
//
// The wire only attaches caveat context per-item — CheckBulkPermissionsRequest
// has no request-level context field, only CheckBulkPermissionsRequestItem.context
// (proto field 4). A call-level default therefore has to be fanned out onto
// every item at request-build time, merged key-by-key with any per-item
// context the item itself carries (Relationship.CheckContext), with the
// item's own keys winning on conflict.
//
// These tests assert on the CheckBulkPermissionsRequest actually built and
// sent — capturing it via the mocked PermissionsServiceClient — rather than
// on CheckResult, since the contract being tested is entirely about request
// construction. C5 (the live-server payoff test proving a caller can resolve
// a conditional into a grant) lives in
// examples/CheckPermission/CheckPermissionTest.cs.

using Authzed.Api.V1;
using FluentAssertions;
using Google.Protobuf.WellKnownTypes;
using Grpc.Core;
using Moq;
using SpiceDB.Client;
using Xunit;
using static SpiceDB.Client.Tests.StreamingTestHelpers;

namespace SpiceDB.Client.Tests;

public class CheckContextTests
{
    private static SpiceDBClient NewClient(
        out Mock<PermissionsService.PermissionsServiceClient> mockPermissions,
        out List<CheckBulkPermissionsRequest> capturedRequests)
    {
        var captured = new List<CheckBulkPermissionsRequest>();
        var mock = new Mock<PermissionsService.PermissionsServiceClient>();
        mock.Setup(c => c.CheckBulkPermissionsAsync(
                It.IsAny<CheckBulkPermissionsRequest>(),
                It.IsAny<Metadata>(),
                It.IsAny<DateTime?>(),
                It.IsAny<CancellationToken>()))
            .Callback<CheckBulkPermissionsRequest, Metadata, DateTime?, CancellationToken>(
                (req, _, _, _) => captured.Add(req))
            .Returns(() => MakeUnaryCall(new CheckBulkPermissionsResponse
            {
                CheckedAt = new ZedToken { Token = "tok" },
                Pairs =
                {
                    Enumerable.Range(0, 8).Select(_ => new CheckBulkPermissionsPair
                    {
                        Item = new CheckBulkPermissionsResponseItem
                        {
                            Permissionship = CheckPermissionResponse.Types.Permissionship.HasPermission,
                        },
                    }),
                },
            }));

        mockPermissions = mock;
        capturedRequests = captured;

        return new SpiceDBClient(
            mock.Object,
            new Mock<SchemaService.SchemaServiceClient>().Object,
            new Mock<WatchService.WatchServiceClient>().Object,
            new Mock<ExperimentalService.ExperimentalServiceClient>().Object);
    }

    private static Struct AsStruct(params (string Key, object Value)[] fields)
    {
        var s = new Struct();
        foreach (var (key, value) in fields)
        {
            s.Fields[key] = value switch
            {
                double d => Value.ForNumber(d),
                int i => Value.ForNumber(i),
                string str => Value.ForString(str),
                bool b => Value.ForBool(b),
                _ => throw new ArgumentException("unsupported test fixture value type"),
            };
        }
        return s;
    }

    // ── C1: call-level context alone reaches every item in a bulk request ──

    [Fact]
    public async Task C1_CallLevelContextAlone_ReachesEveryItem()
    {
        var client = NewClient(out _, out var captured);

        var rel1 = Relationship.FromTriple("document", "doc1", "viewer", "user", "alice");
        var rel2 = Relationship.FromTriple("document", "doc2", "viewer", "user", "bob");

        var callLevel = new Dictionary<string, object> { ["now"] = 42, ["region"] = "us" };

        await client.CheckPermissionsWithContextAsync(
            Consistency.Full(), "view", callLevel, default, rel1, rel2);

        captured.Should().ContainSingle();
        var items = captured[0].Items;
        items.Should().HaveCount(2);

        var want = AsStruct(("now", 42), ("region", "us"));
        items[0].Context.Should().Be(want, "item 0 should carry the call-level context");
        items[1].Context.Should().Be(want, "item 1 should carry the call-level context");
    }

    // ── C2: per-item context alone reaches only that item ──────────────────

    [Fact]
    public async Task C2_PerItemContextAlone_ReachesOnlyThatItem()
    {
        // Uses the plain, unchanged CheckPermissionsAsync (no call-level
        // context argument at all) to prove per-item context works without
        // needing the WithContext-suffixed method.
        var client = NewClient(out _, out var captured);

        var rel1 = Relationship.FromTriple("document", "doc1", "viewer", "user", "alice");
        var rel2 = Relationship
            .FromTriple("document", "doc2", "viewer", "user", "bob")
            .WithCheckContext(new Dictionary<string, object> { ["now"] = 42 });

        await client.CheckPermissionsAsync(Consistency.Full(), "view", default, rel1, rel2);

        captured.Should().ContainSingle();
        var items = captured[0].Items;
        items.Should().HaveCount(2);

        items[0].Context.Should().BeNull(
            "item 0 has no per-item context and no call-level default, so no context field should be set");
        items[1].Context.Should().Be(
            AsStruct(("now", 42)), "item 1 should carry only its own per-item context");
    }

    // ── C3: the merge rule — item wins per-key, call-level keys the item ──
    // ── doesn't mention are retained ────────────────────────────────────

    [Fact]
    public async Task C3_MergeRule_ItemWinsPerKey_CallLevelRetainedForAbsentKeys()
    {
        var client = NewClient(out _, out var captured);

        var callLevel = new Dictionary<string, object> { ["now"] = 42, ["region"] = "us" };

        var item0 = Relationship
            .FromTriple("document", "doc1", "viewer", "user", "alice")
            .WithCheckContext(new Dictionary<string, object> { ["region"] = "eu" });
        var item1 = Relationship.FromTriple("document", "doc2", "viewer", "user", "bob");

        await client.CheckPermissionsWithContextAsync(
            Consistency.Full(), "view", callLevel, default, item0, item1);

        captured.Should().ContainSingle();
        var items = captured[0].Items;
        items.Should().HaveCount(2);

        // Asserting BOTH items — asserting only the overriding item0 would
        // also pass under wholesale-replacement semantics (call-level
        // discarded entirely whenever an item supplies any context), so a
        // single-item assertion would not pin the per-key merge rule.
        items[0].Context.Should().Be(
            AsStruct(("now", 42), ("region", "eu")),
            "item 0 overrides 'region' but must retain the call-level 'now'");
        items[1].Context.Should().Be(
            AsStruct(("now", 42), ("region", "us")),
            "item 1 supplied no per-item context, so it must retain the call-level default unchanged");
    }

    // ── C4: neither call-level nor per-item context supplied => no context ──
    // ── field set on the wire (null, not an empty Struct) ───────────────────

    [Fact]
    public async Task C4_NeitherSupplied_NoContextFieldSetOnWire()
    {
        var client = NewClient(out _, out var captured);

        var rel1 = Relationship.FromTriple("document", "doc1", "viewer", "user", "alice");
        var rel2 = Relationship.FromTriple("document", "doc2", "viewer", "user", "bob");

        // Explicit null call-level context via the WithContext overload —
        // equivalent to calling the plain CheckPermissionsAsync.
        await client.CheckPermissionsWithContextAsync(
            Consistency.Full(), "view", null, default, rel1, rel2);

        captured.Should().ContainSingle();
        var items = captured[0].Items;
        items.Should().HaveCount(2);

        items[0].Context.Should().BeNull();
        items[1].Context.Should().BeNull();
    }

    [Fact]
    public async Task C4_PlainCheckPermissionsAsync_NoContextFieldSetOnWire()
    {
        var client = NewClient(out _, out var captured);

        var rel = Relationship.FromTriple("document", "doc1", "viewer", "user", "alice");
        await client.CheckPermissionsAsync(Consistency.Full(), "view", default, rel);

        captured.Should().ContainSingle();
        captured[0].Items[0].Context.Should().BeNull();
    }

    // ── CheckPermissionAsync (singular) gets the same trailing optional
    // ── context parameter, widened in place ─────────────────────────────

    [Fact]
    public async Task CheckPermissionAsync_TrailingContextParameter_ReachesTheItem()
    {
        var client = NewClient(out _, out var captured);

        var rel = Relationship.FromTriple("document", "doc1", "viewer", "user", "alice");
        var context = new Dictionary<string, object> { ["now"] = 42 };

        await client.CheckPermissionAsync(Consistency.Full(), "view", rel, default, context);

        captured.Should().ContainSingle();
        captured[0].Items[0].Context.Should().Be(AsStruct(("now", 42)));
    }

    // ── CheckAnyWithContextAsync/CheckAllWithContextAsync are aggregates
    // ── over the same request-building path and must fan out context too ──

    [Fact]
    public async Task CheckAnyWithContextAsync_FansOutCallLevelContext()
    {
        var client = NewClient(out _, out var captured);

        var rel1 = Relationship.FromTriple("document", "doc1", "viewer", "user", "alice");
        var rel2 = Relationship.FromTriple("document", "doc2", "viewer", "user", "bob");
        var context = new Dictionary<string, object> { ["now"] = 42 };

        await client.CheckAnyWithContextAsync(Consistency.Full(), "view", context, default, rel1, rel2);

        captured.Should().ContainSingle();
        var items = captured[0].Items;
        items[0].Context.Should().Be(AsStruct(("now", 42)));
        items[1].Context.Should().Be(AsStruct(("now", 42)));
    }

    [Fact]
    public async Task CheckAllWithContextAsync_FansOutCallLevelContext()
    {
        var client = NewClient(out _, out var captured);

        var rel1 = Relationship.FromTriple("document", "doc1", "viewer", "user", "alice");
        var rel2 = Relationship.FromTriple("document", "doc2", "viewer", "user", "bob");
        var context = new Dictionary<string, object> { ["now"] = 42 };

        await client.CheckAllWithContextAsync(Consistency.Full(), "view", context, default, rel1, rel2);

        captured.Should().ContainSingle();
        var items = captured[0].Items;
        items[0].Context.Should().Be(AsStruct(("now", 42)));
        items[1].Context.Should().Be(AsStruct(("now", 42)));
    }
}
