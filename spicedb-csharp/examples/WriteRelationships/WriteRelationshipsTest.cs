// Example WriteRelationships demonstrates writing relationships with a
// transaction builder.

using SpiceDB.Client;

namespace WriteRelationships;

public class WriteRelationshipsTest
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
    public async Task WriteRelationships_ReturnsRevision()
    {
        await using var client = SpiceDBClient.CreatePlaintext("localhost:50051", "somerandomkeyhere");

        await client.WriteSchemaAsync(Schema);

        // Write relationships with transaction builder
        var txn = new Transaction();
        txn.Touch(Relationship.FromTriple("document", "firstdoc", "viewer", "user", "alice"));
        txn.Touch(Relationship.FromTriple("document", "firstdoc", "editor", "user", "bob"));

        var revision = await client.WriteAsync(txn);

        Assert.NotEmpty(revision);
    }

    [Fact]
    public async Task WriteRelationships_WithPrecondition()
    {
        await using var client = SpiceDBClient.CreatePlaintext("localhost:50051", "somerandomkeyhere");

        await client.WriteSchemaAsync(Schema);

        // Write with a precondition that no owner exists
        var txn = new Transaction();
        txn.Touch(Relationship.FromTriple("document", "firstdoc", "viewer", "user", "alice"));
        txn.MustNotMatch(new Filter("document")
            .WithResourceID("firstdoc")
            .WithRelation("editor")
            .WithSubjectType("user")
            .WithSubjectID("mallory"));

        var revision = await client.WriteAsync(txn);

        Assert.NotEmpty(revision);
    }
}
