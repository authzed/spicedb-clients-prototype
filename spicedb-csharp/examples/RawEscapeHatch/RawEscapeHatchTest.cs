// Example RawEscapeHatch demonstrates reaching past the idiomatic API with
// RawProto().
//
// RawProto returns the existing authenticated connection and generated service clients.
// This example demonstrates protocol-shaped writes and checks; ordinary applications
// can use WriteOptions.Metadata and CheckAsync for the same operations with native
// values, total deadlines, typed errors, and retry handling.
//
// What you give up on the raw path, and why the idiomatic methods stay the
// default: no SpiceDBException mapping (you catch RpcException), no retry on a
// transient failure, and no DefaultTimeout — pass a deadline yourself.

using Authzed.Api.V1;
using Google.Protobuf.WellKnownTypes;
using SpiceDB.Client;
using Xunit;
using static SpiceDB.Client.Consistency;

namespace RawEscapeHatch;

public class RawEscapeHatchTest
{
    private const string Schema = """
        definition user {}

        definition document {
            relation viewer: user
            permission view = viewer
        }
        """;

    [Fact]
    public async Task RawProto_SendsAFieldTheIdiomaticApiDoesNotExpose()
    {
        await using var client = SpiceDBClient.CreatePlaintext(SpiceDBTestServer.Endpoint, SpiceDBTestServer.Token);
        // The schema above is deliberately narrower than the one the other
        // examples write, and all thirteen share one SpiceDB. SpiceDB refuses a
        // WriteSchema that drops a relation while a relationship still exists
        // under it, so clear what an earlier example left behind first --
        // otherwise this fails with "cannot delete relation `editor` in object
        // definition `document`".
        await SpiceDBTestServer.ClearDocumentRelationshipsAsync(client);
        await client.WriteSchemaAsync(Schema, null, default);

        // The bearer token rides this client's own call invoker, so there is
        // nothing extra to attach to a raw call.
        var metadata = new Struct
        {
            Fields =
            {
                ["correlation_id"] = Value.ForString("example-42"),
                ["actor"] = Value.ForString("billing-job"),
            },
        };

        // Fully qualified on purpose, not out of habit: `Relationship`,
        // `RelationshipUpdate` and `Consistency` exist in BOTH namespaces — the
        // idiomatic `SpiceDB.Client` one and the generated `Authzed.Api.V1` one —
        // and a raw call takes the proto types. A `using` alias would hide exactly
        // the tier boundary this example is about, so the names say which tier they
        // mean.
        var written = await client.RawProto().Permissions.WriteRelationshipsAsync(
            new WriteRelationshipsRequest
            {
                Updates =
                {
                    new Authzed.Api.V1.RelationshipUpdate
                    {
                        Operation = Authzed.Api.V1.RelationshipUpdate.Types.Operation.Touch,
                        Relationship = new Authzed.Api.V1.Relationship
                        {
                            Resource = new ObjectReference
                            {
                                ObjectType = "document", ObjectId = "ledger",
                            },
                            Relation = "viewer",
                            Subject = new SubjectReference
                            {
                                Object = new ObjectReference
                                {
                                    ObjectType = "user", ObjectId = "jimmy",
                                },
                            },
                        },
                    },
                },
                OptionalTransactionMetadata = metadata,
            });

        var revision = written.WrittenAt.Token;
        Console.WriteLine($"raw write committed at revision {revision}");
        Assert.False(string.IsNullOrEmpty(revision));

        // The idiomatic API picks up right where the raw call left off — same
        // client, same connection, including read-your-writes on the raw
        // revision.
        var result = await client.CheckAsync(new PermissionCheck(new SpiceDB.Client.Relationship(new ObjectRef("document", "ledger"), "view", new SubjectRef(new ObjectRef("user", "jimmy"))).Resource, "view", new SpiceDB.Client.Relationship(new ObjectRef("document", "ledger"), "view", new SubjectRef(new ObjectRef("user", "jimmy"))).Subject, AtLeast(new SpiceDB.Client.ZedToken(revision))), null, default);
        Console.WriteLine($"user:jimmy can view document:ledger: {result.HasPermission}");
        Assert.True(result.HasPermission);

        await client.DeleteAllRelationshipsNonAtomicAsync(new Filter("document").WithResourceId("ledger"), null, default);
    }

    [Fact]
    public async Task RawProto_CallsAnRpcTheIdiomaticApiRoutesAround()
    {
        await using var client = SpiceDBClient.CreatePlaintext(SpiceDBTestServer.Endpoint, SpiceDBTestServer.Token);
        // The schema above is deliberately narrower than the one the other
        // examples write, and all thirteen share one SpiceDB. SpiceDB refuses a
        // WriteSchema that drops a relation while a relationship still exists
        // under it, so clear what an earlier example left behind first --
        // otherwise this fails with "cannot delete relation `editor` in object
        // definition `document`".
        await SpiceDBTestServer.ClearDocumentRelationshipsAsync(client);
        await client.WriteSchemaAsync(Schema, null, default);
        var txn = new Transaction();
        txn.Touch(new SpiceDB.Client.Relationship(new ObjectRef("document", "ledger"), "viewer", new SubjectRef(new ObjectRef("user", "jimmy"))));
        await client.WriteAsync(txn, null, default);

        // A raw call gets no client default deadline — pass one yourself.
        var response = await client.RawProto().Permissions.CheckPermissionAsync(
            new CheckPermissionRequest
            {
                Consistency = new Authzed.Api.V1.Consistency { FullyConsistent = true },
                Resource = new ObjectReference { ObjectType = "document", ObjectId = "ledger" },
                Permission = "view",
                Subject = new SubjectReference
                {
                    Object = new ObjectReference { ObjectType = "user", ObjectId = "jimmy" },
                },
            },
            deadline: DateTime.UtcNow.AddSeconds(30));

        Console.WriteLine($"raw CheckPermission permissionship: {response.Permissionship}");
        Assert.Equal(
            CheckPermissionResponse.Types.Permissionship.HasPermission,
            response.Permissionship);

        // Dispose the CLIENT, never the object RawProto() returned — it holds
        // this client's connection, and DisposeAsync is what releases it.
        await client.DeleteAllRelationshipsNonAtomicAsync(new Filter("document").WithResourceId("ledger"), null, default);
    }
}
