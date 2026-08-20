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
    public string Reason { get; }

    /// <summary>Who produced the reason. SpiceDB uses <c>"authzed.com"</c>.</summary>
    public string ReasonDomain { get; }

    /// <summary>
    /// The specifics behind the reason — which precondition failed, what depth limit was hit.
    /// Empty when the server attached no <c>ErrorInfo</c>.
    /// </summary>
    public IReadOnlyDictionary<string, string> ReasonMetadata { get; }

    public SpiceDBException() : this(null, null) { }
    public SpiceDBException(string message) : this(message, null) { }

    public SpiceDBException(string? message, Exception? innerException) : base(message, innerException)
    {
        var info = ErrorInfoOf(innerException);
        Reason = info?.Reason ?? string.Empty;
        ReasonDomain = info?.Domain ?? string.Empty;
        ReasonMetadata = info is null
            ? new Dictionary<string, string>()
            : new Dictionary<string, string>(info.Metadata);
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
    public PermissionDeniedException(string message) : base(message) { }
    public PermissionDeniedException(string message, Exception innerException) : base(message, innerException) { }
}

/// <summary>The requested resource was not found.</summary>
public sealed class NotFoundException : SpiceDBException
{
    public NotFoundException(string message) : base(message) { }
    public NotFoundException(string message, Exception innerException) : base(message, innerException) { }
}

/// <summary>The resource already exists.</summary>
public sealed class AlreadyExistsException : SpiceDBException
{
    public AlreadyExistsException(string message) : base(message) { }
    public AlreadyExistsException(string message, Exception innerException) : base(message, innerException) { }
}

/// <summary>The request contained an invalid argument.</summary>
public sealed class InvalidArgumentException : SpiceDBException
{
    public InvalidArgumentException(string message) : base(message) { }
    public InvalidArgumentException(string message, Exception innerException) : base(message, innerException) { }
}

/// <summary>A precondition for the operation was not met.</summary>
public sealed class FailedPreconditionException : SpiceDBException
{
    public FailedPreconditionException(string message) : base(message) { }
    public FailedPreconditionException(string message, Exception innerException) : base(message, innerException) { }
}

/// <summary>The service is currently unavailable.</summary>
public sealed class UnavailableException : SpiceDBException
{
    public UnavailableException(string message) : base(message) { }
    public UnavailableException(string message, Exception innerException) : base(message, innerException) { }
}

/// <summary>The operation was cancelled.</summary>
public sealed class CancelledException : SpiceDBException
{
    public CancelledException(string message) : base(message) { }
    public CancelledException(string message, Exception innerException) : base(message, innerException) { }
}

/// <summary>The server's resources have been exhausted (e.g. rate limited).</summary>
public sealed class ResourceExhaustedException : SpiceDBException
{
    public ResourceExhaustedException(string message) : base(message) { }
    public ResourceExhaustedException(string message, Exception innerException) : base(message, innerException) { }
}

/// <summary>The operation exceeded its deadline.</summary>
public sealed class DeadlineExceededException : SpiceDBException
{
    public DeadlineExceededException(string message) : base(message) { }
    public DeadlineExceededException(string message, Exception innerException) : base(message, innerException) { }
}

/// <summary>The operation was aborted, typically due to a concurrency conflict.</summary>
public sealed class AbortedException : SpiceDBException
{
    public AbortedException(string message) : base(message) { }
    public AbortedException(string message, Exception innerException) : base(message, innerException) { }
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
    public UnauthenticatedException(string message) : base(message) { }
    public UnauthenticatedException(string message, Exception innerException) : base(message, innerException) { }
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
    public OutOfRangeException(string message) : base(message) { }
    public OutOfRangeException(string message, Exception innerException) : base(message, innerException) { }
}

/// <summary>
/// Maps gRPC RpcException status codes to typed SpiceDB exceptions.
/// </summary>
public static class ErrorMapper
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
    public static SpiceDBException ToSpiceDBException(RpcException rpcException)
    {
        ArgumentNullException.ThrowIfNull(rpcException);

        var message = rpcException.Status.Detail ?? rpcException.Message;
        return rpcException.StatusCode switch
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
