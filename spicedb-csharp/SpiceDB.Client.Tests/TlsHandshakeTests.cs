using Xunit;

using SpiceDB.Client;

namespace SpiceDB.Client.Tests;

/// <summary>
/// Root DESIGN.md, "RULE: A system-TLS constructor must reach a real server".
/// </summary>
/// <remarks>
/// <para>
/// <c>CreateSystemTls_ReturnsClient</c> in <c>SpiceDBClientTests.cs</c> constructs
/// against <c>grpc.example.com:443</c> — a reserved, non-routable name — and asserts
/// only that the result is non-null. <see cref="Grpc.Net.Client.GrpcChannel"/> connects
/// lazily, so no packet leaves the process: that test passes with an empty trust store,
/// which is precisely the defect this rule exists to catch. This class is its honest
/// counterpart.
/// </para>
/// <para>
/// <b>Why a trait rather than an environment variable.</b> Clause 3 asks for a gate, so
/// that a test needing the network does not run where there is none, plus a CI step that
/// fails if the test did not actually run. xunit 2 has no runtime skip: an
/// <c>if (env is null) return;</c> gate reports the test as <b>Passed</b> while doing
/// nothing, which is the same "reads as coverage, provides none" failure the rule is
/// about. A trait keeps it out of the default run entirely and makes the CI step's
/// <c>Passed: 1</c> check meaningful — there is no state in which this test passes
/// without completing a handshake.
/// </para>
/// </remarks>
[Trait("Category", "TlsHandshake")]
public class TlsHandshakeTests
{
    private const string Endpoint = "grpc.authzed.com:443";

    /// <summary>
    /// Substrings of a failure that happened <em>before</em> any server could answer.
    /// Matched as prose rather than by status code on purpose: gRPC surfaces a failed
    /// TLS handshake and a live server's "no healthy upstream" alike, so the status
    /// cannot discriminate between "the trust store is empty" and "the server replied".
    /// </summary>
    private static readonly string[] TrustStoreFailureSignatures =
    [
        "ssl connection could not be established",
        "remotecertificate",
        "authenticationexception",
        "certificate is invalid",
        "untrustedroot",
        "partialchain",
    ];

    /// <summary>
    /// Substrings meaning the endpoint was never reached at all. Kept separate from the
    /// trust-store signatures so a network outage in CI reports as an outage rather than
    /// as a TLS regression.
    /// </summary>
    private static readonly string[] UnreachableSignatures =
    [
        "no such host is known",
        "connection refused",
        "actively refused",
        "name or service not known",
    ];

    /// <summary>
    /// Drives <see cref="SpiceDBClient.CreateSystemTls"/> against a real public endpoint
    /// and requires the TLS handshake to complete.
    /// </summary>
    /// <remarks>
    /// Any gRPC reply proves the handshake completed: producing one at all requires the
    /// far side to have accepted our TLS session and spoken HTTP/2 back. As of writing an
    /// unauthenticated caller gets Unavailable "no healthy upstream" from Authzed's edge
    /// rather than Unauthenticated, so pinning a status code here would assert a
    /// deployment detail of someone else's service. What gets pinned is the distinction
    /// the rule cares about: did we reach a server, or did we fail on trust material.
    /// </remarks>
    [Fact]
    public async Task CreateSystemTls_CompletesRealHandshake()
    {
        await using var client = SpiceDBClient.CreateSystemTls(Endpoint, "not-a-real-token");

        string detail;
        try
        {
            // The channel is lazy, so the constructor proves nothing by itself. This RPC
            // is what forces the connection, and with it the handshake — clause 2:
            // "where the constructor is lazy, force the connection inside the test".
            await client.ReadSchemaAsync(timeout: TimeSpan.FromSeconds(30));
            return; // a successful RPC is, a fortiori, a completed handshake
        }
        catch (Exception ex)
        {
            // The whole chain: Grpc.Net wraps the TLS failure in an HttpRequestException
            // inside an RpcException, and this client maps that again, so the certificate
            // detail lives several levels down from the message on top.
            detail = Flatten(ex).ToLowerInvariant();
        }

        foreach (var signature in TrustStoreFailureSignatures)
        {
            Assert.False(
                detail.Contains(signature),
                $"system TLS handshake failed — the platform trust store is probably not " +
                $"loaded, or the client is supplying its own (empty) root set: {detail}");
        }

        foreach (var signature in UnreachableSignatures)
        {
            Assert.False(
                detail.Contains(signature),
                $"could not reach {Endpoint} at all: this is a network problem, not a TLS " +
                $"result, and says nothing about the trust store: {detail}");
        }
    }

    private static string Flatten(Exception ex)
    {
        var parts = new List<string>();
        for (Exception? current = ex; current is not null; current = current.InnerException)
        {
            parts.Add($"{current.GetType().Name}: {current.Message}");
        }

        return string.Join(" <- ", parts);
    }
}
