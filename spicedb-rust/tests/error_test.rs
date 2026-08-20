use spicedb::error::{from_grpc_status, is_transient, SpiceDBError};
use spicedb_proto::authzed::api::v1::ErrorReason;
use std::collections::HashMap;
use tonic::{Code, Status};
use tonic_types::{ErrorDetails, StatusExt};

/// `from_grpc_status` now takes the whole `tonic::Status` rather than a code
/// and a message, so nothing the server sent is discarded before mapping
/// runs. This helper keeps each test reading as "this code, this message".
fn status(code: Code, message: &str) -> Status {
    Status::new(code, message)
}

#[test]
fn test_permission_denied() {
    let err = from_grpc_status(status(Code::PermissionDenied, "no access"));
    assert!(matches!(err, SpiceDBError::PermissionDenied(_)));
    assert_eq!(err.to_string(), "permission denied: no access");
}

#[test]
fn test_not_found() {
    let err = from_grpc_status(status(Code::NotFound, "missing"));
    assert!(matches!(err, SpiceDBError::NotFound(_)));
    assert_eq!(err.to_string(), "not found: missing");
}

#[test]
fn test_already_exists() {
    let err = from_grpc_status(status(Code::AlreadyExists, "dup"));
    assert!(matches!(err, SpiceDBError::AlreadyExists(_)));
    assert_eq!(err.to_string(), "already exists: dup");
}

#[test]
fn test_invalid_argument() {
    let err = from_grpc_status(status(Code::InvalidArgument, "bad arg"));
    assert!(matches!(err, SpiceDBError::InvalidArgument(_)));
    assert_eq!(err.to_string(), "invalid argument: bad arg");
}

#[test]
fn test_failed_precondition() {
    let err = from_grpc_status(status(Code::FailedPrecondition, "precond"));
    assert!(matches!(err, SpiceDBError::FailedPrecondition(_)));
    assert_eq!(err.to_string(), "failed precondition: precond");
}

#[test]
fn test_unavailable() {
    let err = from_grpc_status(status(Code::Unavailable, "down"));
    assert!(matches!(err, SpiceDBError::Unavailable(_)));
    assert_eq!(err.to_string(), "unavailable: down");
}

#[test]
fn test_cancelled() {
    let err = from_grpc_status(status(Code::Cancelled, "stopped"));
    assert!(matches!(err, SpiceDBError::Cancelled(_)));
    assert_eq!(err.to_string(), "cancelled: stopped");
}

#[test]
fn test_deadline_exceeded() {
    let err = from_grpc_status(status(Code::DeadlineExceeded, "timeout"));
    assert!(matches!(err, SpiceDBError::DeadlineExceeded(_)));
    assert_eq!(err.to_string(), "deadline exceeded: timeout");
}

#[test]
fn test_resource_exhausted() {
    let err = from_grpc_status(status(Code::ResourceExhausted, "quota"));
    assert!(matches!(err, SpiceDBError::ResourceExhausted(_)));
    assert_eq!(err.to_string(), "resource exhausted: quota");
}

#[test]
fn test_unknown_status_code() {
    // A code with no dedicated variant falls through to `Status`. The code is
    // whatever tonic resolved it to -- an integer outside the standard gRPC set
    // arrives as `Code::Unknown` (2), because a `tonic::Status` cannot hold one.
    let err = from_grpc_status(Status::new(Code::from_i32(99), "unknown"));
    assert!(matches!(err, SpiceDBError::Status { code: 2, .. }));
    assert_eq!(err.to_string(), "grpc status 2: unknown");
}

#[test]
fn test_transport_error() {
    let err = SpiceDBError::Transport("connection refused".into());
    assert_eq!(err.to_string(), "transport error: connection refused");
}

#[test]
fn test_is_transient_unavailable() {
    let err = SpiceDBError::Unavailable("down".into());
    assert!(is_transient(&err));
}

#[test]
fn test_deadline_exceeded_is_not_transient() {
    let err = SpiceDBError::Status {
        code: 4, // DEADLINE_EXCEEDED
        message: "timeout".into(),
    };
    assert!(!is_transient(&err));
}

#[test]
fn test_is_transient_resource_exhausted() {
    // Inverted from "assert!(is_transient(...))" -- RESOURCE_EXHAUSTED must
    // NOT be retried. In SpiceDB it signals memory load-shed or a
    // deterministic MaxDepthExceeded, never a transient hiccup. See
    // DESIGN.md, "Automatic retry is for idempotent operations only".
    let err = SpiceDBError::Status {
        code: 8, // RESOURCE_EXHAUSTED
        message: "quota".into(),
    };
    assert!(!is_transient(&err));
}

#[test]
fn test_is_transient_resource_exhausted_via_from_grpc_status() {
    // Inverted from "assert!(is_transient(...))" -- see above.
    let err = from_grpc_status(status(Code::ResourceExhausted, "quota"));
    assert!(matches!(err, SpiceDBError::ResourceExhausted(_)));
    assert!(!is_transient(&err));
}

#[test]
fn test_deadline_exceeded_via_from_grpc_status_is_not_transient() {
    let err = from_grpc_status(status(Code::DeadlineExceeded, "timeout"));
    assert!(matches!(err, SpiceDBError::DeadlineExceeded(_)));
    assert!(!is_transient(&err));
}

#[test]
fn test_is_transient_aborted() {
    let err = SpiceDBError::Status {
        code: 10, // ABORTED
        message: "aborted".into(),
    };
    assert!(is_transient(&err));
}

#[test]
fn test_not_transient_not_found() {
    let err = SpiceDBError::NotFound("gone".into());
    assert!(!is_transient(&err));
}

#[test]
fn test_not_transient_permission_denied() {
    let err = SpiceDBError::PermissionDenied("no".into());
    assert!(!is_transient(&err));
}

#[test]
fn test_not_transient_invalid_argument() {
    let err = SpiceDBError::InvalidArgument("bad".into());
    assert!(!is_transient(&err));
}

#[test]
fn test_not_transient_already_exists() {
    let err = SpiceDBError::AlreadyExists("dup".into());
    assert!(!is_transient(&err));
}

#[test]
fn test_not_transient_failed_precondition() {
    let err = SpiceDBError::FailedPrecondition("precond".into());
    assert!(!is_transient(&err));
}

#[test]
fn test_error_is_send_sync() {
    fn assert_send_sync<T: Send + Sync>() {}
    assert_send_sync::<SpiceDBError>();
}

// OUT_OF_RANGE is SpiceDB's code for an expired or garbage-collected ZedToken.
// Recovery is mechanical -- drop the token, re-read at full consistency -- so it
// must be distinguishable by variant rather than by message. See root DESIGN.md,
// "RULE: Error mapping must not lose the server's detail".
#[test]
fn test_out_of_range_is_its_own_variant() {
    let err = from_grpc_status(status(Code::OutOfRange, "revision no longer available"));
    assert!(matches!(err, SpiceDBError::OutOfRange(_)));
    assert!(!matches!(err, SpiceDBError::InvalidArgument(_)));
    assert!(!matches!(err, SpiceDBError::Status { .. }));
    assert_eq!(
        err.to_string(),
        "out of range: revision no longer available"
    );
    assert!(!is_transient(&err));
}

// A wrong, expired, or rotated token must be distinguishable from an internal
// server fault, so a caller can refresh credentials on one and page someone on
// the other.
#[test]
fn test_unauthenticated_is_its_own_variant() {
    let err = from_grpc_status(status(Code::Unauthenticated, "bad token"));
    assert!(matches!(err, SpiceDBError::Unauthenticated(_)));
    assert!(!matches!(err, SpiceDBError::PermissionDenied(_)));
    assert!(!matches!(err, SpiceDBError::Status { .. }));
    assert_eq!(err.to_string(), "unauthenticated: bad token");
    assert!(!is_transient(&err));
}

/// SpiceDB's structured explanation of a failure -- the `google.rpc.ErrorInfo`
/// detail on the status -- must survive the mapping into a typed error.
fn status_with_error_info(
    code: Code,
    message: &str,
    reason: &str,
    metadata: &[(&str, &str)],
) -> Status {
    Status::with_error_details(
        code,
        message,
        ErrorDetails::with_error_info(
            reason,
            "authzed.com",
            metadata
                .iter()
                .map(|(k, v)| (k.to_string(), v.to_string()))
                .collect::<HashMap<_, _>>(),
        ),
    )
}

#[test]
fn test_error_reason_and_metadata_survive_mapping() {
    let err = from_grpc_status(status_with_error_info(
        Code::ResourceExhausted,
        "max depth exceeded",
        "ERROR_REASON_MAXIMUM_DEPTH_EXCEEDED",
        &[("maximum_depth_allowed", "50")],
    ));

    assert!(matches!(err, SpiceDBError::ResourceExhausted(_)));
    // The exposed reason is exactly the authzed.api.v1.ErrorReason enum name,
    // so a caller can compare against the generated enum without this client
    // carrying a hand-maintained copy of it.
    assert_eq!(err.reason(), Some("ERROR_REASON_MAXIMUM_DEPTH_EXCEEDED"));
    assert_eq!(
        err.reason(),
        Some(ErrorReason::MaximumDepthExceeded.as_str_name())
    );
    assert_eq!(err.reason_domain(), Some("authzed.com"));
    assert_eq!(
        err.reason_metadata()
            .get("maximum_depth_allowed")
            .map(String::as_str),
        Some("50")
    );
}

#[test]
fn test_reason_metadata_names_the_failing_precondition() {
    let err = from_grpc_status(status_with_error_info(
        Code::FailedPrecondition,
        "precondition failed",
        "ERROR_REASON_WRITE_OR_DELETE_PRECONDITION_FAILURE",
        &[
            ("precondition_resource_id", "firstdoc"),
            ("precondition_relation", "viewer"),
        ],
    ));

    assert!(matches!(err, SpiceDBError::FailedPrecondition(_)));
    assert_eq!(
        err.reason(),
        Some("ERROR_REASON_WRITE_OR_DELETE_PRECONDITION_FAILURE")
    );
    assert_eq!(
        err.reason_metadata()
            .get("precondition_resource_id")
            .map(String::as_str),
        Some("firstdoc")
    );
}

// A reason a newer server knows and this client does not is server-supplied:
// root DESIGN.md's "RULE: A conversion that cannot preserve meaning must fail"
// requires it to degrade safely, not to fail.
#[test]
fn test_unrecognized_reason_passes_through() {
    let err = from_grpc_status(status_with_error_info(
        Code::InvalidArgument,
        "from the future",
        "ERROR_REASON_INVENTED_BY_A_NEWER_SERVER",
        &[("k", "v")],
    ));

    assert!(matches!(err, SpiceDBError::InvalidArgument(_)));
    assert_eq!(
        err.reason(),
        Some("ERROR_REASON_INVENTED_BY_A_NEWER_SERVER")
    );
    assert_eq!(
        err.reason_metadata().get("k").map(String::as_str),
        Some("v")
    );
}

#[test]
fn test_absent_error_info_leaves_no_reason() {
    let err = from_grpc_status(status(Code::NotFound, "nope"));
    assert_eq!(err.reason(), None);
    assert_eq!(err.reason_domain(), None);
    assert!(err.reason_metadata().is_empty());
}

/// The originating status stays reachable both directly and through the
/// `std::error::Error` chain -- root DESIGN.md, "RULE: Error mapping must not
/// lose the server's detail", clause 2.
#[test]
fn test_source_chain_reaches_the_originating_status() {
    let err = from_grpc_status(status(Code::NotFound, "missing"));

    let status = err.status().expect("mapped errors keep their Status");
    assert_eq!(status.code(), Code::NotFound);

    let source = std::error::Error::source(&err).expect("source() should yield the Status");
    let downcast = source
        .downcast_ref::<Status>()
        .expect("source should be the originating tonic::Status");
    assert_eq!(downcast.message(), "missing");
}
