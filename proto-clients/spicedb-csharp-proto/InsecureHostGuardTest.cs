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
