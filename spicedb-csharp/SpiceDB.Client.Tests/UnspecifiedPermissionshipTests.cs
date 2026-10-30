using Authzed.Api.V1;
using FluentAssertions;
using Grpc.Core;
using Moq;
using SpiceDB.Client;
using SpiceDB.Client.Testing;
using Xunit;
using static SpiceDB.Client.Tests.StreamingTestHelpers;

namespace SpiceDB.Client.Tests;

/// <summary>
/// SpiceDB always sets the permissionship of a check, a bulk-check item and a lookup result, so
/// <c>PERMISSIONSHIP_UNSPECIFIED</c> there is a response the client cannot interpret: it fails
/// with INTERNAL as a client-detected malformed response, never as
/// <see cref="Permissionship.Unspecified"/> (root DESIGN.md, "Expose native errors…"). An
/// unrecognised newer value still maps to <see cref="Permissionship.Unspecified"/>, and the
/// in-memory double never produces it.
/// </summary>
public class UnspecifiedPermissionshipTests
{
    private const CheckPermissionResponse.Types.Permissionship Unset = CheckPermissionResponse.Types.Permissionship.Unspecified;

    private static readonly PermissionCheck Check = new(new ObjectRef("document", "1"), "view", new ObjectRef("user", "alice"), Consistency.Full());

    private static SpiceDBClient NewClient(Mock<PermissionsService.PermissionsServiceClient> permissions) =>
        new(permissions.Object, new Mock<SchemaService.SchemaServiceClient>().Object, new Mock<WatchService.WatchServiceClient>().Object, new Mock<ExperimentalService.ExperimentalServiceClient>().Object);

    private static async Task<SpiceDBException> ExpectMalformed(Func<Task> act)
    {
        var error = (await act.Should().ThrowAsync<SpiceDBException>()).Which;
        error.StatusCode.Should().Be(StatusCode.Internal);
        error.Message.Should().Contain("client detected malformed response from server").And.Contain("permissionship is unspecified");
        return error;
    }

    [Fact]
    public async Task CheckFailsOnUnspecifiedPermissionship()
    {
        var mock = new Mock<PermissionsService.PermissionsServiceClient>();
        var calls = 0;
        mock.Setup(c => c.CheckPermissionAsync(It.IsAny<CheckPermissionRequest>(), It.IsAny<Metadata>(), It.IsAny<DateTime?>(), It.IsAny<CancellationToken>()))
            .Returns(() => { calls++; return MakeUnaryCall(new CheckPermissionResponse { Permissionship = Unset, CheckedAt = new() { Token = "t" } }); });
        await using var client = NewClient(mock);
        await ExpectMalformed(() => client.CheckAsync(Check));
        calls.Should().Be(1, "INTERNAL is not retried by the default policy");
    }

    [Theory]
    [InlineData(BulkItemErrors.Fail)]
    [InlineData(BulkItemErrors.Returned)]
    public async Task BulkCheckItemFailsOnUnspecifiedPermissionship(BulkItemErrors itemErrors)
    {
        var mock = new Mock<PermissionsService.PermissionsServiceClient>();
        mock.Setup(c => c.CheckBulkPermissionsAsync(It.IsAny<CheckBulkPermissionsRequest>(), It.IsAny<Metadata>(), It.IsAny<DateTime?>(), It.IsAny<CancellationToken>()))
            .Returns((CheckBulkPermissionsRequest request, Metadata _, DateTime? _, CancellationToken _) =>
            {
                var response = new CheckBulkPermissionsResponse { CheckedAt = new() { Token = "t" } };
                response.Pairs.Add(new CheckBulkPermissionsPair { Request = request.Items[0].Clone(), Item = new() { Permissionship = CheckPermissionResponse.Types.Permissionship.HasPermission } });
                response.Pairs.Add(new CheckBulkPermissionsPair { Request = request.Items[1].Clone(), Item = new() { Permissionship = Unset } });
                return MakeUnaryCall(response);
            });
        await using var client = NewClient(mock);
        var error = await ExpectMalformed(() => client.CheckBulkAsync([Check, Check], new CheckOptions { ItemErrors = itemErrors }));
        error.InputIndex.Should().Be(1);
        error.Message.Should().StartWith("check item 1: ");
    }

    private static Mock<PermissionsService.PermissionsServiceClient> LookupServer(int permissionship)
    {
        var mock = new Mock<PermissionsService.PermissionsServiceClient>();
        mock.Setup(c => c.LookupResources(It.IsAny<LookupResourcesRequest>(), It.IsAny<Metadata>(), It.IsAny<DateTime?>(), It.IsAny<CancellationToken>()))
            .Returns(() => MakeServerStreamingCall(new FakeStreamReader<LookupResourcesResponse>(
            [
                new() { ResourceObjectId = "doc0", Permissionship = LookupPermissionship.HasPermission, AfterResultCursor = new() { Token = "1" } },
                new() { ResourceObjectId = "doc1", Permissionship = (LookupPermissionship)permissionship, AfterResultCursor = new() { Token = "2" } },
            ])));
        mock.Setup(c => c.LookupSubjects(It.IsAny<LookupSubjectsRequest>(), It.IsAny<Metadata>(), It.IsAny<DateTime?>(), It.IsAny<CancellationToken>()))
            .Returns(() => MakeServerStreamingCall(new FakeStreamReader<LookupSubjectsResponse>(
            [
                new() { Subject = new() { SubjectObjectId = "alice", Permissionship = LookupPermissionship.HasPermission } },
                new() { Subject = new() { SubjectObjectId = "bob", Permissionship = (LookupPermissionship)permissionship } },
            ])));
        return mock;
    }

    [Theory]
    [InlineData(LookupPermissionshipFilter.All)]
    [InlineData(LookupPermissionshipFilter.HasPermissionOnly)]
    public async Task LookupResultsFailOnUnspecifiedPermissionship(LookupPermissionshipFilter filter)
    {
        await using var client = NewClient(LookupServer(0));
        var resources = new List<LookupResource>();
        await ExpectMalformed(async () => { await foreach (var r in client.LookupResourcesAsync(Consistency.Full(), "document", "view", new ObjectRef("user", "alice"), new LookupOptions { PermissionshipFilter = filter })) resources.Add(r); });
        resources.Select(r => r.ResourceId).Should().Equal(["doc0"], "the good item is yielded and the bad one fails the lookup, never skipped by the filter");

        var subjects = new List<LookupSubject>();
        await ExpectMalformed(async () => { await foreach (var s in client.LookupSubjectsAsync(Consistency.Full(), new ObjectRef("document", "1"), "view", "user")) subjects.Add(s); });
        subjects.Should().ContainSingle();
    }

    [Fact]
    public async Task UnrecognisedPermissionshipStillMapsToUnspecified()
    {
        await using var client = NewClient(LookupServer(99));
        var resources = new List<LookupResource>();
        await foreach (var r in client.LookupResourcesAsync(Consistency.Full(), "document", "view", new ObjectRef("user", "alice"))) resources.Add(r);
        resources.Select(r => r.Permissionship).Should().Equal(Permissionship.HasPermission, Permissionship.Unspecified);
    }

    [Fact]
    public async Task DoubleNeverProducesUnspecified()
    {
        await using var store = new InMemorySpiceDBClient();
        // SetCheckResult registers a value; the message says it was given one, not that it returned one.
        store.Invoking(s => s.SetCheckResult(Check.Resource, Check.Permission, Check.Subject, Permissionship.Unspecified))
            .Should().Throw<InvalidArgumentException>().WithMessage("SetCheckResult was given permissionship Unspecified*");

        store.SetCheckResolver(_ => new CheckResult(Permissionship.Unspecified, [], default));
        await store.Invoking(s => s.CheckAsync(Check)).Should().ThrowAsync<InvalidArgumentException>()
            .WithMessage("The check resolver returned permissionship Unspecified*");
        await store.Invoking(s => s.CheckBulkAsync([Check])).Should().ThrowAsync<InvalidArgumentException>();

        var seed = new Transaction();
        seed.Touch(Relationship.FromTuple("document:1#reader@user:alice"));
        await store.WriteAsync(seed);
        await store.Invoking(async s => { await foreach (var _ in s.LookupResourcesAsync(Consistency.Full(), "document", "view", new ObjectRef("user", "alice"))) { } })
            .Should().ThrowAsync<InvalidArgumentException>();
        await store.Invoking(async s => { await foreach (var _ in s.LookupSubjectsAsync(Consistency.Full(), Check.Resource, "view", "user")) { } })
            .Should().ThrowAsync<InvalidArgumentException>();

        store.SetCheckResolver(null);
        var granted = new List<Permissionship>();
        await foreach (var s in store.LookupSubjectsAsync(Consistency.Full(), Check.Resource, "reader", "user")) granted.Add(s.Subject.Permissionship);
        await foreach (var r in store.LookupResourcesAsync(Consistency.Full(), "document", "reader", new ObjectRef("user", "alice"))) granted.Add(r.Permissionship);
        granted.Should().Equal(Permissionship.HasPermission, Permissionship.HasPermission);
        (await store.CheckAsync(Check)).Permissionship.Should().Be(Permissionship.NoPermission);
        (await store.CheckBulkAsync([Check])).Single().Permissionship.Should().Be(Permissionship.NoPermission);
    }
}
