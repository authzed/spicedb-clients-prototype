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

    [Fact]
    public async Task CheckPermission_GrantedWhenCaveatContextSupplied()
    {
        // C5 payoff test: the same caveated relationship as
        // CheckPermission_ConditionalWhenCaveatContextMissing above, but this
        // time the caller supplies the "now" the server said was missing —
        // proving a caller can actually act on CheckResult.MissingContext and
        // resolve a ConditionalPermission into a real grant, not just
        // observe the conditional state.
        await using var client = SpiceDBClient.CreatePlaintext("localhost:50051", "somerandomkeyhere");

        await client.WriteSchemaAsync(Schema);

        var txn = new Transaction();
        txn.Touch(Relationship
            .FromTriple("document", "confidential-with-context", "caveated_viewer", "user", "alice")
            .WithCaveat("active"));
        await client.WriteAsync(txn);

        var rel = Relationship.FromTriple("document", "confidential-with-context", "view", "user", "alice");

        // caveat active(now int) { now < 100 } — 42 satisfies the caveat.
        var context = new Dictionary<string, object> { ["now"] = 42 };
        var result = await client.CheckPermissionAsync(Full(), "view", rel, default, context);

        Assert.Equal(Permissionship.HasPermission, result.Permissionship);
        Assert.True(result.HasPermission, "supplying the missing 'now' context should resolve the caveat to a grant");
    }

    [Fact]
    public async Task CheckPermissionsWithContext_ItemContextOverridesCallLevelDefault()
    {
        // Demonstrates both forms of caveat context together against a live
        // server: a call-level default applied via
        // CheckPermissionsWithContextAsync, and a per-item override via
        // Relationship.WithCheckContext that wins for its own item while a
        // sibling item still falls back to the call-level default.
        await using var client = SpiceDBClient.CreatePlaintext("localhost:50051", "somerandomkeyhere");

        await client.WriteSchemaAsync(Schema);

        var txn = new Transaction();
        txn.Touch(Relationship
            .FromTriple("document", "call-level-doc", "caveated_viewer", "user", "alice")
            .WithCaveat("active"));
        txn.Touch(Relationship
            .FromTriple("document", "per-item-doc", "caveated_viewer", "user", "alice")
            .WithCaveat("active"));
        await client.WriteAsync(txn);

        // caveat active(now int) { now < 100 }
        var callLevelContext = new Dictionary<string, object> { ["now"] = 42 };

        var relUsingCallLevelDefault = Relationship.FromTriple("document", "call-level-doc", "view", "user", "alice");
        var relWithPerItemOverride = Relationship
            .FromTriple("document", "per-item-doc", "view", "user", "alice")
            .WithCheckContext(new Dictionary<string, object> { ["now"] = 200 });

        var results = await client.CheckPermissionsWithContextAsync(
            Full(), "view", callLevelContext, default, relUsingCallLevelDefault, relWithPerItemOverride);

        Assert.True(results[0].HasPermission, "no per-item context, so it inherits the call-level default (now=42 < 100)");

        // The per-item context (now=200) OVERRODE the call-level default —
        // this is a definite caveat evaluation to false (NoPermission), not
        // ConditionalPermission, which proves the item's own value was
        // actually used rather than the call-level default winning instead.
        Assert.False(results[1].HasPermission, "per-item context overrides the call-level default (now=200 is NOT < 100)");
        Assert.Equal(Permissionship.NoPermission, results[1].Permissionship);
    }
}
