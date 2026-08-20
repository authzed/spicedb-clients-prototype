using Xunit;
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
        await using var client = SpiceDBClient.CreatePlaintext(SpiceDBTestServer.Endpoint, SpiceDBTestServer.Token);

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
        await using var client = SpiceDBClient.CreatePlaintext(SpiceDBTestServer.Endpoint, SpiceDBTestServer.Token);

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

    // A failed precondition arrives with SpiceDB's structured explanation attached, not just a
    // message: the typed exception carries the authzed.api.v1.ErrorReason name and the metadata
    // naming which precondition did not hold, so recovery can be written against data rather than
    // against a parsed string.
    [Fact]
    public async Task WriteRelationships_FailedPreconditionCarriesTheReason()
    {
        await using var client = SpiceDBClient.CreatePlaintext(SpiceDBTestServer.Endpoint, SpiceDBTestServer.Token);

        await client.WriteSchemaAsync(Schema);

        var txn = new Transaction();
        txn.Touch(Relationship.FromTriple("document", "seconddoc", "viewer", "user", "alice"));
        txn.MustMatch(new Filter("document")
            .WithResourceID("firstdoc")
            .WithRelation("editor")
            .WithSubjectType("user")
            .WithSubjectID("nobody"));

        var ex = await Assert.ThrowsAsync<FailedPreconditionException>(
            () => client.WriteAsync(txn));

        Assert.Equal("ERROR_REASON_WRITE_OR_DELETE_PRECONDITION_FAILURE", ex.Reason);
        Assert.Equal("authzed.com", ex.ReasonDomain);
        Assert.Equal("firstdoc", ex.ReasonMetadata["precondition_resource_id"]);
    }
}
