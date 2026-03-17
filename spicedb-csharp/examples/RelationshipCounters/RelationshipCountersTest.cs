// Example RelationshipCounters demonstrates registering, reading, and
// unregistering relationship counters.

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

    [Fact]
    public async Task RelationshipCounters_RegisterCountAndUnregister()
    {
        await using var client = SpiceDBClient.CreatePlaintext("localhost:50051", "somerandomkeyhere");

        await client.WriteSchemaAsync(Schema);

        var txn = new Transaction();
        txn.Touch(Relationship.FromTriple("document", "firstdoc", "viewer", "user", "alice"));
        txn.Touch(Relationship.FromTriple("document", "seconddoc", "viewer", "user", "bob"));
        await client.WriteAsync(txn);

        // Unregister any existing counter from a prior run (ignore errors)
        var counterName = "csharp_document_viewers";
        var filter = new Filter("document").WithRelation("viewer");

        try { await client.ExperimentalUnregisterRelationshipCounterAsync(counterName); }
        catch { /* ignore */ }

        // Register a counter
        await client.ExperimentalRegisterRelationshipCounterAsync(counterName, filter);

        // Wait for the counter to be computed
        await Task.Delay(TimeSpan.FromSeconds(2));

        // Read the counter value
        var (result, stillCalculating) = await client.ExperimentalCountRelationshipsAsync(counterName);

        if (!stillCalculating)
        {
            Assert.NotNull(result);
            Assert.True(result!.RelationshipCount > 0, "expected non-zero relationship count");
            Assert.NotEmpty(result.Revision);
        }

        // Unregister the counter
        await client.ExperimentalUnregisterRelationshipCounterAsync(counterName);
    }
}
