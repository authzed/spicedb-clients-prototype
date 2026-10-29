using FluentAssertions;
using Grpc.Core;
using SpiceDB.Client.Testing;
using Xunit;

namespace SpiceDB.Client.Tests;

/// <summary>
/// The double resolves a check in a fixed order (a registered result, then the resolver, then
/// the stored relationships, then no permission) and serves LookupResources and LookupSubjects
/// through the same resolution, so tool and service tests over lookups need no fake server.
/// </summary>
public class InMemoryCheckResolutionTests
{
    private static readonly ObjectRef Doc1 = new("document", "1");
    private static readonly ObjectRef AliceObject = new("user", "alice");
    private static readonly SubjectRef Alice = AliceObject;
    private static readonly SubjectRef Bob = new ObjectRef("user", "bob");

    private static PermissionCheck Check(ObjectRef resource, string permission, SubjectRef subject) =>
        new(resource, permission, subject, Consistency.Full());

    private static async Task<InMemorySpiceDBClient> Seeded(params string[] tuples)
    {
        var store = new InMemorySpiceDBClient();
        var txn = new Transaction();
        foreach (var t in tuples) txn.Touch(Relationship.FromTuple(t));
        await store.WriteAsync(txn);
        return store;
    }

    private static async Task<List<T>> All<T>(IAsyncEnumerable<T> items)
    {
        var list = new List<T>();
        await foreach (var item in items) list.Add(item);
        return list;
    }

    [Fact]
    public async Task StoredDirectRelationshipsGrantByRelationNameExactSubjectOrWildcard()
    {
        var store = await Seeded("document:1#view@user:alice", "document:2#view@user:*", "document:3#view@team:eng#member", "document:4#edit@user:alice");
        (await store.CheckAsync(Check(Doc1, "view", Alice))).Permissionship.Should().Be(Permissionship.HasPermission);
        (await store.CheckAsync(Check(Doc1, "view", Bob))).Permissionship.Should().Be(Permissionship.NoPermission);
        (await store.CheckAsync(Check(new ObjectRef("document", "2"), "view", Bob))).HasPermission.Should().BeTrue();
        // A wildcard grants direct subjects only.
        (await store.CheckAsync(Check(new ObjectRef("document", "2"), "view", SubjectRef.Named(new ObjectRef("user", "bob"), "member")))).HasPermission.Should().BeFalse();
        (await store.CheckAsync(Check(new ObjectRef("document", "3"), "view", SubjectRef.Named(new ObjectRef("team", "eng"), "member")))).HasPermission.Should().BeTrue();
        (await store.CheckAsync(Check(new ObjectRef("document", "3"), "view", new ObjectRef("team", "eng")))).HasPermission.Should().BeFalse();
        // The relation must be the permission's name: the double evaluates no schema.
        (await store.CheckAsync(Check(new ObjectRef("document", "4"), "view", Alice))).HasPermission.Should().BeFalse();
    }

    [Fact]
    public async Task CaveatedRelationshipsGrantConditionallyAndExpiredOnesNotAtAll()
    {
        var store = new InMemorySpiceDBClient();
        var txn = new Transaction();
        txn.Touch(Relationship.FromTuple("document:1#view@user:alice[only_weekdays]"));
        txn.Touch(new Relationship(Doc1, "view", Bob).WithExpiration(DateTimeOffset.UtcNow.AddMinutes(-1)));
        await store.WriteAsync(txn);
        (await store.CheckAsync(Check(Doc1, "view", Alice))).Permissionship.Should().Be(Permissionship.ConditionalPermission);
        (await store.CheckAsync(Check(Doc1, "view", Bob))).Permissionship.Should().Be(Permissionship.NoPermission);
    }

    [Fact]
    public async Task ResolutionOrderIsRegisteredThenResolverThenStoredThenNoPermission()
    {
        var store = await Seeded("document:1#view@user:alice");
        var seen = new List<PermissionCheck>();
        store.SetCheckResolver(check =>
        {
            seen.Add(check);
            return check.Subject.Object.ObjectId == "bob" ? new CheckResult(Permissionship.ConditionalPermission, ["weekday"], default) : null;
        });

        // The resolver answers bob; null falls through to the stored relationship for alice.
        var bob = await store.CheckAsync(Check(Doc1, "view", Bob));
        bob.Permissionship.Should().Be(Permissionship.ConditionalPermission);
        bob.MissingContext.Should().Equal("weekday");
        bob.CheckedAt.Should().Be(store.Revision);
        (await store.CheckAsync(Check(Doc1, "view", Alice))).Permissionship.Should().Be(Permissionship.HasPermission);
        (await store.CheckAsync(Check(Doc1, "edit", Alice))).Permissionship.Should().Be(Permissionship.NoPermission);
        seen.Select(c => c.Subject.ToString()).Should().Equal("user:bob", "user:alice", "user:alice");

        // A registration beats the resolver and the stored relationships.
        store.SetCheckResult(Doc1, "view", Alice, Permissionship.NoPermission);
        store.SetCheckResult(Doc1, "view", Bob, Permissionship.HasPermission);
        (await store.CheckAsync(Check(Doc1, "view", Alice))).Permissionship.Should().Be(Permissionship.NoPermission);
        (await store.CheckAsync(Check(Doc1, "view", Bob))).Permissionship.Should().Be(Permissionship.HasPermission);
        seen.Should().HaveCount(3);
    }

    [Fact]
    public async Task GrantAllIsOneLineAndTheResolverSeesTheMergedCallContext()
    {
        var store = new InMemorySpiceDBClient();
        store.SetCheckResolver(InMemorySpiceDBClient.GrantAll);
        var generated = new ObjectRef("document", Guid.NewGuid().ToString("N"));
        (await store.CheckAsync(Check(generated, "anything", Alice))).HasPermission.Should().BeTrue();
        (await store.CheckAnyAsync([Check(generated, "view", Bob)])).Should().BeTrue();

        IReadOnlyDictionary<string, object?>? seen = null;
        store.SetCheckResolver(c => { seen = c.Context; return null; });
        var withContext = Check(generated, "view", Alice).WithContext(new Dictionary<string, object?> { ["day"] = "mon" });
        await store.CheckAsync(withContext, new CheckOptions { Context = new Dictionary<string, object?> { ["day"] = "sun", ["tz"] = "utc" } });
        seen.Should().NotBeNull();
        seen!["day"].Should().Be("mon");
        seen["tz"].Should().Be("utc");

        store.GrantAllChecks();
        (await store.CheckAsync(Check(generated, "view", Bob))).HasPermission.Should().BeTrue();
        store.SetCheckResolver(null);
        (await store.CheckAsync(Check(generated, "view", Alice))).Permissionship.Should().Be(Permissionship.NoPermission);
    }

    [Fact]
    public async Task LookupResourcesEnumeratesKnownObjectsOfTheTypeThroughCheckResolution()
    {
        var store = new InMemorySpiceDBClient();
        var txn = new Transaction();
        foreach (var t in new[] { "document:1#view@user:alice", "document:2#view@user:bob", "document:3#view@user:*", "folder:x#parent@document:4" })
            txn.Touch(Relationship.FromTuple(t));
        txn.Touch(Relationship.FromTuple("document:5#view@user:alice[only_weekdays]"));
        await store.WriteAsync(txn);

        var all = await All(store.LookupResourcesAsync(Consistency.Full(), "document", "view", Alice));
        all.Select(r => (r.ResourceId, r.Permissionship)).Should().Equal(
            ("1", Permissionship.HasPermission), ("3", Permissionship.HasPermission), ("5", Permissionship.ConditionalPermission));
        all[2].PartialCaveat.Should().NotBeNull();
        all[0].PartialCaveat.Should().BeNull();
        all.Should().OnlyContain(r => r.LookedUpAt == store.Revision);

        // document:4 appears only as a subject; the resolver can grant it.
        store.SetCheckResolver(c => c.Resource.ObjectId == "4" ? new CheckResult(Permissionship.HasPermission, [], default) : null);
        var granted = await All(store.LookupResourcesAsync(Consistency.Full(), "document", "view", Alice,
            new LookupOptions { PermissionshipFilter = LookupPermissionshipFilter.HasPermissionOnly }));
        granted.Select(r => r.ResourceId).Should().Equal("1", "3", "4");

        // Limit counts yielded results; the cursor resumes after the item that carries it.
        var first = await All(store.LookupResourcesAsync(Consistency.Full(), "document", "view", Alice, new LookupOptions { Limit = 2 }));
        first.Select(r => r.ResourceId).Should().Equal("1", "3");
        first[1].AfterResultCursor.Should().Be(InMemorySpiceDBClient.CursorAfter(2));
        var rest = await All(store.LookupResourcesAsync(Consistency.Full(), "document", "view", Alice, new LookupOptions { ResumeCursor = first[1].AfterResultCursor }));
        rest.Select(r => r.ResourceId).Should().Equal("4", "5");

        store.CallsOf(SpiceDBOperation.LookupResources).Should().HaveCount(4);
    }

    [Fact]
    public async Task LookupsCarryTheResolvedMissingContext()
    {
        var store = await Seeded("document:1#view@user:alice");
        store.SetCheckResolver(_ => new CheckResult(Permissionship.ConditionalPermission, ["ip"], default));
        var resources = await All(store.LookupResourcesAsync(Consistency.Full(), "document", "view", Alice));
        resources.Single().PartialCaveat!.MissingRequiredContext.Should().Equal("ip");
        (await All(store.LookupResourcesAsync(Consistency.Full(), "document", "view", Alice,
            new LookupOptions { PermissionshipFilter = LookupPermissionshipFilter.HasPermissionOnly }))).Should().BeEmpty();
        var subjects = await All(store.LookupSubjectsAsync(Consistency.Full(), Doc1, "view", "user"));
        subjects.Single().Subject.PartialCaveat!.MissingRequiredContext.Should().Equal("ip");
    }

    [Fact]
    public async Task UncaveatedMatchesWinOverCaveatedOnes()
    {
        var store = await Seeded("document:1#view@user:alice[only_weekdays]", "document:1#view@user:*");
        (await store.CheckAsync(Check(Doc1, "view", Alice))).Permissionship.Should().Be(Permissionship.HasPermission);
    }

    [Fact]
    public async Task LookupSubjectsReturnsGrantedSubjectsIncludingAWildcard()
    {
        var store = await Seeded("document:1#view@user:alice", "document:1#view@team:eng#member", "document:2#view@user:bob", "document:2#view@user:*", "team:eng#member@user:carol");

        var one = await All(store.LookupSubjectsAsync(Consistency.Full(), Doc1, "view", "user"));
        one.Select(s => s.Subject.SubjectId).Should().Equal("alice");

        var two = await All(store.LookupSubjectsAsync(Consistency.Full(), new ObjectRef("document", "2"), "view", "user"));
        // Every known user is granted through the wildcard, and the wildcard itself is reported.
        two.Select(s => s.Subject.SubjectId).Should().Equal("alice", "bob", "*", "carol");
        two.Should().OnlyContain(s => s.Subject.Permissionship == Permissionship.HasPermission && s.ExcludedSubjects.Count == 0);

        var teams = await All(store.LookupSubjectsAsync(Consistency.Full(), Doc1, "view", "team", new LookupOptions { SubjectRelation = "member" }));
        teams.Select(s => s.Subject.SubjectId).Should().Equal("eng");
        (await All(store.LookupSubjectsAsync(Consistency.Full(), Doc1, "view", "team"))).Should().BeEmpty();

        (await All(store.LookupSubjectsAsync(Consistency.Full(), new ObjectRef("document", "2"), "view", "user", new LookupOptions { Limit = 1 }))).Should().HaveCount(1);
        await Assert.ThrowsAsync<InvalidArgumentException>(async () => await All(store.LookupSubjectsAsync(Consistency.Full(), Doc1, "view", "user",
            new LookupOptions { PermissionshipFilter = LookupPermissionshipFilter.HasPermissionOnly })));
    }

    [Fact]
    public async Task LookupsKeepRecordingFaultHooksAndRegisteredErrors()
    {
        var store = await Seeded("document:1#view@user:alice", "document:2#view@user:alice");
        store.SetFaultHook(f => f.Call.Operation == SpiceDBOperation.LookupResources && f.Stage == FaultStage.Item && f.Item == 1
            ? InMemorySpiceDBClient.ServerError(StatusCode.Unavailable, "gone") : null);
        var yielded = new List<string>();
        await Assert.ThrowsAsync<UnavailableException>(async () =>
        {
            await foreach (var r in store.LookupResourcesAsync(Consistency.Full(), "document", "view", Alice)) yielded.Add(r.ResourceId);
        });
        yielded.Should().Equal("1");
        store.SetFaultHook(null);

        store.SetCheckError(new ObjectRef("document", "2"), "view", Alice, InMemorySpiceDBClient.ServerError(StatusCode.PermissionDenied, "no"));
        await Assert.ThrowsAsync<PermissionDeniedException>(async () => await All(store.LookupResourcesAsync(Consistency.Full(), "document", "view", Alice)));

        var call = store.CallsOf(SpiceDBOperation.LookupSubjects);
        call.Should().BeEmpty();
        var options = new LookupOptions { Limit = 5 };
        await All(store.LookupSubjectsAsync(Consistency.Full(), Doc1, "view", "user", options));
        store.CallsOf(SpiceDBOperation.LookupSubjects).Single().OptionsAs<LookupOptions>().Should().BeSameAs(options);
    }

    [Fact]
    public async Task ATypedWrapperOverTheInterfaceRunsOnTheDouble()
    {
        // Code written against ISpiceDBClient (as a typed wrapper is) runs over the double.
        ISpiceDBClient client = await Seeded("document:1#view@user:alice");
        var ids = new List<string>();
        await foreach (var r in client.LookupResourcesAsync(Consistency.Full(), "document", "view", AliceObject)) ids.Add(r.ResourceId);
        ids.Should().Equal("1");
    }
}
