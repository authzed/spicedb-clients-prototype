"""Unit tests for spicedb.errors — no SpiceDB instance needed."""

from spicedb.errors import (
    AlreadyExistsError,
    CancelledError,
    FailedPreconditionError,
    InvalidArgumentError,
    NotFoundError,
    PermissionDeniedError,
    SpiceDBError,
    UnavailableError,
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
