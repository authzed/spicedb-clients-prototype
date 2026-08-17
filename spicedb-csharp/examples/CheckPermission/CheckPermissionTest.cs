using Xunit;
// Example CheckPermission demonstrates checking a single permission.

using SpiceDB.Client;
using static SpiceDB.Client.Consistency;

namespace CheckPermission;

public class CheckPermissionTest
{
    private const string Schema = """
        definition user {}

        caveat active(now int) {
            now < 100
        }

        definition document {
            relation viewer: user
            relation editor: user
            relation caveated_viewer: user with active
            permission view = viewer + editor + caveated_viewer
            permission edit = editor
        }
        """;

    [Fact]
    public async Task CheckPermission_Granted()
    {
        await using var client = SpiceDBClient.CreatePlaintext("localhost:50051", "somerandomkeyhere");

        // Setup: write schema and test data
        await client.WriteSchemaAsync(Schema);

        var txn = new Transaction();
        txn.Touch(Relationship.FromTriple("document", "firstdoc", "viewer", "user", "alice"));
        await client.WriteAsync(txn);

        // Check permission
        var rel = Relationship.FromTriple("document", "firstdoc", "view", "user", "alice");
        var result = await client.CheckPermissionAsync(Full(), "view", rel);

        // Always go through HasPermission — never treat the result itself as
        // a condition. A ConditionalPermission result would resolve to false
        // once evaluated, so only HasPermission is a safe boolean answer.
        Assert.True(result.HasPermission, "expected alice to have view permission");
    }

    [Fact]
    public async Task CheckPermission_Denied()
    {
        await using var client = SpiceDBClient.CreatePlaintext("localhost:50051", "somerandomkeyhere");

        await client.WriteSchemaAsync(Schema);

        var txn = new Transaction();
        txn.Touch(Relationship.FromTriple("document", "firstdoc", "viewer", "user", "alice"));
        await client.WriteAsync(txn);

        // alice is only a viewer, not an editor
        var rel = Relationship.FromTriple("document", "firstdoc", "edit", "user", "alice");
        var result = await client.CheckPermissionAsync(Full(), "edit", rel);

        Assert.False(result.HasPermission, "expected alice to NOT have edit permission");
        Assert.Equal(Permissionship.NoPermission, result.Permissionship);
    }

    [Fact]
    public async Task CheckPermission_ConditionalWhenCaveatContextMissing()
    {
        // A caveated relationship whose caveat context isn't supplied at
        // check time is exactly where the server says "I need more
        // information" — it is NOT a grant. See root DESIGN.md, "RULE: Only
        // an unconditional grant is true". Uses the shared Schema's
        // `caveated_viewer` relation (caveat `active`) so this test can run
        // against the same live server/schema as the other tests in this
        // class without deleting the `document` definition out from under
        // their relationships.
        await using var client = SpiceDBClient.CreatePlaintext("localhost:50051", "somerandomkeyhere");

        await client.WriteSchemaAsync(Schema);

        // Write the caveated relationship WITHOUT a stored caveat context —
        // "now" must come from context supplied at check time.
        var txn = new Transaction();
        txn.Touch(Relationship
            .FromTriple("document", "confidential", "caveated_viewer", "user", "alice")
            .WithCaveat("active"));
        await client.WriteAsync(txn);

        // Check with no context (this client's CheckPermissionAsync does not
        // currently accept caveat context, so this is inherently "no
        // context" today) — the caveat cannot be evaluated.
        var rel = Relationship.FromTriple("document", "confidential", "view", "user", "alice");
        var result = await client.CheckPermissionAsync(Full(), "view", rel);

        Assert.Equal(Permissionship.ConditionalPermission, result.Permissionship);
        Assert.False(result.HasPermission, "a conditional result must never be treated as a grant");
        Assert.Contains("now", result.MissingContext);
    }
}
