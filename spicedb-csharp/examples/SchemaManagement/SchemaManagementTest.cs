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
        await using var client = SpiceDBClient.CreatePlaintext("localhost:50051", "somerandomkeyhere");

        var revision = await client.WriteSchemaAsync(Schema);

        Assert.NotEmpty(revision);
    }

    [Fact]
    public async Task ReadSchema_ReturnsWrittenSchema()
    {
        await using var client = SpiceDBClient.CreatePlaintext("localhost:50051", "somerandomkeyhere");

        await client.WriteSchemaAsync(Schema);

        var (readSchema, readRevision) = await client.ReadSchemaAsync();

        Assert.NotEmpty(readRevision);
        Assert.Contains("definition user", readSchema);
        Assert.Contains("definition document", readSchema);
    }
}
