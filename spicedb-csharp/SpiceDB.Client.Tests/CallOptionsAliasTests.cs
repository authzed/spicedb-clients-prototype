// This file imports Authzed.Api.V1, Grpc.Core and SpiceDB.Client, which share eleven type
// names (NamespaceCollisionTests finds them by reflection), and writes each of them bare. It
// also writes a custom CallInvoker whose overrides take Grpc.Core.CallOptions, as a caller
// adapting CreateFromCallInvoker would. It compiles only because of the alias block the AI guide
// and migration guide document, copied here verbatim: a using-alias takes precedence over
// namespace imports, so each bare name below names SpiceDB.Client's type instead of being
// ambiguous (CS0104), and the prefixed aliases name the other namespaces' types.
// The file uses a namespace outside SpiceDB.Client on purpose: inside it, SpiceDB.Client's
// own types would win over any alias, which is not what a caller's code sees.
using Authzed.Api.V1;
using Grpc.Core;
using SpiceDB.Client;
using Xunit;

// Shared with Grpc.Core
using CallOptions = SpiceDB.Client.CallOptions;
using WriteOptions = SpiceDB.Client.WriteOptions;
using GrpcCallOptions = Grpc.Core.CallOptions;   // CallInvoker overrides
using GrpcWriteOptions = Grpc.Core.WriteOptions;

// Shared with Authzed.Api.V1
using Relationship = SpiceDB.Client.Relationship;
using Consistency = SpiceDB.Client.Consistency;
using ZedToken = SpiceDB.Client.ZedToken;
using Precondition = SpiceDB.Client.Precondition;
using RelationshipUpdate = SpiceDB.Client.RelationshipUpdate;
using ResolvedSubject = SpiceDB.Client.ResolvedSubject;
using PartialCaveatInfo = SpiceDB.Client.PartialCaveatInfo;
using DebugInformation = SpiceDB.Client.DebugInformation;
using CheckDebugTrace = SpiceDB.Client.CheckDebugTrace;
using ProtoRelationship = Authzed.Api.V1.Relationship;
using ProtoConsistency = Authzed.Api.V1.Consistency;
using ProtoZedToken = Authzed.Api.V1.ZedToken;
using ProtoPrecondition = Authzed.Api.V1.Precondition;
using ProtoRelationshipUpdate = Authzed.Api.V1.RelationshipUpdate;
using ProtoResolvedSubject = Authzed.Api.V1.ResolvedSubject;
using ProtoPartialCaveatInfo = Authzed.Api.V1.PartialCaveatInfo;
using ProtoDebugInformation = Authzed.Api.V1.DebugInformation;
using ProtoCheckDebugTrace = Authzed.Api.V1.CheckDebugTrace;

namespace CallOptionsAliasConsumer;

public class CallOptionsAliasTests
{
    private sealed class CannedSchemaInvoker : CallInvoker
    {
        public int Calls { get; private set; }

        public override TResponse BlockingUnaryCall<TRequest, TResponse>(Method<TRequest, TResponse> method, string? host, GrpcCallOptions options, TRequest request) =>
            throw new NotSupportedException();

        public override AsyncUnaryCall<TResponse> AsyncUnaryCall<TRequest, TResponse>(Method<TRequest, TResponse> method, string? host, GrpcCallOptions options, TRequest request)
        {
            Calls++;
            var response = (TResponse)(object)new ReadSchemaResponse
            {
                SchemaText = "definition user {}",
                ReadAt = new ProtoZedToken { Token = "rev1" },
            };
            return new AsyncUnaryCall<TResponse>(
                Task.FromResult(response),
                Task.FromResult(new Metadata()),
                () => Status.DefaultSuccess,
                () => new Metadata(),
                () => { });
        }

        public override AsyncServerStreamingCall<TResponse> AsyncServerStreamingCall<TRequest, TResponse>(Method<TRequest, TResponse> method, string? host, GrpcCallOptions options, TRequest request) =>
            throw new NotSupportedException();

        public override AsyncClientStreamingCall<TRequest, TResponse> AsyncClientStreamingCall<TRequest, TResponse>(Method<TRequest, TResponse> method, string? host, GrpcCallOptions options) =>
            throw new NotSupportedException();

        public override AsyncDuplexStreamingCall<TRequest, TResponse> AsyncDuplexStreamingCall<TRequest, TResponse>(Method<TRequest, TResponse> method, string? host, GrpcCallOptions options) =>
            throw new NotSupportedException();
    }

    [Fact]
    public async Task CustomCallInvokerCompilesAlongsideTheClientNamespace()
    {
        var invoker = new CannedSchemaInvoker();
        await using var client = SpiceDBClient.CreateFromCallInvoker(invoker);

        var schema = await client.ReadSchemaAsync(new CallOptions { Timeout = TimeSpan.FromSeconds(5) });

        Assert.Equal("definition user {}", schema.Schema);
        Assert.Equal("rev1", schema.Revision.Value);
        Assert.Equal(1, invoker.Calls);
    }

    /// <summary>Each bare name is SpiceDB.Client's type, and each prefixed alias the other namespace's.</summary>
    [Fact]
    public void EveryAliasNamesTheIntendedType()
    {
        Assert.Equal(typeof(SpiceDB.Client.CallOptions), typeof(CallOptions));
        Assert.Equal(typeof(SpiceDB.Client.WriteOptions), typeof(WriteOptions));
        Assert.Equal(typeof(SpiceDB.Client.Relationship), typeof(Relationship));
        Assert.Equal(typeof(SpiceDB.Client.Consistency), typeof(Consistency));
        Assert.Equal(typeof(SpiceDB.Client.ZedToken), typeof(ZedToken));
        Assert.Equal(typeof(SpiceDB.Client.Precondition), typeof(Precondition));
        Assert.Equal(typeof(SpiceDB.Client.RelationshipUpdate), typeof(RelationshipUpdate));
        Assert.Equal(typeof(SpiceDB.Client.ResolvedSubject), typeof(ResolvedSubject));
        Assert.Equal(typeof(SpiceDB.Client.PartialCaveatInfo), typeof(PartialCaveatInfo));
        Assert.Equal(typeof(SpiceDB.Client.DebugInformation), typeof(DebugInformation));
        Assert.Equal(typeof(SpiceDB.Client.CheckDebugTrace), typeof(CheckDebugTrace));

        Assert.Equal("Grpc.Core", typeof(GrpcCallOptions).Namespace);
        Assert.Equal("Grpc.Core", typeof(GrpcWriteOptions).Namespace);
        foreach (var proto in new[]
        {
            typeof(ProtoRelationship), typeof(ProtoConsistency), typeof(ProtoZedToken), typeof(ProtoPrecondition),
            typeof(ProtoRelationshipUpdate), typeof(ProtoResolvedSubject), typeof(ProtoPartialCaveatInfo),
            typeof(ProtoDebugInformation), typeof(ProtoCheckDebugTrace),
        })
            Assert.Equal("Authzed.Api.V1", proto.Namespace);

        // The bare names work as values too, not only in typeof.
        var token = new ZedToken("rev1");
        Assert.Equal("AtLeast(rev1)", Consistency.AtLeast(token).ToString());
    }
}
