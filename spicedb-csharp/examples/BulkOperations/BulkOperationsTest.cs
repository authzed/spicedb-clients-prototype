using Xunit;
// Example BulkOperations demonstrates bulk checks and batch writes.

using SpiceDB.Client;
using static SpiceDB.Client.Consistency;

namespace BulkOperations;

public class BulkOperationsTest
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
    public async Task BulkWrite_And_BulkCheck()
    {
        await using var client = SpiceDBClient.CreatePlaintext("localhost:50051", "somerandomkeyhere");

        await client.WriteSchemaAsync(Schema);

        // Bulk write relationships
        var users = new[] { "alice", "bob", "charlie" };
        var txn = new Transaction();
        foreach (var user in users)
        {
            txn.Touch(Relationship.FromTriple("document", "report", "viewer", "user", user));
        }

        var revision = await client.WriteAsync(txn);
        Assert.NotEmpty(revision);

        // Bulk check permissions
        var checks = users
            .Select(u => Relationship.FromTriple("document", "report", "view", "user", u))
            .ToArray();

        var results = await client.CheckPermissionsAsync(
            AtLeast(revision), "view", default, checks);

        for (var i = 0; i < users.Length; i++)
        {
            Assert.True(results[i], $"expected {users[i]} to have view permission");
        }
    }

    [Fact]
    public async Task CheckAll_AllPermitted()
    {
        await using var client = SpiceDBClient.CreatePlaintext("localhost:50051", "somerandomkeyhere");

        await client.WriteSchemaAsync(Schema);

        var users = new[] { "alice", "bob", "charlie" };
        var txn = new Transaction();
        foreach (var user in users)
        {
            txn.Touch(Relationship.FromTriple("document", "report", "viewer", "user", user));
        }

        var revision = await client.WriteAsync(txn);

        var checks = users
            .Select(u => Relationship.FromTriple("document", "report", "view", "user", u))
            .ToArray();

        var allAllowed = await client.CheckAllAsync(
            AtLeast(revision), "view", default, checks);

        Assert.True(allAllowed, "expected all users to have view permission");
    }

    [Fact]
    public async Task CheckAny_AtLeastOnePermitted()
    {
        await using var client = SpiceDBClient.CreatePlaintext("localhost:50051", "somerandomkeyhere");

        await client.WriteSchemaAsync(Schema);

        var txn = new Transaction();
        txn.Touch(Relationship.FromTriple("document", "report", "viewer", "user", "alice"));
        var revision = await client.WriteAsync(txn);

        var checks = new[]
        {
            Relationship.FromTriple("document", "report", "view", "user", "alice"),
            Relationship.FromTriple("document", "report", "view", "user", "nobody"),
        };

        var anyAllowed = await client.CheckAnyAsync(
            AtLeast(revision), "view", default, checks);

        Assert.True(anyAllowed, "expected at least one user to have view permission");
    }
}
