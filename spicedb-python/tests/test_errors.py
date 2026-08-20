"""Unit tests for spicedb.errors — no SpiceDB instance needed."""

import grpc
from authzed.api.v1 import error_reason_pb2
from google.protobuf import any_pb2
from google.rpc import error_details_pb2, status_pb2

from spicedb.errors import (
    AlreadyExistsError,
    CancelledError,
    DeadlineExceededError,
    FailedPreconditionError,
    InvalidArgumentError,
    NotFoundError,
    OutOfRangeError,
    PermissionDeniedError,
    ResourceExhaustedError,
    SpiceDBError,
    UnauthenticatedError,
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
    assert issubclass(DeadlineExceededError, SpiceDBError)
    assert issubclass(ResourceExhaustedError, SpiceDBError)


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


def test_is_transient_false_for_spicedb_resource_exhausted_error():
    """RESOURCE_EXHAUSTED must NOT be retried (inverted from the original
    "is True" assertion -- see DESIGN.md, "Automatic retry is for
    idempotent operations only"). In SpiceDB it signals memory load-shed
    (retrying adds load to an already-overloaded server) or a deterministic
    MaxDepthExceeded (retrying can never succeed)."""
    assert is_transient(ResourceExhaustedError("quota")) is False


def test_is_transient_false_for_sync_resource_exhausted():
    assert is_transient(_SyncRpcError(grpc.StatusCode.RESOURCE_EXHAUSTED)) is False


def test_to_spicedb_error_maps_sync_error():
    err = to_spicedb_error(_SyncRpcError(grpc.StatusCode.UNAVAILABLE, "gone"))
    assert isinstance(err, UnavailableError)
    assert str(err) == "gone"


def test_to_spicedb_error_maps_deadline_exceeded():
    err = to_spicedb_error(_SyncRpcError(grpc.StatusCode.DEADLINE_EXCEEDED, "too slow"))
    assert isinstance(err, DeadlineExceededError)
    assert str(err) == "too slow"


def test_to_spicedb_error_maps_resource_exhausted():
    err = to_spicedb_error(_SyncRpcError(grpc.StatusCode.RESOURCE_EXHAUSTED, "quota"))
    assert isinstance(err, ResourceExhaustedError)
    assert str(err) == "quota"


def test_all_error_types_are_exported():
    import spicedb

    for name in (
        "FailedPreconditionError",
        "UnavailableError",
        "CancelledError",
        "DeadlineExceededError",
        "ResourceExhaustedError",
        "UnauthenticatedError",
        "OutOfRangeError",
    ):
        assert name in spicedb.__all__, f"{name} missing from spicedb.__all__"
        assert hasattr(spicedb, name)


def _status_with_error_info(
    code: int,
    message: str,
    reason: str,
    metadata: dict[str, str],
    domain: str = "authzed.com",
) -> status_pb2.Status:
    """Build a google.rpc.Status carrying an ErrorInfo detail, the shape
    SpiceDB uses to explain a failure."""
    detail = any_pb2.Any()
    detail.Pack(
        error_details_pb2.ErrorInfo(reason=reason, domain=domain, metadata=metadata)
    )
    return status_pb2.Status(code=code, message=message, details=[detail])


class _RichRpcError(grpc.RpcError):
    """Stands in for a real gRPC error carrying rich status details, which
    arrive in the `grpc-status-details-bin` trailer."""

    def __init__(self, code: grpc.StatusCode, details: str, status: status_pb2.Status | None):
        self._code = code
        self._details = details
        self._trailers = (
            (("grpc-status-details-bin", status.SerializeToString()),)
            if status is not None
            else ()
        )

    def code(self) -> grpc.StatusCode:
        return self._code

    def details(self) -> str:
        return self._details

    def trailing_metadata(self):
        return self._trailers


class TestErrorReasonIsReachable:
    """SpiceDB's structured explanation of a failure — the google.rpc.ErrorInfo
    detail on the status — must survive the mapping into a typed exception, so
    a caller can branch on the reason and read its metadata instead of
    string-matching a message. See root DESIGN.md, "Error mapping must not lose
    the server's detail"."""

    def test_reason_and_metadata_survive_to_spicedb_error(self):
        status = _status_with_error_info(
            8,  # RESOURCE_EXHAUSTED
            "max depth exceeded",
            "ERROR_REASON_MAXIMUM_DEPTH_EXCEEDED",
            {"maximum_depth_allowed": "50"},
        )
        err = to_spicedb_error(
            _RichRpcError(grpc.StatusCode.RESOURCE_EXHAUSTED, "max depth exceeded", status)
        )

        assert isinstance(err, ResourceExhaustedError)
        # The exposed reason is exactly the authzed.api.v1.ErrorReason enum
        # name, so a caller can compare against the generated enum without this
        # client carrying a hand-maintained copy of it.
        assert err.reason == "ERROR_REASON_MAXIMUM_DEPTH_EXCEEDED"
        assert err.reason == error_reason_pb2.ErrorReason.Name(
            error_reason_pb2.ERROR_REASON_MAXIMUM_DEPTH_EXCEEDED
        )
        assert err.reason_domain == "authzed.com"
        assert err.reason_metadata == {"maximum_depth_allowed": "50"}

    def test_precondition_metadata_names_the_failing_precondition(self):
        status = _status_with_error_info(
            9,  # FAILED_PRECONDITION
            "precondition failed",
            "ERROR_REASON_WRITE_OR_DELETE_PRECONDITION_FAILURE",
            {
                "precondition_resource_id": "firstdoc",
                "precondition_relation": "viewer",
            },
        )
        err = error_from_status_proto(status)

        assert isinstance(err, FailedPreconditionError)
        assert err.reason == "ERROR_REASON_WRITE_OR_DELETE_PRECONDITION_FAILURE"
        assert err.reason_metadata["precondition_resource_id"] == "firstdoc"
        assert err.reason_metadata["precondition_relation"] == "viewer"

    def test_unrecognized_reason_passes_through_without_raising(self):
        """A reason a newer server knows and this client does not is
        server-supplied: root DESIGN.md's "A conversion that cannot preserve
        meaning must fail" requires it to degrade safely, not to raise."""
        status = _status_with_error_info(
            3,  # INVALID_ARGUMENT
            "from the future",
            "ERROR_REASON_INVENTED_BY_A_NEWER_SERVER",
            {"k": "v"},
        )
        err = error_from_status_proto(status)

        assert isinstance(err, InvalidArgumentError)
        assert err.reason == "ERROR_REASON_INVENTED_BY_A_NEWER_SERVER"
        assert err.reason_metadata == {"k": "v"}

    def test_absent_error_info_leaves_reason_empty(self):
        err = to_spicedb_error(_RichRpcError(grpc.StatusCode.NOT_FOUND, "nope", None))
        assert isinstance(err, NotFoundError)
        assert err.reason == ""
        assert err.reason_domain == ""
        assert err.reason_metadata == {}

    def test_malformed_status_trailer_does_not_break_mapping(self):
        """A trailer this client cannot decode must not turn a typed error into
        a crash — the code mapping still has to happen."""

        class _Garbled(_RichRpcError):
            def __init__(self):
                super().__init__(grpc.StatusCode.NOT_FOUND, "nope", None)
                self._trailers = (("grpc-status-details-bin", b"\xff\xff\xff\xff"),)

        err = to_spicedb_error(_Garbled())
        assert isinstance(err, NotFoundError)
        assert err.reason == ""


class TestNewlyMappedCodes:
    def test_out_of_range_is_its_own_type(self):
        """OUT_OF_RANGE is SpiceDB's code for an expired or garbage-collected
        ZedToken. Recovery is mechanical — drop the token and re-read at full
        consistency — so it must be distinguishable by type."""
        err = to_spicedb_error(
            _SyncRpcError(grpc.StatusCode.OUT_OF_RANGE, "revision no longer available")
        )
        assert isinstance(err, OutOfRangeError)
        assert isinstance(err, SpiceDBError)
        assert not isinstance(err, InvalidArgumentError)
        assert not isinstance(err, FailedPreconditionError)

    def test_out_of_range_from_status_proto(self):
        err = error_from_status_proto(
            status_pb2.Status(code=11, message="revision no longer available")
        )
        assert isinstance(err, OutOfRangeError)

    def test_unauthenticated_is_not_a_generic_error(self):
        """A wrong, expired, or rotated token must be distinguishable from an
        internal server fault, so a caller can refresh credentials on one and
        page someone on the other."""
        err = to_spicedb_error(_SyncRpcError(grpc.StatusCode.UNAUTHENTICATED, "bad token"))
        assert isinstance(err, UnauthenticatedError)
        assert type(err) is not SpiceDBError
        assert not isinstance(err, PermissionDeniedError)

    def test_unauthenticated_from_status_proto(self):
        err = error_from_status_proto(status_pb2.Status(code=16, message="bad token"))
        assert isinstance(err, UnauthenticatedError)

    def test_new_types_are_in_the_hierarchy(self):
        assert issubclass(OutOfRangeError, SpiceDBError)
        assert issubclass(UnauthenticatedError, SpiceDBError)

    def test_neither_new_code_is_retried(self):
        assert is_transient(_SyncRpcError(grpc.StatusCode.OUT_OF_RANGE)) is False
        assert is_transient(_SyncRpcError(grpc.StatusCode.UNAUTHENTICATED)) is False
