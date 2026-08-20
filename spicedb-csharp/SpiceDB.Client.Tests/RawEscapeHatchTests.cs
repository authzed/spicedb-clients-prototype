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
// SpiceDBClient.RawProto() -- the escape hatch.
//
// It exists so a request the idiomatic surface cannot express has a
// workaround short of forking the client. Asserting the accessor returns
// something non-null would prove none of that. What matters is whether a
// caller can drive a generated service client through it and get an answer
// out of a real server, with this client's bearer token attached -- so these
// tests run a REAL Kestrel-hosted gRPC server and assert the `authorization`
// header the handler actually received.
//
// The RPC driven here is CheckPermission, the single-check call the idiomatic
// client never makes (CheckPermissionAsync routes every check through
// CheckBulkPermissions), so the gap is genuine rather than contrived.
// ─────────────────────────────────────────────────────────────────────────

public class RawEscapeHatchTests
{
    private const string Token = "test-token";

    private sealed class RecordingPermissionsService : PermissionsService.PermissionsServiceBase
    {
        public List<string> Authorizations { get; } = new();

        public override Task<CheckPermissionResponse> CheckPermission(
            CheckPermissionRequest request, ServerCallContext context)
        {
            Authorizations.Add(context.RequestHeaders.GetValue("authorization") ?? "");
            return Task.FromResult(new CheckPermissionResponse
            {
                Permissionship = CheckPermissionResponse.Types.Permissionship.HasPermission,
                CheckedAt = new ZedToken { Token = "rev-raw" },
            });
        }

        public override Task<CheckBulkPermissionsResponse> CheckBulkPermissions(
            CheckBulkPermissionsRequest request, ServerCallContext context)
        {
            Authorizations.Add(context.RequestHeaders.GetValue("authorization") ?? "");
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

    private static async Task<(WebApplication App, string Authority, RecordingPermissionsService Service)>
        StartServerAsync()
    {
        AppContext.SetSwitch("System.Net.Http.SocketsHttpHandler.Http2UnencryptedSupport", true);

        var service = new RecordingPermissionsService();
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
        // Registered as a singleton so the test holds the SAME instance the
        // server dispatches to, and can read back what actually arrived.
        builder.Services.AddSingleton(service);

        var app = builder.Build();
        app.MapGrpcService<RecordingPermissionsService>();
        await app.StartAsync();

        var address = app.Services.GetRequiredService<IServer>()
            .Features.Get<IServerAddressesFeature>()!
            .Addresses.First();

        return (app, new Uri(address).Authority, service);
    }

    private static CheckPermissionRequest Request() => new()
    {
        Consistency = new Authzed.Api.V1.Consistency { FullyConsistent = true },
        Resource = new ObjectReference { ObjectType = "document", ObjectId = "readme" },
        Permission = "view",
        Subject = new SubjectReference
        {
            Object = new ObjectReference { ObjectType = "user", ObjectId = "jimmy" },
        },
    };

    [Fact]
    public async Task RawProto_DrivesARealServiceClientAgainstARealServer()
    {
        var (app, authority, service) = await StartServerAsync();
        try
        {
            await using var client = SpiceDBClient.CreatePlaintext(authority, Token);

            var response = await client.RawProto().Permissions.CheckPermissionAsync(Request());

            response.Permissionship.Should()
                .Be(CheckPermissionResponse.Types.Permissionship.HasPermission);
            response.CheckedAt.Token.Should().Be("rev-raw");
            // The bearer token rides the client's own call invoker, so a raw
            // call is authenticated exactly as an idiomatic one is.
            service.Authorizations.Should().Equal($"Bearer {Token}");
        }
        finally
        {
            await app.DisposeAsync();
        }
    }

    [Fact]
    public async Task RawProto_SharesTheConnectionTheIdiomaticMethodsUse()
    {
        var (app, authority, service) = await StartServerAsync();
        try
        {
            await using var client = SpiceDBClient.CreatePlaintext(authority, Token);

            // Not a second connection built behind the caller's back.
            client.RawProto().Should().BeSameAs(client.RawProto());

            var idiomatic = await client.CheckPermissionAsync(
                SpiceDB.Client.Consistency.Full(), "view",
                Relationship.FromTriple("document", "readme", "view", "user", "jimmy"));
            idiomatic.HasPermission.Should().BeTrue();

            await client.RawProto().Permissions.CheckPermissionAsync(Request());

            // One idiomatic call (via CheckBulkPermissions) and one raw call
            // (via the single-check RPC), both authenticated, both on this
            // client's own connection.
            service.Authorizations.Should().Equal($"Bearer {Token}", $"Bearer {Token}");
        }
        finally
        {
            await app.DisposeAsync();
        }
    }

    [Fact]
    public async Task RawProto_ReachesACallerSuppliedChannelToo()
    {
        var (app, authority, service) = await StartServerAsync();
        var channel = GrpcChannel.ForAddress($"http://{authority}");
        try
        {
            await using var client = SpiceDBClient.CreateFromChannel(channel, Token);

            var response = await client.RawProto().Permissions.CheckPermissionAsync(Request());

            response.Permissionship.Should()
                .Be(CheckPermissionResponse.Types.Permissionship.HasPermission);
            service.Authorizations.Should().Equal($"Bearer {Token}");
        }
        finally
        {
            channel.Dispose();
            await app.DisposeAsync();
        }
    }

    /// <summary>
    /// The hatch must never grow into a way to build a connection. Root DESIGN.md,
    /// "RULE: Credentials over insecure transport require an explicit opt-in", is
    /// enforced in SpiceDBProtoClient's endpoint constructor, on the single path that
    /// builds a channel. Handing back an already-built client cannot bypass that;
    /// accepting an endpoint, key, or transport setting would.
    /// </summary>
    [Fact]
    public void RawProto_IsAnAccessorNotASecondConstructionPath()
    {
        typeof(SpiceDBClient).GetMethod(nameof(SpiceDBClient.RawProto))!
            .GetParameters().Should().BeEmpty();

        // And the guard still refuses what it always did.
        var act = () => SpiceDBClient.CreatePlaintext("evil.example.com:50051", Token);
        act.Should().Throw<InvalidArgumentException>()
            .WithMessage("*allowInsecureRemoteCredentials*");
    }
}
