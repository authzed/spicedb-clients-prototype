using System.Net;
using Authzed.Api.Materialize.V0;
using Grpc.Core;
using Microsoft.AspNetCore.Builder;
using Microsoft.AspNetCore.Hosting;
using Microsoft.AspNetCore.Hosting.Server;
using Microsoft.AspNetCore.Hosting.Server.Features;
using Microsoft.AspNetCore.Server.Kestrel.Core;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.Logging;
using Xunit;
using M = Authzed.Api.Materialize.V0;
namespace SpiceDB.Client.Tests;

public class MaterializeCoverageTests
{
    private static void Auth(ServerCallContext context) => Assert.Equal("Bearer materialize-test", context.RequestHeaders.GetValue("authorization"));
    public sealed class Counts : RelationshipsService.RelationshipsServiceBase
    {
        public override Task<ExperimentalCountRelationshipsByFilterResponse> ExperimentalCountRelationshipsByFilter(ExperimentalCountRelationshipsByFilterRequest request, ServerCallContext context)
        {
            Auth(context); Assert.True(context.Deadline < DateTime.MaxValue); Assert.Equal("document", request.RelationshipFilter.ResourceType);
            return Task.FromResult(new ExperimentalCountRelationshipsByFilterResponse { RelationshipCount = 8, ReadAt = new() { Token = "count-revision" } });
        }
    }
    public sealed class Sets : WatchPermissionSetsService.WatchPermissionSetsServiceBase
    {
        public override async Task WatchPermissionSets(WatchPermissionSetsRequest request, IServerStreamWriter<WatchPermissionSetsResponse> response, ServerCallContext context)
        {
            Auth(context); Assert.Equal("start", request.OptionalStartingAfter.Token);
            await response.WriteAsync(new() { CompletedRevision = new() { Token = "completed" } });
            await response.WriteAsync(new() { LookupPermissionSetsRequired = new() { RequiredLookupAt = new() { Token = "lookup" } } });
            await response.WriteAsync(new() { BreakingSchemaChange = new() { ChangeAt = new() { Token = "schema" }, AffectedPermissions = { new M.WatchedPermission { ResourceType = "document", Permission = "view", SubjectType = "user" } } } });
            await response.WriteAsync(new() { Change = new() { AtRevision = new() { Token = "change" }, Operation = M.PermissionSetChange.Types.SetOperation.Added, ParentSet = new() { ObjectType = "document", ObjectId = "one", PermissionOrRelation = "view" }, ChildMember = new() { ObjectType = "user", ObjectId = "alice" } } });
            await response.WriteAsync(new());
        }
        public override async Task LookupPermissionSets(LookupPermissionSetsRequest request, IServerStreamWriter<LookupPermissionSetsResponse> response, ServerCallContext context)
        {
            Auth(context); Assert.Equal(5u, request.Limit); Assert.Equal("at", request.OptionalAtRevision.Token); Assert.Equal("key", request.OptionalStartingAfterCursor.StartingKey);
            await response.WriteAsync(new() { Cursor = new() { Limit = 5, Token = new() { Token = "next" }, StartingIndex = 3, CompletedMembers = true, StartingKey = "next-key" } });
        }
        public override Task<DownloadPermissionSetsResponse> DownloadPermissionSets(DownloadPermissionSetsRequest request, ServerCallContext context)
        {
            Auth(context); Assert.True(context.Deadline < DateTime.MaxValue); Assert.Equal("at", request.OptionalAtRevision.Token);
            return Task.FromResult(new DownloadPermissionSetsResponse { AtRevision = new() { Token = "download" }, Timestamp = Google.Protobuf.WellKnownTypes.Timestamp.FromDateTimeOffset(DateTimeOffset.UnixEpoch), Files = { new M.File { Name = "sets.avro", Url = "https://example.test/sets.avro" } } });
        }
    }
    /// <summary>Fails a watch with UNAVAILABLE before any event while <see cref="FailNext"/> is set (clearing it); otherwise delivers one event.</summary>
    public sealed class FlakySets : WatchPermissionSetsService.WatchPermissionSetsServiceBase
    {
        public int Calls;
        public volatile bool FailNext;
        public override async Task WatchPermissionSets(WatchPermissionSetsRequest request, IServerStreamWriter<WatchPermissionSetsResponse> response, ServerCallContext context)
        {
            Interlocked.Increment(ref Calls);
            if (FailNext) { FailNext = false; throw new RpcException(new Status(StatusCode.Unavailable, "transient")); }
            await response.WriteAsync(new() { CompletedRevision = new() { Token = "completed" } });
        }
    }
    [Fact]
    public async Task PermissionSetWatchIsReopenedOnlyUnderAPerCallOrPerOperationPolicy()
    {
        var builder = WebApplication.CreateBuilder(); builder.Logging.ClearProviders(); builder.Services.AddGrpc();
        var sets = new FlakySets(); builder.Services.AddSingleton(sets);
        builder.WebHost.ConfigureKestrel(k => k.Listen(IPAddress.Loopback, 0, o => o.Protocols = HttpProtocols.Http2));
        await using var app = builder.Build(); app.MapGrpcService<FlakySets>(); await app.StartAsync();
        var authority = new Uri(app.Services.GetRequiredService<IServer>().Features.Get<IServerAddressesFeature>()!.Addresses.Single()).Authority;
        var retrying = new RetryPolicy { MaxRetries = 1, RetryableCodes = [StatusCode.Unavailable], InitialBackoff = TimeSpan.FromMilliseconds(1) };

        // The client-wide policy (default or explicit) never reopens it: a reopened watch without
        // a start revision would begin later and skip the changes in between.
        await using (var client = SpiceDBClient.CreatePlaintext(authority, "materialize-test", new ClientOptions { RetryPolicy = retrying }))
        {
            sets.FailNext = true;
            await Assert.ThrowsAsync<UnavailableException>(async () => { await foreach (var _ in client.ExperimentalWatchPermissionSetsAsync(null)) { } });
            Assert.Equal(1, sets.Calls);

            sets.FailNext = true;
            var events = new List<PermissionSetEvent>();
            await foreach (var e in client.ExperimentalWatchPermissionSetsAsync(null, new CallOptions { RetryPolicy = retrying })) events.Add(e);
            Assert.IsType<PermissionSetEvent.Completed>(Assert.Single(events));
            Assert.Equal(3, sets.Calls);
        }
        await using (var client = SpiceDBClient.CreatePlaintext(authority, "materialize-test", new ClientOptions { OperationRetryPolicies = new Dictionary<SpiceDBOperation, RetryPolicy> { [SpiceDBOperation.ExperimentalWatchPermissionSets] = retrying } }))
        {
            sets.FailNext = true;
            await foreach (var _ in client.ExperimentalWatchPermissionSetsAsync(null)) { }
            Assert.Equal(5, sets.Calls);
        }
    }
    [Fact]
    public async Task EveryMaterializeRpcUsesAuthenticatedTransportAndNativeValues()
    {
        var builder = WebApplication.CreateBuilder(); builder.Logging.ClearProviders(); builder.Services.AddGrpc();
        builder.WebHost.ConfigureKestrel(k => k.Listen(IPAddress.Loopback, 0, o => o.Protocols = HttpProtocols.Http2));
        await using var app = builder.Build(); app.MapGrpcService<Counts>(); app.MapGrpcService<Sets>(); await app.StartAsync();
        var address = app.Services.GetRequiredService<IServer>().Features.Get<IServerAddressesFeature>()!.Addresses.Single();
        await using var client = SpiceDBClient.CreatePlaintext(new Uri(address).Authority, "materialize-test");
        var count = await client.ExperimentalCountRelationshipsByFilterAsync(new Filter("document"));
        Assert.Equal(8ul, count.RelationshipCount); Assert.Equal("count-revision", count.Revision.Value);
        var events = new List<PermissionSetEvent>();
        await foreach (var item in client.ExperimentalWatchPermissionSetsAsync(new ZedToken("start"))) events.Add(item);
        Assert.Equal(5, events.Count); Assert.Equal(new ZedToken("completed"), Assert.IsType<PermissionSetEvent.Completed>(events[0]).Revision);
        Assert.Equal(new ZedToken("lookup"), Assert.IsType<PermissionSetEvent.LookupRequired>(events[1]).Revision);
        Assert.Equal("view", Assert.IsType<PermissionSetEvent.SchemaChanged>(events[2]).AffectedPermissions[0].Permission);
        var change = Assert.IsType<PermissionSetEvent.Change>(events[3]).Value;
        Assert.Equal(PermissionSetOperation.Added, change.Operation); Assert.Equal("alice", Assert.IsType<PermissionSetChild.Member>(change.Child).Reference.Object.ObjectId);
        Assert.IsType<PermissionSetEvent.Unspecified>(events[4]);
        await foreach (var page in client.ExperimentalLookupPermissionSetsAsync(5, new ZedToken("at"), new PermissionSetLookupOptions { ResumeCursor = new(5, new("at"), 1, false, "key") }))
        { Assert.True(page.Cursor!.CompletedMembers); Assert.Equal("next-key", page.Cursor.StartingKey); }
        var download = await client.ExperimentalDownloadPermissionSetsAsync(new ZedToken("at"));
        Assert.Equal("sets.avro", Assert.Single(download.Files).Name); Assert.Equal(DateTimeOffset.UnixEpoch, download.Timestamp); Assert.Equal("download", download.AtRevision.Value);
        Assert.NotNull(client.RawProto().MaterializeRelationships); Assert.NotNull(client.RawProto().MaterializePermissionSets);
        await app.StopAsync();
    }
}
