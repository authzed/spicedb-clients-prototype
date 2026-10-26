using Xunit;
namespace SpiceDB.Client.Tests;

public class NativeContractTests
{
    [Fact]
    public void RequestsSnapshotNestedNullContext()
    {
        var nested = new List<object?> { null, 1d };
        var context = new Dictionary<string, object?> { ["nested"] = nested };
        var request = new PermissionCheck(new ObjectRef("document", "one"), "view",
            SubjectRef.Direct(new ObjectRef("user", "alice")), Consistency.Full(), context);
        nested.Add("changed"); context.Clear();
        Assert.Equal(2, Assert.IsAssignableFrom<IReadOnlyList<object?>>(request.Context!["nested"]).Count);
        Assert.Null(Assert.IsAssignableFrom<IReadOnlyList<object?>>(request.Context["nested"])[0]);
    }

    [Fact]
    public void ExactDirectFilterPreservesRelationPresence()
    {
        var relationship = Relationship.FromTuple("document:one#viewer@user:alice");
        var wire = relationship.ToFilter().ToProto();
        Assert.NotNull(wire.OptionalSubjectFilter.OptionalRelation);
        Assert.Equal("", wire.OptionalSubjectFilter.OptionalRelation.Relation);
    }

    [Fact]
    public void TokensAndConsistencyAreNativeValues()
    {
        var token = new ZedToken("opaque:/token");
        Assert.Equal(token, new ZedToken(token.Value));
        Assert.Equal(Consistency.AtLeast(token), Consistency.AtLeast(new ZedToken(token.Value)));
        Assert.Throws<InvalidArgumentException>(() => Consistency.Snapshot(default));
    }

    [Fact]
    public void ExplicitNullOverridesCallContextAndNestedCollectionsStayOwned()
    {
        var merged = SpiceDBClient.MergeCheckContext(new Dictionary<string, object?> { ["key"] = 1, ["kept"] = true }, new Dictionary<string, object?> { ["key"] = null });
        Assert.Equal(Google.Protobuf.WellKnownTypes.Value.KindOneofCase.NullValue, merged!.Fields["key"].KindCase);
        Assert.True(merged.Fields["kept"].BoolValue);
        Assert.Throws<InvalidArgumentException>(() => new PermissionTree(intermediate: new(), leaf: new()));
    }

    [Fact]
    public void SchemaDiffPreservesSubjectTypeOneofAndParameterDetails()
    {
        var relation = new Authzed.Api.V1.ReflectionRelation { Name = "viewer", ParentDefinitionName = "document", SubjectTypes = { new Authzed.Api.V1.ReflectionTypeReference { SubjectDefinitionName = "user", IsPublicWildcard = false } } };
        var diff = SchemaDiff.FromProto(new() { RelationSubjectTypeAdded = new() { Relation = relation, ChangedSubjectType = new() { SubjectDefinitionName = "team", OptionalRelationName = "member", OptionalCaveatName = "active" } } });
        Assert.False(diff.Relation!.SubjectTypes[0].PublicWildcard);
        Assert.Null(diff.Relation.SubjectTypes[0].Terminal);
        Assert.Equal("active", diff.ChangedSubjectType!.CaveatName);
        Assert.Equal("member", diff.ChangedSubjectType.Relation);
        relation.SubjectTypes.Clear(); Assert.Single(diff.Relation.SubjectTypes);
        var parameter = SchemaDiff.FromProto(new() { CaveatParameterTypeChanged = new() { Parameter = new() { Name = "x", Type = "int", ParentCaveatName = "active" }, PreviousType = "string" } });
        Assert.Equal("string", parameter.PreviousType); Assert.Equal("int", parameter.Parameter!.Type);
    }
}
