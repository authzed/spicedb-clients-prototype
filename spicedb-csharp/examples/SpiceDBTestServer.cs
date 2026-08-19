// Shared by every example project via examples/Directory.Build.props, which
// links this one file into each of them. It is not part of the published
// package -- example projects set IsPackable=false.

using SpiceDB.Client;

/// <summary>
/// The SpiceDB the examples talk to.
/// </summary>
/// <remarks>
/// <c>mage integrationTest</c> starts the container from
/// <c>docker-compose.test.yml</c> and exports both variables; the defaults are
/// that file's endpoint and preshared key, so an example run by hand against a
/// container started the same way needs no environment at all.
/// </remarks>
internal static class SpiceDBTestServer
{
    /// <summary>The <c>host:port</c> of the SpiceDB to connect to.</summary>
    public static string Endpoint =>
        Environment.GetEnvironmentVariable("SPICEDB_ENDPOINT") is { Length: > 0 } endpoint
            ? endpoint
            : "localhost:50051";

    /// <summary>The preshared key to authenticate with.</summary>
    public static string Token =>
        Environment.GetEnvironmentVariable("SPICEDB_TOKEN") is { Length: > 0 } token
            ? token
            : "somerandomkeyhere";

    /// <summary>
    /// Deletes every <c>document</c> relationship, ignoring the case where no
    /// <c>document</c> definition exists yet.
    /// </summary>
    /// <remarks>
    /// Every example project runs against the same SpiceDB and writes a whole
    /// schema, and SpiceDB refuses a <c>WriteSchema</c> that drops a relation
    /// while a relationship still exists under it. An example whose schema is
    /// narrower than an earlier one's therefore has to clear what that earlier
    /// example left behind before it can write its own.
    /// </remarks>
    public static async Task ClearDocumentRelationshipsAsync(SpiceDBClient client)
    {
        try
        {
            await client.DeleteRelationshipsAsync(new Filter("document"));
        }
        catch (FailedPreconditionException)
        {
            // No `document` definition in the live schema yet: nothing to clear.
        }
    }
}
