// NOTE: This test requires buf-generated code in gen/ to compile.
// Run `buf generate` before running tests with `dotnet test`.

using Xunit;
using Authzed.Api.SpiceDB.Proto;

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
}
