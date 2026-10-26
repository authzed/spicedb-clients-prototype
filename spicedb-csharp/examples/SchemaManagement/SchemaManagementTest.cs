using Xunit;
// Example SchemaManagement demonstrates reading and writing schema.

using SpiceDB.Client;

namespace SchemaManagement;

public class SchemaManagementTest
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
    public async Task WriteSchema_ReturnsRevision()
    {
        await using var client = SpiceDBClient.CreatePlaintext(SpiceDBTestServer.Endpoint, SpiceDBTestServer.Token);

        var revision = await client.WriteSchemaAsync(Schema, null, default);

        Assert.NotEmpty(revision.Value);
    }

    [Fact]
    public async Task ReadSchema_ReturnsWrittenSchema()
    {
        await using var client = SpiceDBClient.CreatePlaintext(SpiceDBTestServer.Endpoint, SpiceDBTestServer.Token);

        await client.WriteSchemaAsync(Schema, null, default);

        var (readSchema, readRevision) = await client.ReadSchemaAsync(null, default);

        Assert.NotEmpty(readRevision.Value);
        Assert.Contains("definition user", readSchema);
        Assert.Contains("definition document", readSchema);
    }
}
