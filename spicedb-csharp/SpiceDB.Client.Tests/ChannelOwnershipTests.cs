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
// Channel ownership across DisposeAsync.
//
// SpiceDBClient.CreateFromChannel is the documented escape hatch for a
// caller-built GrpcChannel, and the idiomatic .NET way to supply one is a
// DI-registered SINGLETON channel shared by the whole application. Disposing
// the client used to dispose that channel regardless of where it came from
// (SpiceDBClient.DisposeAsync -> SpiceDBProtoClient.Dispose ->
// _channel.Dispose()), so the first scoped consumer to finish tore down a
// connection every other consumer was still using.
//
// These tests run a REAL Kestrel-hosted gRPC server and prove ownership by
// USING the channel after the client that borrowed it was disposed -- not by
// reading a flag. The mirror test proves the fix did not become "never
// dispose anything": a client that built its own channel must still tear it
// down, which is likewise observed through a call, not a field.
// ─────────────────────────────────────────────────────────────────────────

public class ChannelOwnershipTests
{
    private const string SchemaText = "definition user {}";

    private sealed class StaticSchemaService : SchemaService.SchemaServiceBase
    {
        public override Task<ReadSchemaResponse> ReadSchema(
            ReadSchemaRequest request, ServerCallContext context) =>
            Task.FromResult(new ReadSchemaResponse { SchemaText = SchemaText });
    }

    /// <summary>
    /// Starts a real Kestrel-hosted gRPC server (plaintext HTTP/2) serving
    /// <see cref="StaticSchemaService"/>, and returns the app plus the
    /// "host:port" authority it is listening on.
    /// </summary>
    private static async Task<(WebApplication App, string Address)> StartServerAsync()
    {
        // Grpc.Net.Client requires this switch to allow cleartext (h2c) HTTP/2 -- the
        // server below has no TLS, matching how this client's "insecure" constructors work.
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
        app.MapGrpcService<StaticSchemaService>();
        await app.StartAsync();

        var address = app.Services.GetRequiredService<IServer>()
            .Features.Get<IServerAddressesFeature>()!
            .Addresses.First();

        return (app, address);
    }

    /// <summary>
    /// A caller-supplied channel belongs to the caller. Disposing the client
    /// that borrowed it must leave it connected and usable -- proven by making
    /// a real RPC over that same channel after the client is gone.
    /// </summary>
    [Fact]
    public async Task DisposingClientBuiltFromCallerChannel_LeavesChannelUsable()
    {
        var (app, address) = await StartServerAsync();
        // Deliberately NOT `using`: the caller owns this channel, and the point of
        // the test is what survives the client's disposal.
        var channel = GrpcChannel.ForAddress(address);
        try
        {
            var client = SpiceDBClient.CreateFromChannel(channel, "test-token");
            var before = await client.ReadSchemaAsync();
            before.Schema.Should().Be(SchemaText);

            await client.DisposeAsync();

            // The channel is the caller's: it must still carry a real call.
            var schemaStub = new SchemaService.SchemaServiceClient(channel);
            var after = await schemaStub.ReadSchemaAsync(new ReadSchemaRequest());
            after.SchemaText.Should().Be(SchemaText);

            // And a second client built on the same channel must work too --
            // the DI-singleton case this bug actually broke.
            await using var second = SpiceDBClient.CreateFromChannel(channel, "test-token");
            (await second.ReadSchemaAsync()).Schema.Should().Be(SchemaText);
        }
        finally
        {
            channel.Dispose();
            await app.DisposeAsync();
        }
    }

    /// <summary>
    /// The mirror of the test above, and the reason the fix cannot be "never
    /// dispose anything": a client that BUILT its own channel must still
    /// dispose it. Observed through a call on the disposed client rather than
    /// through a private field.
    /// </summary>
    [Fact]
    public async Task DisposingClientThatOwnsItsChannel_StillDisposesIt()
    {
        var (app, address) = await StartServerAsync();
        try
        {
            var authority = new Uri(address).Authority;
            var client = SpiceDBClient.CreatePlaintext(authority, "test-token");
            (await client.ReadSchemaAsync()).Schema.Should().Be(SchemaText);

            await client.DisposeAsync();

            var act = async () => await client.ReadSchemaAsync();
            await act.Should().ThrowAsync<ObjectDisposedException>();
        }
        finally
        {
            await app.DisposeAsync();
        }
    }
}
