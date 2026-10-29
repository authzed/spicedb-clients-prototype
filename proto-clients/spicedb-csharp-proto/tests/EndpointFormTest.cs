using Xunit;

namespace Authzed.Api.SpiceDB.Proto.Tests;

/// <summary>
/// Endpoint forms: <c>host:port</c>, <c>http://host:port</c> and <c>https://host[:port]</c>
/// build a client that dials the named host; every other form fails at construction
/// with an <see cref="ArgumentException"/> naming the problem and the accepted forms, and
/// never reaches the transport.
/// </summary>
public class EndpointFormTest
{
    [Theory]
    [InlineData("127.0.0.1:50101", true, "http://127.0.0.1:50101/")]
    [InlineData("http://127.0.0.1:50101", true, "http://127.0.0.1:50101/")]
    [InlineData("HTTP://127.0.0.1:50101/", true, "http://127.0.0.1:50101/")]
    [InlineData("http://[::1]:50101", true, "http://[::1]:50101/")]
    [InlineData("https://example.com", false, "https://example.com/")]
    [InlineData("https://example.com:8443", false, "https://example.com:8443/")]
    [InlineData("example.com:443", false, "https://example.com/")]
    // A TLS endpoint without a port uses 443.
    [InlineData("example.com", false, "https://example.com/")]
    [InlineData("::1", false, "https://[::1]/")]
    public async Task TestAcceptedFormsDialTheParsedAuthority(string endpoint, bool insecure, string expectedBase)
    {
        var handler = new AuthCapturingHandler();
        using var client = new SpiceDBProtoClient(endpoint, "t", insecure, allowInsecureRemoteCredentials: false, handler);

        await InsecureHostGuardTest.AttemptCall(client);

        Assert.NotNull(handler.CapturedUri);
        Assert.Equal(expectedBase, handler.CapturedUri!.GetLeftPart(UriPartial.Authority) + "/");
        Assert.Equal("Bearer t", handler.CapturedAuth);
    }

    [Theory]
    // https:// without a port uses 443, which Uri reports as the default port.
    [InlineData("https://example.com", 443)]
    [InlineData("https://example.com/", 443)]
    public async Task TestHttpsWithoutPortUses443(string endpoint, int port)
    {
        var handler = new AuthCapturingHandler();
        using var client = new SpiceDBProtoClient(endpoint, "t", insecure: false, allowInsecureRemoteCredentials: false, handler);
        await InsecureHostGuardTest.AttemptCall(client);
        Assert.Equal(port, handler.CapturedUri!.Port);
    }

    [Theory]
    [InlineData("http://localhost", true, "a plaintext endpoint needs an explicit port, e.g. localhost:50051")]
    [InlineData("localhost", true, "a plaintext endpoint needs an explicit port, e.g. localhost:50051")]
    [InlineData("127.0.0.1", true, "e.g. 127.0.0.1:50051")]
    [InlineData("::1", true, "e.g. [::1]:50051")]
    [InlineData("[::1]", true, "e.g. [::1]:50051")]
    [InlineData("0:0:0:0:0:0:0:1", true, "e.g. [0:0:0:0:0:0:0:1]:50051")]
    [InlineData("http://127.0.0.1", true, "needs an explicit port")]
    [InlineData("https://example.com", true, "selects TLS")]
    [InlineData("http://127.0.0.1:50101", false, "selects plaintext")]
    [InlineData("http://127.0.0.1:50101/x", true, "a path is not allowed")]
    [InlineData("127.0.0.1:50101/x", true, "a path is not allowed")]
    [InlineData("http://127.0.0.1:50101?a=b", true, "a query is not allowed")]
    [InlineData("http://127.0.0.1:50101#f", true, "a fragment is not allowed")]
    [InlineData("http://u@127.0.0.1:50101", true, "userinfo")]
    [InlineData("http://127.0.0.1:abc", true, "invalid port \"abc\"")]
    [InlineData("localhost:abc", true, "invalid port \"abc\"")]
    [InlineData("localhost:70000", true, "invalid port \"70000\"")]
    [InlineData("localhost:", true, "invalid port \"\"")]
    [InlineData("http://", true, "missing host")]
    [InlineData("http://:50101", true, "missing host")]
    [InlineData("ftp://host:1", true, "unsupported scheme \"ftp\"")]
    [InlineData("ftp://host:1", false, "unsupported scheme \"ftp\"")]
    [InlineData("grpc://host:1", false, "unsupported scheme \"grpc\"")]
    public void TestMalformedFormsFailAtConstruction(string endpoint, bool insecure, string reason)
    {
        var handler = new AuthCapturingHandler();

        var ex = Assert.Throws<ArgumentException>(() =>
            new SpiceDBProtoClient(endpoint, "t", insecure, allowInsecureRemoteCredentials: false, handler));

        Assert.Contains($"invalid endpoint \"{endpoint}\"", ex.Message);
        Assert.Contains(reason, ex.Message);
        Assert.Contains(SpiceDBProtoClient.EndpointForms, ex.Message);
        Assert.Equal(0, handler.InvocationCount);
    }

    /// <summary>
    /// A malformed remote endpoint names its form problem, not the remote-plaintext
    /// policy: the policy message is for well-formed remote hosts only.
    /// </summary>
    [Theory]
    [InlineData("http://example.com:50051/x", "a path is not allowed")]
    [InlineData("http://u@example.com:50051", "userinfo")]
    [InlineData("http://example.com", "needs an explicit port")]
    [InlineData("example.com:port", "invalid port")]
    public void TestFormErrorsComeBeforeRemotePlaintextPolicy(string endpoint, string reason)
    {
        var ex = Assert.Throws<ArgumentException>(() =>
            new SpiceDBProtoClient(endpoint, "t", insecure: true, allowInsecureRemoteCredentials: false));
        Assert.Contains(reason, ex.Message);
        Assert.DoesNotContain("allowInsecureRemoteCredentials", ex.Message);
    }

    /// <summary>The loopback check runs on the parsed host, so a URL's scheme does not hide it.</summary>
    [Fact]
    public void TestRemotePlaintextPolicyJudgesTheParsedHost()
    {
        var handler = new AuthCapturingHandler();
        var ex = Assert.Throws<InsecureRemoteHostException>(() =>
            new SpiceDBProtoClient("http://example.com:50051", "t", insecure: true, allowInsecureRemoteCredentials: false, handler));
        Assert.Contains("http://example.com:50051", ex.Message);
        Assert.Contains("allowInsecureRemoteCredentials", ex.Message);
        Assert.Equal(0, handler.InvocationCount);

        using var allowed = new SpiceDBProtoClient("http://example.com:50051", "t", insecure: true, allowInsecureRemoteCredentials: true, handler);
        using var loopback = new SpiceDBProtoClient("http://localhost:50101", "t", insecure: true, allowInsecureRemoteCredentials: false, handler);
    }
}
