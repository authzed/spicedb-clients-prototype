using System.Diagnostics;
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

/// <summary>
/// Per-page default deadlines (root DESIGN.md, "Treat a deadline as a budget"): finite paged
/// reads apply the client default deadline to each page request, a caller's total deadline
/// still bounds the whole read, and watch keeps no default. Runs against a real Kestrel gRPC
/// server so the transport's deadline handling is exercised end to end.
/// </summary>
public class PageDeadlineTests
{
    private const int PageDelayMs = 150;
    private const int StallMs = 5000;
    private static readonly TimeSpan Default = TimeSpan.FromMilliseconds(400);

    /// <summary>
    /// Serves pages of two results, each after <see cref="PageDelayMs"/>, and stalls on the third
    /// page. Watch stalls for longer than the client default before sending one event.
    /// </summary>
    private sealed class PagingService : PermissionsService.PermissionsServiceBase
    {
        public override async Task LookupResources(LookupResourcesRequest request, IServerStreamWriter<LookupResourcesResponse> stream, ServerCallContext context)
        {
            var page = request.OptionalCursor?.Token switch { null or "" => 1, "c1" => 2, _ => 3 };
            if (page == 3) { await Stall(StallMs, context.CancellationToken); return; }
            await Stall(PageDelayMs, context.CancellationToken);
            for (var i = 0; i < 2; i++)
                await stream.WriteAsync(new() { ResourceObjectId = $"doc{page}-{i}", Permissionship = LookupPermissionship.HasPermission, AfterResultCursor = new() { Token = $"c{page}" } });
        }

        public override async Task ReadRelationships(ReadRelationshipsRequest request, IServerStreamWriter<ReadRelationshipsResponse> stream, ServerCallContext context) =>
            await Stall(StallMs, context.CancellationToken);

        /// <summary>An unpaged stream: one subject, then a stall before the second.</summary>
        public override async Task LookupSubjects(LookupSubjectsRequest request, IServerStreamWriter<LookupSubjectsResponse> stream, ServerCallContext context)
        {
            await stream.WriteAsync(new() { Subject = new() { SubjectObjectId = "alice", Permissionship = LookupPermissionship.HasPermission } });
            await Stall(StallMs, context.CancellationToken);
        }
    }

    private sealed class SlowWatchService : WatchService.WatchServiceBase
    {
        public override async Task Watch(WatchRequest request, IServerStreamWriter<WatchResponse> stream, ServerCallContext context)
        {
            await Stall(1000, context.CancellationToken);
            await stream.WriteAsync(new() { ChangesThrough = new() { Token = "rev" } });
        }
    }

    private static Task Stall(int ms, CancellationToken token) => Task.Delay(ms, token).ContinueWith(_ => { }, TaskScheduler.Default);

    private static async Task<(WebApplication App, SpiceDBClient Client)> StartAsync(ClientOptions options)
    {
        AppContext.SetSwitch("System.Net.Http.SocketsHttpHandler.Http2UnencryptedSupport", true);
        var builder = WebApplication.CreateBuilder();
        builder.Logging.ClearProviders();
        builder.WebHost.ConfigureKestrel(o => o.Listen(IPAddress.Loopback, 0, l => l.Protocols = HttpProtocols.Http2));
        builder.Services.AddGrpc();
        var app = builder.Build();
        app.MapGrpcService<PagingService>();
        app.MapGrpcService<SlowWatchService>();
        await app.StartAsync();
        var address = app.Services.GetRequiredService<IServer>().Features.Get<IServerAddressesFeature>()!.Addresses.First();
        return (app, SpiceDBClient.CreateFromChannel(GrpcChannel.ForAddress(address), "test-token", options));
    }

    private static async Task<List<string>> Lookup(SpiceDBClient client, LookupOptions options, List<string> seen)
    {
        await foreach (var item in client.LookupResourcesAsync(Consistency.Full(), "document", "view", new ObjectRef("user", "alice"), options))
            seen.Add(item.ResourceId);
        return seen;
    }

    private static async Task<T> Watchdog<T>(Task<T> task)
    {
        if (await Task.WhenAny(task, Task.Delay(TimeSpan.FromSeconds(10))) != task) throw new TimeoutException("call did not complete within the watchdog");
        return await task;
    }

    [Fact]
    public async Task LookupStalledOnPageThreeFailsAfterThePerPageDefaultWithNoCallerOption()
    {
        var (app, client) = await StartAsync(new ClientOptions { DefaultTimeout = Default });
        try
        {
            var seen = new List<string>();
            var clock = Stopwatch.StartNew();
            Func<Task> act = () => Watchdog(Lookup(client, new LookupOptions { PageSize = 2 }, seen));
            var error = (await act.Should().ThrowAsync<DeadlineExceededException>()).Which;
            clock.Stop();

            seen.Should().Equal("doc1-0", "doc1-1", "doc2-0", "doc2-1");
            error.StatusCode.Should().Be(StatusCode.DeadlineExceeded);
            clock.Elapsed.Should().BeGreaterThanOrEqualTo(Default + TimeSpan.FromMilliseconds(2 * PageDelayMs - 50),
                "pages one and two succeed, so the read outlives a single default before page three fails");
            clock.Elapsed.Should().BeLessThan(TimeSpan.FromMilliseconds(StallMs), "page three must fail at the per-page default, not wait out the stall");
        }
        finally { await client.DisposeAsync(); await app.DisposeAsync(); }
    }

    [Fact]
    public async Task CallerTotalDeadlineWinsWhenEarlierThanThePerPageDefault()
    {
        var (app, client) = await StartAsync(new ClientOptions { DefaultTimeout = TimeSpan.FromSeconds(30) });
        try
        {
            var seen = new List<string>();
            var clock = Stopwatch.StartNew();
            Func<Task> act = () => Watchdog(Lookup(client, new LookupOptions { PageSize = 2, Timeout = TimeSpan.FromMilliseconds(600) }, seen));
            (await act.Should().ThrowAsync<DeadlineExceededException>()).Which.Message.Should().Contain("total deadline");
            clock.Elapsed.Should().BeLessThan(TimeSpan.FromMilliseconds(StallMs));
        }
        finally { await client.DisposeAsync(); await app.DisposeAsync(); }
    }

    [Fact]
    public async Task PerPageDefaultAppliesEvenWithALongerCallerTotal()
    {
        var (app, client) = await StartAsync(new ClientOptions { DefaultTimeout = Default });
        try
        {
            var clock = Stopwatch.StartNew();
            Func<Task> act = () => Watchdog(Task.Run(async () =>
            {
                await foreach (var _ in client.ReadRelationshipsAsync(Consistency.Full(), new Filter("document"), new ReadOptions { Timeout = TimeSpan.FromSeconds(30) })) { }
                return 0;
            }));
            (await act.Should().ThrowAsync<DeadlineExceededException>()).Which.Message.Should().Contain("Page request deadline");
            clock.Elapsed.Should().BeLessThan(TimeSpan.FromMilliseconds(StallMs));
        }
        finally { await client.DisposeAsync(); await app.DisposeAsync(); }
    }

    [Fact]
    public async Task UnpagedLookupSubjectsStalledBetweenMessagesFailsAfterThePerMessageDefault()
    {
        var (app, client) = await StartAsync(new ClientOptions { DefaultTimeout = Default });
        try
        {
            var seen = new List<string>();
            var clock = Stopwatch.StartNew();
            Func<Task> act = () => Watchdog(Task.Run(async () =>
            {
                await foreach (var s in client.LookupSubjectsAsync(Consistency.Full(), new ObjectRef("document", "1"), "view", "user"))
                    seen.Add(s.Subject.SubjectId);
                return 0;
            }));
            (await act.Should().ThrowAsync<DeadlineExceededException>()).Which.Message.Should().Contain("Page request deadline");
            seen.Should().Equal("alice");
            clock.Elapsed.Should().BeLessThan(TimeSpan.FromMilliseconds(StallMs));
        }
        finally { await client.DisposeAsync(); await app.DisposeAsync(); }
    }

    [Fact]
    public async Task WatchKeepsNoDefaultDeadline()
    {
        // The watch's first event arrives after 1s, well past the 400ms client default.
        var (app, client) = await StartAsync(new ClientOptions { DefaultTimeout = Default });
        try
        {
            var first = await Watchdog(Task.Run(async () =>
            {
                await foreach (var e in client.UpdatesAsync(["document"], null)) return e;
                return null;
            }));
            first!.ChangesThrough.Value.Should().Be("rev");
        }
        finally { await client.DisposeAsync(); await app.DisposeAsync(); }
    }
}
