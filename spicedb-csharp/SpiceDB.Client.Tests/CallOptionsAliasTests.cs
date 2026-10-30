// This file imports both Grpc.Core and SpiceDB.Client, which each define a CallOptions
// type, and writes a custom CallInvoker whose overrides take Grpc.Core.CallOptions, as a
// caller adapting CreateFromCallInvoker would. It compiles only because of the alias the AI
// guide and migration guide document: a using-alias takes precedence over namespace imports,
// so `CallOptions` below names Grpc.Core's type instead of being ambiguous (CS0104).
// Aliasing only this library's type (SpiceDBCallOptions) leaves the bare name ambiguous.
// The file uses a namespace outside SpiceDB.Client on purpose: inside it, SpiceDB.Client's
// own CallOptions would win over any alias, which is not what a caller's code sees.
using Authzed.Api.V1;
using Grpc.Core;
using SpiceDB.Client;
using Xunit;
using CallOptions = Grpc.Core.CallOptions;
using SpiceDBCallOptions = SpiceDB.Client.CallOptions;

namespace CallOptionsAliasConsumer;

public class CallOptionsAliasTests
{
    private sealed class CannedSchemaInvoker : CallInvoker
    {
        public int Calls { get; private set; }

        public override TResponse BlockingUnaryCall<TRequest, TResponse>(Method<TRequest, TResponse> method, string? host, CallOptions options, TRequest request) =>
            throw new NotSupportedException();

        public override AsyncUnaryCall<TResponse> AsyncUnaryCall<TRequest, TResponse>(Method<TRequest, TResponse> method, string? host, CallOptions options, TRequest request)
        {
            Calls++;
            var response = (TResponse)(object)new ReadSchemaResponse
            {
                SchemaText = "definition user {}",
                ReadAt = new Authzed.Api.V1.ZedToken { Token = "rev1" },
            };
            return new AsyncUnaryCall<TResponse>(
                Task.FromResult(response),
                Task.FromResult(new Metadata()),
                () => Status.DefaultSuccess,
                () => new Metadata(),
                () => { });
        }

        public override AsyncServerStreamingCall<TResponse> AsyncServerStreamingCall<TRequest, TResponse>(Method<TRequest, TResponse> method, string? host, CallOptions options, TRequest request) =>
            throw new NotSupportedException();

        public override AsyncClientStreamingCall<TRequest, TResponse> AsyncClientStreamingCall<TRequest, TResponse>(Method<TRequest, TResponse> method, string? host, CallOptions options) =>
            throw new NotSupportedException();

        public override AsyncDuplexStreamingCall<TRequest, TResponse> AsyncDuplexStreamingCall<TRequest, TResponse>(Method<TRequest, TResponse> method, string? host, CallOptions options) =>
            throw new NotSupportedException();
    }

    [Fact]
    public async Task CustomCallInvokerCompilesAlongsideTheClientNamespace()
    {
        var invoker = new CannedSchemaInvoker();
        await using var client = SpiceDBClient.CreateFromCallInvoker(invoker);

        var schema = await client.ReadSchemaAsync(new SpiceDBCallOptions { Timeout = TimeSpan.FromSeconds(5) });

        Assert.Equal("definition user {}", schema.Schema);
        Assert.Equal("rev1", schema.Revision.Value);
        Assert.Equal(1, invoker.Calls);
    }
}
