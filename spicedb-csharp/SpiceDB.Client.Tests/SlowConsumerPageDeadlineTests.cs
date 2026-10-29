using System.Net;
using Authzed.Api.V1;
using FluentAssertions;
using Grpc.Core;
using Grpc.Net.Client;
using Microsoft.AspNetCore.Builder;
using Microsoft.AspNetCore.Hosting;
using Microsoft.AspNetCore.Hosting.Server;
using Microsoft.AspNetCore.Hosting.Server.Features;
using Microsoft.AspNetCore.Server.Kestrel.Core;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.Logging;
using SpiceDB.Client;
using Xunit;
using M = Authzed.Api.Materialize.V0;

namespace SpiceDB.Client.Tests;

/// <summary>
/// The per-page default deadline bounds waiting on the server, never the caller's processing
/// (root DESIGN.md, "Treat a deadline as a budget"). Each paged read runs against a healthy
/// server that answers immediately, with a 200ms per-page default while the caller spends
/// 100ms on each of five items (500ms in all). Every read must succeed.
/// </summary>
public class SlowConsumerPageDeadlineTests
{
    private const int Items = 5;
    private static readonly TimeSpan PerItem = TimeSpan.FromMilliseconds(100);
    private static readonly ClientOptions Options = new() { DefaultTimeout = TimeSpan.FromMilliseconds(200) };

    private static Authzed.Api.V1.Relationship Rel(int i) => new()
    {
        Resource = new() { ObjectType = "document", ObjectId = $"doc{i}" },
        Relation = "viewer",
        Subject = new() { Object = new() { ObjectType = "user", ObjectId = "alice" } },
    };

    // A full page of Items for a request without a cursor; an empty page after it.
    private static bool FirstPage(Cursor? cursor) => string.IsNullOrEmpty(cursor?.Token);

    private sealed class Healthy : PermissionsService.PermissionsServiceBase
    {
        public override async Task ReadRelationships(ReadRelationshipsRequest request, IServerStreamWriter<ReadRelationshipsResponse> stream, ServerCallContext context)
        {
            if (!FirstPage(request.OptionalCursor)) return;
            for (var i = 0; i < Items; i++) await stream.WriteAsync(new() { Relationship = Rel(i), AfterResultCursor = new() { Token = $"c{i}" } });
        }
        public override async Task LookupResources(LookupResourcesRequest request, IServerStreamWriter<LookupResourcesResponse> stream, ServerCallContext context)
        {
            if (!FirstPage(request.OptionalCursor)) return;
            for (var i = 0; i < Items; i++) await stream.WriteAsync(new() { ResourceObjectId = $"doc{i}", Permissionship = LookupPermissionship.HasPermission, AfterResultCursor = new() { Token = $"c{i}" } });
        }
        public override async Task LookupSubjects(LookupSubjectsRequest request, IServerStreamWriter<LookupSubjectsResponse> stream, ServerCallContext context)
        {
            for (var i = 0; i < Items; i++) await stream.WriteAsync(new() { Subject = new() { SubjectObjectId = $"user{i}", Permissionship = LookupPermissionship.HasPermission } });
        }
        public override async Task ExportBulkRelationships(ExportBulkRelationshipsRequest request, IServerStreamWriter<ExportBulkRelationshipsResponse> stream, ServerCallContext context)
        {
            if (!FirstPage(request.OptionalCursor)) return;
            for (var i = 0; i < Items; i++)
            {
                var response = new ExportBulkRelationshipsResponse { AfterResultCursor = new() { Token = $"c{i}" } };
                response.Relationships.Add(Rel(i));
                await stream.WriteAsync(response);
            }
        }
    }

    private sealed class HealthySets : M.WatchPermissionSetsService.WatchPermissionSetsServiceBase
    {
        public override async Task LookupPermissionSets(M.LookupPermissionSetsRequest request, IServerStreamWriter<M.LookupPermissionSetsResponse> stream, ServerCallContext context)
        {
            for (var i = 0; i < Items; i++)
                await stream.WriteAsync(new()
                {
                    Change = new()
                    {
                        AtRevision = new() { Token = "rev" },
                        Operation = M.PermissionSetChange.Types.SetOperation.Added,
                        ParentSet = new() { ObjectType = "document", ObjectId = "1", PermissionOrRelation = "view" },
                        ChildMember = new() { ObjectType = "user", ObjectId = $"user{i}" },
                    },
                });
        }
    }

    private static async Task<(WebApplication App, SpiceDBClient Client)> StartAsync()
    {
        AppContext.SetSwitch("System.Net.Http.SocketsHttpHandler.Http2UnencryptedSupport", true);
        var builder = WebApplication.CreateBuilder();
        builder.Logging.ClearProviders();
        builder.WebHost.ConfigureKestrel(o => o.Listen(IPAddress.Loopback, 0, l => l.Protocols = HttpProtocols.Http2));
        builder.Services.AddGrpc();
        var app = builder.Build();
        app.MapGrpcService<Healthy>();
        app.MapGrpcService<HealthySets>();
        await app.StartAsync();
        var address = app.Services.GetRequiredService<IServer>().Features.Get<IServerAddressesFeature>()!.Addresses.First();
        return (app, SpiceDBClient.CreateFromChannel(GrpcChannel.ForAddress(address), "test-token", Options));
    }

    /// <summary>Consumes every item slowly and returns how many there were.</summary>
    private static async Task<int> SlowlyConsume<T>(Func<SpiceDBClient, IAsyncEnumerable<T>> read)
    {
        var (app, client) = await StartAsync();
        try
        {
            var count = 0;
            await foreach (var _ in read(client))
            {
                count++;
                await Task.Delay(PerItem);
            }
            return count;
        }
        finally { await client.DisposeAsync(); await app.DisposeAsync(); }
    }

    [Fact]
    public async Task ReadRelationships() =>
        (await SlowlyConsume(c => c.ReadRelationshipsAsync(Consistency.Full(), new Filter("document"), new ReadOptions { PageSize = Items }))).Should().Be(Items);

    [Fact]
    public async Task LookupResources() =>
        (await SlowlyConsume(c => c.LookupResourcesAsync(Consistency.Full(), "document", "view", new ObjectRef("user", "alice"), new LookupOptions { PageSize = Items }))).Should().Be(Items);

    [Fact]
    public async Task LookupSubjects() =>
        (await SlowlyConsume(c => c.LookupSubjectsAsync(Consistency.Full(), new ObjectRef("document", "1"), "view", "user"))).Should().Be(Items);

    [Fact]
    public async Task LookupPermissionSets() =>
        (await SlowlyConsume(c => c.ExperimentalLookupPermissionSetsAsync(Items, null))).Should().Be(Items);

    [Fact]
    public async Task ExportRelationships() =>
        (await SlowlyConsume(c => c.ExportRelationshipsAsync(Consistency.Full(), new Filter("document"), new ExportOptions { PageSize = Items }))).Should().Be(Items);

    [Fact]
    public async Task SingleRequestReadIsTimedPerMessage() =>
        (await SlowlyConsume(c => c.ReadRelationshipsAsync(Consistency.Full(), new Filter("document"), new ReadOptions { Paging = Paging.SingleRequest }))).Should().Be(Items);
}
