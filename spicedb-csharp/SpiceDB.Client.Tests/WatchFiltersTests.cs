using Authzed.Api.V1;
using Grpc.Core;
using Moq;
using Xunit;
using static SpiceDB.Client.Tests.StreamingTestHelpers;

namespace SpiceDB.Client.Tests;

public class WatchFiltersTests
{
    [Fact]
    public async Task FiltersSnapshotAndSerializeEveryConstraintAndRejectConflictingObjectTypes()
    {
        var requests = new List<WatchRequest>();
        var watch = new Mock<WatchService.WatchServiceClient>();
        watch.Setup(c => c.Watch(It.IsAny<WatchRequest>(), It.IsAny<Metadata>(), It.IsAny<DateTime?>(), It.IsAny<CancellationToken>()))
            .Returns((WatchRequest request, Metadata _, DateTime? _, CancellationToken _) =>
            {
                requests.Add(request.Clone());
                return MakeServerStreamingCall(new FakeStreamReader<WatchResponse>([]));
            });
        await using var client = new SpiceDBClient(new Mock<PermissionsService.PermissionsServiceClient>().Object, new Mock<SchemaService.SchemaServiceClient>().Object, watch.Object, new Mock<ExperimentalService.ExperimentalServiceClient>().Object);
        var filters = new List<Filter> {
            new Filter("document").WithResourceId("one").WithRelation("viewer").WithSubjectType("user").WithSubjectId("alice").WithSubjectRelation(SubjectRelationFilter.Direct),
            new Filter("folder").WithResourceIdPrefix("public-").WithRelation("reader").WithSubjectType("team").WithSubjectId("eng").WithSubjectRelation(SubjectRelationFilter.Named("member"))
        };
        var options = new WatchOptions { RelationshipFilters = filters };
        filters.Clear();
        Assert.Equal(2, options.RelationshipFilters.Count);
        await Assert.ThrowsAsync<InvalidArgumentException>(async () => { await foreach (var value in client.UpdatesAsync(["document"], null, options)) { } });
        Assert.Empty(requests);
        await foreach (var value in client.UpdatesAsync([], new ZedToken("start"), options)) { }
        var request = requests[0];
        Assert.Equal("start", request.OptionalStartCursor.Token); Assert.Empty(request.OptionalObjectTypes); Assert.Equal(2, request.OptionalRelationshipFilters.Count);
        var direct = request.OptionalRelationshipFilters[0];
        Assert.Equal("document", direct.ResourceType); Assert.Equal("one", direct.OptionalResourceId); Assert.Equal("viewer", direct.OptionalRelation);
        Assert.Equal("user", direct.OptionalSubjectFilter.SubjectType); Assert.Equal("alice", direct.OptionalSubjectFilter.OptionalSubjectId);
        Assert.NotNull(direct.OptionalSubjectFilter.OptionalRelation); Assert.Equal("", direct.OptionalSubjectFilter.OptionalRelation.Relation);
        var named = request.OptionalRelationshipFilters[1];
        Assert.Equal("folder", named.ResourceType); Assert.Equal("public-", named.OptionalResourceIdPrefix); Assert.Equal("reader", named.OptionalRelation);
        Assert.Equal("team", named.OptionalSubjectFilter.SubjectType); Assert.Equal("eng", named.OptionalSubjectFilter.OptionalSubjectId); Assert.Equal("member", named.OptionalSubjectFilter.OptionalRelation.Relation);
        await foreach (var value in client.UpdatesAsync([], null, new WatchOptions { RelationshipFilters = [] })) { }
        Assert.Empty(requests[1].OptionalRelationshipFilters); Assert.Empty(requests[1].OptionalObjectTypes);
        await foreach (var value in client.UpdatesAsync(["document"], null)) { }
        Assert.Equal("document", Assert.Single(requests[2].OptionalObjectTypes));
    }
}
