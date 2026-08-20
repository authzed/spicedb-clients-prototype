using Xunit;

using SpiceDB.Client;

namespace InsecureOptIn;

/// <summary>
/// Example InsecureOptIn demonstrates the guard on sending a bearer token over a
/// plaintext connection -- see root DESIGN.md, "RULE: Credentials over insecure
/// transport require an explicit opt-in".
/// </summary>
/// <remarks>
/// <para>
/// The failure this rule exists to prevent is mundane and common: a developer copies
/// an insecure constructor out of a localhost example into a staging config, and a
/// long-lived SpiceDB token -- a complete authorization bypass in anyone else's hands
/// -- goes onto the wire in cleartext with nothing signalling that it happened. So
/// <c>CreatePlaintext</c> is loopback-only, and reaching a remote host over plaintext
/// takes a second, separately-named argument the caller cannot supply by accident.
/// </para>
/// <para>
/// The sharpest case is the last one. The rule requires the guard's answer to be
/// <em>the transport's</em> answer -- here <c>System.Uri</c>, what Grpc.Net.Client
/// parses with -- rather than a hand-rolled string split. Given
/// <c>127.0.0.1:443@evil.com</c>, a last-colon split reads the host as
/// <c>127.0.0.1</c> and waves it through, while <c>Uri</c> reads <c>127.0.0.1:443</c>
/// as <em>userinfo</em> and the authority as <c>evil.com</c>.
/// </para>
/// </remarks>
public class InsecureOptInTest
{
    [Fact]
    public async Task LoopbackPlaintext_NeedsNoOptIn()
    {
        // The case the rule deliberately leaves ergonomic: a token on a loopback socket
        // never leaves the machine, so requiring ceremony here would only train
        // developers to reach for the opt-in reflexively.
        await using var client = SpiceDBClient.CreatePlaintext(
            SpiceDBTestServer.Endpoint, SpiceDBTestServer.Token);

        // Prove the client is usable, not merely constructed: the channel connects
        // lazily, so a constructor returning a client that could not talk to anything
        // would still satisfy a "did not throw" assertion.
        //
        // All thirteen-plus example projects share one SpiceDB and this schema is
        // narrower than what others write, so clear first: SpiceDB refuses a
        // WriteSchema that drops a relation while a relationship still exists under it.
        await SpiceDBTestServer.ClearDocumentRelationshipsAsync(client);
        await client.WriteSchemaAsync("""
            definition user {}

            definition document {
                relation viewer: user
                permission view = viewer
            }
            """);
    }

    [Fact]
    public void RemotePlaintext_IsRefusedWithoutTheOptIn()
    {
        // No connection is attempted: the refusal happens during construction, so the
        // token never reaches a socket. This is not about whether the host exists --
        // example.com is refused because it is not loopback, full stop.
        // This client's own typed argument error, the same one a filter the wire cannot
        // express uses -- not the proto tier's InvalidOperationException, which a caller
        // of this class should never see. Root DESIGN.md, "RULE: Credentials over
        // insecure transport require an explicit opt-in", clause 4.
        var ex = Assert.Throws<InvalidArgumentException>(
            () => SpiceDBClient.CreatePlaintext("example.com:50051", SpiceDBTestServer.Token));
        Assert.Contains("example.com:50051", ex.Message);
        Assert.Contains("allowInsecureRemoteCredentials", ex.Message);
    }

    [Fact]
    public async Task RemotePlaintext_IsAllowedWithTheNamedOptIn()
    {
        // Two arguments, not one, and that separation is the point: selecting the
        // plaintext transport and accepting the credential exposure that follows are
        // different decisions, and clause 1 forbids one boolean from doing both jobs.
        await using var client = SpiceDBClient.CreatePlaintext(
            "example.com:50051", SpiceDBTestServer.Token, allowInsecureRemoteCredentials: true);
        Assert.NotNull(client);
    }

    [Theory]
    [InlineData("127.0.0.1:443@evil.com")]
    [InlineData("127.0.0.1:50051/../evil.com")]
    [InlineData("127.0.0.1:50051?x=evil.com")]
    [InlineData("127.0.0.1:50051#evil.com")]
    public void AuthorityMovingEndpoints_AreRefused(string endpoint)
    {
        // Fail closed on anything whose authority could move under URI parsing. A client
        // that split on the last colon would call 127.0.0.1:443@evil.com loopback and
        // hand the token to evil.com.
        Assert.Throws<InvalidArgumentException>(
            () => SpiceDBClient.CreatePlaintext(endpoint, SpiceDBTestServer.Token));
    }
}
