"""Unit tests for spicedb.errors — no SpiceDB instance needed."""

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
