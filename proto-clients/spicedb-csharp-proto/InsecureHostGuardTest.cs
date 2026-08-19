// Regression tests for root DESIGN.md, "RULE: Credentials over insecure transport
// require an explicit opt-in".
//
// NOTE: This test requires buf-generated code in gen/ to compile.
// Run `buf generate` before running tests with `dotnet test`.

using System.Net;
using System.Net.Http.Headers;
using Xunit;
using Authzed.Api.SpiceDB.Proto;
using Authzed.Api.V1;

namespace Authzed.Api.SpiceDB.Proto.Tests;

/// <summary>
/// A fake <see cref="HttpMessageHandler"/> that captures the "authorization" header
/// on every outgoing request instead of touching a real socket. Since Grpc.Net.Client
/// hands its fully-built <see cref="HttpRequestMessage"/> straight to this handler,
/// capturing here is capturing at the exact boundary the credential would cross on
/// its way to the wire -- stronger proof than "an exception was thrown", because it
/// directly observes whether the token was ever handed to the transport layer at all.
/// </summary>
internal sealed class AuthCapturingHandler : HttpMessageHandler
{
    public int InvocationCount;
    public string? CapturedAuth;

    protected override Task<HttpResponseMessage> SendAsync(HttpRequestMessage request, CancellationToken cancellationToken)
    {
        Interlocked.Increment(ref InvocationCount);
        CapturedAuth = request.Headers.TryGetValues("authorization", out var values) ? values.FirstOrDefault() : null;

        // The response content/framing doesn't matter -- callers only inspect
        // CapturedAuth/InvocationCount, not the RPC's outcome. A well-formed
        // "OK" gRPC trailer just avoids a noisy client-side exception.
        var response = new HttpResponseMessage(HttpStatusCode.OK)
        {
            Version = HttpVersion.Version20,
            Content = new ByteArrayContent(Array.Empty<byte>()),
        };
        response.Content.Headers.ContentType = new MediaTypeHeaderValue("application/grpc");
        response.TrailingHeaders.Add("grpc-status", "2"); // UNKNOWN -- fine, we don't assert on the call's outcome.
        return Task.FromResult(response);
    }
}

public class InsecureHostGuardTest
{
    /// <summary>
    /// Table test for <see cref="SpiceDBProtoClient.IsLoopbackEndpoint"/>. Includes
    /// typosquat/lookalike hosts that must NOT be treated as loopback -- a future
    /// refactor toward Contains/StartsWith on "localhost" or "127.0.0.1" would
    /// silently reopen a credential leak to any of these, and this test is what
    /// would catch it.
    /// </summary>
    [Theory]
    [InlineData("localhost:50051", true)]
    [InlineData("LOCALHOST:50051", true)]
    [InlineData("localhost", true)]
    [InlineData("127.0.0.1:50051", true)]
    [InlineData("127.0.0.1", true)]
    [InlineData("127.55.66.77:50051", true)]
    [InlineData("[::1]:50051", true)]
    [InlineData("::1", true)]
    // Unix targets are NOT loopback for this client, deliberately, and this pair used
    // to assert the opposite. Grpc.Net.Client cannot dial a UDS path from an address
    // string: GrpcChannel.ForAddress("http://unix:/var/run/spicedb.sock") reports
    // Target "unix", so the exemption was handing a bearer token to whatever DNS
    // answers for the name "unix". The constructor now refuses these outright -- see
    // TestRefusesUnixSocketTargets -- and the loopback question never arises.
    [InlineData("unix:/var/run/spicedb.sock", false)]
    [InlineData("unix:///var/run/spicedb.sock", false)]
    [InlineData("UNIX:/var/run/spicedb.sock", false)]
    // IDN-invalid hosts: Uri.TryCreate accepts these but Uri.IdnHost throws on them.
    // This method must be total -- a System.UriFormatException escaping a constructor
    // documented to throw InvalidOperationException is a broken contract, and the
    // string comparisons this parse replaced could not throw at all.
    [InlineData("‥localhost", false)]
    [InlineData("‥localhost:50051", false)]
    [InlineData("loc‥alhost", false)]
    [InlineData("127.0.0.1‥x", false)]
    [InlineData("example.com:443", false)]
    [InlineData("staging.internal:443", false)]
    [InlineData("10.0.0.5:50051", false)]
    [InlineData("8.8.8.8:443", false)]
    [InlineData("0.0.0.0:50051", false)]
    [InlineData("localhost.evil.com:443", false)]
    [InlineData("127.0.0.1.evil.com:443", false)]
    [InlineData("evil-localhost:443", false)]
    // Authority-shifting targets. Every one of these was, or could become, a
    // credential leak: the guard's own last-colon split read a loopback host out of
    // them while GrpcChannel.ForAddress parsed the SAME string as URI userinfo/path/
    // query/fragment and dialed somewhere else entirely. See AuthorityShiftingEndpoints
    // below for the ones proven to dial off-host, and IsLoopbackEndpoint's doc comment
    // for why the fix is "ask the transport's parser", not "validate the port".
    [InlineData("127.0.0.1:443@evil.com", false)]  // dialed evil.com; guard said loopback
    [InlineData("[::1]:443@evil.com", false)]      // dialed evil.com; guard said loopback
    [InlineData("[::1]:0@127.0.0.1:19999", false)] // guard read "::1"; transport dialed :19999
    [InlineData("[localhost]:1@127.0.0.1:19999", false)]
    [InlineData("localhost@evil.com", false)]
    [InlineData("localhost/../evil.com", false)]
    [InlineData("localhost#@evil.com", false)]
    [InlineData("localhost?@evil.com", false)]
    [InlineData("localhost.", false)]
    [InlineData("localhost :50051", false)]
    [InlineData("127.0.0.1 :50051", false)]
    public void TestIsLoopbackEndpoint(string endpoint, bool expected)
    {
        Assert.Equal(expected, SpiceDBProtoClient.IsLoopbackEndpoint(endpoint));
    }

    /// <summary>
    /// Endpoints a reviewer proved dial a host the guard never evaluated:
    /// GrpcChannel.ForAddress("http://127.0.0.1:443@evil.com") reports
    /// <c>Target == "evil.com"</c>. Before the fix, IsLoopbackEndpoint returned true
    /// for each of these, so an insecure client was constructed with no opt-in and
    /// shipped its bearer token to the attacker-controlled host in cleartext.
    /// </summary>
    public static TheoryData<string> AuthorityShiftingEndpoints() => new()
    {
        "127.0.0.1:443@evil.com",
        "[::1]:443@evil.com",
        "[::1]:0@127.0.0.1:19999",
        "[localhost]:1@127.0.0.1:19999",
    };

    /// <summary>
    /// The regression test for the guard bypass. Asserting only that the constructor
    /// throws would be satisfied by an implementation that opens the connection, sends
    /// the token, and raises afterwards -- so this asserts on the transport instead:
    /// handler.InvocationCount proves the HttpMessageHandler that would carry the
    /// bearer token was never invoked, and CapturedAuth proves no authorization header
    /// was ever handed to it. Same bar as TestRefusesInsecureNonLoopbackWithoutOptIn
    /// below, applied to the inputs that used to slip past the guard entirely.
    /// </summary>
    [Theory]
    [MemberData(nameof(AuthorityShiftingEndpoints))]
    public void TestRefusesEndpointThatDialsOffHost(string endpoint)
    {
        var handler = new AuthCapturingHandler();

        var ex = Assert.Throws<InvalidOperationException>(() =>
            new SpiceDBProtoClient(endpoint, "super-secret-token", insecure: true, allowInsecureRemoteCredentials: false, httpHandler: handler));

        Assert.Contains(endpoint, ex.Message);
        Assert.Contains("allowInsecureRemoteCredentials", ex.Message);

        Assert.Equal(0, handler.InvocationCount);
        Assert.Null(handler.CapturedAuth);
    }

    /// <summary>
    /// TestRefusesInsecureNonLoopbackWithoutOptIn is the regression test for root
    /// DESIGN.md, "RULE: Credentials over insecure transport require an explicit
    /// opt-in": constructing insecurely against a non-loopback endpoint, with no
    /// allowInsecureRemoteCredentials, must fail before any credential reaches the
    /// wire.
    /// <para>
    /// handler.InvocationCount proves the HttpMessageHandler that WOULD carry the
    /// connection (and, over it, the bearer token) was never invoked at all -- a
    /// stronger assertion than "the constructor threw": an implementation that sent
    /// the token and only then surfaced an error would still fail a bare
    /// Assert.Throws check but would fail this one, because InvocationCount would be
    /// nonzero and CapturedAuth would hold the token.
    /// </para>
    /// </summary>
    [Fact]
    public void TestRefusesInsecureNonLoopbackWithoutOptIn()
    {
        var handler = new AuthCapturingHandler();

        var ex = Assert.Throws<InvalidOperationException>(() =>
            new SpiceDBProtoClient("evil.example.com:1234", "super-secret-token", insecure: true, allowInsecureRemoteCredentials: false, httpHandler: handler));

        Assert.Contains("evil.example.com:1234", ex.Message);
        Assert.Contains("allowInsecureRemoteCredentials", ex.Message);

        Assert.Equal(0, handler.InvocationCount);
        Assert.Null(handler.CapturedAuth);
    }

    /// <summary>
    /// A bare IPv6 literal must produce a WORKING client, not merely satisfy the
    /// guard. <c>"::1"</c> is item 8 of the loopback contract and an explicit fixture
    /// in TestIsLoopbackEndpoint above -- but it is not a legal URI authority, so
    /// while the guard bracketed it for its own parse and the constructor built the
    /// address from the raw endpoint, <c>GrpcChannel.ForAddress("http://::1")</c>
    /// threw <see cref="UriFormatException"/> and no client could be created at all.
    /// That is the same escaping-exception defect as the IdnHost one, one call
    /// further down, for an input the fixtures assert is supported.
    /// </summary>
    [Theory]
    [InlineData("::1")]
    [InlineData("[::1]")]
    [InlineData("0:0:0:0:0:0:0:1")]
    [InlineData("[::1]:50051")]
    [InlineData("127.0.0.1")]
    public void TestBareIpv6LoopbackConstructsAClient(string endpoint)
    {
        var handler = new AuthCapturingHandler();
        using var client = new SpiceDBProtoClient(endpoint, "test-token", insecure: true, allowInsecureRemoteCredentials: false, httpHandler: handler);
        Assert.NotNull(client);
    }

    /// <summary>
    /// A null endpoint or token must surface as ArgumentNullException, not as a
    /// NullReferenceException from whichever string operation happens to run first.
    /// </summary>
    [Fact]
    public void TestNullArgumentsThrowArgumentNullException()
    {
        Assert.Throws<ArgumentNullException>(() =>
            new SpiceDBProtoClient(null!, "token", insecure: true, allowInsecureRemoteCredentials: false));
        Assert.Throws<ArgumentNullException>(() =>
            new SpiceDBProtoClient("localhost:50051", null!, insecure: true, allowInsecureRemoteCredentials: false));
    }

    /// <summary>
    /// A unix-socket target must be refused outright, not treated as loopback. This
    /// transport has no UDS support reachable from an address string, so
    /// GrpcChannel.ForAddress("http://unix:/var/run/spicedb.sock") resolves the DNS
    /// name "unix" -- meaning the old "a unix socket never leaves the kernel"
    /// exemption was shipping the bearer token to a remote host in cleartext while the
    /// guard reported "loopback".
    /// <para>
    /// The refusal is unconditional: TLS and allowInsecureRemoteCredentials are both
    /// exercised here, because neither makes dialing a host called "unix" the thing
    /// the caller asked for. handler.InvocationCount proves nothing reached the
    /// transport in any of those combinations.
    /// </para>
    /// </summary>
    [Theory]
    [InlineData("unix:/var/run/spicedb.sock", true, false)]
    [InlineData("unix:///var/run/spicedb.sock", true, false)]
    [InlineData("UNIX:/var/run/spicedb.sock", true, false)]
    // The opt-in does not buy a unix target either: it authorizes cleartext to a
    // remote host, not "resolve a hostname called unix".
    [InlineData("unix:/var/run/spicedb.sock", true, true)]
    // Nor does TLS.
    [InlineData("unix:/var/run/spicedb.sock", false, false)]
    public void TestRefusesUnixSocketTargets(string endpoint, bool insecure, bool allowInsecureRemoteCredentials)
    {
        var handler = new AuthCapturingHandler();

        var ex = Assert.Throws<InvalidOperationException>(() =>
            new SpiceDBProtoClient(endpoint, "super-secret-token", insecure, allowInsecureRemoteCredentials, handler));

        Assert.Contains(endpoint, ex.Message);
        Assert.Contains("unix-domain-socket", ex.Message);

        Assert.Equal(0, handler.InvocationCount);
        Assert.Null(handler.CapturedAuth);
    }

    /// <summary>
    /// TestLoopbackWorksWithNoOptIn proves the loopback exemption needs no ceremony:
    /// an insecure connection to a loopback endpoint succeeds and actually delivers
    /// the bearer token, with no allowInsecureRemoteCredentials involved anywhere.
    /// </summary>
    [Fact]
    public async Task TestLoopbackWorksWithNoOptIn()
    {
        var handler = new AuthCapturingHandler();
        using var client = new SpiceDBProtoClient("localhost:50051", "test-token", insecure: true, allowInsecureRemoteCredentials: false, httpHandler: handler);

        await AttemptCall(client);

        Assert.True(handler.InvocationCount > 0, "expected the call to actually reach the (fake) transport");
        Assert.Equal("Bearer test-token", handler.CapturedAuth);
    }

    /// <summary>
    /// TestAllowInsecureRemoteCredentialsSendsToken proves the named opt-in actually
    /// works: with allowInsecureRemoteCredentials: true, an insecure connection to a
    /// non-loopback endpoint is permitted and the bearer token is sent.
    /// </summary>
    [Fact]
    public async Task TestAllowInsecureRemoteCredentialsSendsToken()
    {
        var handler = new AuthCapturingHandler();
        using var client = new SpiceDBProtoClient("evil.example.com:1234", "remote-token", insecure: true, allowInsecureRemoteCredentials: true, httpHandler: handler);

        await AttemptCall(client);

        Assert.True(handler.InvocationCount > 0, "expected the call to actually reach the (fake) transport");
        Assert.Equal("Bearer remote-token", handler.CapturedAuth);
    }

    /// <summary>
    /// Makes a real CheckPermission call against the fake transport. The call is
    /// expected to fail (the fake handler doesn't speak real gRPC framing) -- these
    /// tests only care about what the handler observed, not the RPC's outcome.
    /// </summary>
    private static async Task AttemptCall(SpiceDBProtoClient client)
    {
        try
        {
            await client.Permissions.CheckPermissionAsync(new CheckPermissionRequest
            {
                Resource = new ObjectReference { ObjectType = "document", ObjectId = "1" },
                Permission = "view",
                Subject = new SubjectReference { Object = new ObjectReference { ObjectType = "user", ObjectId = "alice" } },
            });
        }
        catch (Exception)
        {
            // Expected: the fake handler's response isn't a real gRPC response.
        }
    }
}
