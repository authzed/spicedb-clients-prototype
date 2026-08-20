// NOTE: This test requires buf-generated code in gen/ to compile.
// Run `buf generate` before running tests with `dotnet test`.

using Xunit;
using Authzed.Api.SpiceDB.Proto;
using Authzed.Api.V1;
using Grpc.Net.Client;

namespace Authzed.Api.SpiceDB.Proto.Tests;

public class ClientTest
{
    [Fact]
    public void Constructor_PopulatesAllServiceClients()
    {
        using var client = new SpiceDBProtoClient("localhost:50051", "test-token", insecure: true);

        Assert.NotNull(client.Permissions);
        Assert.NotNull(client.Schema);
        Assert.NotNull(client.Watch);
        Assert.NotNull(client.Experimental);
    }

    [Fact]
    public void Constructor_SecureChannel_PopulatesAllServiceClients()
    {
        using var client = new SpiceDBProtoClient("grpc.authzed.com:443", "test-token");

        Assert.NotNull(client.Permissions);
        Assert.NotNull(client.Schema);
        Assert.NotNull(client.Watch);
        Assert.NotNull(client.Experimental);
    }

    [Fact]
    public void Dispose_DoesNotThrow()
    {
        var client = new SpiceDBProtoClient("localhost:50051", "test-token", insecure: true);
        client.Dispose();
    }

    /// <summary>
    /// A channel handed to the GrpcChannel overload belongs to the caller -- typically a
    /// DI-registered singleton shared by the whole application -- so disposing this client
    /// must leave it open. Proven by USING the channel afterwards: building a service client
    /// on it calls GrpcChannel.CreateCallInvoker(), which throws ObjectDisposedException on a
    /// disposed channel (and did, before this client tracked ownership).
    /// </summary>
    [Fact]
    public void Dispose_LeavesACallerSuppliedChannelUsable()
    {
        // Deliberately not `using`: the caller owns it, and the point is what survives.
        var channel = GrpcChannel.ForAddress("http://localhost:50051");
        try
        {
            var client = new SpiceDBProtoClient(channel, "test-token");
            client.Dispose();

            var stub = new PermissionsService.PermissionsServiceClient(channel);
            Assert.NotNull(stub);

            // A second client on the same channel must still be constructible.
            using var second = new SpiceDBProtoClient(channel, "test-token");
            Assert.NotNull(second.Permissions);
        }
        finally
        {
            channel.Dispose();
        }
    }

    /// <summary>
    /// The mirror of the test above: the endpoint-string constructor DID create the channel,
    /// so it must still dispose it. Without this, "don't dispose a borrowed channel" could be
    /// satisfied by never disposing anything. Observed through a call, which fails with
    /// ObjectDisposedException once the channel underneath the stub is gone.
    /// </summary>
    [Fact]
    public async Task Dispose_StillDisposesAChannelThisClientCreated()
    {
        var client = new SpiceDBProtoClient("localhost:50051", "test-token", insecure: true);
        client.Dispose();

        await Assert.ThrowsAsync<ObjectDisposedException>(
            async () => await client.Schema.ReadSchemaAsync(new Authzed.Api.V1.ReadSchemaRequest()));
    }
}
