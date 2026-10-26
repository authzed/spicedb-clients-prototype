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

        await client.WriteSchemaAsync(Schema, null, default);

        var txn = new Transaction();
        txn.Touch(new Relationship(new ObjectRef("document", "firstdoc"), "viewer", new SubjectRef(new ObjectRef("user", "alice"))));
        txn.Touch(new Relationship(new ObjectRef("document", "seconddoc"), "editor", new SubjectRef(new ObjectRef("user", "alice"))));
        await client.WriteAsync(txn, null, default);

        // Lookup resources alice can view. Each result is a native
        // LookupResource carrying the resource ID plus permissionship — check
        // Permissionship before treating a result as a full grant, since
        // ConditionalPermission results depend on caveat context that was not
        // fully evaluated by the server.
        var resourceIDs = new HashSet<string>();
        await foreach (var result in client.LookupResourcesAsync(Full(), "document", "view", new SubjectRef(new ObjectRef("user", "alice")), new LookupOptions { PermissionshipFilter = LookupPermissionshipFilter.HasPermissionOnly }, default))
        {
            Assert.Equal(Permissionship.HasPermission, result.Permissionship);
            if (result.Permissionship == Permissionship.HasPermission)
                resourceIDs.Add(result.ResourceID); // Also safe with the default All mode.
        }

        Assert.Contains("firstdoc", resourceIDs);
        Assert.Contains("seconddoc", resourceIDs);
    }
}
