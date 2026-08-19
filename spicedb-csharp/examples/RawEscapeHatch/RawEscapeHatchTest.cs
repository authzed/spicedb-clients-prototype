// Example RawEscapeHatch demonstrates reaching past the idiomatic API with
// RawProto().
//
// Every wrapper eventually meets a request the wrapper does not express. This
// client's answer is RawProto(): the underlying SpiceDBProtoClient, with the
// four generated service clients it makes its own calls through — a workaround
// short of forking the library. Root DESIGN.md, "What NOT To Do", allows
// exactly this as "clearly marked secondary API".
//
// The gaps demonstrated here are real, not hypothetical:
//
//   1. WriteRelationshipsRequest.OptionalTransactionMetadata is a proto field
//      this client does not surface anywhere. Applications use it to stamp an
//      audit correlation ID onto a write, which comes back out of the Watch
//      stream.
//   2. CheckPermission — the single-check RPC. The idiomatic
//      CheckPermissionAsync routes every check through CheckBulkPermissions,
//      so the raw client is how you drive the unary RPC itself.
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
        await using var client = SpiceDBClient.CreatePlaintext("localhost:50051", "somerandomkeyhere");
        await client.WriteSchemaAsync(Schema);

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
        var result = await client.CheckPermissionAsync(
            AtLeast(revision), "view",
            SpiceDB.Client.Relationship.FromTriple("document", "ledger", "view", "user", "jimmy"));
        Console.WriteLine($"user:jimmy can view document:ledger: {result.HasPermission}");
        Assert.True(result.HasPermission);

        await client.DeleteRelationshipsAsync(
            new Filter("document").WithResourceID("ledger"));
    }

    [Fact]
    public async Task RawProto_CallsAnRpcTheIdiomaticApiRoutesAround()
    {
        await using var client = SpiceDBClient.CreatePlaintext("localhost:50051", "somerandomkeyhere");
        await client.WriteSchemaAsync(Schema);
        var txn = new Transaction();
        txn.Touch(SpiceDB.Client.Relationship.FromTriple(
            "document", "ledger", "viewer", "user", "jimmy"));
        await client.WriteAsync(txn);

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
        await client.DeleteRelationshipsAsync(
            new Filter("document").WithResourceID("ledger"));
    }
}
