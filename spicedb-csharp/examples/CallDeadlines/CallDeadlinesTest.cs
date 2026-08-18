using Xunit;
// Example CallDeadlines demonstrates the client-level defaultTimeout
// construction parameter, a per-call timeout override, and that bulk import
// (ImportRelationshipsAsync) is a client-streaming call that is NOT bounded
// by the unary default -- see root DESIGN.md, "RULE: A unary call must have
// a deadline".

using SpiceDB.Client;
using static SpiceDB.Client.Consistency;

namespace CallDeadlines;

public class CallDeadlinesTest
{
    private const string Schema = """
        definition user {}

        definition document {
            relation viewer: user
            permission view = viewer
        }
        """;

    [Fact]
    public async Task DefaultTimeout_ConstructionParam_AppliesEndToEnd()
    {
        // defaultTimeout is the documented, real construction path -- not a
        // mock -- so a signature drift here (e.g. the parameter silently
        // disappearing) would fail this example, not just a unit test
        // against a stalling stub.
        await using var client = SpiceDBClient.CreatePlaintext(
            "localhost:50051", "somerandomkeyhere", defaultTimeout: TimeSpan.FromSeconds(5));

        await client.WriteSchemaAsync(Schema);

        var txn = new Transaction();
        txn.Touch(Relationship.FromTriple("document", "readme", "viewer", "user", "alice"));
        await client.WriteAsync(txn);

        // Bound by the 5s default set above.
        var rel = Relationship.FromTriple("document", "readme", "view", "user", "alice");
        var result = await client.CheckPermissionAsync(Full(), "view", rel);
        Assert.True(result.HasPermission, "expected alice to have view permission");
    }

    [Fact]
    public async Task PerCallTimeout_OverridesTheClientDefault()
    {
        await using var client = SpiceDBClient.CreatePlaintext("localhost:50051", "somerandomkeyhere");

        await client.WriteSchemaAsync(Schema);

        var txn = new Transaction();
        txn.Touch(Relationship.FromTriple("document", "readme", "viewer", "user", "alice"));
        await client.WriteAsync(txn);

        // 2 seconds is generous for a real call against a local SpiceDB --
        // this exercises the real timeout parameter end-to-end, not testing
        // how small a timeout can be.
        var rel = Relationship.FromTriple("document", "readme", "view", "user", "alice");
        var result = await client.CheckPermissionAsync(
            Full(), "view", rel, timeout: TimeSpan.FromSeconds(2));
        Assert.True(result.HasPermission, "expected alice to have view permission");
    }

    [Fact]
    public async Task BulkImport_IsNotBoundedByTheUnaryDefault()
    {
        // ImportRelationshipsAsync (ImportBulkRelationships) is client-streaming: its duration
        // scales with the size of the caller's dataset, not with server latency, so it is
        // explicitly excluded from the unary default. Calling it with no timeout bound at all --
        // as below -- must still succeed; if a future change accidentally routed the unary
        // default into this call, a large enough import would start failing with
        // DeadlineExceededException well before it finished.
        await using var client = SpiceDBClient.CreatePlaintext("localhost:50051", "somerandomkeyhere");

        await client.WriteSchemaAsync(Schema);

        var relationships = Enumerable.Range(0, 50)
            .Select(i => Relationship.FromTriple("document", $"bulk-{i}", "viewer", "user", "alice"))
            .ToArray();
        var numLoaded = await client.ImportRelationshipsAsync(ToAsyncEnumerable(relationships));
        Assert.Equal(50u, numLoaded);

        // A caller-supplied timeout on the same client-streaming call must still be honored --
        // the exclusion is from the *default*, not from the ability to bound the call at all.
        var moreRelationships = Enumerable.Range(0, 50)
            .Select(i => Relationship.FromTriple("document", $"bulk2-{i}", "viewer", "user", "alice"))
            .ToArray();
        var numLoadedBounded = await client.ImportRelationshipsAsync(
            ToAsyncEnumerable(moreRelationships), timeout: TimeSpan.FromSeconds(30));
        Assert.Equal(50u, numLoadedBounded);
    }

    private static async IAsyncEnumerable<Relationship> ToAsyncEnumerable(
        IEnumerable<Relationship> relationships)
    {
        foreach (var rel in relationships)
        {
            yield return rel;
        }

        await Task.CompletedTask;
    }
}
