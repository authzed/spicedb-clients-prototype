using System.Net.Sockets;
using Xunit;

using SpiceDB.Client;

namespace EndpointForms;

/// <summary>
/// Example EndpointForms makes a real call over every endpoint form the client accepts --
/// <c>host:port</c>, <c>http://host:port</c>, <c>dns:///host:port</c> and
/// <c>unix:/path</c> -- and uses <see cref="SpiceDBClient.ConnectAsync"/>, the startup
/// readiness check, so a wrong or unreachable endpoint fails at startup instead of on the
/// first request.
/// </summary>
public class EndpointFormsTest
{
    private static async Task AssertWorks(string endpoint)
    {
        await using var client = SpiceDBClient.CreatePlaintext(endpoint, SpiceDBTestServer.Token);
        await client.ConnectAsync(new ConnectOptions { Timeout = TimeSpan.FromSeconds(10) });
        // A real authenticated call, not just readiness. Other examples own the schema, so
        // this only reads it; a server with no schema yet answers NotFound.
        try { await client.ReadSchemaAsync(); }
        catch (NotFoundException) { }
    }

    [Fact]
    public Task HostPort() => AssertWorks(SpiceDBTestServer.Endpoint);

    [Fact]
    public Task HttpUrl() => AssertWorks($"http://{SpiceDBTestServer.Endpoint}");

    /// <summary><c>dns:///</c> resolves the host with client-side round-robin load balancing across its addresses.</summary>
    [Fact]
    public Task DnsTarget() => AssertWorks($"dns:///{SpiceDBTestServer.Endpoint}");

    /// <summary>
    /// <c>unix:</c> dials a socket path. SpiceDB can listen on one itself
    /// (<c>--grpc-network unix --grpc-addr /path</c>); here a local socket forwards to the
    /// test server's TCP port, so the example needs no second server.
    /// </summary>
    [Fact]
    public async Task UnixSocket()
    {
        var path = Path.Combine(Path.GetTempPath(), $"spicedb-{Guid.NewGuid():N}.sock");
        using var cts = new CancellationTokenSource();
        using var listener = new Socket(AddressFamily.Unix, SocketType.Stream, ProtocolType.Unspecified);
        listener.Bind(new UnixDomainSocketEndPoint(path));
        listener.Listen();
        var forwarding = Forward(listener, cts.Token);
        try
        {
            await AssertWorks($"unix:{path}");
        }
        finally
        {
            cts.Cancel();
            listener.Close();
            await forwarding;
            File.Delete(path);
        }
    }

    [Fact]
    public async Task ConnectAsyncFailsAtStartupForAnUnreachableEndpoint()
    {
        await using var client = SpiceDBClient.CreatePlaintext("localhost:1", SpiceDBTestServer.Token);
        var error = await Assert.ThrowsAnyAsync<SpiceDBException>(() => client.ConnectAsync(new ConnectOptions { Timeout = TimeSpan.FromSeconds(1) }));
        Assert.Contains("localhost:1", error.Message);
    }

    /// <summary>Accepts unix-socket connections and pipes each to the test server's TCP endpoint.</summary>
    private static async Task Forward(Socket listener, CancellationToken token)
    {
        var colon = SpiceDBTestServer.Endpoint.LastIndexOf(':');
        var host = SpiceDBTestServer.Endpoint[..colon];
        var port = int.Parse(SpiceDBTestServer.Endpoint[(colon + 1)..]);
        var pipes = new List<Task>();
        try
        {
            while (!token.IsCancellationRequested)
            {
                var inbound = await listener.AcceptAsync(token);
                pipes.Add(Task.Run(async () =>
                {
                    using var local = new NetworkStream(inbound, ownsSocket: true);
                    using var tcp = new TcpClient();
                    await tcp.ConnectAsync(host, port, token);
                    using var remote = tcp.GetStream();
                    await Task.WhenAny(local.CopyToAsync(remote, token), remote.CopyToAsync(local, token));
                }, token));
            }
        }
        catch (Exception) when (token.IsCancellationRequested) { }
        catch (SocketException) { }
        catch (ObjectDisposedException) { }
        try { await Task.WhenAll(pipes); } catch (Exception) { /* torn down with the test */ }
    }
}
