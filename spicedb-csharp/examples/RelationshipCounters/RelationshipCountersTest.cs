using Xunit;
// Example RelationshipCounters demonstrates registering, reading, and
// unregistering relationship counters.
//
// This is the only test in this assembly, so what it asserts is the entirety of
// the counter feature's example coverage. It used to sleep two seconds and then
// wrap every assertion in `if (!stillCalculating)`, which asserts nothing at all
// on a slow run -- and nothing on ANY run if the stillCalculating mapping is
// inverted, which is the likeliest bug on that exact field. A 100%-broken
// counter feature shipped green. It now polls to a terminal state, with a
// timeout that fails rather than skips, and asserts an exact count.

using SpiceDB.Client;

namespace RelationshipCounters;

public class RelationshipCountersTest
{
    private const string Schema = """
        definition user {}

        definition document {
            relation viewer: user
            relation editor: user
            permission view = viewer + editor
            permission edit = editor
        }
        """;

    /// <summary>
    /// Bounds how long the counter may stay "still calculating". Expiry is a
    /// failure, deliberately, and not a way out of asserting.
    /// </summary>
    private static readonly TimeSpan CounterTimeout = TimeSpan.FromSeconds(30);

    private static readonly TimeSpan CounterPollInterval = TimeSpan.FromMilliseconds(100);

    [Fact]
    public async Task RelationshipCounters_RegisterCountAndUnregister()
    {
        await using var client = SpiceDBClient.CreatePlaintext(SpiceDBTestServer.Endpoint, SpiceDBTestServer.Token);

        // All thirteen example projects share one SpiceDB. Clearing first makes
        // the count below this example's own writes rather than whatever ran
        // before it -- a "non-zero" assertion passes on leftovers even when this
        // example's writes never landed.
        await SpiceDBTestServer.ClearDocumentRelationshipsAsync(client);
        await client.WriteSchemaAsync(Schema);

        var txn = new Transaction();
        txn.Touch(Relationship.FromTriple("document", "firstdoc", "viewer", "user", "alice"));
        txn.Touch(Relationship.FromTriple("document", "seconddoc", "viewer", "user", "bob"));
        // An `editor` the counter's filter must NOT count. Without a
        // relationship the filter has to exclude, a counter that ignored the
        // relation filter entirely would still report the expected number.
        txn.Touch(Relationship.FromTriple("document", "firstdoc", "editor", "user", "carol"));
        await client.WriteAsync(txn);

        // Unregister any existing counter from a prior run (ignore errors)
        var counterName = "csharp_document_viewers";
        var filter = new Filter("document").WithRelation("viewer");

        try { await client.ExperimentalUnregisterRelationshipCounterAsync(counterName); }
        catch (FailedPreconditionException) { /* not registered yet */ }

        // Register a counter
        await client.ExperimentalRegisterRelationshipCounterAsync(counterName, filter);

        var result = await SettledCounterAsync(client, counterName);

        // Exactly the two viewer relationships written above, and not the
        // editor. A count of zero -- registration silently no-op'ing, or the
        // value never being read off the response -- fails here, and so does a
        // count of three, which is what ignoring the relation filter would
        // produce.
        Assert.Equal(2u, result.RelationshipCount);
        Assert.NotEmpty(result.Revision);

        // Unregister the counter
        await client.ExperimentalUnregisterRelationshipCounterAsync(counterName);

        // Unregistering has to actually remove it: reading a counter that is
        // not registered is an error, so a no-op unregister would leave this
        // call succeeding.
        await Assert.ThrowsAsync<FailedPreconditionException>(
            () => client.ExperimentalCountRelationshipsAsync(counterName));

        await SpiceDBTestServer.ClearDocumentRelationshipsAsync(client);
    }

    /// <summary>
    /// Polls the named counter until it settles, failing the test if it never
    /// does within <see cref="CounterTimeout"/>.
    /// </summary>
    private static async Task<CountResult> SettledCounterAsync(SpiceDBClient client, string counterName)
    {
        var deadline = DateTime.UtcNow + CounterTimeout;
        while (true)
        {
            var (result, stillCalculating) = await client.ExperimentalCountRelationshipsAsync(counterName);
            if (!stillCalculating)
            {
                Assert.NotNull(result);
                return result!;
            }

            Assert.True(
                DateTime.UtcNow < deadline,
                $"counter {counterName} never settled within {CounterTimeout}");
            await Task.Delay(CounterPollInterval);
        }
    }
}
