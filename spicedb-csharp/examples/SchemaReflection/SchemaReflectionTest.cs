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
        await using var client = SpiceDBClient.CreatePlaintext(SpiceDBTestServer.Endpoint, SpiceDBTestServer.Token);

        await SpiceDBTestServer.ClearDocumentRelationshipsAsync(client);
        await client.WriteSchemaAsync(Schema);

        var result = await client.ReflectSchemaAsync(Full());

        Assert.NotEmpty(result.Revision);

        // The exact names, not just how many. Counting alone passes on a
        // reflection that conflated relations with permissions, or that
        // returned the right number of empty-named entries.
        Assert.Equal(
            new[] { "document", "user" },
            result.Definitions.Select(d => d.Name).OrderBy(n => n).ToArray());

        var docDef = result.Definitions.FirstOrDefault(d => d.Name == "document");
        Assert.NotNull(docDef);
        Assert.Equal(
            new[] { "editor", "viewer" },
            docDef.Relations.Select(r => r.Name).OrderBy(n => n).ToArray());
        Assert.Equal(
            new[] { "edit", "view" },
            docDef.Permissions.Select(p => p.Name).OrderBy(n => n).ToArray());
    }

    [Fact]
    public async Task ComputablePermissions_ForRelation()
    {
        await using var client = SpiceDBClient.CreatePlaintext(SpiceDBTestServer.Endpoint, SpiceDBTestServer.Token);

        await SpiceDBTestServer.ClearDocumentRelationshipsAsync(client);
        await client.WriteSchemaAsync(Schema);

        var (perms, revision) = await client.ComputablePermissionsAsync(
            Full(), "document", "viewer");

        Assert.NotEmpty(revision);

        // `viewer` appears in `view` and nowhere else in this schema, so the
        // answer is exactly one reference -- and it names the permission rather
        // than the relation it was asked about.
        Assert.Equal(
            new[] { "document#view" },
            perms.Select(p => $"{p.DefinitionName}#{p.RelationName}").ToArray());
        Assert.True(perms[0].IsPermission, "expected document#view to be reported as a permission");
    }

    [Fact]
    public async Task DependentRelations_ForPermission()
    {
        await using var client = SpiceDBClient.CreatePlaintext(SpiceDBTestServer.Endpoint, SpiceDBTestServer.Token);

        await SpiceDBTestServer.ClearDocumentRelationshipsAsync(client);
        await client.WriteSchemaAsync(Schema);

        var (deps, revision) = await client.DependentRelationsAsync(
            Full(), "document", "view");

        Assert.NotEmpty(revision);

        // `view = viewer + editor`, so both relations are dependencies and
        // nothing else is.
        Assert.Equal(
            new[] { "document#editor", "document#viewer" },
            deps.Select(d => $"{d.DefinitionName}#{d.RelationName}").OrderBy(n => n).ToArray());
    }

    [Fact]
    public async Task DiffSchema_DetectsChanges()
    {
        await using var client = SpiceDBClient.CreatePlaintext(SpiceDBTestServer.Endpoint, SpiceDBTestServer.Token);

        await SpiceDBTestServer.ClearDocumentRelationshipsAsync(client);
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

        // newSchema adds one relation and one permission and changes the
        // expression of the other two, so the diff is a known set. Asserting
        // only that it is non-empty would pass on a diff that reported every
        // kind as "unknown" -- the exact outcome of SchemaDiff.FromProto
        // falling through to its default branch.
        var tuples = diffs
            .Select(d => (d.Kind, d.DefinitionName, d.RelationName, d.PermissionName))
            .ToList();
        Assert.Contains(("relation_added", "document", "admin", ""), tuples);
        Assert.Contains(("permission_added", "document", "", "manage"), tuples);
        Assert.Contains(("permission_expr_changed", "document", "", "view"), tuples);
        Assert.DoesNotContain(diffs, d => d.Kind == "unknown");
    }
}
