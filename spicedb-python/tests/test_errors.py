"""Unit tests for spicedb.errors — no SpiceDB instance needed."""

import grpc
from google.rpc import status_pb2

from spicedb.errors import (
    AlreadyExistsError,
    CancelledError,
    FailedPreconditionError,
    InvalidArgumentError,
    NotFoundError,
    PermissionDeniedError,
    SpiceDBError,
    UnavailableError,
    error_from_status_proto,
    is_transient,
    to_spicedb_error,
)


def test_error_hierarchy():
    assert issubclass(PermissionDeniedError, SpiceDBError)
    assert issubclass(NotFoundError, SpiceDBError)
    assert issubclass(AlreadyExistsError, SpiceDBError)
    assert issubclass(InvalidArgumentError, SpiceDBError)
    assert issubclass(FailedPreconditionError, SpiceDBError)
    assert issubclass(UnavailableError, SpiceDBError)
    assert issubclass(CancelledError, SpiceDBError)


def test_is_transient_unavailable():
    assert is_transient(UnavailableError("down"))


def test_is_transient_other():
    assert not is_transient(NotFoundError("not found"))
    assert not is_transient(ValueError("nope"))


class TestErrorFromStatusProto:
    """error_from_status_proto must map a per-item google.rpc.Status (e.g.
    from a CheckBulkPermissions pair error) to the correct typed
    SpiceDBError, preserving the real message instead of fabricating a
    generic INTERNAL error (CI-2)."""

    def test_maps_invalid_argument(self):
        status = status_pb2.Status(code=3, message="bad item")  # INVALID_ARGUMENT
        err = error_from_status_proto(status)
        assert isinstance(err, InvalidArgumentError)
        assert not isinstance(err, NotFoundError)
        assert str(err) == "bad item"

    def test_maps_not_found(self):
        status = status_pb2.Status(code=5, message="resource missing")  # NOT_FOUND
        err = error_from_status_proto(status)
        assert isinstance(err, NotFoundError)
        assert str(err) == "resource missing"

    def test_unmapped_code_falls_back_to_base_error(self):
        # code=2 is UNKNOWN, which has no dedicated SpiceDBError subclass.
        status = status_pb2.Status(code=2, message="something odd")
        err = error_from_status_proto(status)
        assert type(err) is SpiceDBError
        assert str(err) == "something odd"


class _SyncRpcError(grpc.RpcError):
    """Stands in for grpc._channel._InactiveRpcError, which is what the sync
    channel actually raises. Only .code()/.details() are used by errors.py."""

    def __init__(self, code: grpc.StatusCode, details: str = "boom"):
        self._code = code
        self._details = details

    def code(self) -> grpc.StatusCode:
        return self._code

    def details(self) -> str:
        return self._details


def test_is_transient_true_for_sync_unavailable():
    assert is_transient(_SyncRpcError(grpc.StatusCode.UNAVAILABLE)) is True


def test_is_transient_false_for_sync_permission_denied():
    assert is_transient(_SyncRpcError(grpc.StatusCode.PERMISSION_DENIED)) is False


def test_is_transient_still_true_for_spicedb_unavailable_error():
    assert is_transient(UnavailableError("down")) is True


def test_to_spicedb_error_maps_sync_error():
    err = to_spicedb_error(_SyncRpcError(grpc.StatusCode.UNAVAILABLE, "gone"))
    assert isinstance(err, UnavailableError)
    assert str(err) == "gone"


def test_all_error_types_are_exported():
    import spicedb

    for name in (
        "FailedPreconditionError",
        "UnavailableError",
        "CancelledError",
    ):
        assert name in spicedb.__all__, f"{name} missing from spicedb.__all__"
        assert hasattr(spicedb, name)
