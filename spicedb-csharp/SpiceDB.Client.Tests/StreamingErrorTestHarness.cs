// Reusable harness for testing that streaming gRPC errors surface as typed
// SpiceDBException instances from the consumer's `await foreach`, instead of
// raw RpcExceptions. Used by ReadRelationships/LookupResources/LookupSubjects/
// ExportRelationships/UpdatesAsync (server-streaming) and ImportRelationships
// (client-streaming) error-mapping tests.

using Grpc.Core;

namespace SpiceDB.Client.Tests;

/// <summary>
/// A fake <see cref="IAsyncStreamReader{T}"/> that yields a fixed sequence of
/// items and then either completes the stream or throws a given
/// <see cref="RpcException"/> — simulating a server-streaming RPC that fails
/// mid-stream (or immediately, if <paramref name="items"/> is empty).
/// </summary>
internal sealed class FakeStreamReader<T> : IAsyncStreamReader<T>
{
    private readonly IEnumerator<T> _items;
    private readonly RpcException? _error;

    public FakeStreamReader(IEnumerable<T> items, RpcException? error = null)
    {
        _items = items.GetEnumerator();
        _error = error;
    }

    public T Current { get; private set; } = default!;

    public Task<bool> MoveNext(CancellationToken cancellationToken)
    {
        if (_items.MoveNext())
        {
            Current = _items.Current;
            return Task.FromResult(true);
        }

        if (_error != null)
            return Task.FromException<bool>(_error);

        return Task.FromResult(false);
    }
}

/// <summary>
/// A fake <see cref="IClientStreamWriter{T}"/> that always throws a given
/// <see cref="RpcException"/> from <see cref="WriteAsync(T)"/> — simulating a
/// client-streaming RPC whose send fails (e.g. because the server has already
/// aborted the call).
/// </summary>
internal sealed class ThrowingClientStreamWriter<T> : IClientStreamWriter<T>
{
    private readonly RpcException _error;

    public ThrowingClientStreamWriter(RpcException error)
    {
        _error = error;
    }

    public WriteOptions? WriteOptions { get; set; }

    public Task WriteAsync(T message) => Task.FromException(_error);

    public Task CompleteAsync() => Task.FromException(_error);
}

internal static class StreamingTestHelpers
{
    /// <summary>
    /// Wraps a fake response-stream reader in a real <see cref="AsyncServerStreamingCall{TResponse}"/>,
    /// as returned by generated gRPC server-streaming client methods.
    /// </summary>
    public static AsyncServerStreamingCall<TResponse> MakeServerStreamingCall<TResponse>(
        IAsyncStreamReader<TResponse> reader) =>
        new(
            reader,
            Task.FromResult(new Metadata()),
            () => Status.DefaultSuccess,
            () => new Metadata(),
            () => { });

    /// <summary>
    /// Wraps a fake request-stream writer in a real <see cref="AsyncClientStreamingCall{TRequest,TResponse}"/>,
    /// as returned by generated gRPC client-streaming client methods. The
    /// response task is one that never completes with a value — the send-side
    /// failure is expected to surface before completion is awaited.
    /// </summary>
    public static AsyncClientStreamingCall<TRequest, TResponse> MakeClientStreamingCall<TRequest, TResponse>(
        IClientStreamWriter<TRequest> writer,
        Task<TResponse> responseAsync) =>
        new(
            writer,
            responseAsync,
            Task.FromResult(new Metadata()),
            () => Status.DefaultSuccess,
            () => new Metadata(),
            () => { });

    /// <summary>
    /// Wraps a response in a real <see cref="AsyncUnaryCall{TResponse}"/>, as
    /// returned by generated gRPC unary client methods (e.g.
    /// DeleteRelationshipsAsync/WriteRelationshipsAsync).
    /// </summary>
    public static AsyncUnaryCall<TResponse> MakeUnaryCall<TResponse>(TResponse response) =>
        new(
            Task.FromResult(response),
            Task.FromResult(new Metadata()),
            () => Status.DefaultSuccess,
            () => new Metadata(),
            () => { });

    /// <summary>A representative RpcException(NotFound) used across streaming-error tests.</summary>
    public static RpcException NotFoundError() =>
        new(new Status(StatusCode.NotFound, "nope"));

    /// <summary>
    /// A representative transient RpcException(Unavailable), used across
    /// streaming-establishment-retry tests.
    /// </summary>
    public static RpcException UnavailableError() =>
        new(new Status(StatusCode.Unavailable, "unavailable"));
}

/// <summary>
/// Scripts a sequence of server-streaming calls: each call to <see cref="Next"/>
/// pops the next scripted (items, trailing-error) entry and returns a fresh
/// <see cref="AsyncServerStreamingCall{TResponse}"/> backed by it, recording
/// how many times the underlying gRPC stub method was invoked. Used to
/// simulate a stream that fails transiently on first open/establishment and
/// succeeds on a later retry (or keeps failing, to test budget exhaustion),
/// while asserting exactly how many times the mocked stub was called — the
/// signal that establishment was retried (or, symmetrically, was NOT retried
/// after a yield, to prove no replay).
/// </summary>
internal sealed class StreamCallScript<T>
{
    private readonly Queue<(IEnumerable<T> Items, RpcException? Error)> _script = new();

    public int CallCount { get; private set; }

    public StreamCallScript<T> Then(IEnumerable<T> items, RpcException? error = null)
    {
        _script.Enqueue((items, error));
        return this;
    }

    public AsyncServerStreamingCall<T> Next()
    {
        CallCount++;
        if (_script.Count == 0)
            throw new InvalidOperationException(
                "StreamCallScript exhausted — the client made more streaming calls than scripted.");

        var (items, error) = _script.Dequeue();
        return StreamingTestHelpers.MakeServerStreamingCall(new FakeStreamReader<T>(items, error));
    }
}
