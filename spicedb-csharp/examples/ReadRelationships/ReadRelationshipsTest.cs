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

        await client.WriteSchemaAsync(Schema, null, default);

        var txn = new Transaction();
        txn.Touch(new Relationship(new ObjectRef("document", "firstdoc"), "viewer", new SubjectRef(new ObjectRef("user", "alice"))));
        txn.Touch(new Relationship(new ObjectRef("document", "firstdoc"), "viewer", new SubjectRef(new ObjectRef("user", "bob"))));
        await client.WriteAsync(txn, null, default);

        // Read relationships
        var filter = new Filter("document")
            .WithResourceId("firstdoc")
            .WithRelation("viewer");

        var relationships = new List<Relationship>();
        await foreach (var rel in client.ReadRelationshipsAsync(Full(), filter, null, default))
        {
            relationships.Add(rel.Relationship);
        }

        Assert.True(relationships.Count >= 2, "expected at least two viewer relationships");

        var subjectIDs = relationships.Select(r => r.Subject.Object.ObjectId).ToHashSet();
        Assert.Contains("alice", subjectIDs);
        Assert.Contains("bob", subjectIDs);
    }

    // Reads across several pages. SpiceDB hashes the consistency and page size into every
    // cursor, so a pager that changed either on page two failed with "the cursor provided
    // does not have the same arguments as the original API call". Each item carries its
    // after-result cursor and read-at revision; a read resumes from a cursor.
    [Fact]
    public async Task ReadRelationships_PagesResumeAndExposeCursors()
    {
        await using var client = SpiceDBClient.CreatePlaintext(SpiceDBTestServer.Endpoint, SpiceDBTestServer.Token);
        await client.WriteSchemaAsync(Schema, null, default);

        var txn = new Transaction();
        for (var i = 0; i < 7; i++)
            txn.Touch(Relationship.FromTuple($"document:paged#viewer@user:u{i}"));
        var written = await client.WriteAsync(txn, null, default);

        var filter = new Filter("document").WithResourceId("paged");
        foreach (var consistency in new[] { Full(), AtLeast(written.Revision) })
        {
            var all = new List<ReadRelationship>();
            await foreach (var item in client.ReadRelationshipsAsync(consistency, filter, new ReadOptions { PageSize = 2 }, default))
                all.Add(item);
            Assert.Equal(7, all.Count);
            Assert.All(all, item => Assert.False(string.IsNullOrEmpty(item.AfterResultCursor)));
            Assert.All(all, item => Assert.False(string.IsNullOrEmpty(item.ReadAt.Value)));
        }

        // A total limit that is not a multiple of the page size is enforced client-side.
        var limited = new List<ReadRelationship>();
        await foreach (var item in client.ReadRelationshipsAsync(Full(), filter, new ReadOptions { PageSize = 2, Limit = 3 }, default))
            limited.Add(item);
        Assert.Equal(3, limited.Count);

        // Resume after the third item, repeating the request and page size.
        var resumed = new List<ReadRelationship>();
        await foreach (var item in client.ReadRelationshipsAsync(Full(), filter, new ReadOptions { PageSize = 2, ResumeCursor = limited[2].AfterResultCursor }, default))
            resumed.Add(item);
        Assert.Equal(4, resumed.Count);
        Assert.Empty(limited.Select(r => r.Relationship.SubjectId).Intersect(resumed.Select(r => r.Relationship.SubjectId)));
    }
}
