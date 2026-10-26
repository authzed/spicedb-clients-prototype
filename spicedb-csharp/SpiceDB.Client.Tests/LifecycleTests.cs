using System.Net;
using System.Runtime.CompilerServices;
using Authzed.Api.V1;
using Grpc.Core;
using Microsoft.AspNetCore.Builder;
using Microsoft.AspNetCore.Hosting;
using Microsoft.AspNetCore.Hosting.Server;
using Microsoft.AspNetCore.Hosting.Server.Features;
using Microsoft.AspNetCore.Server.Kestrel.Core;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.Logging;
using Xunit;
namespace SpiceDB.Client.Tests;

public class LifecycleTests
{
    public sealed class Service : PermissionsService.PermissionsServiceBase
    {
        public TaskCompletionSource Started { get; } = new(TaskCreationOptions.RunContinuationsAsynchronously);
        public TaskCompletionSource Stopped { get; } = new(TaskCreationOptions.RunContinuationsAsynchronously);
        public bool EmitFirst { get; set; } = true;
        public bool RejectUpload { get; set; }
        public override async Task ReadRelationships(ReadRelationshipsRequest request, IServerStreamWriter<ReadRelationshipsResponse> response, ServerCallContext context)
        {
            Started.TrySetResult();
            try
            {
                if (EmitFirst) await response.WriteAsync(new() { Relationship = Relationship.FromTuple("document:one#viewer@user:alice").ToProto() });
                await Task.Delay(Timeout.InfiniteTimeSpan, context.CancellationToken);
            }
            catch (OperationCanceledException) { }
            finally { Stopped.TrySetResult(); }
        }
        public override async Task<ImportBulkRelationshipsResponse> ImportBulkRelationships(IAsyncStreamReader<ImportBulkRelationshipsRequest> request, ServerCallContext context)
        {
            try
            {
                while (await request.MoveNext(context.CancellationToken))
                {
                    Started.TrySetResult();
                    if (RejectUpload) throw new RpcException(new Status(StatusCode.InvalidArgument, "upload rejected"));
                }
                return new();
            }
            catch (Exception) when (context.CancellationToken.IsCancellationRequested) { return new(); }
            finally { Stopped.TrySetResult(); }
        }
    }
    private sealed class Host(WebApplication app, SpiceDBClient client, Service service) : IAsyncDisposable
    {
        public SpiceDBClient Client { get; } = client;
        public Service Service { get; } = service;
        public async ValueTask DisposeAsync() { await Client.DisposeAsync(); await app.StopAsync(); await app.DisposeAsync(); }
    }
    private static async Task<Host> Start(bool emitFirst = true)
    {
        var service = new Service { EmitFirst = emitFirst };
        var builder = WebApplication.CreateBuilder(); builder.Logging.ClearProviders();
        builder.WebHost.ConfigureKestrel(k => k.Listen(IPAddress.Loopback, 0, o => o.Protocols = HttpProtocols.Http2));
        builder.Services.AddGrpc(); builder.Services.AddSingleton(service);
        var app = builder.Build(); app.MapGrpcService<Service>(); await app.StartAsync();
        var address = app.Services.GetRequiredService<IServer>().Features.Get<IServerAddressesFeature>()!.Addresses.Single();
        return new(app, SpiceDBClient.CreatePlaintext(new Uri(address).Authority, "test"), service);
    }
    [Fact]
    public async Task EarlyBreakReleasesActualServerStream()
    {
        await using var host = await Start();
        await foreach (var item in host.Client.ReadRelationshipsAsync(Consistency.Full(), new Filter("document"))) { Assert.Equal("one", item.Resource.ObjectId); break; }
        await host.Service.Stopped.Task.WaitAsync(TimeSpan.FromSeconds(5));
    }
    [Theory]
    [InlineData(false)]
    [InlineData(true)]
    public async Task CancellationOrClientDisposalUnblocksWaitingStreamAndStopsServer(bool disposeClient)
    {
        await using var host = await Start(false); using var cts = new CancellationTokenSource();
        await using var iterator = host.Client.ReadRelationshipsAsync(Consistency.Full(), new Filter("document")).GetAsyncEnumerator(cts.Token);
        var pending = iterator.MoveNextAsync().AsTask(); await host.Service.Started.Task.WaitAsync(TimeSpan.FromSeconds(5));
        if (disposeClient) await host.Client.DisposeAsync(); else cts.Cancel();
        var error = await Assert.ThrowsAnyAsync<OperationCanceledException>(() => pending);
        if (!disposeClient) Assert.Equal(cts.Token, error.CancellationToken);
        await host.Service.Stopped.Task.WaitAsync(TimeSpan.FromSeconds(5));
    }
    [Fact]
    public async Task ServerRejectionCancelsBlockedProducerAndPreservesRemoteError()
    {
        await using var host = await Start(); host.Service.RejectUpload = true;
        bool disposed = false;
        async IAsyncEnumerable<Relationship> Input([EnumeratorCancellation] CancellationToken token = default)
        {
            try
            {
                yield return Relationship.FromTuple("document:one#viewer@user:alice");
                await Task.Delay(Timeout.InfiniteTimeSpan, token);
            }
            finally { disposed = true; }
        }
        var error = await Assert.ThrowsAsync<InvalidArgumentException>(() => host.Client.ImportRelationshipsAsync(Input(), new ImportOptions { BatchSize = 1 }).WaitAsync(TimeSpan.FromSeconds(5)));
        Assert.Contains("upload rejected", error.Message);
        Assert.IsType<RpcException>(error.InnerException);
        Assert.True(disposed);
    }

    [Fact]
    public async Task ProducerFailureDisposesInputAndCancelsStartedUpload()
    {
        await using var host = await Start(); bool disposed = false;
        async IAsyncEnumerable<Relationship> Input([EnumeratorCancellation] CancellationToken token = default)
        {
            try
            {
                yield return Relationship.FromTuple("document:one#viewer@user:alice");
                await host.Service.Started.Task.WaitAsync(TimeSpan.FromSeconds(5), token);
                throw new InvalidOperationException("producer failed");
            }
            finally { disposed = true; }
        }
        var error = await Assert.ThrowsAsync<InvalidOperationException>(() => host.Client.ImportRelationshipsAsync(Input(), new ImportOptions { BatchSize = 1 }));
        Assert.Equal("producer failed", error.Message); Assert.True(disposed);
        await host.Service.Stopped.Task.WaitAsync(TimeSpan.FromSeconds(5));
    }
}
