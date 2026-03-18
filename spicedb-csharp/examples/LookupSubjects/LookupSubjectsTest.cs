using Xunit;
// Example LookupSubjects demonstrates finding subjects with access to a resource.

using SpiceDB.Client;
using static SpiceDB.Client.Consistency;

namespace LookupSubjects;

public class LookupSubjectsTest
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
    public async Task LookupSubjects_FindsUsersWithAccess()
    {
        await using var client = SpiceDBClient.CreatePlaintext("localhost:50051", "somerandomkeyhere");

        await client.WriteSchemaAsync(Schema);

        var txn = new Transaction();
        txn.Touch(Relationship.FromTriple("document", "firstdoc", "viewer", "user", "alice"));
        txn.Touch(Relationship.FromTriple("document", "firstdoc", "editor", "user", "bob"));
        await client.WriteAsync(txn);

        // Lookup subjects who can view the document
        var subjectIDs = new HashSet<string>();
        await foreach (var subjectID in client.LookupSubjectsAsync(
            Full(), "document", "firstdoc", "view", "user"))
        {
            subjectIDs.Add(subjectID);
        }

        Assert.Contains("alice", subjectIDs);
        Assert.Contains("bob", subjectIDs); // editor implies view
    }
}
