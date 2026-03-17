//! Typed error hierarchy for SpiceDB operations.
//!
//! All errors from SpiceDB operations are represented as [`SpiceDBError`] variants.
//! Raw gRPC status codes are mapped to descriptive enum variants so callers can
//! match on specific failure modes without importing tonic or gRPC types.

use thiserror::Error;

/// The error type returned by all SpiceDB client operations.
#[derive(Debug, Error)]
pub enum SpiceDBError {
    /// The caller does not have permission to execute the operation.
    #[error("permission denied: {0}")]
    PermissionDenied(String),

    /// The requested resource was not found.
    #[error("not found: {0}")]
    NotFound(String),

    /// The resource already exists (e.g., duplicate relationship create).
    #[error("already exists: {0}")]
    AlreadyExists(String),

    /// The request contained an invalid argument.
    #[error("invalid argument: {0}")]
    InvalidArgument(String),

    /// A precondition for the operation was not met.
    #[error("failed precondition: {0}")]
    FailedPrecondition(String),

    /// The service is currently unavailable.
    #[error("unavailable: {0}")]
    Unavailable(String),

    /// The operation was cancelled.
    #[error("cancelled: {0}")]
    Cancelled(String),

    /// A transport-level error occurred (connection refused, TLS failure, etc.).
    #[error("transport error: {0}")]
    Transport(String),

    /// A gRPC status error that does not map to a specific variant above.
    #[error("grpc status {code}: {message}")]
    Status {
        /// The gRPC status code as an integer.
        code: i32,
        /// The human-readable error message.
        message: String,
    },
}

/// gRPC status codes used for error mapping and retry logic.
/// These mirror tonic::Code values.
mod codes {
    pub const CANCELLED: i32 = 1;
    pub const NOT_FOUND: i32 = 5;
    pub const ALREADY_EXISTS: i32 = 6;
    pub const PERMISSION_DENIED: i32 = 7;
    pub const RESOURCE_EXHAUSTED: i32 = 8;
    pub const FAILED_PRECONDITION: i32 = 9;
    pub const UNAVAILABLE: i32 = 14;
    pub const INVALID_ARGUMENT: i32 = 3;
    pub const DEADLINE_EXCEEDED: i32 = 4;
}

/// Convert a gRPC status code and message to a [`SpiceDBError`].
///
/// This is used internally by the client to map tonic::Status errors to
/// idiomatic error types. When the proto crate is available, this will
/// accept `tonic::Status` directly.
pub fn from_grpc_status(code: i32, message: String) -> SpiceDBError {
    match code {
        codes::PERMISSION_DENIED => SpiceDBError::PermissionDenied(message),
        codes::NOT_FOUND => SpiceDBError::NotFound(message),
        codes::ALREADY_EXISTS => SpiceDBError::AlreadyExists(message),
        codes::INVALID_ARGUMENT => SpiceDBError::InvalidArgument(message),
        codes::FAILED_PRECONDITION => SpiceDBError::FailedPrecondition(message),
        codes::UNAVAILABLE => SpiceDBError::Unavailable(message),
        codes::CANCELLED => SpiceDBError::Cancelled(message),
        _ => SpiceDBError::Status { code, message },
    }
}

/// Returns `true` if the error represents a transient gRPC failure that is
/// worth retrying with exponential backoff (UNAVAILABLE, DEADLINE_EXCEEDED,
/// RESOURCE_EXHAUSTED).
pub fn is_transient(err: &SpiceDBError) -> bool {
    match err {
        SpiceDBError::Unavailable(_) => true,
        SpiceDBError::Status { code, .. } => {
            *code == codes::DEADLINE_EXCEEDED || *code == codes::RESOURCE_EXHAUSTED
        }
        _ => false,
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_from_grpc_status_permission_denied() {
        let err = from_grpc_status(codes::PERMISSION_DENIED, "no access".into());
        assert!(matches!(err, SpiceDBError::PermissionDenied(_)));
        assert_eq!(err.to_string(), "permission denied: no access");
    }

    #[test]
    fn test_from_grpc_status_not_found() {
        let err = from_grpc_status(codes::NOT_FOUND, "missing".into());
        assert!(matches!(err, SpiceDBError::NotFound(_)));
    }

    #[test]
    fn test_from_grpc_status_already_exists() {
        let err = from_grpc_status(codes::ALREADY_EXISTS, "dup".into());
        assert!(matches!(err, SpiceDBError::AlreadyExists(_)));
    }

    #[test]
    fn test_from_grpc_status_invalid_argument() {
        let err = from_grpc_status(codes::INVALID_ARGUMENT, "bad arg".into());
        assert!(matches!(err, SpiceDBError::InvalidArgument(_)));
    }

    #[test]
    fn test_from_grpc_status_failed_precondition() {
        let err = from_grpc_status(codes::FAILED_PRECONDITION, "precond".into());
        assert!(matches!(err, SpiceDBError::FailedPrecondition(_)));
    }

    #[test]
    fn test_from_grpc_status_unavailable() {
        let err = from_grpc_status(codes::UNAVAILABLE, "down".into());
        assert!(matches!(err, SpiceDBError::Unavailable(_)));
    }

    #[test]
    fn test_from_grpc_status_cancelled() {
        let err = from_grpc_status(codes::CANCELLED, "stopped".into());
        assert!(matches!(err, SpiceDBError::Cancelled(_)));
    }

    #[test]
    fn test_from_grpc_status_unknown_code() {
        let err = from_grpc_status(99, "unknown".into());
        assert!(matches!(err, SpiceDBError::Status { code: 99, .. }));
        assert_eq!(err.to_string(), "grpc status 99: unknown");
    }

    #[test]
    fn test_is_transient_unavailable() {
        let err = SpiceDBError::Unavailable("down".into());
        assert!(is_transient(&err));
    }

    #[test]
    fn test_is_transient_deadline_exceeded() {
        let err = SpiceDBError::Status {
            code: codes::DEADLINE_EXCEEDED,
            message: "timeout".into(),
        };
        assert!(is_transient(&err));
    }

    #[test]
    fn test_is_transient_resource_exhausted() {
        let err = SpiceDBError::Status {
            code: codes::RESOURCE_EXHAUSTED,
            message: "quota".into(),
        };
        assert!(is_transient(&err));
    }

    #[test]
    fn test_is_not_transient() {
        let err = SpiceDBError::NotFound("gone".into());
        assert!(!is_transient(&err));

        let err = SpiceDBError::PermissionDenied("no".into());
        assert!(!is_transient(&err));

        let err = SpiceDBError::InvalidArgument("bad".into());
        assert!(!is_transient(&err));
    }
}
