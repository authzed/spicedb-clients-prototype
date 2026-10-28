using Grpc.Core;
using Grpc.Core.Interceptors;
using Grpc.Net.Client;
using SpiceDB.Client;
using SpiceDB.Client.Protocol;
using Xunit;
using static SpiceDB.Client.Consistency;

// Example ToolingEscapeHatches demonstrates what schema-agnostic tools (CLIs, backup and
// restore, test harnesses) need without dropping to RawProto(): a client over an existing
// caller-owned CallInvoker, response metadata capture, the server's abort status from an
// import, all-or-nothing and progress-reporting deletes, full tuple syntax, watch schema
// changes, gRPC status interop on native errors, and exported protocol conversions.

namespace ToolingEscapeHatches;

public class ToolingEscapeHatchesTest
{
    private const string Schema = """
        use expiration

        caveat only_on(day string) {
            day == "monday"
        }

        definition user {}

        definition document {
            relation viewer: user | user with only_on | user with expiration
            permission view = viewer
        }
        """;

    private static (GrpcChannel Channel, SpiceDBClient Client) Borrowed(ClientOptions? options = null)
    {
        // A caller-built channel and invoker, as a test server or embedded SpiceDB would hand
        // out. The invoker carries its own credentials; the client adds none and never
        // disposes the channel.
        var channel = GrpcChannel.ForAddress("http://" + SpiceDBTestServer.Endpoint);
        var invoker = channel.CreateCallInvoker().Intercept(metadata =>
        {
            metadata.Add("authorization", "Bearer " + SpiceDBTestServer.Token);
            return metadata;
        });
        return (channel, SpiceDBClient.CreateFromCallInvoker(invoker, options));
    }

    [Fact]
    public async Task ToolingNeedsNoRawProto()
    {
        var (channel, client) = Borrowed(new ClientOptions { DefaultDeadline = DefaultDeadline.Disabled, StreamRetries = StreamRetries.Disabled });
        using var _ = channel;
        await using (client)
        {
            await SpiceDBTestServer.ClearDocumentRelationshipsAsync(client);
            var sink = new ResponseMetadata();
            await client.WriteSchemaAsync(Schema, new SpiceDB.Client.CallOptions { Transport = new TransportOptions { ResponseMetadata = sink } }, default);
            Assert.NotNull(sink.Headers);
            Assert.NotNull(sink.Trailers);

            // Full tuple syntax round-trips through the server.
            var caveated = Relationship.FromTuple("document:tool#viewer@user:carol[only_on:{\"day\":\"monday\"}]");
            var expiring = Relationship.FromTuple("document:tool#viewer@user:dave[expiration:2099-01-02T03:04:05Z]");
            var txn = new Transaction();
            txn.Touch(caveated); txn.Touch(expiring);
            Assert.Equal(2, txn.Count);
            var written = await client.WriteAsync(txn, null, default);
            var read = new Dictionary<string, string>();
            await foreach (var item in client.ReadRelationshipsAsync(AtLeast(written.Revision), new Filter("document").WithResourceId("tool"), null, default))
                read[item.Relationship.SubjectId] = item.Relationship.ToString();
            Assert.Equal(caveated.ToString(), read["carol"]);
            Assert.Equal(expiring.ToString(), read["dave"]);

            // Exported conversions produce the protocol shape tools persist or print.
            Assert.Equal("only_on", ProtoConvert.ToProto(caveated).OptionalCaveat.CaveatName);

            // Importing a duplicate reports the server's ALREADY_EXISTS, not a failed send.
            static async IAsyncEnumerable<Relationship> Duplicates(Relationship r)
            {
                for (var i = 0; i < 3; i++) { yield return r; await Task.Yield(); }
            }
            var duplicate = await Assert.ThrowsAsync<AlreadyExistsException>(() =>
                client.ImportRelationshipsAsync(Duplicates(Relationship.FromTuple("document:tool#viewer@user:erin")), new ImportOptions { BatchSize = 1 }, default));
            Assert.Equal(StatusCode.AlreadyExists, duplicate.StatusCode);
            Assert.False(string.IsNullOrEmpty(duplicate.ServerMessage));
            Assert.Equal(StatusCode.AlreadyExists, SpiceDBException.CodeOf(duplicate.ToRpcException()));

            // All-or-nothing deletion refuses to exceed its limit instead of deleting part.
            var tooMany = await Assert.ThrowsAnyAsync<SpiceDBException>(() =>
                client.DeleteRelationshipsAsync(new Filter("document").WithResourceId("tool"), 1, new DeleteOptions { Partial = PartialDeletion.Disallowed }, default));
            Assert.Equal(tooMany.StatusCode, tooMany.Status.StatusCode);
            var stillThere = 0;
            await foreach (var _r in client.ReadRelationshipsAsync(Full(), new Filter("document").WithResourceId("tool"), null, default)) stillThere++;
            Assert.Equal(2, stillThere);

            // Delete-all reports cumulative progress after every batch.
            var batch = new Transaction();
            for (var i = 0; i < 5; i++) batch.Touch(Relationship.FromTuple($"document:bulk{i}#viewer@user:frank"));
            await client.WriteAsync(batch, null, default);
            var progress = new List<DeleteResult>();
            var result = await client.DeleteAllRelationshipsNonAtomicAsync(new Filter("document").WithSubjectType("user").WithSubjectId("frank"),
                new DeleteAllOptions { PageSize = 2, Progress = new Recorder(progress) }, default);
            Assert.Equal(5ul, result.DeletedCount);
            Assert.Equal(progress.Select(p => p.DeletedCount).OrderBy(x => x), progress.Select(p => p.DeletedCount));
            Assert.Equal(5ul, progress[^1].DeletedCount);
            Assert.True(progress.Count >= 3);

            // All-or-nothing with the server's default limit succeeds for a small match.
            var deleted = await client.DeleteRelationshipsAsync(new Filter("document").WithResourceId("tool"), null, new DeleteOptions { Partial = PartialDeletion.Disallowed }, default);
            Assert.True(deleted.Completed);
            Assert.Equal(2ul, deleted.DeletedCount);
        }

        // The borrowed channel outlives the client.
        await using var again = SpiceDBClient.CreateFromCallInvoker(channel.CreateCallInvoker().Intercept(m => { m.Add("authorization", "Bearer " + SpiceDBTestServer.Token); return m; }));
        Assert.Contains("definition document", (await again.ReadSchemaAsync(null, default)).Schema);
    }

    [Fact]
    public async Task WatchReportsSchemaChanges()
    {
        await using var client = SpiceDBClient.CreatePlaintext(SpiceDBTestServer.Endpoint, SpiceDBTestServer.Token);
        await SpiceDBTestServer.ClearDocumentRelationshipsAsync(client);
        var start = await client.WriteSchemaAsync(Schema, null, default);
        using var cts = new CancellationTokenSource(TimeSpan.FromSeconds(30));
        var watch = client.UpdatesAsync([], start, new WatchOptions { Content = WatchContent.Relationships | WatchContent.SchemaUpdates | WatchContent.Checkpoints }, cts.Token).GetAsyncEnumerator(cts.Token);
        await client.WriteSchemaAsync(Schema.Replace("permission view = viewer", "permission view = viewer\n    permission read = viewer"), null, default);
        var sawSchema = false;
        while (!sawSchema && await watch.MoveNextAsync())
            sawSchema = watch.Current.SchemaUpdated;
        Assert.True(sawSchema);
        await watch.DisposeAsync();
    }

    private sealed class Recorder(List<DeleteResult> sink) : IProgress<DeleteResult>
    {
        public void Report(DeleteResult value) => sink.Add(value);
    }
}
