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

    /// The operation deadline was exceeded before it could complete.
    #[error("deadline exceeded: {0}")]
    DeadlineExceeded(String),

    /// A resource quota or limit was exhausted, such as a rate limit.
    #[error("resource exhausted: {0}")]
    ResourceExhausted(String),

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
    pub const DEADLINE_EXCEEDED: i32 = 4;
    pub const NOT_FOUND: i32 = 5;
    pub const ALREADY_EXISTS: i32 = 6;
    pub const PERMISSION_DENIED: i32 = 7;
    pub const RESOURCE_EXHAUSTED: i32 = 8;
    pub const FAILED_PRECONDITION: i32 = 9;
    pub const UNAVAILABLE: i32 = 14;
    pub const INVALID_ARGUMENT: i32 = 3;
    pub const ABORTED: i32 = 10;
    /// Not currently used by `from_grpc_status` (no server response maps to
    /// it today) — reused by [`internal`] below for locally-detected
    /// protocol invariant violations, so those errors share the same code
    /// space as a real server-side INTERNAL status.
    pub const INTERNAL: i32 = 13;
}

/// Convert a gRPC status code and message to a [`SpiceDBError`].
///
/// This is used internally by the client to map tonic::Status errors to
/// idiomatic error types. When the proto crate is available, this will
/// accept `tonic::Status` directly.
pub fn from_grpc_status(code: i32, message: String) -> SpiceDBError {
    // tonic's own per-call timeout enforcement -- driven by the
    // `grpc-timeout` header set via `tonic::Request::set_timeout`, and
    // enforced client-side by `tonic::transport::Channel`'s built-in
    // `GrpcTimeout` middleware racing a local `tokio::time::sleep` against
    // the call -- surfaces as `Status::cancelled("Timeout expired")`, NOT
    // `Status::deadline_exceeded` (tonic 0.12: `TimeoutExpired`'s `Display`
    // impl in src/status.rs, matched via `Status::from_error`'s downcast
    // handling). Left unmapped, that would make `DeadlineExceeded`
    // unreachable for exactly the case a deadline exists to guard against:
    // a server that never responds at all. "Timeout expired" is
    // `TimeoutExpired`'s exact, stable `Display` text.
    //
    // A structural alternative DOES exist upstream of this function:
    // `tonic::Status` implements `std::error::Error`, whose `source()` would
    // return the original `TimeoutExpired` (also publicly exported by
    // tonic), so a caller holding the `Status` itself could downcast rather
    // than string-match. What actually forecloses that here is this
    // function's own signature -- `from_grpc_status(code: i32, message:
    // String)` deliberately takes only the code and message (so this module
    // stays usable without a live `tonic::Status`, e.g. from tests), and
    // that reduction is what discards the original error's type before this
    // match ever runs. The string match below is the price of that
    // decoupling, not a tonic limitation -- and it fails loudly (a
    // `Cancelled`, not a silently-wrong `DeadlineExceeded`) if tonic ever
    // changes `TimeoutExpired`'s `Display` text.
    if code == codes::CANCELLED && message == "Timeout expired" {
        return SpiceDBError::DeadlineExceeded(message);
    }
    match code {
        codes::PERMISSION_DENIED => SpiceDBError::PermissionDenied(message),
        codes::NOT_FOUND => SpiceDBError::NotFound(message),
        codes::ALREADY_EXISTS => SpiceDBError::AlreadyExists(message),
        codes::INVALID_ARGUMENT => SpiceDBError::InvalidArgument(message),
        codes::FAILED_PRECONDITION => SpiceDBError::FailedPrecondition(message),
        codes::UNAVAILABLE => SpiceDBError::Unavailable(message),
        codes::CANCELLED => SpiceDBError::Cancelled(message),
        codes::DEADLINE_EXCEEDED => SpiceDBError::DeadlineExceeded(message),
        codes::RESOURCE_EXHAUSTED => SpiceDBError::ResourceExhausted(message),
        _ => SpiceDBError::Status { code, message },
    }
}

/// Constructs a [`SpiceDBError`] for a locally-detected protocol invariant
/// violation that has no backing gRPC status at all — e.g. a `oneof` field
/// the proto schema guarantees is always populated (such as
/// `CheckBulkPermissionsPair.response`) arriving unset. Uses gRPC code 13
/// (INTERNAL) so the error is classified consistently with a genuine
/// server-side internal error rather than any of the more specific variants.
pub fn internal(message: String) -> SpiceDBError {
    SpiceDBError::Status {
        code: codes::INTERNAL,
        message,
    }
}

/// Returns `true` if the error represents a transient gRPC failure that is
/// worth retrying with exponential backoff (UNAVAILABLE, ABORTED).
///
/// RESOURCE_EXHAUSTED is deliberately excluded. In SpiceDB it signals either
/// memory load-shed (retrying adds load to an already-overloaded server) or a
/// deterministic MaxDepthExceeded (retrying can never succeed -- it just
/// re-runs the most expensive class of check several times before surfacing
/// the same error). See DESIGN.md, "Automatic retry is for idempotent
/// operations only".
pub fn is_transient(err: &SpiceDBError) -> bool {
    match err {
        SpiceDBError::Unavailable(_) => true,
        SpiceDBError::Status { code, .. } => *code == codes::ABORTED,
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
    fn test_from_grpc_status_deadline_exceeded() {
        let err = from_grpc_status(codes::DEADLINE_EXCEEDED, "timeout".into());
        assert!(matches!(err, SpiceDBError::DeadlineExceeded(_)));
    }

    #[test]
    fn test_from_grpc_status_resource_exhausted() {
        let err = from_grpc_status(codes::RESOURCE_EXHAUSTED, "quota".into());
        assert!(matches!(err, SpiceDBError::ResourceExhausted(_)));
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
    fn test_deadline_exceeded_is_not_transient() {
        let err = SpiceDBError::Status {
            code: 4, // DEADLINE_EXCEEDED
            message: "timeout".into(),
        };
        assert!(!is_transient(&err));
    }

    #[test]
    fn test_is_transient_resource_exhausted() {
        // Inverted from "assert!(is_transient(...))" -- RESOURCE_EXHAUSTED
        // must NOT be retried. See DESIGN.md, "Automatic retry is for
        // idempotent operations only".
        let err = SpiceDBError::Status {
            code: codes::RESOURCE_EXHAUSTED,
            message: "quota".into(),
        };
        assert!(!is_transient(&err));
    }

    #[test]
    fn test_is_transient_resource_exhausted_via_from_grpc_status() {
        // Inverted from "assert!(is_transient(...))" -- see above.
        let err = from_grpc_status(codes::RESOURCE_EXHAUSTED, "quota".into());
        assert!(matches!(err, SpiceDBError::ResourceExhausted(_)));
        assert!(!is_transient(&err));
    }

    #[test]
    fn test_deadline_exceeded_via_from_grpc_status_is_not_transient() {
        let err = from_grpc_status(codes::DEADLINE_EXCEEDED, "timeout".into());
        assert!(matches!(err, SpiceDBError::DeadlineExceeded(_)));
        assert!(!is_transient(&err));
    }

    #[test]
    fn test_is_transient_aborted() {
        let err = SpiceDBError::Status {
            code: codes::ABORTED,
            message: "aborted".into(),
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
