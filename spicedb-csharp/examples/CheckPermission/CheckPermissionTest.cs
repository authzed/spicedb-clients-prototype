using Xunit;
// Example CheckPermission demonstrates checking a single permission.

using SpiceDB.Client;
using static SpiceDB.Client.Consistency;

namespace CheckPermission;

public class CheckPermissionTest
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
    public async Task CheckPermission_Granted()
    {
        await using var client = SpiceDBClient.CreatePlaintext("localhost:50051", "somerandomkeyhere");

        // Setup: write schema and test data
        await client.WriteSchemaAsync(Schema);

        var txn = new Transaction();
        txn.Touch(Relationship.FromTriple("document", "firstdoc", "viewer", "user", "alice"));
        await client.WriteAsync(txn);

        // Check permission
        var rel = Relationship.FromTriple("document", "firstdoc", "view", "user", "alice");
        var allowed = await client.CheckPermissionAsync(Full(), "view", rel);

        Assert.True(allowed, "expected alice to have view permission");
    }

    [Fact]
    public async Task CheckPermission_Denied()
    {
        await using var client = SpiceDBClient.CreatePlaintext("localhost:50051", "somerandomkeyhere");

        await client.WriteSchemaAsync(Schema);

        var txn = new Transaction();
        txn.Touch(Relationship.FromTriple("document", "firstdoc", "viewer", "user", "alice"));
        await client.WriteAsync(txn);

        // alice is only a viewer, not an editor
        var rel = Relationship.FromTriple("document", "firstdoc", "edit", "user", "alice");
        var allowed = await client.CheckPermissionAsync(Full(), "edit", rel);

        Assert.False(allowed, "expected alice to NOT have edit permission");
    }
}
