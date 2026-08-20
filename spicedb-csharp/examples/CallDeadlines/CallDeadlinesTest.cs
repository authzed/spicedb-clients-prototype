using Xunit;
// Example CallDeadlines demonstrates the client-level defaultTimeout
// construction parameter, a per-call timeout override, and that bulk import
// (ImportRelationshipsAsync) is a client-streaming call that is NOT bounded
// by the unary default -- see root DESIGN.md, "RULE: A unary call must have
// a deadline".
//
// The failure that rule exists to close is a *wedged* server: one that accepts
// the connection and then never answers. Nothing looks wrong at the transport
// level, so an unbounded call hangs forever rather than erroring. The tests
// against a real SpiceDB below pass identically whether or not the timeout ever
// reaches the wire, so the last two stand up a socket that behaves exactly that
// way and require the call to come back DeadlineExceededException on the
// caller's schedule.

using System.Net;
using System.Net.Sockets;
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
            SpiceDBTestServer.Endpoint, SpiceDBTestServer.Token, defaultTimeout: TimeSpan.FromSeconds(5));

        // The schema above is narrower than the one the other examples write,
        // and all thirteen projects share one SpiceDB. SpiceDB refuses a
        // WriteSchema that drops a relation while a relationship still exists
        // under it, so clear first. Today this project happens to run after
        // BulkOperations, which writes no `editor`; reorder the solution and
        // it would fail exactly as RawEscapeHatch did.
        await SpiceDBTestServer.ClearDocumentRelationshipsAsync(client);
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
        await using var client = SpiceDBClient.CreatePlaintext(SpiceDBTestServer.Endpoint, SpiceDBTestServer.Token);

        // The schema above is narrower than the one the other examples write,
        // and all thirteen projects share one SpiceDB. SpiceDB refuses a
        // WriteSchema that drops a relation while a relationship still exists
        // under it, so clear first. Today this project happens to run after
        // BulkOperations, which writes no `editor`; reorder the solution and
        // it would fail exactly as RawEscapeHatch did.
        await SpiceDBTestServer.ClearDocumentRelationshipsAsync(client);
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
        await using var client = SpiceDBClient.CreatePlaintext(SpiceDBTestServer.Endpoint, SpiceDBTestServer.Token);

        // The schema above is narrower than the one the other examples write,
        // and all thirteen projects share one SpiceDB. SpiceDB refuses a
        // WriteSchema that drops a relation while a relationship still exists
        // under it, so clear first. Today this project happens to run after
        // BulkOperations, which writes no `editor`; reorder the solution and
        // it would fail exactly as RawEscapeHatch did.
        await SpiceDBTestServer.ClearDocumentRelationshipsAsync(client);
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

    /// <summary>
    /// The deadline handed to the calls against the wedged server. Short,
    /// because the point is to watch it expire.
    /// </summary>
    private static readonly TimeSpan WedgedTimeout = TimeSpan.FromSeconds(2);

    /// <summary>
    /// Wall-clock bound on a wedged call. If a call with a 2s deadline has not
    /// returned after this long, the deadline is not reaching the RPC -- and
    /// the test fails with that message instead of hanging the CI job.
    /// </summary>
    private static readonly TimeSpan WedgedWatchdog = TimeSpan.FromSeconds(17);

    [Fact]
    public async Task DefaultTimeout_ExpiresAgainstAServerThatNeverAnswers()
    {
        using var wedged = WedgedListener(out var endpoint);

        await using var client = SpiceDBClient.CreatePlaintext(
            endpoint, SpiceDBTestServer.Token, defaultTimeout: WedgedTimeout);

        var rel = Relationship.FromTriple("document", "readme", "view", "user", "alice");
        await ExpectDeadlineToFireAsync(
            "defaultTimeout",
            () => client.CheckPermissionAsync(Full(), "view", rel));
    }

    [Fact]
    public async Task PerCallTimeout_ExpiresAgainstAServerThatNeverAnswers()
    {
        using var wedged = WedgedListener(out var endpoint);

        // No defaultTimeout here, so only the per-call argument can bound this.
        // The override is a different code path, and one that accepted the
        // argument and dropped it would still pass every fast-local-call test
        // above.
        await using var client = SpiceDBClient.CreatePlaintext(endpoint, SpiceDBTestServer.Token);

        var rel = Relationship.FromTriple("document", "readme", "view", "user", "alice");
        await ExpectDeadlineToFireAsync(
            "per-call timeout",
            () => client.CheckPermissionAsync(Full(), "view", rel, timeout: WedgedTimeout));
    }

    /// <summary>
    /// A socket that accepts TCP connections and never speaks gRPC. The kernel
    /// completes the handshake for connections sitting in the backlog, so a
    /// client connects successfully and then waits forever for the HTTP/2
    /// server preface. That is what a wedged SpiceDB looks like from a client
    /// -- an open, healthy-looking connection with no reply behind it -- and it
    /// is why "the connection worked" is not a bound.
    /// </summary>
    private static TcpListener WedgedListener(out string endpoint)
    {
        var listener = new TcpListener(IPAddress.Loopback, 0);
        listener.Start();
        endpoint = $"127.0.0.1:{((IPEndPoint)listener.LocalEndpoint).Port}";
        return listener;
    }

    /// <summary>
    /// Runs the call under a watchdog and requires it to fail with
    /// DeadlineExceededException.
    /// </summary>
    private static async Task ExpectDeadlineToFireAsync(string what, Func<Task<CheckResult>> call)
    {
        var task = call();
        var finished = await Task.WhenAny(task, Task.Delay(WedgedWatchdog));
        Assert.True(
            ReferenceEquals(finished, task),
            $"a call with a {WedgedTimeout} {what} had not returned after {WedgedWatchdog} " +
            "against a server that never answers: the deadline is not reaching the RPC");

        // The specific exception matters. ThrowsAnyAsync<Exception> is also
        // satisfied by an UnavailableException from a refused connection, which
        // says nothing at all about deadlines.
        await Assert.ThrowsAsync<DeadlineExceededException>(() => task);
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
