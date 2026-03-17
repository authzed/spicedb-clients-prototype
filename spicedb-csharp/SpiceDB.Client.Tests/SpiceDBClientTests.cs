using FluentAssertions;
using SpiceDB.Client;
using Xunit;

namespace SpiceDB.Client.Tests;

public class SpiceDBClientTests
{
    [Fact]
    public void CreatePlaintext_ThrowsOnEmptyEndpoint()
    {
        var act = () => SpiceDBClient.CreatePlaintext("", "token");
        act.Should().Throw<ArgumentException>().Which.ParamName.Should().Be("endpoint");
    }

    [Fact]
    public void CreatePlaintext_ThrowsOnEmptyKey()
    {
        var act = () => SpiceDBClient.CreatePlaintext("localhost:50051", "");
        act.Should().Throw<ArgumentException>().Which.ParamName.Should().Be("presharedKey");
    }

    [Fact]
    public void CreatePlaintext_ThrowsOnNullEndpoint()
    {
        var act = () => SpiceDBClient.CreatePlaintext(null!, "token");
        act.Should().Throw<ArgumentException>();
    }

    [Fact]
    public void CreatePlaintext_ThrowsOnNullKey()
    {
        var act = () => SpiceDBClient.CreatePlaintext("localhost:50051", null!);
        act.Should().Throw<ArgumentException>();
    }

    [Fact]
    public void CreateSystemTls_ThrowsOnEmptyEndpoint()
    {
        var act = () => SpiceDBClient.CreateSystemTls("", "token");
        act.Should().Throw<ArgumentException>().Which.ParamName.Should().Be("endpoint");
    }

    [Fact]
    public void CreateSystemTls_ThrowsOnEmptyKey()
    {
        var act = () => SpiceDBClient.CreateSystemTls("localhost:443", "");
        act.Should().Throw<ArgumentException>().Which.ParamName.Should().Be("presharedKey");
    }

    [Fact]
    public void CreatePlaintext_ReturnsClient()
    {
        var client = SpiceDBClient.CreatePlaintext("localhost:50051", "testtoken");
        client.Should().NotBeNull();
    }

    [Fact]
    public void CreateSystemTls_ReturnsClient()
    {
        var client = SpiceDBClient.CreateSystemTls("grpc.example.com:443", "testtoken");
        client.Should().NotBeNull();
    }

    [Fact]
    public async Task Client_IsAsyncDisposable()
    {
        await using var client = SpiceDBClient.CreatePlaintext("localhost:50051", "testtoken");
        client.Should().NotBeNull();
        // Disposing should not throw
    }

    [Fact]
    public void CreateFromChannel_ThrowsOnNullChannel()
    {
        var act = () => SpiceDBClient.CreateFromChannel(null!, "token");
        act.Should().Throw<ArgumentNullException>();
    }

    [Fact]
    public void CreateFromChannel_ThrowsOnEmptyKey()
    {
        using var channel = Grpc.Net.Client.GrpcChannel.ForAddress("http://localhost:50051");
        var act = () => SpiceDBClient.CreateFromChannel(channel, "");
        act.Should().Throw<ArgumentException>();
    }

    [Fact]
    public async Task CheckPermissionAsync_ThrowsOnNullConsistency()
    {
        using var client = SpiceDBClient.CreatePlaintext("localhost:50051", "testtoken");
        var rel = Relationship.FromTriple("document", "doc1", "viewer", "user", "alice");

        var act = async () => await client.CheckPermissionAsync(null!, "view", rel);
        await act.Should().ThrowAsync<ArgumentNullException>();
    }

    [Fact]
    public async Task CheckPermissionsAsync_ThrowsOnEmptyPermission()
    {
        using var client = SpiceDBClient.CreatePlaintext("localhost:50051", "testtoken");

        var act = async () => await client.CheckPermissionsAsync(
            Consistency.Full(), "", default,
            Relationship.FromTriple("document", "doc1", "viewer", "user", "alice"));
        await act.Should().ThrowAsync<ArgumentException>();
    }

    [Fact]
    public async Task CheckPermissionsAsync_EmptyRelationships_ReturnsEmpty()
    {
        using var client = SpiceDBClient.CreatePlaintext("localhost:50051", "testtoken");

        var results = await client.CheckPermissionsAsync(Consistency.Full(), "view", default);
        results.Should().BeEmpty();
    }

    [Fact]
    public async Task WriteAsync_ThrowsOnNullTransaction()
    {
        using var client = SpiceDBClient.CreatePlaintext("localhost:50051", "testtoken");

        var act = async () => await client.WriteAsync(null!);
        await act.Should().ThrowAsync<ArgumentNullException>();
    }

    [Fact]
    public async Task WriteSchemaAsync_ThrowsOnEmptySchema()
    {
        using var client = SpiceDBClient.CreatePlaintext("localhost:50051", "testtoken");

        var act = async () => await client.WriteSchemaAsync("");
        await act.Should().ThrowAsync<ArgumentException>();
    }

    [Fact]
    public async Task ExperimentalRegisterRelationshipCounterAsync_ThrowsOnEmptyName()
    {
        using var client = SpiceDBClient.CreatePlaintext("localhost:50051", "testtoken");

        var act = async () => await client.ExperimentalRegisterRelationshipCounterAsync("", new Filter("doc"));
        await act.Should().ThrowAsync<ArgumentException>();
    }

    [Fact]
    public async Task ExperimentalCountRelationshipsAsync_ThrowsOnEmptyName()
    {
        using var client = SpiceDBClient.CreatePlaintext("localhost:50051", "testtoken");

        var act = async () => await client.ExperimentalCountRelationshipsAsync("");
        await act.Should().ThrowAsync<ArgumentException>();
    }

    [Fact]
    public async Task ExperimentalUnregisterRelationshipCounterAsync_ThrowsOnEmptyName()
    {
        using var client = SpiceDBClient.CreatePlaintext("localhost:50051", "testtoken");

        var act = async () => await client.ExperimentalUnregisterRelationshipCounterAsync("");
        await act.Should().ThrowAsync<ArgumentException>();
    }
}
