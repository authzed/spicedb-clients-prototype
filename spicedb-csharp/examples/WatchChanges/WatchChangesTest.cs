using Xunit;
// Example WatchChanges demonstrates watching for relationship changes.

using SpiceDB.Client;

namespace WatchChanges;

public class WatchChangesTest
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
    public async Task WatchChanges_ReceivesUpdates()
    {
        await using var client = SpiceDBClient.CreatePlaintext("localhost:50051", "somerandomkeyhere");

        await client.WriteSchemaAsync(Schema);

        // Write a relationship to get a starting revision
        var txn = new Transaction();
        txn.Touch(Relationship.FromTriple("document", "firstdoc", "viewer", "user", "alice"));
        var revision = await client.WriteAsync(txn);

        // Write another relationship that we expect to see in the watch stream
        var txn2 = new Transaction();
        txn2.Touch(Relationship.FromTriple("document", "seconddoc", "editor", "user", "bob"));
        await client.WriteAsync(txn2);

        // Watch for changes starting from the first revision
        using var cts = new CancellationTokenSource(TimeSpan.FromSeconds(5));
        var updates = new List<RelationshipUpdate>();

        try
        {
            await foreach (var update in client.UpdatesAsync(
                ["document"], revision, cts.Token))
            {
                updates.Add(update);
                // We only need the first update to verify it works
                break;
            }
        }
        catch (OperationCanceledException)
        {
            // Expected if no updates arrive in time
        }

        Assert.NotEmpty(updates);
        Assert.Equal("document", updates[0].Relationship.ResourceType);
    }
}
