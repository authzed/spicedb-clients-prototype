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

        await client.WriteSchemaAsync(Schema, null, default);

        // Write relationships with transaction builder
        var txn = new Transaction();
        txn.Touch(new Relationship(new ObjectRef("document", "firstdoc"), "viewer", new SubjectRef(new ObjectRef("user", "alice"))));
        txn.Touch(new Relationship(new ObjectRef("document", "firstdoc"), "editor", new SubjectRef(new ObjectRef("user", "bob"))));

        var revision = (await client.WriteAsync(txn, null, default)).Revision;

        Assert.NotEmpty(revision.Value);
    }

    [Fact]
    public async Task WriteRelationships_WithPrecondition()
    {
        await using var client = SpiceDBClient.CreatePlaintext(SpiceDBTestServer.Endpoint, SpiceDBTestServer.Token);

        await client.WriteSchemaAsync(Schema, null, default);

        // Write with a precondition that no owner exists
        var txn = new Transaction();
        txn.Touch(new Relationship(new ObjectRef("document", "firstdoc"), "viewer", new SubjectRef(new ObjectRef("user", "alice"))));
        txn.MustNotMatch(new Filter("document")
            .WithResourceId("firstdoc")
            .WithRelation("editor")
            .WithSubjectType("user")
            .WithSubjectId("mallory"));

        var revision = (await client.WriteAsync(txn, null, default)).Revision;

        Assert.NotEmpty(revision.Value);
    }

    // An empty write is sent like the raw API's: SpiceDB evaluates its preconditions and returns
    // the current revision, so it doubles as a guarded "assert and get a revision" call.
    [Fact]
    public async Task WriteRelationships_EmptyWriteChecksPreconditionsAndReturnsARevision()
    {
        await using var client = SpiceDBClient.CreatePlaintext(SpiceDBTestServer.Endpoint, SpiceDBTestServer.Token);
        await client.WriteSchemaAsync(Schema, null, default);

        Assert.NotEmpty((await client.WriteAsync(new Transaction(), null, default)).Revision.Value);

        var guarded = new Transaction();
        guarded.MustMatch(new Filter("document").WithResourceId("never-written"));
        await Assert.ThrowsAsync<FailedPreconditionException>(() => client.WriteAsync(guarded, null, default));
    }

    // A failed precondition arrives with SpiceDB's structured explanation attached, not just a
    // message: the typed exception carries the authzed.api.v1.ErrorReason name and the metadata
    // naming which precondition did not hold, so recovery can be written against data rather than
    // against a parsed string.
    [Fact]
    public async Task WriteRelationships_FailedPreconditionCarriesTheReason()
    {
        await using var client = SpiceDBClient.CreatePlaintext(SpiceDBTestServer.Endpoint, SpiceDBTestServer.Token);

        await client.WriteSchemaAsync(Schema, null, default);

        var txn = new Transaction();
        txn.Touch(new Relationship(new ObjectRef("document", "seconddoc"), "viewer", new SubjectRef(new ObjectRef("user", "alice"))));
        txn.MustMatch(new Filter("document")
            .WithResourceId("firstdoc")
            .WithRelation("editor")
            .WithSubjectType("user")
            .WithSubjectId("nobody"));

        var ex = await Assert.ThrowsAsync<FailedPreconditionException>(
            () => client.WriteAsync(txn, null, default));

        Assert.Equal("ERROR_REASON_WRITE_OR_DELETE_PRECONDITION_FAILURE", ex.Reason);
        Assert.Equal("authzed.com", ex.ReasonDomain);
        Assert.Equal("firstdoc", ex.ReasonMetadata["precondition_resource_id"]);
    }
}
