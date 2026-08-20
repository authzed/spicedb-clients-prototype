using Xunit;

using System.Net;
using Authzed.Api.V1;
using Grpc.Core;
using Grpc.Net.Client;
using Microsoft.AspNetCore.Builder;
using Microsoft.AspNetCore.Hosting;
using Microsoft.AspNetCore.Hosting.Server;
using Microsoft.AspNetCore.Hosting.Server.Features;
using Microsoft.AspNetCore.Server.Kestrel.Core;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.Hosting;
using Microsoft.Extensions.Logging;
using SpiceDB.Client;
using static SpiceDB.Client.Consistency;
using Relationship = SpiceDB.Client.Relationship;

namespace UnrepresentableValues;

/// <summary>
/// Example UnrepresentableValues demonstrates both directions of root DESIGN.md,
/// "RULE: A conversion that cannot preserve meaning must fail".
/// </summary>
/// <remarks>
/// <para>
/// The rule has two clauses that point opposite ways, and confusing them is the
/// failure mode either way:
/// </para>
/// <list type="number">
/// <item>Data the CALLER supplied that the client cannot represent must raise a typed
/// error <em>naming what could not be converted</em>. The caller can see the failure
/// and fix their input, so the client neither approximates the value nor drops it --
/// silently discarding it turns a caller's mistake into a silent wrong answer.</item>
/// <item>Values the SERVER supplied that the client does not recognise must NOT raise,
/// and must map to the safe, non-permissive default -- never a grant. Raising would
/// turn a routine SpiceDB upgrade that adds an enum value into a client-side
/// outage.</item>
/// </list>
/// <para>
/// The last test covers clause 2, and needs a server that emits a permissionship this
/// client has never heard of -- which is why it stands up a stand-in.
/// </para>
/// </remarks>
public class UnrepresentableValuesTest
{
    [Fact]
    public async Task UnconvertibleCaveatContext_NamesTheKey()
    {
        // A value with no protobuf representation fails loudly, naming the key. Dropping
        // it would leave a caveat evaluating against context the caller believes it sent,
        // and a caller with a large context map should not have to bisect it.
        await using var client = SpiceDBClient.CreatePlaintext(
            SpiceDBTestServer.Endpoint, SpiceDBTestServer.Token);

        var rel = Relationship.FromTriple("document", "readme", "viewer", "user", "alice")
            .WithCaveat("only_on_tuesday", new Dictionary<string, object?>
            {
                ["day"] = "tuesday",
                ["impostor"] = new object(),
            });

        var txn = new Transaction();
        var ex = Assert.ThrowsAny<Exception>(() => { txn.Touch(rel); });
        Assert.Contains("impostor", ex.Message);
        Assert.DoesNotContain("day", ex.Message);
    }

    [Fact]
    public async Task FilterWithSubjectIdAndNoSubjectType_IsRefused()
    {
        // A subject ID with no subject type is not a narrower filter -- the wire format
        // simply drops it, so the filter silently WIDENS. Applied to
        // DeleteRelationshipsAsync that is the difference between deleting alice's
        // relationships and deleting every relationship on every document.
        await using var client = SpiceDBClient.CreatePlaintext(
            SpiceDBTestServer.Endpoint, SpiceDBTestServer.Token);

        var ex = await Assert.ThrowsAnyAsync<Exception>(
            () => client.DeleteRelationshipsAsync(new Filter("document").WithSubjectID("alice")));
        Assert.Contains("SubjectType", ex.Message);

        // The same filter with the missing piece supplied converts fine, which is what
        // makes the check above a real constraint rather than a blanket ban.
        await client.DeleteRelationshipsAsync(
            new Filter("document").WithSubjectType("user").WithSubjectID("alice"));
    }

    /// <summary>
    /// Answers with a permissionship value from a SpiceDB newer than this client.
    /// </summary>
    private sealed class FutureService : PermissionsService.PermissionsServiceBase
    {
        public override Task<CheckBulkPermissionsResponse> CheckBulkPermissions(
            CheckBulkPermissionsRequest request, ServerCallContext context)
        {
            var response = new CheckBulkPermissionsResponse();
            foreach (var _ in request.Items)
            {
                response.Pairs.Add(new CheckBulkPermissionsPair
                {
                    // 4242 is not a value this client's enum knows. A SpiceDB that added
                    // a permissionship after this client shipped would look exactly like
                    // this on the wire.
                    Item = new CheckBulkPermissionsResponseItem
                    {
                        Permissionship = (CheckPermissionResponse.Types.Permissionship)4242,
                    },
                });
            }

            return Task.FromResult(response);
        }
    }

    [Fact]
    public async Task UnknownServerPermissionship_NeitherRaisesNorGrants()
    {
        // Clause 2: the opposite posture. Raising here would break forward compatibility
        // -- a SpiceDB rolling out a new enum value would make every deployed client
        // throw on every check.
        AppContext.SetSwitch("System.Net.Http.SocketsHttpHandler.Http2UnencryptedSupport", true);

        var builder = WebApplication.CreateBuilder();
        builder.Logging.ClearProviders();
        builder.WebHost.ConfigureKestrel(options =>
            options.Listen(IPAddress.Loopback, 0, o => o.Protocols = HttpProtocols.Http2));
        builder.Services.AddGrpc();

        var app = builder.Build();
        app.MapGrpcService<FutureService>();
        await app.StartAsync();
        try
        {
            var address = app.Services.GetRequiredService<IServer>()
                .Features.Get<IServerAddressesFeature>()!.Addresses.First();
            using var channel = GrpcChannel.ForAddress(address);
            await using var client = SpiceDBClient.CreateFromChannel(channel, "some-token");

            var result = await client.CheckPermissionAsync(
                Full(), "view",
                Relationship.FromTriple("document", "readme", "view", "user", "alice"));

            Assert.False(
                result.HasPermission,
                "SECURITY: an unrecognised permissionship was treated as a grant");
        }
        finally
        {
            await app.StopAsync();
        }
    }
}
