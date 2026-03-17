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
        await using var client = SpiceDBClient.CreatePlaintext("localhost:50051", "somerandomkeyhere");

        await client.WriteSchemaAsync(Schema);

        var txn = new Transaction();
        txn.Touch(Relationship.FromTriple("document", "firstdoc", "viewer", "user", "alice"));
        txn.Touch(Relationship.FromTriple("document", "seconddoc", "editor", "user", "alice"));
        await client.WriteAsync(txn);

        // Lookup resources alice can view
        var resourceIDs = new HashSet<string>();
        await foreach (var resourceID in client.LookupResourcesAsync(
            Full(), "document", "view", "user", "alice"))
        {
            resourceIDs.Add(resourceID);
        }

        Assert.Contains("firstdoc", resourceIDs);
        Assert.Contains("seconddoc", resourceIDs);
    }
}
