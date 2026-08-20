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

        // Clearing first makes the writes below real changes: a TOUCH of an
        // already-identical relationship is not a change, and SpiceDB emits no
        // watch event for it.
        await SpiceDBTestServer.ClearDocumentRelationshipsAsync(client);
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

        // The update must be the one that was written, not merely "an update":
        // asserting only the resource type would pass on a stream that
        // delivered the seed write, or any other document relationship an
        // earlier example left behind.
        Assert.NotEmpty(updates);
        var update = Assert.Single(updates);
        Assert.Equal("document", update.Relationship.ResourceType);
        Assert.Equal("seconddoc", update.Relationship.ResourceID);
        Assert.Equal("editor", update.Relationship.ResourceRelation);
        Assert.Equal("user", update.Relationship.SubjectType);
        Assert.Equal("bob", update.Relationship.SubjectID);
        // TOUCH is a write, so it can only be the mapping for an explicit
        // OPERATION_TOUCH -- never a default an unrecognized operation falls
        // into.
        Assert.True(
            update.Operation is UpdateOperation.Create or UpdateOperation.Touch,
            $"expected a Create or Touch for the relationship just written, got {update.Operation}");
        Assert.False(string.IsNullOrEmpty(resumeToken));

        await SpiceDBTestServer.ClearDocumentRelationshipsAsync(client);
    }

    [Fact]
    public async Task WatchChanges_CancellingTheConsumerStopsIt()
    {
        // A caller that walks away mid-stream must not be left waiting: the
        // consumer here is parked on a quiet watch stream -- nothing is being
        // written, so nothing will ever arrive -- and cancelling its token
        // ends it.
        //
        // What that does and does not prove was settled by mutation, not by
        // reading the code. Cutting the cancellation token out of BOTH places
        // UpdatesAsync passes it -- the Watch call and the
        // ResponseStream.MoveNext read -- still ends this consumer 5ms after
        // the cancel, because `await foreach` disposes the async iterator,
        // which disposes the gRPC call. **In C# the release half of R8 is a
        // language guarantee, not something this client implements**, so no
        // assertion here can fail on it, and the bound below is a regression
        // net rather than a proof.
        //
        // What IS this client's responsibility -- and what the final assertion
        // pins -- is that abandoning the stream surfaces as the native
        // CancelledException instead of leaking a raw Grpc.Core.RpcException
        // out of the streaming path. That one does fail when broken.
        // See root DESIGN.md, "RULE: Abandoning a stream must release it".
        await using var client = SpiceDBClient.CreatePlaintext(SpiceDBTestServer.Endpoint, SpiceDBTestServer.Token);
        await SpiceDBTestServer.ClearDocumentRelationshipsAsync(client);
        await client.WriteSchemaAsync(Schema);

        using var cts = new CancellationTokenSource();
        var consumer = Task.Run(async () =>
        {
            await foreach (var _ in client.UpdatesAsync(
                objectTypes: ["document"], cancellationToken: cts.Token))
            {
                // Consume forever; only the cancellation below ends this.
            }
        });

        // Give the stream a moment to actually open before abandoning it.
        await Task.Delay(200);
        await cts.CancelAsync();

        var finished = await Task.WhenAny(consumer, Task.Delay(TimeSpan.FromSeconds(10)));
        Assert.True(
            ReferenceEquals(finished, consumer),
            "the watch consumer was still running 10s after its token was cancelled: " +
            "abandoning the stream did not stop it");
        // The native error type, not the raw gRPC one: a cancelled stream
        // surfaces as SpiceDB.Client.CancelledException. This is the assertion
        // in this test that a plausible mutation can break.
        await Assert.ThrowsAsync<CancelledException>(() => consumer);
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
        await SpiceDBTestServer.ClearDocumentRelationshipsAsync(client);
        await client.WriteSchemaAsync(Schema);

        using var cts = new CancellationTokenSource(TimeSpan.FromSeconds(30));
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
