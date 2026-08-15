// Typed exception hierarchy for SpiceDB errors and gRPC error mapping.

using Grpc.Core;

namespace SpiceDB.Client;

/// <summary>
/// Base exception for all SpiceDB errors.
/// </summary>
public class SpiceDBException : Exception
{
    public SpiceDBException() { }
    public SpiceDBException(string message) : base(message) { }
    public SpiceDBException(string message, Exception innerException) : base(message, innerException) { }
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
/// Maps gRPC RpcException status codes to typed SpiceDB exceptions.
/// </summary>
public static class ErrorMapper
{
    private static readonly HashSet<StatusCode> TransientCodes =
    [
        StatusCode.Unavailable,
        StatusCode.ResourceExhausted,
        StatusCode.Aborted,
    ];

    /// <summary>
    /// Converts a gRPC RpcException to a typed SpiceDB exception.
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
            _ => new SpiceDBException(message, rpcException),
        };
    }

    /// <summary>
    /// Returns true if the exception is transient and worth retrying
    /// (UNAVAILABLE, RESOURCE_EXHAUSTED, ABORTED).
    /// </summary>
    public static bool IsTransient(Exception exception)
    {
        if (exception is RpcException rpc)
            return TransientCodes.Contains(rpc.StatusCode);

        return exception is UnavailableException
            or ResourceExhaustedException
            or AbortedException;
    }
}
