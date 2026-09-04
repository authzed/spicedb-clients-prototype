using System.Net;
using Authzed.Api.V1;
using FluentAssertions;
using Grpc.Core;
using Grpc.Net.Client;
using Microsoft.AspNetCore.Builder;
using Microsoft.AspNetCore.Hosting;
using Microsoft.AspNetCore.Hosting.Server;
using Microsoft.AspNetCore.Hosting.Server.Features;
using Microsoft.AspNetCore.Server.Kestrel.Core;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.Logging;
using SpiceDB.Client;
using Xunit;

namespace SpiceDB.Client.Tests;

// ─────────────────────────────────────────────────────────────────────────
// Call-deadline enforcement. Root DESIGN.md, "RULE: A unary call must have
// a deadline".
//
// Runs a REAL Kestrel-hosted gRPC server (Grpc.AspNetCore.Server) whose
// handlers deliberately stall, so these tests exercise CallOptions.Deadline
// enforcement through the real Grpc.Net.Client transport end to end -- a
// Moq-mocked service client (as used elsewhere in this suite) can't prove a
// deadline is actually enforced, since grpc's deadline machinery lives
// below the mock, inside Grpc.Net.Client's HTTP/2 pipeline.
//
// Every stalling handler self-bounds its stall to StallMs (and polls the
// server call's CancellationToken so it returns promptly once the client
// gives up, keeping teardown fast) rather than blocking forever: long
// enough to dwarf the tiny per-test deadlines below (proving enforcement,
// not luck). Each call carries its own bounded expectation (via
// FluentAssertions' WithTimeout/CompleteWithinAsync-style checks below), so
// a regression fails the test instead of hanging it -- and CI along with
// it.
// ─────────────────────────────────────────────────────────────────────────

public class DeadlineTests
{
    private const int StallMs = 1500;
    private static readonly TimeSpan WatchdogTimeout = TimeSpan.FromSeconds(10);

    private sealed class StallingPermissionsService : PermissionsService.PermissionsServiceBase
    {
        public override async Task<CheckBulkPermissionsResponse> CheckBulkPermissions(
            CheckBulkPermissionsRequest request, ServerCallContext context)
        {
            await WaitOutStallOrCancellation(context.CancellationToken);
            return new CheckBulkPermissionsResponse();
        }

        public override async Task ReadRelationships(
            ReadRelationshipsRequest request,
            IServerStreamWriter<ReadRelationshipsResponse> responseStream,
            ServerCallContext context)
        {
            await WaitOutStallOrCancellation(context.CancellationToken);
            await responseStream.WriteAsync(new ReadRelationshipsResponse
            {
                Relationship = new Authzed.Api.V1.Relationship
                {
                    Resource = new ObjectReference { ObjectType = "document", ObjectId = "a" },
                    Relation = "viewer",
                    Subject = new SubjectReference
                    {
                        Object = new ObjectReference { ObjectType = "user", ObjectId = "jimmy" },
                    },
                },
            });
        }

        public override async Task<ImportBulkRelationshipsResponse> ImportBulkRelationships(
            IAsyncStreamReader<ImportBulkRelationshipsRequest> requestStream, ServerCallContext context)
        {
            ulong numLoaded = 0;
            // Drain the client-streaming request fully before ever stalling -- a real server
            // can't respond mid-stream, so the stall must be *after* the last chunk, not instead
            // of reading it.
            while (await requestStream.MoveNext(context.CancellationToken))
            {
                numLoaded += (ulong)requestStream.Current.Relationships.Count;
            }
            await WaitOutStallOrCancellation(context.CancellationToken);
            return new ImportBulkRelationshipsResponse { NumLoaded = numLoaded };
        }

        /// <summary>
        /// Simulates a wedged server: waits up to <see cref="StallMs"/>, but returns early once
        /// <paramref name="cancellationToken"/> fires (the client's deadline expiry propagates a
        /// cancellation to the server-side call). Without this, teardown after a deadline-exceeded
        /// test would block waiting for this handler to finish its full stall.
        /// </summary>
        private static Task WaitOutStallOrCancellation(CancellationToken cancellationToken) =>
            Task.Delay(StallMs, cancellationToken).ContinueWith(
                _ => { }, TaskScheduler.Default);
    }

    /// <summary>
    /// Starts a real Kestrel-hosted gRPC server (plaintext HTTP/2) exposing
    /// <see cref="StallingPermissionsService"/>, and a real <see cref="SpiceDBClient"/> connected
    /// to it over an actual loopback socket via <see cref="SpiceDBClient.CreateFromChannel"/>.
    /// </summary>
    private static async Task<(WebApplication App, SpiceDBClient Client)> StartAsync(TimeSpan defaultTimeout)
    {
        // Grpc.Net.Client requires this switch to allow cleartext (h2c) HTTP/2 -- the server
        // below has no TLS, matching how this client's own "insecure" constructors work.
        AppContext.SetSwitch("System.Net.Http.SocketsHttpHandler.Http2UnencryptedSupport", true);

        var builder = WebApplication.CreateBuilder();
        builder.Logging.ClearProviders();
        builder.WebHost.ConfigureKestrel(options =>
        {
            options.Listen(IPAddress.Loopback, 0, listenOptions =>
            {
                listenOptions.Protocols = HttpProtocols.Http2;
            });
        });
        builder.Services.AddGrpc();

        var app = builder.Build();
        app.MapGrpcService<StallingPermissionsService>();
        await app.StartAsync();

        var addressFeature = app.Services.GetRequiredService<IServer>()
            .Features.Get<IServerAddressesFeature>()!;
        var address = addressFeature.Addresses.First();

        var channel = GrpcChannel.ForAddress(address);
        var client = SpiceDBClient.CreateFromChannel(channel, "test-token", defaultTimeout);

        return (app, client);
    }

    private static Relationship Rel() =>
        Relationship.FromTriple("document", "doc1", "view", "user", "alice");

    /// <summary>
    /// Waits for <paramref name="task"/> up to <see cref="WatchdogTimeout"/>. Throws a
    /// <see cref="TimeoutException"/> (failing the test loudly) instead of hanging the test --
    /// and CI along with it -- if a regression reintroduces an unbounded call. Once
    /// <paramref name="task"/> settles within the watchdog, its own result/exception propagates
    /// normally via <c>await</c>.
    /// </summary>
    private static async Task<T> RunWithWatchdogAsync<T>(Task<T> task)
    {
        var completed = await Task.WhenAny(task, Task.Delay(WatchdogTimeout));
        if (!ReferenceEquals(completed, task))
        {
            throw new TimeoutException(
                $"call did not complete within {WatchdogTimeout} -- deadline enforcement regressed");
        }
        return await task;
    }

    [Fact]
    public async Task UnaryCallAgainstStubThatNeverResponds_TimesOutWithDeadlineExceeded()
    {
        var (app, client) = await StartAsync(TimeSpan.FromMilliseconds(200));
        try
        {
            var start = DateTime.UtcNow;
            Func<Task> act = () => RunWithWatchdogAsync(client.CheckPermissionAsync(Consistency.Full(), "view", Rel()));

            await act.Should().ThrowAsync<DeadlineExceededException>();
            var elapsed = DateTime.UtcNow - start;

            elapsed.Should().BeLessThan(TimeSpan.FromMilliseconds(StallMs),
                "the call must fail at the ~200ms client default, not wait out the server's stall");
        }
        finally
        {
            await client.DisposeAsync();
            await app.DisposeAsync();
        }
    }

    [Fact]
    public async Task PerCallTimeout_OverridesMuchLargerClientDefault()
    {
        // Client default is larger than the server's stall -- if the per-call override did not
        // take effect, this call would not fail quickly.
        var (app, client) = await StartAsync(TimeSpan.FromMilliseconds(StallMs * 10));
        try
        {
            var start = DateTime.UtcNow;
            Func<Task> act = () => RunWithWatchdogAsync(client.CheckPermissionWithOptionsAsync(
                Consistency.Full(), "view", Rel(),
                new CheckOptions { Timeout = TimeSpan.FromMilliseconds(200) }));

            await act.Should().ThrowAsync<DeadlineExceededException>();
            var elapsed = DateTime.UtcNow - start;

            elapsed.Should().BeLessThan(TimeSpan.FromMilliseconds(StallMs),
                "the per-call timeout=200ms must override the large client default");
        }
        finally
        {
            await client.DisposeAsync();
            await app.DisposeAsync();
        }
    }

    [Fact]
    public async Task StreamingCall_DoesNotInheritTheUnaryDefault()
    {
        // defaultTimeout is far smaller than the server's stall. If ReadRelationshipsAsync
        // inherited it, this would throw DeadlineExceededException instead of yielding the item.
        var (app, client) = await StartAsync(TimeSpan.FromMilliseconds(100));
        try
        {
            var start = DateTime.UtcNow;

            var collectTask = Task.Run(async () =>
            {
                var items = new List<Relationship>();
                await foreach (var rel in client.ReadRelationshipsAsync(Consistency.Full(), new Filter("document")))
                {
                    items.Add(rel);
                }
                return items;
            });

            var completed = await Task.WhenAny(collectTask, Task.Delay(WatchdogTimeout));
            completed.Should().Be(collectTask,
                "the stream did not complete within the watchdog -- deadline enforcement regressed");

            var items = await collectTask;
            var elapsed = DateTime.UtcNow - start;

            items.Should().ContainSingle().Which.ResourceID.Should().Be("a");
            elapsed.Should().BeGreaterThanOrEqualTo(TimeSpan.FromMilliseconds(StallMs),
                "the stream must outlive the tiny unary default, not get cut off by it");
        }
        finally
        {
            await client.DisposeAsync();
            await app.DisposeAsync();
        }
    }

    [Fact]
    public async Task ImportRelationshipsAsync_DoesNotInheritTheUnaryDefault()
    {
        // ImportRelationshipsAsync (ImportBulkRelationships) is client-streaming: its duration
        // scales with the size of the caller's dataset, not with server latency, so root
        // DESIGN.md's "RULE: A unary call must have a deadline" (clause 3, amended to cover
        // client-streaming and bidirectional RPCs) excludes it from DefaultTimeout.
        // defaultTimeout here is far smaller than the server's stall -- if ImportRelationshipsAsync
        // inherited it, this call would throw DeadlineExceededException instead of completing.
        var (app, client) = await StartAsync(TimeSpan.FromMilliseconds(100));
        try
        {
            var start = DateTime.UtcNow;
            var numLoaded = await RunWithWatchdogAsync(client.ImportRelationshipsAsync(ToAsyncEnumerable(Rel())));
            var elapsed = DateTime.UtcNow - start;

            numLoaded.Should().Be(1);
            elapsed.Should().BeGreaterThanOrEqualTo(TimeSpan.FromMilliseconds(StallMs),
                "ImportRelationshipsAsync must outlive the tiny unary default, not get cut off by it");
        }
        finally
        {
            await client.DisposeAsync();
            await app.DisposeAsync();
        }
    }

    [Fact]
    public async Task ImportRelationshipsAsync_WithTimeout_StillBoundsTheCall()
    {
        // The exclusion above is from the *default*, not from the ability to bound the call at
        // all -- an explicit per-call timeout must still fire against a stalling server.
        var (app, client) = await StartAsync(TimeSpan.FromSeconds(30));
        try
        {
            var start = DateTime.UtcNow;
            Func<Task> act = () => RunWithWatchdogAsync(
                client.ImportRelationshipsAsync(ToAsyncEnumerable(Rel()), timeout: TimeSpan.FromMilliseconds(200)));

            await act.Should().ThrowAsync<DeadlineExceededException>();
            var elapsed = DateTime.UtcNow - start;

            elapsed.Should().BeLessThan(TimeSpan.FromMilliseconds(StallMs),
                "an explicit 200ms timeout on ImportRelationshipsAsync must still fire");
        }
        finally
        {
            await client.DisposeAsync();
            await app.DisposeAsync();
        }
    }

    [Fact]
    public async Task CancellationToken_ActuallyStopsALiveCall()
    {
        // Existing tests elsewhere in this suite assert Moq's It.IsAny<CancellationToken>() was
        // passed through to the mocked service client -- that proves plumbing, not propagation.
        // This test cancels a real in-flight call against the real Kestrel-hosted server above
        // and asserts it actually stops promptly, not that a token object merely arrived somewhere.
        var (app, client) = await StartAsync(TimeSpan.FromSeconds(30));
        try
        {
            using var cts = new CancellationTokenSource();
            var callTask = client.CheckPermissionAsync(Consistency.Full(), "view", Rel(), cts.Token);

            // Give the call a moment to actually reach the server (as opposed to cancelling before
            // it's even dispatched, which wouldn't prove mid-flight cancellation).
            await Task.Delay(100);
            var start = DateTime.UtcNow;
            cts.Cancel();

            Func<Task> act = () => RunWithWatchdogAsync(callTask);
            await act.Should().ThrowAsync<Exception>();
            var elapsed = DateTime.UtcNow - start;

            elapsed.Should().BeLessThan(TimeSpan.FromMilliseconds(StallMs),
                "cancelling the token must stop the call promptly, not wait out the server's stall");
        }
        finally
        {
            await client.DisposeAsync();
            await app.DisposeAsync();
        }
    }

    [Fact]
    public void DefaultTimeout_IsThirtySeconds()
    {
        SpiceDBClient.DefaultTimeout.Should().Be(TimeSpan.FromSeconds(30));
    }

    private static async IAsyncEnumerable<Relationship> ToAsyncEnumerable(params Relationship[] relationships)
    {
        foreach (var rel in relationships)
        {
            yield return rel;
        }

        await Task.CompletedTask;
    }
}
