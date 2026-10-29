using System.Reflection;
using System.Text.Json;
using System.Text.Json.Serialization;
using FluentAssertions;
using Google.Protobuf;
using Grpc.Core;
using Xunit;

namespace SpiceDB.Client.Tests;

/// <summary>
/// Native values serialize with System.Text.Json as their canonical forms, identifiers use
/// <c>Id</c>, direct subjects are one call from an object (and <c>...</c> means direct
/// everywhere), errors always carry a server message, and generated messages format as JSON.
/// </summary>
public class InteropAndSerializationTests
{
    [Fact]
    public void CheckResultsSerializeEnumsAsNamesAndTokensAsStrings()
    {
        var result = new CheckResult(Permissionship.HasPermission, [], new ZedToken("GhUKEzE3"));
        var json = JsonSerializer.Serialize(result);
        json.Should().Contain("\"Permissionship\":\"HasPermission\"");
        json.Should().Contain("\"CheckedAt\":\"GhUKEzE3\"");

        JsonSerializer.Deserialize<ZedToken>("\"abc\"").Value.Should().Be("abc");
        JsonSerializer.Serialize<ZedToken?>(null).Should().Be("null");
        JsonSerializer.Serialize(new LookupResource { ResourceId = "1", Permissionship = Permissionship.ConditionalPermission, LookedUpAt = new ZedToken("t") })
            .Should().Be("{\"ResourceId\":\"1\",\"Permissionship\":\"ConditionalPermission\",\"PartialCaveat\":null,\"LookedUpAt\":\"t\",\"AfterResultCursor\":null}");
        JsonSerializer.Serialize(WatchContent.Relationships | WatchContent.Checkpoints).Should().Be("\"Relationships, Checkpoints\"");
    }

    [Fact]
    public void EveryPublicEnumSerializesAsItsName()
    {
        var enums = typeof(SpiceDBClient).Assembly.GetExportedTypes().Where(t => t.IsEnum).ToArray();
        enums.Should().NotBeEmpty();
        foreach (var type in enums)
        {
            type.GetCustomAttribute<JsonConverterAttribute>().Should().NotBeNull($"{type.Name} should serialize as a name");
            var first = Enum.GetValues(type).GetValue(0)!;
            JsonSerializer.Serialize(first, type).Should().Be($"\"{first}\"", type.Name);
        }
    }

    [Fact]
    public void LookupResultsUseIdAndKeepTheFormerNamesAsObsoleteAliases()
    {
        var resource = new LookupResource { ResourceId = "doc" };
        var subject = new ResolvedSubject { SubjectId = "alice" };
#pragma warning disable CS0618 // The obsolete aliases are under test.
        resource.ResourceID.Should().Be("doc");
        subject.SubjectID.Should().Be("alice");
        new LookupResource { ResourceID = "old" }.ResourceId.Should().Be("old");
        new ResolvedSubject { SubjectID = "old" }.SubjectId.Should().Be("old");
#pragma warning restore CS0618
        typeof(LookupResource).GetProperty("ResourceID")!.GetCustomAttribute<ObsoleteAttribute>().Should().NotBeNull();
        typeof(ResolvedSubject).GetProperty("SubjectID")!.GetCustomAttribute<ObsoleteAttribute>().Should().NotBeNull();
        JsonSerializer.Serialize(subject).Should().NotContain("SubjectID");
    }

    [Fact]
    public void DirectSubjectsConvertFromObjectsAndEllipsisMeansDirect()
    {
        var alice = new ObjectRef("user", "alice");
        SubjectRef converted = alice;
        converted.Should().Be(SubjectRef.Direct(alice));
        new Relationship(new ObjectRef("document", "1"), "viewer", alice).Subject.Relation.Should().BeNull();
        new PermissionCheck(new ObjectRef("document", "1"), "view", alice, Consistency.Full()).Subject.Should().Be(SubjectRef.Direct(alice));

        SubjectRef.Named(alice, "...").Should().Be(SubjectRef.Direct(alice));
        new SubjectRef(alice, "...").Relation.Should().BeNull();
        SubjectRelationFilter.Named("...").Should().Be(SubjectRelationFilter.Direct);
        SubjectRelationFilter.Named("member").Kind.Should().Be(SubjectRelationKind.Named);
        Assert.Throws<InvalidArgumentException>(() => new SubjectRef(alice, ""));
        ObjectRef? none = null;
        SubjectRef? fromNull = none!;
        fromNull.Should().BeNull();
    }

    [Fact]
    public void ServerMessageIsNeverNull()
    {
        new InvalidArgumentException("bad input").ServerMessage.Should().Be("bad input");
        new SpiceDBException("plain").ServerMessage.Should().Be("plain");
        new SpiceDBException().ServerMessage.Should().NotBeNullOrEmpty();
        SpiceDBException.Create(StatusCode.NotFound, "from server").ServerMessage.Should().Be("from server");
        SpiceDBException.FromRpcException(new RpcException(new Status(StatusCode.Aborted, "verbatim"))).ServerMessage.Should().Be("verbatim");
    }

    [Fact]
    public void NativeErrorsAreNotRpcExceptionsSoTheServerStatusStaysTheInnerException()
    {
        // Documented in MIGRATION.md: RpcException has no inner-exception constructor, so a
        // subclass could not keep the server's RpcException as InnerException.
        typeof(RpcException).IsAssignableFrom(typeof(SpiceDBException)).Should().BeFalse();
        var server = new RpcException(new Status(StatusCode.NotFound, "gone"));
        var mapped = SpiceDBException.FromRpcException(server);
        mapped.InnerException.Should().BeSameAs(server);
        mapped.ToRpcException().StatusCode.Should().Be(StatusCode.NotFound);
    }

    [Fact]
    public void GeneratedMessagesFormatThroughDescriptors()
    {
        var request = new Authzed.Api.V1.CheckPermissionRequest { Permission = "view" };
        JsonFormatter.Default.Format(request).Should().Contain("\"permission\": \"view\"");
        request.ToString().Should().Contain("view");
        Authzed.Api.V1.CheckPermissionResponse.Descriptor.FullName.Should().Be("authzed.api.v1.CheckPermissionResponse");
    }
}
