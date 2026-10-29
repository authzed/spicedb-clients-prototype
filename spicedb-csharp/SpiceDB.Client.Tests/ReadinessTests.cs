using System.Diagnostics;
using System.Net;
using System.Net.Sockets;
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
using SpiceDB.Client.Testing;
using Xunit;

namespace SpiceDB.Client.Tests;

/// <summary>
/// The startup readiness check (root DESIGN.md, "Accept the endpoint forms…": clients that
/// connect lazily offer one) and local cancellation surfacing as the client's cancellation result
/// rather than the transport's.
/// </summary>
public class ReadinessTests
{
    private sealed class Schema : SchemaService.SchemaServiceBase
    {
        public override Task<ReadSchemaResponse> ReadSchema(ReadSchemaRequest request, ServerCallContext context) =>
            context.RequestHeaders.GetValue("authorization") == "Bearer good"
                ? throw new RpcException(new Status(StatusCode.NotFound, "No schema has been defined"))
                : throw new RpcException(new Status(StatusCode.Unauthenticated, "invalid preshared key"));
    }

    private sealed class Stalling : PermissionsService.PermissionsServiceBase
    {
        public override async Task<CheckPermissionResponse> CheckPermission(CheckPermissionRequest request, ServerCallContext context)
        {
            await Task.Delay(5000, context.CancellationToken).ContinueWith(_ => { }, TaskScheduler.Default);
            return new();
        }
    }

    private static async Task<(WebApplication App, string Address)> StartAsync()
    {
        AppContext.SetSwitch("System.Net.Http.SocketsHttpHandler.Http2UnencryptedSupport", true);
        var builder = WebApplication.CreateBuilder();
        builder.Logging.ClearProviders();
        builder.WebHost.ConfigureKestrel(o => o.Listen(IPAddress.Loopback, 0, l => l.Protocols = HttpProtocols.Http2));
        builder.Services.AddGrpc();
        var app = builder.Build();
        app.MapGrpcService<Schema>();
        app.MapGrpcService<Stalling>();
        await app.StartAsync();
        return (app, app.Services.GetRequiredService<IServer>().Features.Get<IServerAddressesFeature>()!.Addresses.First());
    }

    private static int UnusedPort()
    {
        using var listener = new TcpListener(IPAddress.Loopback, 0);
        listener.Start();
        return ((IPEndPoint)listener.LocalEndpoint).Port;
    }

    [Fact]
    public async Task ConnectAsyncSucceedsAgainstAReachableServerWithoutASchema()
    {
        var (app, address) = await StartAsync();
        try
        {
            await using var client = SpiceDBClient.CreatePlaintext(address.Replace("http://", ""), "good");
            await client.ConnectAsync(new ConnectOptions { Timeout = TimeSpan.FromSeconds(5) });
        }
        finally { await app.DisposeAsync(); }
    }

    [Fact]
    public async Task ConnectAsyncSurfacesARejectedKey()
    {
        var (app, address) = await StartAsync();
        try
        {
            await using var client = SpiceDBClient.CreatePlaintext(address.Replace("http://", ""), "bad");
            Func<Task> act = () => client.ConnectAsync(new ConnectOptions { Timeout = TimeSpan.FromSeconds(5) });
            await act.Should().ThrowAsync<UnauthenticatedException>();
        }
        finally { await app.DisposeAsync(); }
    }

    [Fact]
    public async Task ConnectAsyncFailsWithinTheTimeoutWhenTheEndpointIsUnreachable()
    {
        var endpoint = $"127.0.0.1:{UnusedPort()}";
        await using var client = SpiceDBClient.CreatePlaintext(endpoint, "good");
        var clock = Stopwatch.StartNew();
        Func<Task> act = () => client.ConnectAsync(new ConnectOptions { Timeout = TimeSpan.FromMilliseconds(500) });
        var error = (await act.Should().ThrowAsync<SpiceDBException>()).Which;
        clock.Elapsed.Should().BeLessThan(TimeSpan.FromSeconds(5));
        error.StatusCode.Should().BeOneOf(StatusCode.DeadlineExceeded, StatusCode.Unavailable);
        error.Message.Should().Contain(endpoint);
    }

    [Fact]
    public async Task ConnectAsyncUsesTheClientDefaultDeadline()
    {
        await using var client = SpiceDBClient.CreatePlaintext($"127.0.0.1:{UnusedPort()}", "good", new ClientOptions { DefaultTimeout = TimeSpan.FromMilliseconds(300) });
        var clock = Stopwatch.StartNew();
        Func<Task> act = () => client.ConnectAsync();
        await act.Should().ThrowAsync<DeadlineExceededException>();
        clock.Elapsed.Should().BeLessThan(TimeSpan.FromSeconds(5));
    }

    [Fact]
    public async Task InMemoryDoubleAcceptsConnectAsAnImmediateNoOp()
    {
        ISpiceDBClient client = new InMemorySpiceDBClient();
        await client.ConnectAsync();
        await client.ConnectAsync(new ConnectOptions { Timeout = TimeSpan.FromMilliseconds(1) });
    }

    [Fact]
    public async Task LocalCancellationOfALiveCallIsOperationCanceledWithTheCallersTokenNotATransportError()
    {
        var (app, address) = await StartAsync();
        try
        {
            await using var client = SpiceDBClient.CreatePlaintext(address.Replace("http://", ""), "good");
            using var cts = new CancellationTokenSource();
            var call = client.CheckAsync(new PermissionCheck(new ObjectRef("document", "1"), "view", new ObjectRef("user", "alice"), Consistency.Full()), null, cts.Token);
            await Task.Delay(100);
            cts.Cancel();
            var error = await Assert.ThrowsAnyAsync<OperationCanceledException>(() => call);
            error.CancellationToken.Should().Be(cts.Token);
            error.Should().NotBeAssignableTo<RpcException>();
            SpiceDBException.CodeOf(error).Should().Be(StatusCode.Cancelled);
        }
        finally { await app.DisposeAsync(); }
    }
}
