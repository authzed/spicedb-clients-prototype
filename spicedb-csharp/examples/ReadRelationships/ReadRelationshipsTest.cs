using Xunit;
// Example ReadRelationships demonstrates reading relationships with an
// async enumerable.

using SpiceDB.Client;
using static SpiceDB.Client.Consistency;

namespace ReadRelationships;

public class ReadRelationshipsTest
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
    public async Task ReadRelationships_ReturnsMatchingRelationships()
    {
        await using var client = SpiceDBClient.CreatePlaintext(SpiceDBTestServer.Endpoint, SpiceDBTestServer.Token);

        await client.WriteSchemaAsync(Schema);

        var txn = new Transaction();
        txn.Touch(Relationship.FromTriple("document", "firstdoc", "viewer", "user", "alice"));
        txn.Touch(Relationship.FromTriple("document", "firstdoc", "viewer", "user", "bob"));
        await client.WriteAsync(txn);

        // Read relationships
        var filter = new Filter("document")
            .WithResourceID("firstdoc")
            .WithRelation("viewer");

        var relationships = new List<Relationship>();
        await foreach (var rel in client.ReadRelationshipsAsync(Full(), filter))
        {
            relationships.Add(rel);
        }

        Assert.True(relationships.Count >= 2, "expected at least two viewer relationships");

        var subjectIDs = relationships.Select(r => r.SubjectID).ToHashSet();
        Assert.Contains("alice", subjectIDs);
        Assert.Contains("bob", subjectIDs);
    }
}
