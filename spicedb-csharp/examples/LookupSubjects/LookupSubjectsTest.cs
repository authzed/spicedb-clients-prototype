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
        await using var client = SpiceDBClient.CreatePlaintext(SpiceDBTestServer.Endpoint, SpiceDBTestServer.Token);

        await client.WriteSchemaAsync(Schema);

        var txn = new Transaction();
        txn.Touch(Relationship.FromTriple("document", "firstdoc", "viewer", "user", "alice"));
        txn.Touch(Relationship.FromTriple("document", "firstdoc", "editor", "user", "bob"));
        await client.WriteAsync(txn);

        // Lookup subjects who can view the document. Each result is a native
        // LookupSubject: Subject carries the subject ID plus permissionship,
        // and ExcludedSubjects lists subjects excluded from a wildcard "*"
        // match — callers MUST check ExcludedSubjects before treating a
        // wildcard Subject as a blanket grant.
        var subjectIDs = new HashSet<string>();
        await foreach (var result in client.LookupSubjectsAsync(
            Full(), "document", "firstdoc", "view", "user"))
        {
            Assert.Equal(Permissionship.HasPermission, result.Subject.Permissionship);
            Assert.Empty(result.ExcludedSubjects); // no wildcard relationships in this example
            subjectIDs.Add(result.Subject.SubjectID);
        }

        Assert.Contains("alice", subjectIDs);
        Assert.Contains("bob", subjectIDs); // editor implies view
    }
}
