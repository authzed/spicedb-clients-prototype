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
        await using var client = SpiceDBClient.CreatePlaintext(SpiceDBTestServer.Endpoint, SpiceDBTestServer.Token);

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
        string? resumeToken = null;

        try
        {
            await foreach (var evt in client.UpdatesAsync(
                objectTypes: ["document"], startRevision: revision, cancellationToken: cts.Token))
            {
                // evt.ChangesThrough is a resume point: keep it and pass it as
                // startRevision on a later UpdatesAsync call to pick back up
                // after a dropped stream, instead of reprocessing everything
                // since the original startRevision or silently losing changes
                // by restarting from head.
                resumeToken = evt.ChangesThrough;
                updates.AddRange(evt.Updates);
                // We only need the first event to verify it works
                if (updates.Count > 0)
                    break;
            }
        }
        catch (OperationCanceledException)
        {
            // Expected if no updates arrive in time
        }

        Assert.NotEmpty(updates);
        Assert.Equal("document", updates[0].Relationship.ResourceType);
        Assert.False(string.IsNullOrEmpty(resumeToken));
    }

    [Fact]
    public async Task WatchChanges_WithCheckpoints_ReceivesCheckpointAndUpdate()
    {
        // includeCheckpoints asks the server for periodic checkpoint events in
        // addition to relationship updates -- recommended if this SpiceDB
        // instance is running behind a proxy that aborts idle connections,
        // since a checkpoint keeps the stream alive even when nothing has
        // changed. A checkpoint carries no updates, so a consumer must check
        // WatchEvent.IsCheckpoint to tell "nothing changed, here is a fresh
        // resume point" from "here are changes".
        await using var client = SpiceDBClient.CreatePlaintext(SpiceDBTestServer.Endpoint, SpiceDBTestServer.Token);
        await client.WriteSchemaAsync(Schema);

        using var cts = new CancellationTokenSource(TimeSpan.FromSeconds(5));
        var seenCheckpoint = false;
        var seenUpdate = false;

        var watchTask = Task.Run(async () =>
        {
            try
            {
                await foreach (var evt in client.UpdatesAsync(
                    objectTypes: ["document"], includeCheckpoints: true, cancellationToken: cts.Token))
                {
                    if (evt.IsCheckpoint)
                        seenCheckpoint = true;
                    else if (evt.Updates.Count > 0)
                        seenUpdate = true;

                    if (seenCheckpoint && seenUpdate)
                        break;
                }
            }
            catch (OperationCanceledException)
            {
                // Expected if the watch doesn't observe both in time.
            }
        });

        await Task.Delay(100); // let the watch start
        var txn = new Transaction();
        txn.Touch(Relationship.FromTriple("document", "thirddoc", "viewer", "user", "carol"));
        await client.WriteAsync(txn);

        await watchTask;

        Assert.True(seenCheckpoint);
        Assert.True(seenUpdate);
    }
}
