using Xunit;
// Example LookupResources demonstrates finding resources a subject can access.

using SpiceDB.Client;
using static SpiceDB.Client.Consistency;

namespace LookupResources;

public class LookupResourcesTest
{
    private const string Schema = """
        definition user {}

        definition document {
            relation viewer: user
            relation editor: user
            permission view = viewer + editor
            permission edit = editor
        }
        """;

    [Fact]
    public async Task LookupResources_FindsAccessibleDocuments()
    {
        await using var client = SpiceDBClient.CreatePlaintext(SpiceDBTestServer.Endpoint, SpiceDBTestServer.Token);

        await client.WriteSchemaAsync(Schema);

        var txn = new Transaction();
        txn.Touch(Relationship.FromTriple("document", "firstdoc", "viewer", "user", "alice"));
        txn.Touch(Relationship.FromTriple("document", "seconddoc", "editor", "user", "alice"));
        await client.WriteAsync(txn);

        // Lookup resources alice can view. Each result is a native
        // LookupResource carrying the resource ID plus permissionship — check
        // Permissionship before treating a result as a full grant, since
        // ConditionalPermission results depend on caveat context that was not
        // fully evaluated by the server.
        var resourceIDs = new HashSet<string>();
        await foreach (var result in client.LookupResourcesAsync(
            Full(), "document", "view", "user", "alice"))
        {
            Assert.Equal(Permissionship.HasPermission, result.Permissionship);
            resourceIDs.Add(result.ResourceID);
        }

        Assert.Contains("firstdoc", resourceIDs);
        Assert.Contains("seconddoc", resourceIDs);
    }

    [Fact]
    public async Task LookupResources_WithDebug_StillReturnsResults()
    {
        await using var client = SpiceDBClient.CreatePlaintext(SpiceDBTestServer.Endpoint, SpiceDBTestServer.Token);

        await client.WriteSchemaAsync(Schema);

        var txn = new Transaction();
        txn.Touch(Relationship.FromTriple("document", "thirddoc", "viewer", "user", "alice"));
        await client.WriteAsync(txn);

        // withDebug asks the server to attach debug info (currently limited
        // to maximum-recursion-depth errors) to the error details of a
        // failed call. It must not change the results of a call that
        // succeeds.
        var resourceIDs = new HashSet<string>();
        await foreach (var result in client.LookupResourcesAsync(
            Full(), "document", "view", "user", "alice", withDebug: true))
        {
            resourceIDs.Add(result.ResourceID);
        }

        Assert.Contains("thirddoc", resourceIDs);
    }
}
