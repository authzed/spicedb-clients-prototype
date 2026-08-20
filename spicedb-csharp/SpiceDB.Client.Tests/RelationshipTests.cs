using FluentAssertions;
using Google.Protobuf.WellKnownTypes;
using SpiceDB.Client;
using Xunit;

namespace SpiceDB.Client.Tests;

public class RelationshipTests
{
    [Fact]
    public void FromTriple_CreatesRelationship()
    {
        var rel = Relationship.FromTriple("document", "doc1", "viewer", "user", "alice");

        rel.ResourceType.Should().Be("document");
        rel.ResourceID.Should().Be("doc1");
        rel.ResourceRelation.Should().Be("viewer");
        rel.SubjectType.Should().Be("user");
        rel.SubjectID.Should().Be("alice");
        rel.SubjectRelation.Should().BeEmpty();
        rel.CaveatName.Should().BeNull();
        rel.CaveatContext.Should().BeNull();
        rel.Expiration.Should().BeNull();
    }

    [Fact]
    public void FromTriple_WithSubjectRelation()
    {
        var rel = Relationship.FromTriple("document", "doc1", "viewer", "group", "engineering", "member");

        rel.SubjectRelation.Should().Be("member");
    }

    [Fact]
    public void FromTriple_ThrowsOnEmptyResourceType()
    {
        var act = () => Relationship.FromTriple("", "doc1", "viewer", "user", "alice");
        act.Should().Throw<ArgumentException>();
    }

    [Fact]
    public void FromTriple_ThrowsOnEmptyResourceID()
    {
        var act = () => Relationship.FromTriple("document", "", "viewer", "user", "alice");
        act.Should().Throw<ArgumentException>();
    }

    [Fact]
    public void FromTriple_ThrowsOnEmptyResourceRelation()
    {
        var act = () => Relationship.FromTriple("document", "doc1", "", "user", "alice");
        act.Should().Throw<ArgumentException>();
    }

    [Fact]
    public void FromTriple_ThrowsOnEmptySubjectType()
    {
        var act = () => Relationship.FromTriple("document", "doc1", "viewer", "", "alice");
        act.Should().Throw<ArgumentException>();
    }

    [Fact]
    public void FromTriple_ThrowsOnEmptySubjectID()
    {
        var act = () => Relationship.FromTriple("document", "doc1", "viewer", "user", "");
        act.Should().Throw<ArgumentException>();
    }

    [Fact]
    public void FromTuple_ParsesBasicTuple()
    {
        var rel = Relationship.FromTuple("document:doc1#viewer@user:alice");

        rel.ResourceType.Should().Be("document");
        rel.ResourceID.Should().Be("doc1");
        rel.ResourceRelation.Should().Be("viewer");
        rel.SubjectType.Should().Be("user");
        rel.SubjectID.Should().Be("alice");
        rel.SubjectRelation.Should().BeEmpty();
    }

    [Fact]
    public void FromTuple_ParsesTupleWithSubjectRelation()
    {
        var rel = Relationship.FromTuple("document:doc1#viewer@group:engineering#member");

        rel.SubjectType.Should().Be("group");
        rel.SubjectID.Should().Be("engineering");
        rel.SubjectRelation.Should().Be("member");
    }

    [Fact]
    public void FromTuple_ThrowsOnMissingAt()
    {
        var act = () => Relationship.FromTuple("document:doc1#viewer");
        act.Should().Throw<FormatException>().WithMessage("*@*");
    }

    [Fact]
    public void FromTuple_ThrowsOnMissingHash()
    {
        var act = () => Relationship.FromTuple("document:doc1@user:alice");
        act.Should().Throw<FormatException>().WithMessage("*#*");
    }

    [Fact]
    public void FromTuple_ThrowsOnMissingResourceColon()
    {
        var act = () => Relationship.FromTuple("documentdoc1#viewer@user:alice");
        act.Should().Throw<FormatException>().WithMessage("*:*");
    }

    [Fact]
    public void FromTuple_ThrowsOnEmptyString()
    {
        var act = () => Relationship.FromTuple("");
        act.Should().Throw<FormatException>();
    }

    [Fact]
    public void WithCaveat_ReturnsCopyWithCaveat()
    {
        var original = Relationship.FromTriple("document", "doc1", "viewer", "user", "alice");
        var context = new Dictionary<string, object> { ["region"] = "us" };
        var withCaveat = original.WithCaveat("ip_allowlist", context);

        // Original unchanged
        original.CaveatName.Should().BeNull();
        original.CaveatContext.Should().BeNull();

        // New has caveat
        withCaveat.CaveatName.Should().Be("ip_allowlist");
        withCaveat.CaveatContext.Should().ContainKey("region");

        // Other fields unchanged
        withCaveat.ResourceType.Should().Be("document");
        withCaveat.SubjectID.Should().Be("alice");
    }

    [Fact]
    public void WithExpiration_ReturnsCopyWithExpiration()
    {
        var original = Relationship.FromTriple("document", "doc1", "viewer", "user", "alice");
        var expires = new DateTimeOffset(2026, 12, 31, 0, 0, 0, TimeSpan.Zero);
        var withExp = original.WithExpiration(expires);

        original.Expiration.Should().BeNull();
        withExp.Expiration.Should().Be(expires);
        withExp.ResourceType.Should().Be("document");
    }

    [Fact]
    public void WithCheckContext_ReturnsCopyWithCheckContext()
    {
        var original = Relationship.FromTriple("document", "doc1", "viewer", "user", "alice");
        var context = new Dictionary<string, object> { ["now"] = 42 };
        var withContext = original.WithCheckContext(context);

        // Original unchanged
        original.CheckContext.Should().BeNull();

        // New carries the check-time context
        withContext.CheckContext.Should().ContainKey("now");

        // Other fields unchanged
        withContext.ResourceType.Should().Be("document");
        withContext.SubjectID.Should().Be("alice");
    }

    [Fact]
    public void CheckContext_IsDistinctFromCaveatContext()
    {
        // CheckContext (check-time, supplied fresh on every check call) and
        // CaveatContext (write-time, stored WITH the relationship's caveat)
        // are deliberately separate fields. Setting one must never populate
        // or be confused with the other — a caller who means to attach
        // check-time context must not accidentally alter what would be
        // written to SpiceDB, and vice versa.
        var rel = Relationship
            .FromTriple("document", "doc1", "caveated_viewer", "user", "alice")
            .WithCaveat("active")
            .WithCheckContext(new Dictionary<string, object> { ["now"] = 42 });

        rel.CaveatContext.Should().BeNull();
        rel.CheckContext.Should().NotBeNull();
        rel.CheckContext.Should().ContainKey("now");
    }

    [Fact]
    public void ToProto_NeverIncludesCheckContext()
    {
        // CheckContext is check-time-only and must NEVER reach the wire via
        // WriteAsync (Relationship.ToProto) — only CaveatContext does, via
        // OptionalCaveat.Context. A leak here would silently alter what gets
        // stored in SpiceDB based on data that was only ever meant to answer
        // one check call.
        var rel = Relationship
            .FromTriple("document", "doc1", "caveated_viewer", "user", "alice")
            .WithCaveat("active")
            .WithCheckContext(new Dictionary<string, object> { ["now"] = 42, ["secret"] = "leak-me-not" });

        var proto = rel.ToProto();

        proto.OptionalCaveat.CaveatName.Should().Be("active");
        proto.OptionalCaveat.Context.Should().BeNull("write-time caveat context was never supplied");
    }

    [Fact]
    public void ToString_BasicFormat()
    {
        var rel = Relationship.FromTriple("document", "doc1", "viewer", "user", "alice");
        rel.ToString().Should().Be("document:doc1#viewer@user:alice");
    }

    [Fact]
    public void ToString_WithSubjectRelation()
    {
        var rel = Relationship.FromTriple("document", "doc1", "viewer", "group", "eng", "member");
        rel.ToString().Should().Be("document:doc1#viewer@group:eng#member");
    }

    [Fact]
    public void ToProto_ConvertsCorrectly()
    {
        var rel = Relationship.FromTriple("document", "doc1", "viewer", "user", "alice");
        var proto = rel.ToProto();

        proto.Resource.ObjectType.Should().Be("document");
        proto.Resource.ObjectId.Should().Be("doc1");
        proto.Relation.Should().Be("viewer");
        proto.Subject.Object.ObjectType.Should().Be("user");
        proto.Subject.Object.ObjectId.Should().Be("alice");
    }

    [Fact]
    public void ToProto_WithCaveat()
    {
        var rel = Relationship.FromTriple("document", "doc1", "viewer", "user", "alice")
            .WithCaveat("my_caveat", new Dictionary<string, object> { ["key"] = "val" });
        var proto = rel.ToProto();

        proto.OptionalCaveat.Should().NotBeNull();
        proto.OptionalCaveat.CaveatName.Should().Be("my_caveat");
        proto.OptionalCaveat.Context.Fields.Should().ContainKey("key");
    }

    // ToProto_PreservesCaveatContextTypes is the regression test for the
    // write-time defect: ToProto used to convert every CaveatContext value
    // via Value.ForString(value?.ToString() ?? ""), stringifying numbers,
    // booleans, null, and nested maps/lists. A caveat like `now < 100`
    // stored against a stringified "50" fails to evaluate — and fails
    // silently, as a conditional result. Worse, unlike a bad check-time
    // context (which fails one call), a bad write-time context is
    // persisted: every future check against the relationship mis-evaluates,
    // and re-checking with correct context never repairs it, only
    // rewriting the relationship does. ToProto must dispatch on type
    // instead, via SpiceDBClient.ToProtoValue.
    [Fact]
    public void ToProto_PreservesCaveatContextTypes()
    {
        var context = new Dictionary<string, object>
        {
            ["a_string"] = "hello",
            ["an_int"] = 42,
            ["a_float"] = 3.5,
            ["a_bool"] = true,
            ["a_null"] = null!,
            ["a_map"] = new Dictionary<string, object> { ["nested"] = "value" },
            ["a_list"] = new List<object> { "one", 2, false },
        };
        var rel = Relationship.FromTriple("document", "doc1", "viewer", "user", "alice")
            .WithCaveat("some_caveat", context);

        var proto = rel.ToProto();
        var fields = proto.OptionalCaveat.Context.Fields;

        fields["a_string"].KindCase.Should().Be(Value.KindOneofCase.StringValue);
        fields["a_string"].StringValue.Should().Be("hello");

        // google.protobuf.Value.number_value is a double, so an integer
        // legitimately round-trips as a float (42 -> 42.0). That is
        // inherent to the proto, not a defect in this conversion.
        fields["an_int"].KindCase.Should().Be(Value.KindOneofCase.NumberValue);
        fields["an_int"].NumberValue.Should().Be(42.0);

        fields["a_float"].KindCase.Should().Be(Value.KindOneofCase.NumberValue);
        fields["a_float"].NumberValue.Should().Be(3.5);

        fields["a_bool"].KindCase.Should().Be(Value.KindOneofCase.BoolValue);
        fields["a_bool"].BoolValue.Should().BeTrue();

        fields["a_null"].KindCase.Should().Be(Value.KindOneofCase.NullValue);

        fields["a_map"].KindCase.Should().Be(Value.KindOneofCase.StructValue);
        fields["a_map"].StructValue.Fields["nested"].StringValue.Should().Be("value");

        fields["a_list"].KindCase.Should().Be(Value.KindOneofCase.ListValue);
        fields["a_list"].ListValue.Values.Should().HaveCount(3);
        fields["a_list"].ListValue.Values[0].KindCase.Should().Be(Value.KindOneofCase.StringValue);
        fields["a_list"].ListValue.Values[1].KindCase.Should().Be(Value.KindOneofCase.NumberValue);
        fields["a_list"].ListValue.Values[2].KindCase.Should().Be(Value.KindOneofCase.BoolValue);
    }

    // ToProto_UnrepresentableCaveatContextValue_Throws: a value ToProtoValue
    // cannot dispatch on any known type/kind case (e.g. a custom class
    // instance) used to fall through to Value.ForString(value.ToString()),
    // silently stringifying it instead of raising. Caveat context is
    // caller-supplied, so per root DESIGN.md "RULE: A conversion that
    // cannot preserve meaning must fail", clause 1, it must raise a typed
    // error naming the offending key and the unsupported type instead of
    // guessing. Shared converter -- this exercises the write path
    // (Relationship.ToProto); CheckContextTests covers the check path.
    private sealed class UnrepresentableValue { }

    [Fact]
    public void ToProto_UnrepresentableCaveatContextValue_Throws()
    {
        var context = new Dictionary<string, object>
        {
            ["bad_key"] = new UnrepresentableValue(),
        };
        var rel = Relationship.FromTriple("document", "doc1", "viewer", "user", "alice")
            .WithCaveat("some_caveat", context);

        var act = () => rel.ToProto();

        var thrown = act.Should().Throw<InvalidArgumentException>().Which;
        thrown.Message.Should().Contain("bad_key");
        thrown.Message.Should().Contain(nameof(UnrepresentableValue));
    }

    // RoundTrip_PreservesCaveatContextTypes covers the symmetric read-side
    // defect: FromProto used to read back every context value via
    // f.Value.StringValue, which returns "" for any non-string Value kind.
    // A round trip through ToProto/FromProto must preserve types in both
    // directions, not just avoid corrupting the wire representation.
    [Fact]
    public void RoundTrip_PreservesCaveatContextTypes()
    {
        var context = new Dictionary<string, object>
        {
            ["a_string"] = "hello",
            ["an_int"] = 42,
            ["a_float"] = 3.5,
            ["a_bool"] = true,
            ["a_null"] = null!,
            ["a_map"] = new Dictionary<string, object> { ["nested"] = "value" },
            ["a_list"] = new List<object> { "one", 2, false },
        };
        var original = Relationship.FromTriple("document", "doc1", "viewer", "user", "alice")
            .WithCaveat("some_caveat", context);

        var roundTripped = Relationship.FromProto(original.ToProto());

        roundTripped.CaveatContext.Should().NotBeNull();
        var rt = roundTripped.CaveatContext!;

        rt["a_string"].Should().Be("hello");
        // 42 -> 42.0: inherent to google.protobuf.Value.number_value being a
        // double, not a defect.
        rt["an_int"].Should().Be(42.0);
        rt["a_float"].Should().Be(3.5);
        rt["a_bool"].Should().Be(true);
        rt["a_null"].Should().BeNull();

        var nestedMap = rt["a_map"].Should().BeAssignableTo<IReadOnlyDictionary<string, object>>().Subject;
        nestedMap["nested"].Should().Be("value");

        var list = rt["a_list"].Should().BeAssignableTo<IEnumerable<object>>().Subject.ToList();
        list.Should().HaveCount(3);
        list[0].Should().Be("one");
        list[1].Should().Be(2.0);
        list[2].Should().Be(false);
    }

    [Fact]
    public void ToProto_WithExpiration()
    {
        var expires = new DateTimeOffset(2026, 6, 15, 12, 0, 0, TimeSpan.Zero);
        var rel = Relationship.FromTriple("document", "doc1", "viewer", "user", "alice")
            .WithExpiration(expires);
        var proto = rel.ToProto();

        proto.OptionalExpiresAt.Should().NotBeNull();
        proto.OptionalExpiresAt.ToDateTimeOffset().Should().Be(expires);
    }

    [Fact]
    public void FromProto_ConvertsCorrectly()
    {
        var proto = new Authzed.Api.V1.Relationship
        {
            Resource = new Authzed.Api.V1.ObjectReference { ObjectType = "doc", ObjectId = "1" },
            Relation = "reader",
            Subject = new Authzed.Api.V1.SubjectReference
            {
                Object = new Authzed.Api.V1.ObjectReference { ObjectType = "user", ObjectId = "bob" },
                OptionalRelation = "member",
            },
        };

        var rel = Relationship.FromProto(proto);

        rel.ResourceType.Should().Be("doc");
        rel.ResourceID.Should().Be("1");
        rel.ResourceRelation.Should().Be("reader");
        rel.SubjectType.Should().Be("user");
        rel.SubjectID.Should().Be("bob");
        rel.SubjectRelation.Should().Be("member");
    }

    [Fact]
    public void RoundTrip_ToProtoAndBack()
    {
        var original = Relationship.FromTriple("document", "doc1", "viewer", "user", "alice", "member");
        var roundTripped = Relationship.FromProto(original.ToProto());

        roundTripped.ResourceType.Should().Be(original.ResourceType);
        roundTripped.ResourceID.Should().Be(original.ResourceID);
        roundTripped.ResourceRelation.Should().Be(original.ResourceRelation);
        roundTripped.SubjectType.Should().Be(original.SubjectType);
        roundTripped.SubjectID.Should().Be(original.SubjectID);
        roundTripped.SubjectRelation.Should().Be(original.SubjectRelation);
    }

    [Fact]
    public void ToFilter_CreatesMatchingFilter()
    {
        var rel = Relationship.FromTriple("document", "doc1", "viewer", "user", "alice");
        var filter = rel.ToFilter();

        filter.ResourceType.Should().Be("document");
        filter.ResourceID.Should().Be("doc1");
        filter.Relation.Should().Be("viewer");
        filter.SubjectType.Should().Be("user");
        filter.SubjectID.Should().Be("alice");
    }

    [Fact]
    public void Relationship_IsImmutableRecord()
    {
        var rel = Relationship.FromTriple("document", "doc1", "viewer", "user", "alice");

        // Records support value equality
        var same = Relationship.FromTriple("document", "doc1", "viewer", "user", "alice");
        rel.Should().Be(same);

        // Different should not be equal
        var different = Relationship.FromTriple("document", "doc2", "viewer", "user", "alice");
        rel.Should().NotBe(different);
    }
}
