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
        await using var client = SpiceDBClient.CreatePlaintext(SpiceDBTestServer.Endpoint, SpiceDBTestServer.Token);

        // Setup: write schema and test data
        await client.WriteSchemaAsync(Schema, null, default);

        var txn = new Transaction();
        txn.Touch(new Relationship(new ObjectRef("document", "firstdoc"), "viewer", new SubjectRef(new ObjectRef("user", "alice"))));
        await client.WriteAsync(txn, null, default);

        // Check permission
        var rel = new Relationship(new ObjectRef("document", "firstdoc"), "view", new SubjectRef(new ObjectRef("user", "alice")));
        var result = await client.CheckAsync(PermissionCheck.CreateBuilder().WithResource(rel.Resource).WithPermission("view").WithSubject(rel.Subject).WithConsistency(Full()).Build(), new CheckOptions { Tracing = CheckTracing.Enabled }, default);
        Assert.NotNull(result.DebugTrace);
        Assert.NotEmpty(result.DebugTrace!.SchemaUsed);
        Assert.NotNull(result.DebugTrace.Check);
        Assert.Equal("view", result.DebugTrace.Check!.Permission);
        Assert.Equal("document", result.DebugTrace.Check.Resource!.ObjectType);


        // Always go through HasPermission — never treat the result itself as
        // a condition. A ConditionalPermission result would resolve to false
        // once evaluated, so only HasPermission is a safe boolean answer.
        Assert.True(result.HasPermission, "expected alice to have view permission");
    }

    [Fact]
    public async Task CheckPermission_Denied()
    {
        await using var client = SpiceDBClient.CreatePlaintext(SpiceDBTestServer.Endpoint, SpiceDBTestServer.Token);

        await client.WriteSchemaAsync(Schema, null, default);

        var txn = new Transaction();
        txn.Touch(new Relationship(new ObjectRef("document", "firstdoc"), "viewer", new SubjectRef(new ObjectRef("user", "alice"))));
        await client.WriteAsync(txn, null, default);

        // alice is only a viewer, not an editor
        var rel = new Relationship(new ObjectRef("document", "firstdoc"), "edit", new SubjectRef(new ObjectRef("user", "alice")));
        var result = await client.CheckAsync(new PermissionCheck(rel.Resource, "edit", rel.Subject, Full()), null, default);

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
        await using var client = SpiceDBClient.CreatePlaintext(SpiceDBTestServer.Endpoint, SpiceDBTestServer.Token);

        await client.WriteSchemaAsync(Schema, null, default);

        // Write the caveated relationship WITHOUT a stored caveat context —
        // "now" must come from context supplied at check time.
        var txn = new Transaction();
        txn.Touch(new Relationship(new ObjectRef("document", "confidential"), "caveated_viewer", new SubjectRef(new ObjectRef("user", "alice")))
            .WithCaveat("active"));
        await client.WriteAsync(txn, null, default);

        // Without check-time context, this caveat remains conditional.
        var rel = new Relationship(new ObjectRef("document", "confidential"), "view", new SubjectRef(new ObjectRef("user", "alice")));
        var result = await client.CheckAsync(new PermissionCheck(rel.Resource, "view", rel.Subject, Full()), null, default);

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
        await using var client = SpiceDBClient.CreatePlaintext(SpiceDBTestServer.Endpoint, SpiceDBTestServer.Token);

        await client.WriteSchemaAsync(Schema, null, default);

        var txn = new Transaction();
        txn.Touch(new Relationship(new ObjectRef("document", "confidential-with-context"), "caveated_viewer", new SubjectRef(new ObjectRef("user", "alice")))
            .WithCaveat("active"));
        await client.WriteAsync(txn, null, default);

        var rel = new Relationship(new ObjectRef("document", "confidential-with-context"), "view", new SubjectRef(new ObjectRef("user", "alice")));

        // caveat active(now int) { now < 100 } — 42 satisfies the caveat.
        var context = new Dictionary<string, object?> { ["now"] = 42 };
        var result = await client.CheckAsync(new PermissionCheck(rel.Resource, "view", rel.Subject, Full()), new CheckOptions { Context = context }, default);

        Assert.Equal(Permissionship.HasPermission, result.Permissionship);
        Assert.True(result.HasPermission, "supplying the missing 'now' context should resolve the caveat to a grant");
    }

    [Fact]
    public async Task CheckPermissionsWithContext_ItemContextOverridesCallLevelDefault()
    {
        // Demonstrates both forms of caveat context together against a live
        // server: a call-level default applied via
        // CheckBulkAsync, and a per-item override via
        // PermissionCheck.WithContext that wins for its own item while a
        // sibling item still falls back to the call-level default.
        await using var client = SpiceDBClient.CreatePlaintext(SpiceDBTestServer.Endpoint, SpiceDBTestServer.Token);

        await client.WriteSchemaAsync(Schema, null, default);

        var txn = new Transaction();
        txn.Touch(new Relationship(new ObjectRef("document", "call-level-doc"), "caveated_viewer", new SubjectRef(new ObjectRef("user", "alice")))
            .WithCaveat("active"));
        txn.Touch(new Relationship(new ObjectRef("document", "per-item-doc"), "caveated_viewer", new SubjectRef(new ObjectRef("user", "alice")))
            .WithCaveat("active"));
        await client.WriteAsync(txn, null, default);

        // caveat active(now int) { now < 100 }
        var callLevelContext = new Dictionary<string, object?> { ["now"] = 42 };

        var relUsingCallLevelDefault = new Relationship(new ObjectRef("document", "call-level-doc"), "view", new SubjectRef(new ObjectRef("user", "alice")));
        var relWithPerItemOverride = new Relationship(new ObjectRef("document", "per-item-doc"), "view", new SubjectRef(new ObjectRef("user", "alice")));
        var overrideCheck = new PermissionCheck(relWithPerItemOverride.Resource, "view", relWithPerItemOverride.Subject, Full()).WithContext(new Dictionary<string, object?> { ["now"] = 200 });

        var results = await client.CheckBulkAsync([new PermissionCheck(relUsingCallLevelDefault.Resource, "view", relUsingCallLevelDefault.Subject, Full()), overrideCheck], new CheckOptions { Context = callLevelContext }, default);

        Assert.True(results[0].HasPermission, "no per-item context, so it inherits the call-level default (now=42 < 100)");

        // The per-item context (now=200) OVERRODE the call-level default —
        // this is a definite caveat evaluation to false (NoPermission), not
        // ConditionalPermission, which proves the item's own value was
        // actually used rather than the call-level default winning instead.
        Assert.False(results[1].HasPermission, "per-item context overrides the call-level default (now=200 is NOT < 100)");
        Assert.Equal(Permissionship.NoPermission, results[1].Permissionship);
    }
}
