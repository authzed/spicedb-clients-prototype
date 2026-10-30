using FluentAssertions;
using Grpc.Core;
using Xunit;

namespace SpiceDB.Client.Tests;

/// <summary>
/// A watch start revision or materialize at-revision is optional: null means absent. A present
/// token that is empty or whitespace-only fails locally with <see cref="InvalidArgumentException"/>
/// before any RPC, as the strict consistency strategies do.
/// </summary>
public class OptionalRevisionInputTests
{
    /// <summary>Counts every call and fails it, so a test can prove no RPC was attempted.</summary>
    private sealed class CountingInvoker : CallInvoker
    {
        public int Calls;
        private Exception Fail() { Interlocked.Increment(ref Calls); return new RpcException(new Status(StatusCode.Unavailable, "test invoker")); }
        public override TResponse BlockingUnaryCall<TRequest, TResponse>(Method<TRequest, TResponse> method, string? host, Grpc.Core.CallOptions options, TRequest request) => throw Fail();
        public override AsyncUnaryCall<TResponse> AsyncUnaryCall<TRequest, TResponse>(Method<TRequest, TResponse> method, string? host, Grpc.Core.CallOptions options, TRequest request) => throw Fail();
        public override AsyncServerStreamingCall<TResponse> AsyncServerStreamingCall<TRequest, TResponse>(Method<TRequest, TResponse> method, string? host, Grpc.Core.CallOptions options, TRequest request) => throw Fail();
        public override AsyncClientStreamingCall<TRequest, TResponse> AsyncClientStreamingCall<TRequest, TResponse>(Method<TRequest, TResponse> method, string? host, Grpc.Core.CallOptions options) => throw Fail();
        public override AsyncDuplexStreamingCall<TRequest, TResponse> AsyncDuplexStreamingCall<TRequest, TResponse>(Method<TRequest, TResponse> method, string? host, Grpc.Core.CallOptions options) => throw Fail();
    }

    private static async Task Drain<T>(IAsyncEnumerable<T> items) { await foreach (var _ in items) { } }

    public static TheoryData<string> BlankTokens => new() { "", " ", "\t\n" };

    [Theory]
    [MemberData(nameof(BlankTokens))]
    public async Task BlankRevisionFailsLocallyBeforeAnyRpc(string blank)
    {
        var invoker = new CountingInvoker();
        await using var client = SpiceDBClient.CreateFromCallInvoker(invoker);
        var token = new ZedToken(blank);

        (await FluentActions.Awaiting(() => Drain(client.UpdatesAsync([], token)))
            .Should().ThrowAsync<InvalidArgumentException>()).WithMessage("startRevision must be null or a nonempty revision token.");
        (await FluentActions.Awaiting(() => Drain(client.ExperimentalWatchPermissionSetsAsync(token)))
            .Should().ThrowAsync<InvalidArgumentException>()).WithMessage("startRevision must be null or a nonempty revision token.");
        (await FluentActions.Awaiting(() => Drain(client.ExperimentalLookupPermissionSetsAsync(10, token)))
            .Should().ThrowAsync<InvalidArgumentException>()).WithMessage("atRevision must be null or a nonempty revision token.");
        (await FluentActions.Awaiting(() => client.ExperimentalDownloadPermissionSetsAsync(token))
            .Should().ThrowAsync<InvalidArgumentException>()).WithMessage("atRevision must be null or a nonempty revision token.");
        await FluentActions.Awaiting(() => Drain(client.UpdatesAsync([], default(ZedToken))))
            .Should().ThrowAsync<InvalidArgumentException>();

        invoker.Calls.Should().Be(0);
    }

    [Fact]
    public async Task NullRevisionStillMeansAbsentAndReachesTheServer()
    {
        var invoker = new CountingInvoker();
        await using var client = SpiceDBClient.CreateFromCallInvoker(invoker, new ClientOptions { RetryPolicy = RetryPolicy.None });

        await FluentActions.Awaiting(() => client.ExperimentalDownloadPermissionSetsAsync(null))
            .Should().ThrowAsync<SpiceDBException>().Where(e => e.StatusCode == StatusCode.Unavailable);

        invoker.Calls.Should().BeGreaterThan(0);
    }
}
