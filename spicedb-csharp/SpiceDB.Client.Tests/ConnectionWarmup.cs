namespace SpiceDB.Client.Tests;

/// <summary>
/// Timing tests against a real Kestrel server warm the connection before they start a clock or
/// a short deadline. The first call on a fresh channel pays for the HTTP/2 connection (and the
/// JIT of the call path), which on a busy machine (a parallel test run) can by itself exceed a
/// few hundred milliseconds; an allowance meant for the behavior under test must not also have
/// to cover that.
/// </summary>
internal static class ConnectionWarmup
{
    /// <summary>
    /// Makes one schema read with a generous timeout. The fakes do not serve the schema service,
    /// so it fails with Unimplemented, which still proves the connection is open.
    /// </summary>
    internal static async Task WarmAsync(SpiceDBClient client)
    {
        try
        {
            await client.ConnectAsync(new ConnectOptions { Timeout = TimeSpan.FromSeconds(30) });
        }
        catch (SpiceDBException e) when (e.StatusCode == Grpc.Core.StatusCode.Unimplemented)
        {
            // Reached the server: the connection is up.
        }
    }
}
