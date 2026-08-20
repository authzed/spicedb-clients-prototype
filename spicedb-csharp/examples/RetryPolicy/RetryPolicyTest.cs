using Xunit;

using System.Net;
using Authzed.Api.V1;
using Grpc.Core;
using Grpc.Net.Client;
using Microsoft.AspNetCore.Builder;
using Microsoft.AspNetCore.Hosting;
using Microsoft.AspNetCore.Hosting.Server;
using Microsoft.AspNetCore.Hosting.Server.Features;
using Microsoft.AspNetCore.Server.Kestrel.Core;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.Hosting;
using Microsoft.Extensions.Logging;
using SpiceDB.Client;
using static SpiceDB.Client.Consistency;
using Relationship = SpiceDB.Client.Relationship;

namespace RetryPolicy;

/// <summary>
/// Example RetryPolicy demonstrates which calls this client retries on your behalf and
/// which it deliberately does not -- see root DESIGN.md, "RULE: Automatic retry is for
/// idempotent operations only".
/// </summary>
/// <remarks>
/// <para>
/// The rule exists because a silently retried mutation produces a confident wrong
/// answer. If a <c>WriteRelationships</c> carrying <c>OPERATION_CREATE</c> commits and
/// the response is lost, the retry comes back <c>ALREADY_EXISTS</c> -- and the caller
/// concludes a write failed that in fact succeeded.
/// </para>
/// <para>
/// Attempts are counted <em>server-side</em>, which is the only way to tell a retry from
/// its absence: from the caller's side a transparently-retried success and a first-try
/// success are identical, and that is exactly the property that would rot unnoticed. It
/// stands up a stand-in SpiceDB because a real one cannot be asked to fail transiently
/// on demand.
/// </para>
/// </remarks>
public class RetryPolicyTest
{
    /// <summary>Counters the stand-in increments, read by the assertions.</summary>
    private sealed class Counts
    {
        public int Check;
        public int Write;
        public int CheckFailures;
        public StatusCode CheckCode = StatusCode.Unavailable;
    }

    private sealed class CountingService(Counts counts) : PermissionsService.PermissionsServiceBase
    {
        public override Task<CheckBulkPermissionsResponse> CheckBulkPermissions(
            CheckBulkPermissionsRequest request, ServerCallContext context)
        {
            if (Interlocked.Increment(ref counts.Check) <= counts.CheckFailures)
            {
                throw new RpcException(
                    new Status(counts.CheckCode, "transient, from the stand-in"));
            }

            var response = new CheckBulkPermissionsResponse();
            foreach (var _ in request.Items)
            {
                response.Pairs.Add(new CheckBulkPermissionsPair
                {
                    Item = new CheckBulkPermissionsResponseItem
                    {
                        Permissionship = CheckPermissionResponse.Types.Permissionship.HasPermission,
                    },
                });
            }

            return Task.FromResult(response);
        }

        public override Task<WriteRelationshipsResponse> WriteRelationships(
            WriteRelationshipsRequest request, ServerCallContext context)
        {
            Interlocked.Increment(ref counts.Write);
            // Always fails, transiently. A retrying client would come back.
            throw new RpcException(
                new Status(StatusCode.Unavailable, "transient, from the stand-in"));
        }
    }

    private static async Task<(WebApplication App, SpiceDBClient Client)> StartAsync(Counts counts)
    {
        AppContext.SetSwitch("System.Net.Http.SocketsHttpHandler.Http2UnencryptedSupport", true);
        var builder = WebApplication.CreateBuilder();
        builder.Logging.ClearProviders();
        builder.WebHost.ConfigureKestrel(options =>
            options.Listen(IPAddress.Loopback, 0, o => o.Protocols = HttpProtocols.Http2));
        builder.Services.AddGrpc();
        builder.Services.AddSingleton(counts);
        var app = builder.Build();
        app.MapGrpcService<CountingService>();
        await app.StartAsync();

        var address = app.Services.GetRequiredService<IServer>()
            .Features.Get<IServerAddressesFeature>()!.Addresses.First();
        var channel = GrpcChannel.ForAddress(address);
        return (app, SpiceDBClient.CreateFromChannel(channel, "t"));
    }

    private static Relationship Doc() =>
        Relationship.FromTriple("document", "readme", "view", "user", "alice");

    [Fact]
    public async Task ARead_IsRetriedTransparently()
    {
        // Two UNAVAILABLE responses, then success. The caller sees one successful check
        // and never learns the first two attempts happened -- the entire value of
        // retrying reads, and safe precisely because a repeated read changes nothing.
        var counts = new Counts { CheckFailures = 2 };
        var (app, client) = await StartAsync(counts);
        try
        {
            var result = await client.CheckPermissionAsync(Full(), "view", Doc());
            Assert.True(result.HasPermission);
            Assert.True(
                counts.Check == 3,
                $"expected 2 failures plus 1 success = 3 attempts, got {counts.Check} " +
                "(0 or 1 means reads are not being retried at all)");
        }
        finally
        {
            await client.DisposeAsync();
            await app.StopAsync();
        }
    }

    [Fact]
    public async Task AMutation_IsNotRetried()
    {
        // The same transient code, on a write. The error reaches the caller on the first
        // attempt, so the caller -- who alone knows whether a replay is safe for the
        // transaction they built -- decides what happens next.
        var counts = new Counts();
        var (app, client) = await StartAsync(counts);
        try
        {
            var txn = new Transaction();
            txn.Touch(Relationship.FromTriple("document", "readme", "viewer", "user", "alice"));
            await Assert.ThrowsAsync<UnavailableException>(() => client.WriteAsync(txn));
            Assert.True(
                counts.Write == 1,
                $"a mutation must not be retried silently: WriteRelationships saw {counts.Write} " +
                "attempts, so a lost response would leave the caller believing a committed " +
                "write had failed");
        }
        finally
        {
            await client.DisposeAsync();
            await app.StopAsync();
        }
    }

    [Fact]
    public async Task ResourceExhausted_IsNotRetriedEvenOnARead()
    {
        // In SpiceDB this code means memory load-shed or a deterministic
        // MaxDepthExceeded. Retrying the first makes the overload worse; the second can
        // never succeed however many times it is tried.
        var counts = new Counts { CheckFailures = 99, CheckCode = StatusCode.ResourceExhausted };
        var (app, client) = await StartAsync(counts);
        try
        {
            await Assert.ThrowsAsync<ResourceExhaustedException>(
                () => client.CheckPermissionAsync(Full(), "view", Doc()));
            Assert.True(
                counts.Check == 1,
                $"RESOURCE_EXHAUSTED must not be retried: saw {counts.Check} attempts, which " +
                "turns a load-shedding SpiceDB into a client-driven retry storm");
        }
        finally
        {
            await client.DisposeAsync();
            await app.StopAsync();
        }
    }
}
