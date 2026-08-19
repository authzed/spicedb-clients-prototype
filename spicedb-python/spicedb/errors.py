"""Typed exception hierarchy for SpiceDB errors."""

from __future__ import annotations

import grpc
from google.protobuf import message as _proto_message
from google.rpc import error_details_pb2, status_pb2

# Where a gRPC server puts the serialized `google.rpc.Status` that carries rich
# error details. This is the wire location fixed by the gRPC spec, not a
# behavior being re-implemented: the bytes found here are handed straight to
# `status_pb2.Status.FromString`, and each detail to protobuf's own
# `Any.Is`/`Any.Unpack`.
_STATUS_DETAILS_TRAILER = "grpc-status-details-bin"


class SpiceDBError(Exception):
    """Base exception for all SpiceDB errors.

    Beyond the message, a SpiceDB error carries the server's structured
    explanation of the failure when the server sent one -- the
    `google.rpc.ErrorInfo` detail attached to the status. See root DESIGN.md,
    "Error mapping must not lose the server's detail".

    Attributes:
        reason: The name of an `authzed.api.v1.ErrorReason` enum value, e.g.
            `"ERROR_REASON_MAXIMUM_DEPTH_EXCEEDED"`. Empty when the server
            attached no `ErrorInfo`. The value is surfaced exactly as the
            server sent it -- a reason a newer server knows and this client
            does not is passed through unchanged rather than coerced or
            rejected, because it is server-supplied and root DESIGN.md's
            "A conversion that cannot preserve meaning must fail" requires
            server-supplied unknowns to degrade safely rather than raise.
        reason_domain: Who produced the reason. SpiceDB uses `"authzed.com"`.
        reason_metadata: The specifics behind the reason, such as which
            precondition failed or what depth limit was hit. Empty dict when
            the server attached no `ErrorInfo`.
    """

    def __init__(
        self,
        message: str | None = None,
        *,
        reason: str = "",
        reason_domain: str = "",
        reason_metadata: dict[str, str] | None = None,
    ) -> None:
        super().__init__(message)
        self.reason = reason
        self.reason_domain = reason_domain
        self.reason_metadata: dict[str, str] = dict(reason_metadata or {})


class PermissionDeniedError(SpiceDBError):
    """The caller does not have permission to execute the operation."""


class NotFoundError(SpiceDBError):
    """The requested resource was not found."""


class AlreadyExistsError(SpiceDBError):
    """The resource already exists."""


class InvalidArgumentError(SpiceDBError):
    """The request contained an invalid argument."""


class FailedPreconditionError(SpiceDBError):
    """A precondition for the operation was not met."""


class UnavailableError(SpiceDBError):
    """The service is currently unavailable."""


class CancelledError(SpiceDBError):
    """The operation was cancelled."""


class DeadlineExceededError(SpiceDBError):
    """The operation deadline was exceeded before it could complete."""


class ResourceExhaustedError(SpiceDBError):
    """A resource quota or limit was exhausted, such as a rate limit."""


class UnauthenticatedError(SpiceDBError):
    """The request carried no usable credentials.

    In SpiceDB this is a wrong, expired, or rotated API token -- the most
    common error a new integration produces. It is distinct from
    `PermissionDeniedError`, which means the caller was identified but is not
    allowed, and from a generic `SpiceDBError`, which may be an internal server
    fault: refresh credentials on this one, page someone on that one.
    """


class OutOfRangeError(SpiceDBError):
    """A ZedToken names a revision that is no longer available.

    SpiceDB returns `OUT_OF_RANGE` when the revision a ZedToken refers to has
    expired or been garbage-collected. Recovery is mechanical: discard the
    stale token and re-read at full consistency.
    """


class EventLoopBindingError(SpiceDBError):
    """An async client was used from a different event loop than it bound to.

    A `grpc.aio` channel binds to the event loop running when it is first used.
    Calling into the same client from another loop -- most commonly by calling
    `asyncio.run()` once per request against a client built at startup -- cannot
    work. Use `spicedb.sync.SpiceDBClient` from synchronous code instead.
    """


_CODE_TO_ERROR: dict[grpc.StatusCode, type[SpiceDBError]] = {
    grpc.StatusCode.PERMISSION_DENIED: PermissionDeniedError,
    grpc.StatusCode.NOT_FOUND: NotFoundError,
    grpc.StatusCode.ALREADY_EXISTS: AlreadyExistsError,
    grpc.StatusCode.INVALID_ARGUMENT: InvalidArgumentError,
    grpc.StatusCode.FAILED_PRECONDITION: FailedPreconditionError,
    grpc.StatusCode.UNAVAILABLE: UnavailableError,
    grpc.StatusCode.CANCELLED: CancelledError,
    grpc.StatusCode.DEADLINE_EXCEEDED: DeadlineExceededError,
    grpc.StatusCode.RESOURCE_EXHAUSTED: ResourceExhaustedError,
    grpc.StatusCode.UNAUTHENTICATED: UnauthenticatedError,
    grpc.StatusCode.OUT_OF_RANGE: OutOfRangeError,
}

_TRANSIENT_CODES = frozenset(
    {
        grpc.StatusCode.UNAVAILABLE,
        grpc.StatusCode.ABORTED,
    }
)
"""RESOURCE_EXHAUSTED is deliberately excluded. In SpiceDB it means either
memory load-shed (retrying adds load to an already-overloaded server) or a
deterministic MaxDepthExceeded (retrying can never succeed -- it just re-runs
the most expensive class of check several times before surfacing the same
error). See DESIGN.md, "Automatic retry is for idempotent operations only"."""

# Reverse map: int gRPC code -> grpc.StatusCode, used to interpret the
# int `code` field of a google.rpc.Status (e.g. a per-item bulk-check error).
_INT_TO_STATUS_CODE: dict[int, grpc.StatusCode] = {
    sc.value[0]: sc for sc in grpc.StatusCode
}


def _error_info(status: status_pb2.Status | None) -> error_details_pb2.ErrorInfo | None:
    """Return the `google.rpc.ErrorInfo` detail on `status`, or None.

    Detail types this client does not know about are skipped rather than
    treated as failures, so an unfamiliar detail never hides the familiar one.
    """
    if status is None:
        return None
    for detail in status.details:
        if detail.Is(error_details_pb2.ErrorInfo.DESCRIPTOR):
            info = error_details_pb2.ErrorInfo()
            try:
                detail.Unpack(info)
            except _proto_message.DecodeError:
                return None
            return info
    return None


def _rich_status(err: grpc.RpcError) -> status_pb2.Status | None:
    """Return the `google.rpc.Status` a server attached to `err`, or None.

    A trailer that will not decode yields None rather than propagating: the
    code-to-type mapping is the load-bearing part of the conversion and must
    not be lost because an optional detail was malformed.
    """
    trailers = getattr(err, "trailing_metadata", None)
    if trailers is None:
        return None
    try:
        entries = trailers()
    except Exception:  # noqa: BLE001 -- an unusable trailer is not an error to report
        return None
    for key, value in entries or ():
        if key == _STATUS_DETAILS_TRAILER:
            try:
                return status_pb2.Status.FromString(value)
            except _proto_message.DecodeError:
                return None
    return None


def _reason_kwargs(status: status_pb2.Status | None) -> dict[str, object]:
    info = _error_info(status)
    if info is None:
        return {}
    return {
        "reason": info.reason,
        "reason_domain": info.domain,
        "reason_metadata": dict(info.metadata),
    }


def to_spicedb_error(err: grpc.RpcError) -> SpiceDBError:
    """Convert a gRPC error to a typed SpiceDB exception.

    Accepts both sync (`grpc.RpcError`) and async (`grpc.aio.AioRpcError`)
    errors; `AioRpcError` is a subclass of `grpc.RpcError`.

    The server's `ErrorInfo` detail, when present, is surfaced on the returned
    exception as `reason`/`reason_domain`/`reason_metadata`; the original error
    stays reachable as `__cause__` because every call site raises `from`.
    """
    code = err.code()
    cls = _CODE_TO_ERROR.get(code, SpiceDBError)
    return cls(err.details(), **_reason_kwargs(_rich_status(err)))


def error_from_status_proto(status: status_pb2.Status) -> SpiceDBError:
    """Convert a google.rpc.Status (e.g. a per-item bulk-check error) to a
    typed SpiceDBError, preserving the real code, message, and structured
    reason instead of fabricating a generic error."""
    code = _INT_TO_STATUS_CODE.get(status.code)
    cls = _CODE_TO_ERROR.get(code, SpiceDBError) if code is not None else SpiceDBError
    return cls(status.message, **_reason_kwargs(status))


def is_transient(err: Exception) -> bool:
    """Return True if the error is transient and worth retrying.

    Checks `grpc.RpcError` rather than `grpc.aio.AioRpcError` so that BOTH
    client flavors retry. The sync channel raises `grpc._channel._InactiveRpcError`,
    which is a `grpc.RpcError` but NOT an `AioRpcError` — narrowing this check
    to the aio type silently disables retries for the sync client.
    """
    if isinstance(err, grpc.RpcError):
        return err.code() in _TRANSIENT_CODES
    return isinstance(err, UnavailableError)
