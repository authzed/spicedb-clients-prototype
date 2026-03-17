use spicedb::error::{from_grpc_status, is_transient, SpiceDBError};

#[test]
fn test_permission_denied() {
    let err = from_grpc_status(7, "no access".into());
    assert!(matches!(err, SpiceDBError::PermissionDenied(_)));
    assert_eq!(err.to_string(), "permission denied: no access");
}

#[test]
fn test_not_found() {
    let err = from_grpc_status(5, "missing".into());
    assert!(matches!(err, SpiceDBError::NotFound(_)));
    assert_eq!(err.to_string(), "not found: missing");
}

#[test]
fn test_already_exists() {
    let err = from_grpc_status(6, "dup".into());
    assert!(matches!(err, SpiceDBError::AlreadyExists(_)));
    assert_eq!(err.to_string(), "already exists: dup");
}

#[test]
fn test_invalid_argument() {
    let err = from_grpc_status(3, "bad arg".into());
    assert!(matches!(err, SpiceDBError::InvalidArgument(_)));
    assert_eq!(err.to_string(), "invalid argument: bad arg");
}

#[test]
fn test_failed_precondition() {
    let err = from_grpc_status(9, "precond".into());
    assert!(matches!(err, SpiceDBError::FailedPrecondition(_)));
    assert_eq!(err.to_string(), "failed precondition: precond");
}

#[test]
fn test_unavailable() {
    let err = from_grpc_status(14, "down".into());
    assert!(matches!(err, SpiceDBError::Unavailable(_)));
    assert_eq!(err.to_string(), "unavailable: down");
}

#[test]
fn test_cancelled() {
    let err = from_grpc_status(1, "stopped".into());
    assert!(matches!(err, SpiceDBError::Cancelled(_)));
    assert_eq!(err.to_string(), "cancelled: stopped");
}

#[test]
fn test_unknown_status_code() {
    let err = from_grpc_status(99, "unknown".into());
    assert!(matches!(err, SpiceDBError::Status { code: 99, .. }));
    assert_eq!(err.to_string(), "grpc status 99: unknown");
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
fn test_is_transient_deadline_exceeded() {
    let err = SpiceDBError::Status {
        code: 4, // DEADLINE_EXCEEDED
        message: "timeout".into(),
    };
    assert!(is_transient(&err));
}

#[test]
fn test_is_transient_resource_exhausted() {
    let err = SpiceDBError::Status {
        code: 8, // RESOURCE_EXHAUSTED
        message: "quota".into(),
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
