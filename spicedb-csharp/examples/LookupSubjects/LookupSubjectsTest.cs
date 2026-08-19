using Xunit;
// Example LookupSubjects demonstrates finding subjects with access to a
// resource, including the wildcard/excluded-subjects case: when the server
// resolves a wildcard "*" subject, ExcludedSubjects lists the subjects carved
// out of that wildcard grant. Treating a wildcard match as "everyone has
// access" without checking ExcludedSubjects is a real over-grant risk -- and
// until this example wrote a wildcard, no C# example wrote one at all, so
// `Assert.Empty(result.ExcludedSubjects)` against a schema with no wildcards
// was the only thing exercising that field. Dropped exclusions would have
// turned a partial grant into a blanket grant with nothing to catch it.

using SpiceDB.Client;
using static SpiceDB.Client.Consistency;

namespace LookupSubjects;

public class LookupSubjectsTest
{
    // `banned` carves subjects back out of the public/wildcard viewer grant.
    private const string Schema = """
        definition user {}

        definition document {
            relation viewer: user | user:*
            relation editor: user
            relation banned: user
            permission view = (viewer + editor) - banned
            permission edit = editor
        }
        """;

    [Fact]
    public async Task LookupSubjects_FindsUsersWithAccess()
    {
        await using var client = SpiceDBClient.CreatePlaintext(SpiceDBTestServer.Endpoint, SpiceDBTestServer.Token);

        // All thirteen example projects share one SpiceDB, and this schema
        // differs from the one the others write. SpiceDB refuses a WriteSchema
        // that drops a relation while a relationship still exists under it, so
        // clear first.
        await SpiceDBTestServer.ClearDocumentRelationshipsAsync(client);
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
            Assert.Empty(result.ExcludedSubjects); // no wildcard relationships yet
            Assert.NotEmpty(result.LookedUpAt);
            subjectIDs.Add(result.Subject.SubjectID);
        }

        // The exact set, not merely "alice and bob are somewhere in it": a
        // lookup that ignored the permission and returned every subject on the
        // document would satisfy two Contains checks.
        Assert.Equal(new HashSet<string> { "alice", "bob" }, subjectIDs); // editor implies view
    }

    [Fact]
    public async Task LookupSubjects_WildcardCarriesItsExclusions()
    {
        await using var client = SpiceDBClient.CreatePlaintext(SpiceDBTestServer.Endpoint, SpiceDBTestServer.Token);

        await SpiceDBTestServer.ClearDocumentRelationshipsAsync(client);
        await client.WriteSchemaAsync(Schema);

        var txn = new Transaction();
        // Grant view to every user (wildcard), except those in `banned`.
        txn.Touch(Relationship.FromTriple("document", "publicdoc", "viewer", "user", "*"));
        txn.Touch(Relationship.FromTriple("document", "publicdoc", "banned", "user", "eve"));
        await client.WriteAsync(txn);

        var sawWildcard = false;
        var excludedIDs = new HashSet<string>();
        await foreach (var result in client.LookupSubjectsAsync(
            Full(), "document", "publicdoc", "view", "user"))
        {
            if (result.Subject.SubjectID != "*")
            {
                continue;
            }

            sawWildcard = true;
            // This is the over-grant-risk case: "*" alone would mean "every
            // user", but ExcludedSubjects carves specific subjects back out.
            // Never grant access on the wildcard match alone.
            foreach (var excluded in result.ExcludedSubjects)
            {
                excludedIDs.Add(excluded.SubjectID);
            }
        }

        Assert.True(sawWildcard, "expected a wildcard (*) subject in the results");
        Assert.Contains("eve", excludedIDs);

        // Leave the shared state as the other examples expect it.
        await SpiceDBTestServer.ClearDocumentRelationshipsAsync(client);
    }
}
