// Typed exception hierarchy for SpiceDB errors and gRPC error mapping.

using Google.Rpc;
using Grpc.Core;

namespace SpiceDB.Client;

/// <summary>
/// Base exception for all SpiceDB errors.
///
/// <para>Beyond the message, a SpiceDB error carries the server's structured explanation of the
/// failure when the server sent one — the <c>google.rpc.ErrorInfo</c> detail attached to the
/// status. That explanation is derived from the preserved <see cref="Exception.InnerException"/>,
/// so it can never drift from the status the exception was built out of. See root DESIGN.md,
/// "RULE: Error mapping must not lose the server's detail".</para>
/// </summary>
public class SpiceDBException : Exception
{
    /// <summary>
    /// SpiceDB's structured explanation for this failure: the name of an
    /// <c>authzed.api.v1.ErrorReason</c> enum value, e.g.
    /// <c>"ERROR_REASON_MAXIMUM_DEPTH_EXCEEDED"</c>. Empty when the server attached no
    /// <c>ErrorInfo</c>.
    ///
    /// <para>The value is surfaced exactly as the server sent it. A reason a newer server knows
    /// and this client does not is passed through unchanged rather than coerced or rejected: it is
    /// server-supplied, and root DESIGN.md's "RULE: A conversion that cannot preserve meaning must
    /// fail" requires server-supplied unknowns to degrade safely rather than throw.</para>
    /// </summary>
    public DeleteProgress? DeleteProgress { get; internal set; }
    public int? InputIndex { get; internal set; }
    public string Reason { get; }

    /// <summary>Who produced the reason. SpiceDB uses <c>"authzed.com"</c>.</summary>
    public string ReasonDomain { get; }

    /// <summary>
    /// The specifics behind the reason — which precondition failed, what depth limit was hit.
    /// Empty when the server attached no <c>ErrorInfo</c>.
    /// </summary>
    public IReadOnlyDictionary<string, string> ReasonMetadata { get; }

    /// <summary>
    /// The gRPC status code this error corresponds to. Server errors report the server's code;
    /// locally raised errors report their category's code (<see cref="InvalidArgumentException"/>
    /// is <see cref="StatusCode.InvalidArgument"/>, a local total-deadline expiry is
    /// <see cref="StatusCode.DeadlineExceeded"/>), so code that inspects
    /// <c>RpcException.StatusCode</c> can inspect this the same way.
    /// </summary>
    public StatusCode StatusCode { get; }

    /// <summary>
    /// The server's status message exactly as sent. Never null: for errors raised locally by
    /// this client (or constructed by caller code) it is the error's
    /// <see cref="Exception.Message"/>, so callers can always relay it without a fallback.
    /// Forward this, not <see cref="Exception.Message"/>, when relaying a server error to
    /// another party. <see cref="Create"/> sets it so test doubles look like the server.
    /// </summary>
    public string ServerMessage { get; internal set; }

    public SpiceDBException() : this(null, null) { }
    public SpiceDBException(string message) : this(message, null) { }

    public SpiceDBException(string? message, Exception? innerException)
        : this((innerException as RpcException)?.StatusCode ?? StatusCode.Unknown, message, innerException) { }

    /// <summary>Builds an error with an explicit status code, for subclasses.</summary>
    protected SpiceDBException(StatusCode statusCode, string? message, Exception? innerException) : base(message, innerException)
    {
        StatusCode = statusCode;
        ServerMessage = Message;
        var info = ErrorInfoOf(innerException);
        Reason = info?.Reason ?? string.Empty;
        ReasonDomain = info?.Domain ?? string.Empty;
        ReasonMetadata = info is null
            ? new System.Collections.ObjectModel.ReadOnlyDictionary<string, string>(new Dictionary<string, string>())
            : new System.Collections.ObjectModel.ReadOnlyDictionary<string, string>(new Dictionary<string, string>(info.Metadata));
    }

    /// <summary>
    /// This error as a gRPC <see cref="Grpc.Core.Status"/>. A server error returns the
    /// server's own status; any other error returns a status built from
    /// <see cref="StatusCode"/> and <see cref="ServerMessage"/>.
    /// </summary>
    public Grpc.Core.Status Status => InnerException is RpcException rpc && rpc.StatusCode == StatusCode
        ? rpc.Status
        : new Grpc.Core.Status(StatusCode, ServerMessage);

    /// <summary>
    /// The response trailers of the server error, including <c>grpc-status-details-bin</c>, or
    /// an empty collection for errors raised locally.
    /// </summary>
    public Metadata Trailers => InnerException is RpcException rpc && rpc.StatusCode == StatusCode ? rpc.Trailers : Metadata.Empty;

    /// <summary>
    /// Returns an <see cref="RpcException"/> carrying this error's status and trailers (and so
    /// its <c>ErrorInfo</c> detail), for code that forwards errors over gRPC or expects one.
    /// </summary>
    public RpcException ToRpcException() => new(Status, Trailers, Message);

    /// <summary>
    /// Builds the typed error for <paramref name="statusCode"/>, exactly as the client would
    /// map a server status with that code and message: the subclass matching the code (for
    /// example <see cref="NotFoundException"/>), with <see cref="ServerMessage"/> set. Intended
    /// for test doubles, so their errors satisfy every predicate real ones do.
    /// </summary>
    public static SpiceDBException Create(StatusCode statusCode, string message) =>
        ErrorMapper.ToSpiceDBException(new RpcException(new Grpc.Core.Status(statusCode, Native.Required(message, nameof(message)))));

    /// <summary>
    /// Maps a raw <see cref="RpcException"/> (for example one thrown by a fake or by the raw
    /// protocol client) to the typed error the native client would have thrown, preserving it
    /// as <see cref="Exception.InnerException"/>.
    /// </summary>
    public static SpiceDBException FromRpcException(RpcException exception) => ErrorMapper.ToSpiceDBException(exception);

    /// <summary>
    /// Classifies any exception as a gRPC status code: a <see cref="SpiceDBException"/> reports
    /// <see cref="StatusCode"/>, a raw <see cref="RpcException"/> reports its own code, an
    /// <see cref="OperationCanceledException"/> is <see cref="StatusCode.Cancelled"/>, and a
    /// <see cref="TimeoutException"/> is <see cref="StatusCode.DeadlineExceeded"/>. Other
    /// exceptions are classified by their inner exception chain, else
    /// <see cref="StatusCode.Unknown"/>.
    /// </summary>
    public static StatusCode CodeOf(Exception? exception)
    {
        for (var e = exception; e is not null; e = e.InnerException)
        {
            switch (e)
            {
                case SpiceDBException s: return s.StatusCode;
                case RpcException r: return r.StatusCode;
                case OperationCanceledException: return StatusCode.Cancelled;
                case TimeoutException: return StatusCode.DeadlineExceeded;
            }
        }
        return StatusCode.Unknown;
    }

    /// <summary>
    /// Returns the <c>google.rpc.ErrorInfo</c> detail carried by <paramref name="innerException"/>,
    /// or null.
    ///
    /// <para><c>GetRpcStatus</c> and <c>GetDetail</c> come from Google.Api.CommonProtos, so this
    /// reads the structured status gRPC already parsed out of the <c>grpc-status-details-bin</c>
    /// trailer rather than anything reconstructed from a message. Details of other types are
    /// skipped by <c>GetDetail</c>, so an unfamiliar detail never hides the familiar one.</para>
    /// </summary>
    private static ErrorInfo? ErrorInfoOf(Exception? innerException)
    {
        if (innerException is not RpcException rpc)
            return null;

        return rpc.GetRpcStatus()?.GetDetail<ErrorInfo>();
    }
}

/// <summary>The caller does not have permission to execute the operation.</summary>
public sealed class PermissionDeniedException : SpiceDBException
{
    public PermissionDeniedException(string message) : base(StatusCode.PermissionDenied, message, null) { }
    public PermissionDeniedException(string message, Exception innerException) : base(StatusCode.PermissionDenied, message, innerException) { }
}

/// <summary>The requested resource was not found.</summary>
public sealed class NotFoundException : SpiceDBException
{
    public NotFoundException(string message) : base(StatusCode.NotFound, message, null) { }
    public NotFoundException(string message, Exception innerException) : base(StatusCode.NotFound, message, innerException) { }
}

/// <summary>The resource already exists.</summary>
public sealed class AlreadyExistsException : SpiceDBException
{
    public AlreadyExistsException(string message) : base(StatusCode.AlreadyExists, message, null) { }
    public AlreadyExistsException(string message, Exception innerException) : base(StatusCode.AlreadyExists, message, innerException) { }
}

/// <summary>The request contained an invalid argument.</summary>
public sealed class InvalidArgumentException : SpiceDBException
{
    public InvalidArgumentException(string message) : base(StatusCode.InvalidArgument, message, null) { }
    public InvalidArgumentException(string message, Exception innerException) : base(StatusCode.InvalidArgument, message, innerException) { }
}

/// <summary>A precondition for the operation was not met.</summary>
public sealed class FailedPreconditionException : SpiceDBException
{
    public FailedPreconditionException(string message) : base(StatusCode.FailedPrecondition, message, null) { }
    public FailedPreconditionException(string message, Exception innerException) : base(StatusCode.FailedPrecondition, message, innerException) { }
}

/// <summary>The service is currently unavailable.</summary>
public sealed class UnavailableException : SpiceDBException
{
    public UnavailableException(string message) : base(StatusCode.Unavailable, message, null) { }
    public UnavailableException(string message, Exception innerException) : base(StatusCode.Unavailable, message, innerException) { }
}

/// <summary>The operation was cancelled.</summary>
public sealed class CancelledException : SpiceDBException
{
    public CancelledException(string message) : base(StatusCode.Cancelled, message, null) { }
    public CancelledException(string message, Exception innerException) : base(StatusCode.Cancelled, message, innerException) { }
}

/// <summary>The server's resources have been exhausted (e.g. rate limited).</summary>
public sealed class ResourceExhaustedException : SpiceDBException
{
    public ResourceExhaustedException(string message) : base(StatusCode.ResourceExhausted, message, null) { }
    public ResourceExhaustedException(string message, Exception innerException) : base(StatusCode.ResourceExhausted, message, innerException) { }
}

/// <summary>The operation exceeded its deadline.</summary>
public sealed class DeadlineExceededException : SpiceDBException
{
    public DeadlineExceededException(string message) : base(StatusCode.DeadlineExceeded, message, null) { }
    public DeadlineExceededException(string message, Exception innerException) : base(StatusCode.DeadlineExceeded, message, innerException) { }
}

/// <summary>The operation was aborted, typically due to a concurrency conflict.</summary>
public sealed class AbortedException : SpiceDBException
{
    public AbortedException(string message) : base(StatusCode.Aborted, message, null) { }
    public AbortedException(string message, Exception innerException) : base(StatusCode.Aborted, message, innerException) { }
}

/// <summary>
/// The request carried no usable credentials.
///
/// <para>In SpiceDB this is a wrong, expired, or rotated API token — the most common error a new
/// integration produces. It is distinct from <see cref="PermissionDeniedException"/>, which means
/// the caller was identified but is not allowed, and from a bare <see cref="SpiceDBException"/>,
/// which may be an internal server fault: refresh credentials on this one, page someone on that
/// one.</para>
/// </summary>
public sealed class UnauthenticatedException : SpiceDBException
{
    public UnauthenticatedException(string message) : base(StatusCode.Unauthenticated, message, null) { }
    public UnauthenticatedException(string message, Exception innerException) : base(StatusCode.Unauthenticated, message, innerException) { }
}

/// <summary>
/// A ZedToken names a revision that is no longer available.
///
/// <para>SpiceDB returns <c>OUT_OF_RANGE</c> when the revision a ZedToken refers to has expired or
/// been garbage-collected. Recovery is mechanical: discard the stale token and re-read at full
/// consistency.</para>
/// </summary>
public sealed class OutOfRangeException : SpiceDBException
{
    public OutOfRangeException(string message) : base(StatusCode.OutOfRange, message, null) { }
    public OutOfRangeException(string message, Exception innerException) : base(StatusCode.OutOfRange, message, innerException) { }
}

/// <summary>
/// Maps gRPC RpcException status codes to typed SpiceDB exceptions.
/// </summary>
internal static class ErrorMapper
{
    // ResourceExhausted is deliberately excluded. In SpiceDB it signals
    // either memory load-shed (retrying adds load to an already-overloaded
    // server) or a deterministic MaxDepthExceeded (retrying can never
    // succeed -- it just re-runs the most expensive class of check several
    // times before surfacing the same error). See DESIGN.md, "Automatic
    // retry is for idempotent operations only".
    private static readonly HashSet<StatusCode> TransientCodes =
    [
        StatusCode.Unavailable,
        StatusCode.Aborted,
    ];

    /// <summary>
    /// Converts a gRPC RpcException to a typed SpiceDB exception.
    ///
    /// <para>The originating exception is passed through as the inner exception, which is also
    /// where the returned exception's <see cref="SpiceDBException.Reason"/> and
    /// <see cref="SpiceDBException.ReasonMetadata"/> come from — SpiceDB's
    /// <c>google.rpc.ErrorInfo</c> detail. See root DESIGN.md, "RULE: Error mapping must not lose
    /// the server's detail".</para>
    /// </summary>
    public static SpiceDBException ToSpiceDBException(RpcException rpcException) => ToSpiceDBException(rpcException, null);

    /// <summary>
    /// As <see cref="ToSpiceDBException(RpcException)"/>, and when the transport failed
    /// locally (it attached a <see cref="Status.DebugException"/>, as for a refused
    /// connection or a failed DNS lookup) the message also names
    /// <paramref name="endpoint"/> and that root cause. Grpc.Net.Client otherwise reports
    /// both as "Error connecting to subchannel." <see cref="SpiceDBException.ServerMessage"/>
    /// keeps the status detail unchanged.
    /// </summary>
    public static SpiceDBException ToSpiceDBException(RpcException rpcException, string? endpoint)
    {
        ArgumentNullException.ThrowIfNull(rpcException);

        var message = rpcException.Status.Detail ?? rpcException.Message;
        if (rpcException.Status.DebugException is { } debug)
        {
            var root = debug;
            while (root.InnerException is { } inner) root = inner;
            var parts = new List<string>();
            if (!string.IsNullOrEmpty(endpoint)) parts.Add($"endpoint \"{endpoint}\"");
            if (!message.Contains(root.Message, StringComparison.Ordinal)) parts.Add($"{root.GetType().Name}: {root.Message}");
            if (parts.Count > 0) message = $"{message} ({string.Join("; ", parts)})";
        }
        SpiceDBException mapped = rpcException.StatusCode switch
        {
            StatusCode.PermissionDenied => new PermissionDeniedException(message, rpcException),
            StatusCode.NotFound => new NotFoundException(message, rpcException),
            StatusCode.AlreadyExists => new AlreadyExistsException(message, rpcException),
            StatusCode.InvalidArgument => new InvalidArgumentException(message, rpcException),
            StatusCode.FailedPrecondition => new FailedPreconditionException(message, rpcException),
            StatusCode.Unavailable => new UnavailableException(message, rpcException),
            StatusCode.Cancelled => new CancelledException(message, rpcException),
            StatusCode.ResourceExhausted => new ResourceExhaustedException(message, rpcException),
            StatusCode.DeadlineExceeded => new DeadlineExceededException(message, rpcException),
            StatusCode.Aborted => new AbortedException(message, rpcException),
            StatusCode.Unauthenticated => new UnauthenticatedException(message, rpcException),
            StatusCode.OutOfRange => new OutOfRangeException(message, rpcException),
            _ => new SpiceDBException(message, rpcException),
        };
        mapped.ServerMessage = rpcException.Status.Detail ?? "";
        return mapped;
    }

    /// <summary>
    /// Returns true if the exception is transient and worth retrying
    /// (UNAVAILABLE, ABORTED).
    /// </summary>
    public static bool IsTransient(Exception exception)
    {
        if (exception is RpcException rpc)
            return TransientCodes.Contains(rpc.StatusCode);

        return exception is UnavailableException
            or AbortedException;
    }
}
