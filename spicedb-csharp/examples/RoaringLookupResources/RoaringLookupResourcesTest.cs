using Xunit;

using System.Net;
using Authzed.Api.Materialize.V0;
using Authzed.Api.V1;
using Google.Protobuf;
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

namespace RoaringLookupResources;

/// <summary>
/// Example RoaringLookupResources demonstrates
/// <see cref="SpiceDBClient.ExperimentalRoaringLookupResourcesAsync"/>, the wrapper
/// around the materialize-tier <c>RoaringLookupResourcesService</c>: a roaring64
/// bitmap of resource object IDs a subject has a permission on.
/// </summary>
/// <remarks>
/// <b>Why this example stands up its own server, like ErrorMapping does.</b> This RPC
/// is not served by the SpiceDB the integration job starts: `authzed/spicedb:latest`
/// lists no <c>authzed.api.materialize.v0.RoaringLookupResourcesService</c> via gRPC
/// reflection (verified, not assumed). A stand-in implementing the generated
/// <c>RoaringLookupResourcesServiceBase</c> exercises the real request/response
/// mapping this client added -- consistency/resource type/permission/subject on the
/// way out, bitmap bytes/cardinality/ZedToken on the way back -- without depending on
/// upstream server support that does not exist yet.
/// </remarks>
public class RoaringLookupResourcesTest
{
    /// <summary>A minimal RoaringLookupResourcesService that answers only what this example asks of it.</summary>
    private sealed class StandInService : RoaringLookupResourcesService.RoaringLookupResourcesServiceBase
    {
        public override Task<ExperimentalRoaringLookupResourcesResponse> ExperimentalRoaringLookupResources(
            ExperimentalRoaringLookupResourcesRequest request, ServerCallContext context)
        {
            // Mirrors the real service's FAILED_PRECONDITION for a resource type whose
            // relationships carry a non-canonical object ID (see the RPC's doc comment
            // in RoaringlookupresourcesGrpc.cs) -- triggered here by a sentinel
            // permission name, since this stand-in has no relationships to violate the
            // rule for real.
            if (request.Permission == "non-canonical-ids")
            {
                throw new RpcException(new Status(
                    StatusCode.FailedPrecondition,
                    "resource object id \"007\" is not a canonical 44-bit integer"));
            }

            return Task.FromResult(new ExperimentalRoaringLookupResourcesResponse
            {
                // Not a real roaring64 payload -- this stand-in only proves the bytes
                // round-trip through Bitmap unchanged, which is all the client does
                // with them (it does not decode roaring itself).
                Bitmap = ByteString.CopyFrom([1, 2, 3, 4]),
                Cardinality = 2,
                AtRevision = new ZedToken { Token = "rev-1" },
            });
        }
    }

    private static async Task<WebApplication> StartAsync()
    {
        AppContext.SetSwitch("System.Net.Http.SocketsHttpHandler.Http2UnencryptedSupport", true);
        var builder = WebApplication.CreateBuilder();
        builder.Logging.ClearProviders();
        builder.WebHost.ConfigureKestrel(options =>
            options.Listen(IPAddress.Loopback, 0, o => o.Protocols = HttpProtocols.Http2));
        builder.Services.AddGrpc();
        var app = builder.Build();
        app.MapGrpcService<StandInService>();
        await app.StartAsync();
        return app;
    }

    private static string AddressOf(WebApplication app) =>
        app.Services.GetRequiredService<IServer>()
            .Features.Get<IServerAddressesFeature>()!.Addresses.First();

    [Fact]
    public async Task ReturnsBitmapCardinalityAndRevision()
    {
        var app = await StartAsync();
        try
        {
            using var channel = GrpcChannel.ForAddress(AddressOf(app));
            await using var client = SpiceDBClient.CreateFromChannel(channel, "some-token");

            var result = await client.ExperimentalRoaringLookupResourcesAsync(
                Full(), "document", "view", "user", "alice");

            Assert.Equal(new byte[] { 1, 2, 3, 4 }, result.Bitmap);
            Assert.Equal(2ul, result.Cardinality);
            Assert.Equal("rev-1", result.AtRevision);
        }
        finally
        {
            await app.StopAsync();
        }
    }

    [Fact]
    public async Task NonCanonicalResourceId_FailsWithFailedPrecondition()
    {
        var app = await StartAsync();
        try
        {
            using var channel = GrpcChannel.ForAddress(AddressOf(app));
            await using var client = SpiceDBClient.CreateFromChannel(channel, "some-token");

            var ex = await Assert.ThrowsAsync<FailedPreconditionException>(() =>
                client.ExperimentalRoaringLookupResourcesAsync(
                    Full(), "document", "non-canonical-ids", "user", "alice"));

            Assert.NotNull(ex.InnerException);
        }
        finally
        {
            await app.StopAsync();
        }
    }
}
