using Xunit;
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
        await using var client = SpiceDBClient.CreatePlaintext(SpiceDBTestServer.Endpoint, SpiceDBTestServer.Token);

        await client.WriteSchemaAsync(Schema, null, default);

        var txn = new Transaction();
        txn.Touch(new Relationship(new ObjectRef("document", "firstdoc"), "viewer", new SubjectRef(new ObjectRef("user", "alice"))));
        txn.Touch(new Relationship(new ObjectRef("document", "seconddoc"), "editor", new SubjectRef(new ObjectRef("user", "alice"))));
        await client.WriteAsync(txn, null, default);

        // Lookup resources alice can view. Each result is a native
        // LookupResource carrying the resource ID plus permissionship — check
        // Permissionship before treating a result as a full grant, since
        // ConditionalPermission results depend on caveat context that was not
        // fully evaluated by the server.
        var resourceIDs = new HashSet<string>();
        await foreach (var result in client.LookupResourcesAsync(Full(), "document", "view", new SubjectRef(new ObjectRef("user", "alice")), new LookupOptions { PermissionshipFilter = LookupPermissionshipFilter.HasPermissionOnly }, default))
        {
            Assert.Equal(Permissionship.HasPermission, result.Permissionship);
            if (result.Permissionship == Permissionship.HasPermission)
                resourceIDs.Add(result.ResourceId); // Also safe with the default All mode.
        }

        Assert.Contains("firstdoc", resourceIDs);
        Assert.Contains("seconddoc", resourceIDs);
    }

    // Looks up across several pages with a limit that is not a multiple of the page size,
    // then resumes from an item's cursor. Every page repeats the caller's consistency and
    // page size, which SpiceDB requires for the cursor to be accepted.
    [Fact]
    public async Task LookupResources_PagesAndResumesFromCursor()
    {
        await using var client = SpiceDBClient.CreatePlaintext(SpiceDBTestServer.Endpoint, SpiceDBTestServer.Token);
        await client.WriteSchemaAsync(Schema, null, default);
        var txn = new Transaction();
        for (var i = 0; i < 7; i++) txn.Touch(Relationship.FromTuple($"document:lr{i}#viewer@user:pager"));
        var written = await client.WriteAsync(txn, null, default);
        var pager = SubjectRef.Direct(new ObjectRef("user", "pager"));

        var all = new List<LookupResource>();
        await foreach (var r in client.LookupResourcesAsync(AtLeast(written.Revision), "document", "view", pager, new LookupOptions { PageSize = 2 }, default)) all.Add(r);
        Assert.Equal(7, all.Select(r => r.ResourceId).Distinct().Count());

        var first = new List<LookupResource>();
        await foreach (var r in client.LookupResourcesAsync(Full(), "document", "view", pager, new LookupOptions { PageSize = 2, Limit = 3 }, default)) first.Add(r);
        Assert.Equal(3, first.Count);
        Assert.NotNull(first[2].AfterResultCursor);
        var rest = new List<LookupResource>();
        await foreach (var r in client.LookupResourcesAsync(Full(), "document", "view", pager, new LookupOptions { PageSize = 2, ResumeCursor = first[2].AfterResultCursor }, default)) rest.Add(r);
        Assert.Equal(7, first.Concat(rest).Select(r => r.ResourceId).Distinct().Count());

        // Single-request mode sends one request like the raw API: the page size is ignored,
        // the limit (or none) goes on the request, and no follow-up page is fetched.
        var single = new List<LookupResource>();
        await foreach (var r in client.LookupResourcesAsync(AtLeast(written.Revision), "document", "view", pager, new LookupOptions { Paging = Paging.SingleRequest, PageSize = 2 }, default)) single.Add(r);
        Assert.Equal(7, single.Select(r => r.ResourceId).Distinct().Count());
        var limited = new List<LookupResource>();
        await foreach (var r in client.LookupResourcesAsync(AtLeast(written.Revision), "document", "view", pager, new LookupOptions { Paging = Paging.SingleRequest, Limit = 3 }, default)) limited.Add(r);
        Assert.Equal(3, limited.Count);
        Assert.NotNull(limited[2].AfterResultCursor);
    }
}
