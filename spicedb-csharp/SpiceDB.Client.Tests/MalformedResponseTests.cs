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

namespace SpiceDB.Client.Tests;

/// <summary>
/// A response the client cannot interpret fails with INTERNAL (root DESIGN.md, "Expose native
/// errors…"): never skipped, never filled with empty values, never UNKNOWN, and not retried by
/// the default policy. Each server below sends one well-formed item followed by a malformed one,
/// so a client that skipped the bad item would return a short result instead of failing.
/// </summary>
public class MalformedResponseTests
{
    private static Authzed.Api.V1.Relationship Good() => new()
    {
        Resource = new() { ObjectType = "document", ObjectId = "1" },
        Relation = "viewer",
        Subject = new() { Object = new() { ObjectType = "user", ObjectId = "alice" } },
    };

    private static readonly Dictionary<string, Authzed.Api.V1.Relationship?> Broken = new()
    {
        ["missing relationship"] = null,
        ["missing relationship resource"] = new() { Relation = "viewer", Subject = Good().Subject },
        ["relationship resource has no object id"] = new() { Resource = new() { ObjectType = "document" }, Relation = "viewer", Subject = Good().Subject },
        ["missing relationship subject"] = new() { Resource = Good().Resource, Relation = "viewer" },
        ["missing relationship subject object"] = new() { Resource = Good().Resource, Relation = "viewer", Subject = new() },
    };

    private static int s_calls;
    private static string s_case = "";

    private sealed class Malformed : PermissionsService.PermissionsServiceBase
    {
        public override async Task ReadRelationships(ReadRelationshipsRequest request, IServerStreamWriter<ReadRelationshipsResponse> stream, ServerCallContext context)
        {
            Interlocked.Increment(ref s_calls);
            await stream.WriteAsync(new() { Relationship = Good(), AfterResultCursor = new() { Token = "c1" } });
            await stream.WriteAsync(new() { Relationship = Broken[s_case], AfterResultCursor = new() { Token = "c2" } });
        }
        public override async Task ExportBulkRelationships(ExportBulkRelationshipsRequest request, IServerStreamWriter<ExportBulkRelationshipsResponse> stream, ServerCallContext context)
        {
            Interlocked.Increment(ref s_calls);
            var response = new ExportBulkRelationshipsResponse { AfterResultCursor = new() { Token = "c1" } };
            response.Relationships.Add(Good());
            response.Relationships.Add(Broken[s_case] ?? new Authzed.Api.V1.Relationship());
            await stream.WriteAsync(response);
        }
        public override async Task LookupResources(LookupResourcesRequest request, IServerStreamWriter<LookupResourcesResponse> stream, ServerCallContext context)
        {
            Interlocked.Increment(ref s_calls);
            await stream.WriteAsync(new() { ResourceObjectId = "1", Permissionship = LookupPermissionship.HasPermission, AfterResultCursor = new() { Token = "c1" } });
            await stream.WriteAsync(new() { Permissionship = LookupPermissionship.HasPermission, AfterResultCursor = new() { Token = "c2" } });
        }
        public override async Task LookupSubjects(LookupSubjectsRequest request, IServerStreamWriter<LookupSubjectsResponse> stream, ServerCallContext context)
        {
            Interlocked.Increment(ref s_calls);
            await stream.WriteAsync(new() { Subject = new() { SubjectObjectId = "alice", Permissionship = LookupPermissionship.HasPermission } });
            await stream.WriteAsync(new() { Subject = new() { Permissionship = LookupPermissionship.HasPermission } });
        }
        public override Task<ExpandPermissionTreeResponse> ExpandPermissionTree(ExpandPermissionTreeRequest request, ServerCallContext context)
        {
            Interlocked.Increment(ref s_calls);
            var tree = new PermissionRelationshipTree { ExpandedObject = Good().Resource, ExpandedRelation = "view", Leaf = new() };
            tree.Leaf.Subjects.Add(new SubjectReference());
            return Task.FromResult(new ExpandPermissionTreeResponse { TreeRoot = tree });
        }
        public override Task<CheckBulkPermissionsResponse> CheckBulkPermissions(CheckBulkPermissionsRequest request, ServerCallContext context)
        {
            Interlocked.Increment(ref s_calls);
            return Task.FromResult(new CheckBulkPermissionsResponse());
        }
    }

    private sealed class MalformedWatch : WatchService.WatchServiceBase
    {
        public override async Task Watch(WatchRequest request, IServerStreamWriter<WatchResponse> stream, ServerCallContext context)
        {
            var response = new WatchResponse { ChangesThrough = new() { Token = "rev" } };
            response.Updates.Add(new Authzed.Api.V1.RelationshipUpdate { Operation = Authzed.Api.V1.RelationshipUpdate.Types.Operation.Touch, Relationship = Broken[s_case] });
            await stream.WriteAsync(response);
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
        app.MapGrpcService<Malformed>();
        app.MapGrpcService<MalformedWatch>();
        await app.StartAsync();
        var address = app.Services.GetRequiredService<IServer>().Features.Get<IServerAddressesFeature>()!.Addresses.First();
        return (app, SpiceDBClient.CreateFromChannel(GrpcChannel.ForAddress(address), "test-token"));
    }

    private static async Task ExpectInternal(Func<Task> act, string detail)
    {
        var error = (await act.Should().ThrowAsync<SpiceDBException>()).Which;
        error.StatusCode.Should().Be(StatusCode.Internal);
        error.Status.StatusCode.Should().Be(StatusCode.Internal);
        SpiceDBException.CodeOf(error).Should().Be(StatusCode.Internal);
        error.Message.Should().Contain(detail);
        error.ServerMessage.Should().Be(error.Message);
    }

    private static async Task Drain<T>(IAsyncEnumerable<T> items, List<T> seen)
    {
        await foreach (var item in items) seen.Add(item);
    }

    // Serialised: the cases share the static server state above.
    [Fact]
    public async Task MalformedRelationshipsFailReadsExportsAndWatchWithInternal()
    {
        var (app, client) = await StartAsync();
        try
        {
            foreach (var detail in Broken.Keys)
            {
                s_case = detail;
                var read = new List<ReadRelationship>();
                s_calls = 0;
                await ExpectInternal(() => Drain(client.ReadRelationshipsAsync(Consistency.Full(), new Filter("document")), read), detail);
                read.Should().ContainSingle("the good item is yielded and the bad one fails the read, never skipped");
                s_calls.Should().Be(1, "INTERNAL is not retried by the default policy");

                var exported = new List<ExportedRelationship>();
                await ExpectInternal(() => Drain(client.ExportRelationshipsAsync(Consistency.Full(), new Filter("document")), exported), detail == "missing relationship" ? "relationship resource" : detail);
                exported.Should().ContainSingle();

                await ExpectInternal(() => Drain(client.UpdatesAsync(["document"], null), new List<WatchEvent>()), detail);
            }
        }
        finally { await client.DisposeAsync(); await app.DisposeAsync(); }
    }

    [Fact]
    public async Task MalformedLookupsExpandAndBulkCheckFailWithInternal()
    {
        var (app, client) = await StartAsync();
        try
        {
            var resources = new List<LookupResource>();
            await ExpectInternal(() => Drain(client.LookupResourcesAsync(Consistency.Full(), "document", "view", new ObjectRef("user", "alice")), resources), "missing resource object id");
            resources.Should().ContainSingle();

            var subjects = new List<LookupSubject>();
            await ExpectInternal(() => Drain(client.LookupSubjectsAsync(Consistency.Full(), new ObjectRef("document", "1"), "view", "user"), subjects), "missing subject object id");
            subjects.Should().ContainSingle();

            await ExpectInternal(() => client.ExpandPermissionTreeAsync(Consistency.Full(), new ObjectRef("document", "1"), "view"), "missing tree leaf subject object");

            var check = new PermissionCheck(new ObjectRef("document", "1"), "view", new ObjectRef("user", "alice"), Consistency.Full());
            await ExpectInternal(() => client.CheckBulkAsync([check]), "returned 0 pair(s) for 1 request item(s)");
        }
        finally { await client.DisposeAsync(); await app.DisposeAsync(); }
    }
}
