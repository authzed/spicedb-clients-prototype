using Xunit;
// Example BulkOperations demonstrates bulk checks, batch writes, and bulk
// relationship import/export.

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

        // Bulk check permissions. Inputs over 1,000 relationships are split
        // into one CheckBulkPermissions request per 1,000 automatically and
        // the results concatenated in input order — SpiceDB rejects a single
        // request carrying more than 10,000. Nothing here changes for a
        // larger array.
        var checks = users
            .Select(u => Relationship.FromTriple("document", "report", "view", "user", u))
            .ToArray();

        var results = await client.CheckPermissionsAsync(
            AtLeast(revision), "view", default, checks);

        for (var i = 0; i < users.Length; i++)
        {
            // Always go through HasPermission — never treat a CheckResult
            // itself as a condition.
            Assert.True(results[i].HasPermission, $"expected {users[i]} to have view permission");
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

    [Fact]
    public async Task ImportRelationships_And_ExportRelationships()
    {
        await using var client = SpiceDBClient.CreatePlaintext("localhost:50051", "somerandomkeyhere");

        await client.WriteSchemaAsync(Schema);

        // Bulk import relationships from an async stream. Relationships are
        // automatically batched into chunks of 1,000 under the hood.
        var users = new[] { "alice", "bob", "charlie" };
        var imported = users
            .Select(u => Relationship.FromTriple("document", "imported-doc", "viewer", "user", u))
            .ToArray();

        var numLoaded = await client.ImportRelationshipsAsync(ToAsyncEnumerable(imported));
        Assert.Equal((ulong)imported.Length, numLoaded);

        // Bulk export relationships back out, filtered to the resource we
        // just imported. Cursors are handled transparently by the client.
        var filter = new Filter("document").WithResourceID("imported-doc");

        var exported = new List<Relationship>();
        await foreach (var rel in client.ExportRelationshipsAsync(Full(), filter))
        {
            exported.Add(rel);
        }

        var exportedSubjectIDs = exported.Select(r => r.SubjectID).ToHashSet();
        foreach (var user in users)
        {
            Assert.Contains(user, exportedSubjectIDs);
        }
    }

    private static async IAsyncEnumerable<Relationship> ToAsyncEnumerable(
        IEnumerable<Relationship> relationships)
    {
        foreach (var rel in relationships)
        {
            yield return rel;
        }

        await Task.CompletedTask;
    }
}
