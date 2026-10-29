using System.Net;
using System.Net.Sockets;
using FluentAssertions;
using Grpc.Core;
using SpiceDB.Client;
using Xunit;

namespace SpiceDB.Client.Tests;

/// <summary>
/// Endpoint forms accepted by <see cref="SpiceDBClient.CreatePlaintext"/> and
/// <see cref="SpiceDBClient.CreateSystemTls"/>: <c>host:port</c>, <c>http://host:port</c>
/// (plaintext) and <c>https://host[:port]</c> (TLS). Every other form, or a scheme that
/// disagrees with the factory, fails at construction with
/// <see cref="InvalidArgumentException"/>; none builds a client whose every call fails.
/// </summary>
public class EndpointFormTests
{
    private const string Forms = "expected host:port, http://host:port (plaintext), https://host[:port] (TLS), dns:///host:port or unix:/path/to/socket (plaintext)";

    [Theory]
    [InlineData("127.0.0.1:50101")]
    [InlineData("http://127.0.0.1:50101")]
    [InlineData("http://127.0.0.1:50101/")]
    [InlineData("http://localhost:50101")]
    public async Task CreatePlaintext_AcceptsHostPortAndHttpUrl(string endpoint)
    {
        await using var client = SpiceDBClient.CreatePlaintext(endpoint, "k");
        client.Should().NotBeNull();
    }

    /// <summary>gRPC target syntax the transport supports: dns:/// (loopback check on its host) and plaintext unix sockets. examples/EndpointForms makes a real call over each.</summary>
    [Theory]
    [InlineData("dns:///localhost:50101")]
    [InlineData("unix:/tmp/spicedb.sock")]
    [InlineData("unix:///tmp/spicedb.sock")]
    public async Task CreatePlaintext_AcceptsDnsAndUnixTargets(string endpoint)
    {
        await using var client = SpiceDBClient.CreatePlaintext(endpoint, "k");
        client.Should().NotBeNull();
    }

    [Theory]
    [InlineData("xds:///spicedb", true, "unsupported scheme \"xds\"")]
    [InlineData("ipv4:127.0.0.1:50051", true, "unsupported scheme \"ipv4\"")]
    [InlineData("dns://8.8.8.8/localhost:50051", true, "a DNS server authority is not supported")]
    [InlineData("unix:/tmp/spicedb.sock", false, "a unix socket is plaintext")]
    public void UnsupportedTargetSchemesFailAtConstruction(string endpoint, bool plaintext, string reason)
    {
        var ex = FluentActions.Invoking(() => plaintext ? SpiceDBClient.CreatePlaintext(endpoint, "k") : SpiceDBClient.CreateSystemTls(endpoint, "k"))
            .Should().Throw<InvalidArgumentException>().Which;
        ex.Message.Should().Contain(reason).And.Contain(Forms);
    }

    [Fact]
    public void CreatePlaintext_RemoteDnsTargetNeedsOptIn()
    {
        FluentActions.Invoking(() => SpiceDBClient.CreatePlaintext("dns:///example.com:50051", "k"))
            .Should().Throw<InvalidArgumentException>().Which.Message.Should().Contain("RemotePlaintextCredentials");
    }

    [Theory]
    [InlineData("example.com:443")]
    [InlineData("example.com")]
    [InlineData("https://example.com")]
    [InlineData("https://example.com:8443")]
    public async Task CreateSystemTls_AcceptsHostPortAndHttpsUrl(string endpoint)
    {
        await using var client = SpiceDBClient.CreateSystemTls(endpoint, "k");
        client.Should().NotBeNull();
    }

    [Theory]
    [InlineData("http://localhost", "a plaintext endpoint needs an explicit port, e.g. localhost:50051")]
    [InlineData("localhost", "a plaintext endpoint needs an explicit port, e.g. localhost:50051")]
    [InlineData("::1", "e.g. [::1]:50051")]
    [InlineData("https://example.com", "selects TLS")]
    [InlineData("http://127.0.0.1:50101/x", "a path is not allowed")]
    [InlineData("http://u@127.0.0.1:50101", "userinfo")]
    [InlineData("ftp://host:1", "unsupported scheme \"ftp\"")]
    [InlineData("localhost:abc", "invalid port \"abc\"")]
    // Form problems are reported before the remote-plaintext policy.
    [InlineData("http://example.com:50051/x", "a path is not allowed")]
    public void CreatePlaintext_RejectsOtherForms(string endpoint, string reason)
    {
        var ex = FluentActions.Invoking(() => SpiceDBClient.CreatePlaintext(endpoint, "k"))
            .Should().Throw<InvalidArgumentException>().Which;
        ex.Message.Should().Contain($"invalid endpoint \"{endpoint}\"").And.Contain(reason).And.Contain(Forms);
        ex.Message.Should().NotContain("RemotePlaintextCredentials");
    }

    [Theory]
    [InlineData("http://127.0.0.1:50101", "selects plaintext")]
    [InlineData("http://localhost", "selects plaintext")]
    [InlineData("http://example.com:50051", "selects plaintext")]
    [InlineData("http://127.0.0.1:50101/x", "a path is not allowed")]
    [InlineData("http://u@127.0.0.1:50101", "userinfo")]
    [InlineData("ftp://host:1", "unsupported scheme \"ftp\"")]
    public void CreateSystemTls_RejectsOtherForms(string endpoint, string reason)
    {
        var ex = FluentActions.Invoking(() => SpiceDBClient.CreateSystemTls(endpoint, "k"))
            .Should().Throw<InvalidArgumentException>().Which;
        ex.Message.Should().Contain($"invalid endpoint \"{endpoint}\"").And.Contain(reason).And.Contain(Forms);
    }

    /// <summary>The remote-plaintext policy judges the URL's parsed host.</summary>
    [Fact]
    public async Task CreatePlaintext_RemoteHttpUrlNeedsOptIn()
    {
        var ex = FluentActions.Invoking(() => SpiceDBClient.CreatePlaintext("http://example.com:50051", "k"))
            .Should().Throw<InvalidArgumentException>().Which;
        ex.Message.Should().Contain("non-loopback endpoint \"http://example.com:50051\"").And.Contain("RemotePlaintextCredentials.Allow");

        await using var allowed = SpiceDBClient.CreatePlaintext("http://example.com:50051", "k",
            new ClientOptions { RemotePlaintextCredentials = RemotePlaintextCredentials.Allow });
    }

    /// <summary>
    /// A Docker Compose service name is a remote host: plaintext to it needs both
    /// CreatePlaintext and the RemotePlaintextCredentials opt-in, and the refusal names
    /// both exactly as the caller writes them.
    /// </summary>
    [Fact]
    public async Task CreatePlaintext_ComposeServiceRefusalNamesEveryNeededOption()
    {
        var ex = FluentActions.Invoking(() => SpiceDBClient.CreatePlaintext("spicedb:50051", "k"))
            .Should().Throw<InvalidArgumentException>().Which;
        ex.Message.Should().Be(
            "spicedb: refusing to send the preshared key over plaintext to non-loopback endpoint \"spicedb:50051\". " +
            "Use SpiceDBClient.CreateSystemTls for TLS, or, to send it in cleartext to a remote host on purpose, call " +
            "SpiceDBClient.CreatePlaintext(endpoint, presharedKey, new ClientOptions { RemotePlaintextCredentials = RemotePlaintextCredentials.Allow })");

        await using var allowed = SpiceDBClient.CreatePlaintext("spicedb:50051", "k",
            new ClientOptions { RemotePlaintextCredentials = RemotePlaintextCredentials.Allow });
    }

    /// <summary>
    /// Grpc.Net.Client reports a refused connection as "Error connecting to subchannel.";
    /// the mapped error also names the endpoint and the socket error.
    /// </summary>
    [Fact]
    public async Task RefusedConnection_NamesEndpointAndRootCause()
    {
        var listener = new TcpListener(IPAddress.Loopback, 0);
        listener.Start();
        var port = ((IPEndPoint)listener.LocalEndpoint).Port;
        listener.Stop();

        var endpoint = $"127.0.0.1:{port}";
        await using var client = SpiceDBClient.CreatePlaintext(endpoint, "k", new ClientOptions { Retries = Retries.Disabled });
        var ex = await FluentActions.Awaiting(() => client.ReadSchemaAsync())
            .Should().ThrowAsync<UnavailableException>();
        ex.Which.Message.Should().Contain($"endpoint \"{endpoint}\"").And.Contain("SocketException");
    }

    [Fact]
    public void ErrorMapper_AppendsEndpointAndRootCauseOnlyForLocalTransportFailures()
    {
        var local = new RpcException(new Status(StatusCode.Unavailable, "Error connecting to subchannel.",
            new SocketException((int)SocketError.ConnectionRefused)));
        var mapped = ErrorMapper.ToSpiceDBException(local, "db:50051");
        mapped.Message.Should().StartWith("Error connecting to subchannel. (endpoint \"db:50051\"; SocketException: ");
        mapped.ServerMessage.Should().Be("Error connecting to subchannel.");

        var server = ErrorMapper.ToSpiceDBException(new RpcException(new Status(StatusCode.Unavailable, "overloaded")), "db:50051");
        server.Message.Should().Be("overloaded");
    }
}
