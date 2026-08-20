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

namespace ErrorMapping;

/// <summary>
/// Example ErrorMapping demonstrates the two error codes a caller actually recovers
/// from -- see root DESIGN.md, "RULE: Error mapping must not lose the server's detail".
/// </summary>
/// <remarks>
/// <para>
/// The rule names both consequences, and this example is those two recoveries written
/// out as running code. <c>OUT_OF_RANGE</c> is SpiceDB's signal that a ZedToken has
/// expired or been garbage-collected, and recovery is mechanical: discard the stale
/// token and re-read at full consistency. <c>UNAUTHENTICATED</c> is the most common
/// error a new integration produces, and distinguishing it is what lets a caller write
/// "refresh credentials on auth failure, page someone on internal error".
/// </para>
/// <para>
/// <b>Why this example stands up its own server.</b> Neither code is reachable from the
/// SpiceDB the integration job starts, which was verified rather than assumed: a garbage
/// ZedToken returns <c>INVALID_ARGUMENT</c>, and the in-memory datastore never collects
/// the revision (with a 5s GC window and 35s elapsed, a snapshot read at the old token
/// still succeeded). And a wrong preshared key comes back <c>PERMISSION_DENIED</c>, not
/// <c>UNAUTHENTICATED</c> -- which the last test asserts against the real server, so a
/// reader does not write a credential-refresh branch that can never run.
/// </para>
/// </remarks>
public class ErrorMappingTest
{
    private const string StaleToken = "stale-zedtoken";

    private static Relationship Doc() =>
        Relationship.FromTriple("document", "readme", "view", "user", "alice");

    /// <summary>A minimal SpiceDB that answers only what this example asks of it.</summary>
    private sealed class StandInService : PermissionsService.PermissionsServiceBase
    {
        public override Task<CheckBulkPermissionsResponse> CheckBulkPermissions(
            CheckBulkPermissionsRequest request, ServerCallContext context)
        {
            // A read pinned to a token the server no longer has.
            if (request.Consistency?.AtLeastAsFresh?.Token == StaleToken)
            {
                throw new RpcException(new Status(
                    StatusCode.OutOfRange,
                    "the specified revision has expired or been garbage collected"));
            }

            // Anything else: re-reading at full consistency succeeds. That is the whole
            // point of the recovery -- dropping the stale token is sufficient.
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
    }

    private sealed class RotatedTokenService : PermissionsService.PermissionsServiceBase
    {
        public override Task<CheckBulkPermissionsResponse> CheckBulkPermissions(
            CheckBulkPermissionsRequest request, ServerCallContext context) =>
            throw new RpcException(new Status(StatusCode.Unauthenticated, "invalid token"));
    }

    private static async Task<WebApplication> StartAsync<TService>() where TService : class
    {
        AppContext.SetSwitch("System.Net.Http.SocketsHttpHandler.Http2UnencryptedSupport", true);
        var builder = WebApplication.CreateBuilder();
        builder.Logging.ClearProviders();
        builder.WebHost.ConfigureKestrel(options =>
            options.Listen(IPAddress.Loopback, 0, o => o.Protocols = HttpProtocols.Http2));
        builder.Services.AddGrpc();
        var app = builder.Build();
        app.MapGrpcService<TService>();
        await app.StartAsync();
        return app;
    }

    private static string AddressOf(WebApplication app) =>
        app.Services.GetRequiredService<IServer>()
            .Features.Get<IServerAddressesFeature>()!.Addresses.First();

    [Fact]
    public async Task StaleZedToken_IsRecoverableWithoutParsingAMessage()
    {
        var app = await StartAsync<StandInService>();
        try
        {
            using var channel = GrpcChannel.ForAddress(AddressOf(app));
            await using var client = SpiceDBClient.CreateFromChannel(channel, "some-token");

            var ex = await Assert.ThrowsAsync<OutOfRangeException>(
                () => client.CheckPermissionAsync(AtLeast(StaleToken), "view", Doc()));

            // Clause 2: the underlying status survives the mapping, so google.rpc.Status
            // details and SpiceDB's ErrorReason stay reachable rather than being reduced
            // to a code and a rebuilt string.
            Assert.NotNull(ex.InnerException);

            // The recovery the rule calls mechanical, in full. Nothing parses a message.
            var result = await client.CheckPermissionAsync(Full(), "view", Doc());
            Assert.True(result.HasPermission);
        }
        finally
        {
            await app.StopAsync();
        }
    }

    [Fact]
    public async Task RotatedToken_IsDistinctFromATransportFault()
    {
        var app = await StartAsync<RotatedTokenService>();
        try
        {
            using var channel = GrpcChannel.ForAddress(AddressOf(app));
            await using var client = SpiceDBClient.CreateFromChannel(channel, "rotated-token");

            var ex = await Assert.ThrowsAsync<UnauthenticatedException>(
                () => client.CheckPermissionAsync(Full(), "view", Doc()));

            // Asserting the negative is the half that would silently rot if every code
            // collapsed into one class.
            Assert.IsNotType<UnavailableException>(ex);
        }
        finally
        {
            await app.StopAsync();
        }
    }

    [Fact]
    public async Task RealSpiceDB_RejectsABadPresharedKeyWithPermissionDenied()
    {
        // PERMISSION_DENIED, not UNAUTHENTICATED. Recorded here because it is the case a
        // reader will actually hit first, and assuming otherwise is how a
        // credential-refresh branch ends up unreachable in production code.
        await using var client = SpiceDBClient.CreatePlaintext(
            SpiceDBTestServer.Endpoint, "definitely-the-wrong-key");

        await Assert.ThrowsAsync<PermissionDeniedException>(() => client.ReadSchemaAsync());
    }
}
