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
        act.Should().Throw<InvalidArgumentException>().Which.Message.Should().Contain("endpoint");
    }

    [Fact]
    public void CreatePlaintext_ThrowsOnEmptyKey()
    {
        var act = () => SpiceDBClient.CreatePlaintext("localhost:50051", "");
        act.Should().Throw<InvalidArgumentException>().Which.Message.Should().Contain("presharedKey");
    }

    [Fact]
    public void CreatePlaintext_ThrowsOnNullEndpoint()
    {
        var act = () => SpiceDBClient.CreatePlaintext(null!, "token");
        act.Should().Throw<InvalidArgumentException>();
    }

    [Fact]
    public void CreatePlaintext_ThrowsOnNullKey()
    {
        var act = () => SpiceDBClient.CreatePlaintext("localhost:50051", null!);
        act.Should().Throw<InvalidArgumentException>();
    }

    [Fact]
    public void CreateSystemTls_ThrowsOnEmptyEndpoint()
    {
        var act = () => SpiceDBClient.CreateSystemTls("", "token");
        act.Should().Throw<InvalidArgumentException>().Which.Message.Should().Contain("endpoint");
    }

    [Fact]
    public void CreateSystemTls_ThrowsOnEmptyKey()
    {
        var act = () => SpiceDBClient.CreateSystemTls("localhost:443", "");
        act.Should().Throw<InvalidArgumentException>().Which.Message.Should().Contain("presharedKey");
    }

    [Fact]
    public void CreatePlaintext_ReturnsClient()
    {
        var client = SpiceDBClient.CreatePlaintext("localhost:50051", "testtoken");
        client.Should().NotBeNull();
    }

    // Construction only, and deliberately so: grpc.example.com is a reserved,
    // non-routable name and GrpcChannel connects lazily, so nothing here exercises TLS
    // and this would pass with an empty trust store. The honest handshake test that
    // would not is TlsHandshakeTests.CreateSystemTls_CompletesRealHandshake -- see root
    // DESIGN.md, "RULE: A system-TLS constructor must reach a real server", clause 2.
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

    /// <summary>
    /// Regression test for root DESIGN.md, "RULE: Credentials over insecure
    /// transport require an explicit opt-in": CreatePlaintext against a
    /// non-loopback endpoint, with no RemotePlaintextCredentials.Allow, must
    /// refuse to construct a client at all -- before any channel, credential,
    /// or connection is created. The proto-layer test
    /// (InsecureHostGuardTest.TestRefusesInsecureNonLoopbackWithoutOptIn) is
    /// what proves the token itself is never handed to the transport; this
    /// test proves the idiomatic constructor actually reaches, and propagates,
    /// that same guard.
    /// </summary>
    [Fact]
    public void CreatePlaintext_RefusesNonLoopbackWithoutOptIn()
    {
        var act = () => SpiceDBClient.CreatePlaintext("evil.example.com:1234", "testtoken");
        // This client's own typed argument error, not the proto tier's exception -- see
        // root DESIGN.md, "RULE: Credentials over insecure transport require an explicit
        // opt-in", clause 4.
        var ex = act.Should().Throw<InvalidArgumentException>().Which;
        ex.Message.Should().Contain("evil.example.com:1234");
        ex.Message.Should().Contain("RemotePlaintextCredentials.Allow");
    }

    /// <summary>
    /// The bypass shapes, at the public entry point. The proto layer holds the full
    /// fixture set and the wire-level "token never sent" proof; these are the ones that
    /// must not slip through CreatePlaintext itself, whose two-argument overload gives
    /// a caller no way to opt in at all.
    /// </summary>
    [Theory]
    [InlineData("127.0.0.1:443@evil.com")]
    [InlineData("[::1]:443@evil.com")]
    [InlineData("[::1]:0@127.0.0.1:19999")]
    [InlineData("localhost@evil.com")]
    public void CreatePlaintext_RefusesEndpointWhoseUriAuthorityShiftsTheHost(string endpoint)
    {
        var act = () => SpiceDBClient.CreatePlaintext(endpoint, "testtoken");
        // Userinfo is a malformed endpoint, refused before the remote-plaintext policy.
        var ex = act.Should().Throw<InvalidArgumentException>().Which;
        ex.Message.Should().Contain("userinfo");
    }

    /// <summary>
    /// A unix-socket endpoint dials the socket path, never a DNS lookup for the hostname
    /// "unix": against a missing socket file the readiness check fails naming the endpoint.
    /// examples/EndpointForms makes a real call over a live socket.
    /// </summary>
    [Theory]
    [InlineData("unix:/var/run/spicedb-missing.sock")]
    [InlineData("unix:///var/run/spicedb-missing.sock")]
    public async Task CreatePlaintext_UnixSocketTargetsDialTheSocketPath(string endpoint)
    {
        await using var client = SpiceDBClient.CreatePlaintext(endpoint, "testtoken");
        var ex = (await FluentActions.Awaiting(() => client.ConnectAsync(new ConnectOptions { Timeout = TimeSpan.FromMilliseconds(500) }))
            .Should().ThrowAsync<SpiceDBException>()).Which;
        ex.StatusCode.Should().BeOneOf(Grpc.Core.StatusCode.DeadlineExceeded, Grpc.Core.StatusCode.Unavailable);
        ex.Message.Should().Contain(endpoint);
    }

    [Fact]
    public void CreatePlaintext_LoopbackWorksWithNoOptIn()
    {
        var act = () => SpiceDBClient.CreatePlaintext("localhost:50051", "testtoken");
        act.Should().NotThrow();
    }

    /// <summary>
    /// Bare <c>::1</c> is item 8 of the loopback contract, so it must build a client
    /// through the public entry point — not merely satisfy the guard and then throw
    /// <c>UriFormatException</c> from <c>GrpcChannel.ForAddress</c>, which is what
    /// happened while the guard bracketed the literal and the transport address did
    /// not.
    /// </summary>
    [Theory]
    [InlineData("[::1]:50051")]
    [InlineData("[0:0:0:0:0:0:0:1]:50051")]
    public void CreatePlaintext_BareIpv6LoopbackWorksWithNoOptIn(string endpoint)
    {
        var act = () => SpiceDBClient.CreatePlaintext(endpoint, "testtoken");
        act.Should().NotThrow();
    }

    [Fact]
    public void CreatePlaintext_AllowPolicyPermitsNonLoopback()
    {
        var act = () => SpiceDBClient.CreatePlaintext("evil.example.com:1234", "testtoken", new ClientOptions { RemotePlaintextCredentials = RemotePlaintextCredentials.Allow });
        act.Should().NotThrow();
    }

    [Fact]
    public void CreateFromChannel_ThrowsOnNullChannel()
    {
        var act = () => SpiceDBClient.CreateFromChannel(null!, "token");
        act.Should().Throw<InvalidArgumentException>();
    }

    [Fact]
    public void CreateFromChannel_ThrowsOnEmptyKey()
    {
        using var channel = Grpc.Net.Client.GrpcChannel.ForAddress("http://localhost:50051");
        var act = () => SpiceDBClient.CreateFromChannel(channel, "");
        act.Should().Throw<InvalidArgumentException>();
    }

    [Fact]
    public async Task CheckPermissionAsync_ThrowsOnNullConsistency()
    {
        await using var client = SpiceDBClient.CreatePlaintext("localhost:50051", "testtoken");
        var rel = new Relationship(new ObjectRef("document", "doc1"), "viewer", new SubjectRef(new ObjectRef("user", "alice")));

        var act = async () => await client.CheckAsync(new PermissionCheck(rel.Resource, "view", rel.Subject, null!), null, default);
        await act.Should().ThrowAsync<InvalidArgumentException>();
    }

    [Fact]
    public async Task CheckPermissionsAsync_ThrowsOnEmptyPermission()
    {
        await using var client = SpiceDBClient.CreatePlaintext("localhost:50051", "testtoken");

        var act = async () => await client.CheckBulkAsync([new PermissionCheck(new Relationship(new ObjectRef("document", "doc1"), "viewer", new SubjectRef(new ObjectRef("user", "alice"))).Resource, "", new Relationship(new ObjectRef("document", "doc1"), "viewer", new SubjectRef(new ObjectRef("user", "alice"))).Subject, Consistency.Full())], null, default);
        await act.Should().ThrowAsync<InvalidArgumentException>();
    }

    [Fact]
    public async Task CheckPermissionsAsync_EmptyRelationships_ReturnsEmpty()
    {
        await using var client = SpiceDBClient.CreatePlaintext("localhost:50051", "testtoken");

        var results = await client.CheckBulkAsync([], null, default);
        results.Should().BeEmpty();
    }

    [Fact]
    public async Task WriteAsync_ThrowsOnNullTransaction()
    {
        await using var client = SpiceDBClient.CreatePlaintext("localhost:50051", "testtoken");

        var act = async () => await client.WriteAsync(null!, null, default);
        await act.Should().ThrowAsync<InvalidArgumentException>();
    }

    [Fact]
    public async Task WriteSchemaAsync_ThrowsOnEmptySchema()
    {
        await using var client = SpiceDBClient.CreatePlaintext("localhost:50051", "testtoken");

        var act = async () => await client.WriteSchemaAsync("", null, default);
        await act.Should().ThrowAsync<InvalidArgumentException>();
    }

    [Fact]
    public async Task ExperimentalRegisterRelationshipCounterAsync_ThrowsOnEmptyName()
    {
        await using var client = SpiceDBClient.CreatePlaintext("localhost:50051", "testtoken");

        var act = async () => await client.ExperimentalRegisterRelationshipCounterAsync("", new Filter("doc"), null, default);
        await act.Should().ThrowAsync<InvalidArgumentException>();
    }

    [Fact]
    public async Task ExperimentalCountRelationshipsAsync_ThrowsOnEmptyName()
    {
        await using var client = SpiceDBClient.CreatePlaintext("localhost:50051", "testtoken");

        var act = async () => await client.ExperimentalCountRelationshipsAsync("", null, default);
        await act.Should().ThrowAsync<InvalidArgumentException>();
    }

    [Fact]
    public async Task ExperimentalUnregisterRelationshipCounterAsync_ThrowsOnEmptyName()
    {
        await using var client = SpiceDBClient.CreatePlaintext("localhost:50051", "testtoken");

        var act = async () => await client.ExperimentalUnregisterRelationshipCounterAsync("", null, default);
        await act.Should().ThrowAsync<InvalidArgumentException>();
    }
}
