"""Typed exception hierarchy for SpiceDB errors."""

from __future__ import annotations

import grpc
from google.rpc import status_pb2


class SpiceDBError(Exception):
    """Base exception for all SpiceDB errors."""


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


def to_spicedb_error(err: grpc.RpcError) -> SpiceDBError:
    """Convert a gRPC error to a typed SpiceDB exception.

    Accepts both sync (`grpc.RpcError`) and async (`grpc.aio.AioRpcError`)
    errors; `AioRpcError` is a subclass of `grpc.RpcError`.
    """
    code = err.code()
    cls = _CODE_TO_ERROR.get(code, SpiceDBError)
    return cls(err.details())


def error_from_status_proto(status: status_pb2.Status) -> SpiceDBError:
    """Convert a google.rpc.Status (e.g. a per-item bulk-check error) to a
    typed SpiceDBError, preserving the real code and message instead of
    fabricating a generic error."""
    code = _INT_TO_STATUS_CODE.get(status.code)
    cls = _CODE_TO_ERROR.get(code, SpiceDBError) if code is not None else SpiceDBError
    return cls(status.message)


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
