"""Typed exception hierarchy for SpiceDB errors."""

from __future__ import annotations

import grpc


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


_CODE_TO_ERROR: dict[grpc.StatusCode, type[SpiceDBError]] = {
    grpc.StatusCode.PERMISSION_DENIED: PermissionDeniedError,
    grpc.StatusCode.NOT_FOUND: NotFoundError,
    grpc.StatusCode.ALREADY_EXISTS: AlreadyExistsError,
    grpc.StatusCode.INVALID_ARGUMENT: InvalidArgumentError,
    grpc.StatusCode.FAILED_PRECONDITION: FailedPreconditionError,
    grpc.StatusCode.UNAVAILABLE: UnavailableError,
    grpc.StatusCode.CANCELLED: CancelledError,
}

_TRANSIENT_CODES = frozenset({
    grpc.StatusCode.UNAVAILABLE,
    grpc.StatusCode.RESOURCE_EXHAUSTED,
    grpc.StatusCode.ABORTED,
})


def to_spicedb_error(err: grpc.aio.AioRpcError) -> SpiceDBError:
    """Convert a gRPC error to a typed SpiceDB exception."""
    code = err.code()
    cls = _CODE_TO_ERROR.get(code, SpiceDBError)
    return cls(err.details())


def is_transient(err: Exception) -> bool:
    """Return True if the error is transient and worth retrying."""
    if isinstance(err, grpc.aio.AioRpcError):
        return err.code() in _TRANSIENT_CODES
    return isinstance(err, UnavailableError)
