using Xunit;
// Example SchemaReflection demonstrates using schema reflection APIs to
// inspect definitions, compute permissions, find dependent relations, and
// diff schemas.

using SpiceDB.Client;
using static SpiceDB.Client.Consistency;

namespace SchemaReflection;

public class SchemaReflectionTest
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
    public async Task ReflectSchema_ReturnsDefinitions()
    {
        await using var client = SpiceDBClient.CreatePlaintext("localhost:50051", "somerandomkeyhere");

        await client.WriteSchemaAsync(Schema);

        var result = await client.ReflectSchemaAsync(Full());

        Assert.NotEmpty(result.Revision);
        Assert.True(result.Definitions.Count >= 2, "expected at least two definitions");

        var docDef = result.Definitions.FirstOrDefault(d => d.Name == "document");
        Assert.NotNull(docDef);
        Assert.Equal(2, docDef.Relations.Count);
        Assert.Equal(2, docDef.Permissions.Count);
    }

    [Fact]
    public async Task ComputablePermissions_ForRelation()
    {
        await using var client = SpiceDBClient.CreatePlaintext("localhost:50051", "somerandomkeyhere");

        await client.WriteSchemaAsync(Schema);

        var (perms, revision) = await client.ComputablePermissionsAsync(
            Full(), "document", "viewer");

        Assert.NotEmpty(revision);
        Assert.NotEmpty(perms);
    }

    [Fact]
    public async Task DependentRelations_ForPermission()
    {
        await using var client = SpiceDBClient.CreatePlaintext("localhost:50051", "somerandomkeyhere");

        await client.WriteSchemaAsync(Schema);

        var (deps, revision) = await client.DependentRelationsAsync(
            Full(), "document", "view");

        Assert.NotEmpty(revision);
        Assert.NotEmpty(deps);
    }

    [Fact]
    public async Task DiffSchema_DetectsChanges()
    {
        await using var client = SpiceDBClient.CreatePlaintext("localhost:50051", "somerandomkeyhere");

        await client.WriteSchemaAsync(Schema);

        // Diff against a modified schema that adds a new relation and permission
        var newSchema = """
            definition user {}

            definition document {
                relation viewer: user
                relation editor: user
                relation admin: user
                permission view = viewer + editor + admin
                permission edit = editor + admin
                permission manage = admin
            }
            """;

        var (diffs, revision) = await client.DiffSchemaAsync(Full(), newSchema);

        Assert.NotEmpty(revision);
        Assert.NotEmpty(diffs);
    }
}
