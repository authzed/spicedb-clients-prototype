//! Typed error hierarchy for SpiceDB operations.
//!
//! All errors from SpiceDB operations are represented as [`SpiceDBError`] variants.
//! Raw gRPC status codes are mapped to descriptive enum variants so callers can
//! match on specific failure modes without importing tonic or gRPC types.
//!
//! Mapping never discards what the server said. Every error built from a
//! response keeps the originating [`tonic::Status`], reachable both through
//! [`SpiceDBError::status`] and as the [`std::error::Error::source`] of the
//! error, and SpiceDB's structured `ErrorReason` — the `google.rpc.ErrorInfo`
//! detail on that status — is surfaced through [`SpiceDBError::reason`] and
//! [`SpiceDBError::reason_metadata`]. Errors that never had a status, such as a
//! transport failure, keep the underlying error as their `source()` instead.
//! See root DESIGN.md, "RULE: Error mapping must not lose the server's detail".

use std::collections::HashMap;
use std::error::Error;
use std::fmt;
use std::ops::Deref;
use std::sync::Arc;

use tonic::{Code, Status};
use tonic_types::StatusExt;

/// What the server said alongside the message: the status it sent and the
/// `google.rpc.ErrorInfo` detail read off it.
///
/// Held behind a `Box` so that [`ErrorPayload`] -- and therefore every
/// `Result<_, SpiceDBError>` in this crate -- stays small.
#[derive(Debug, Clone)]
struct ServerDetail {
    reason: String,
    reason_domain: String,
    reason_metadata: HashMap<String, String>,
    status: Status,
}

/// The message carried by a [`SpiceDBError`], together with everything the
/// server said alongside it.
///
/// This is the payload of every [`SpiceDBError`] variant. It exists so that a
/// variant can carry the originating [`tonic::Status`] and SpiceDB's structured
/// reason without every variant growing extra fields — `matches!(err,
/// SpiceDBError::NotFound(_))` and `SpiceDBError::NotFound("gone".into())` both
/// still work. It `Display`s and derefs as the bare message, so formatting and
/// string inspection are unchanged.
#[derive(Debug, Clone, Default)]
pub struct ErrorPayload {
    message: String,
    detail: Option<Box<ServerDetail>>,
    /// A non-gRPC cause, for errors that never had a status at all -- today,
    /// transport failures. Kept as an `Arc` rather than a `Box` so the payload
    /// stays `Clone`, which is how `tonic::Status` holds its own source.
    cause: Option<Arc<dyn Error + Send + Sync + 'static>>,
}

impl ErrorPayload {
    /// Builds a payload from a server status, reading SpiceDB's `ErrorInfo`
    /// detail off it.
    ///
    /// `get_details_error_info` (tonic-types) does the decoding, so this reads
    /// the structured detail out of `grpc-status-details-bin` rather than
    /// anything reconstructed from the status message. `message` is passed
    /// separately because a per-item bulk failure prefixes it with the item
    /// index.
    fn from_status(message: String, status: Status) -> Self {
        let info = status.get_details_error_info();
        Self {
            message,
            detail: Some(Box::new(ServerDetail {
                reason: info.as_ref().map(|i| i.reason.clone()).unwrap_or_default(),
                reason_domain: info.as_ref().map(|i| i.domain.clone()).unwrap_or_default(),
                reason_metadata: info.map(|i| i.metadata).unwrap_or_default(),
                status,
            })),
            cause: None,
        }
    }

    /// Builds a payload for a failure that never produced a gRPC status --
    /// today, a transport error -- keeping `cause` as the error's source.
    ///
    /// Without this, connection and TLS failures would be the only errors in
    /// the hierarchy with no `source()` chain at all, which is backwards: they
    /// are the class where the underlying cause is most diagnostic.
    pub(crate) fn with_cause(message: String, cause: impl Error + Send + Sync + 'static) -> Self {
        Self {
            message,
            detail: None,
            cause: Some(Arc::new(cause)),
        }
    }

    /// The human-readable message, without the prefix [`SpiceDBError`]'s
    /// `Display` adds.
    pub fn message(&self) -> &str {
        &self.message
    }
}

impl fmt::Display for ErrorPayload {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str(&self.message)
    }
}

impl Deref for ErrorPayload {
    type Target = str;

    fn deref(&self) -> &str {
        &self.message
    }
}

impl AsRef<str> for ErrorPayload {
    fn as_ref(&self) -> &str {
        &self.message
    }
}

impl From<String> for ErrorPayload {
    fn from(message: String) -> Self {
        Self {
            message,
            ..Self::default()
        }
    }
}

impl From<&str> for ErrorPayload {
    fn from(message: &str) -> Self {
        Self::from(message.to_string())
    }
}

/// The error type returned by all SpiceDB client operations.
#[derive(Debug, Clone)]
pub enum SpiceDBError {
    /// The caller does not have permission to execute the operation.
    PermissionDenied(ErrorPayload),

    /// The request carried no usable credentials.
    ///
    /// In SpiceDB this is a wrong, expired, or rotated API token -- the most
    /// common error a new integration produces. It is distinct from
    /// [`SpiceDBError::PermissionDenied`], which means the caller was
    /// identified but is not allowed: refresh credentials on this one, page
    /// someone on an internal fault.
    Unauthenticated(ErrorPayload),

    /// The requested resource was not found.
    NotFound(ErrorPayload),

    /// The resource already exists (e.g., duplicate relationship create).
    AlreadyExists(ErrorPayload),

    /// The request contained an invalid argument.
    InvalidArgument(ErrorPayload),

    /// A precondition for the operation was not met.
    FailedPrecondition(ErrorPayload),

    /// A ZedToken names a revision that is no longer available.
    ///
    /// SpiceDB returns `OUT_OF_RANGE` when the revision a ZedToken refers to
    /// has expired or been garbage-collected. Recovery is mechanical: discard
    /// the stale token and re-read at full consistency.
    OutOfRange(ErrorPayload),

    /// The service is currently unavailable.
    Unavailable(ErrorPayload),

    /// The operation was cancelled.
    Cancelled(ErrorPayload),

    /// The operation deadline was exceeded before it could complete.
    DeadlineExceeded(ErrorPayload),

    /// A resource quota or limit was exhausted, such as a rate limit.
    ResourceExhausted(ErrorPayload),

    /// A transport-level error occurred (connection refused, TLS failure, etc.).
    Transport(ErrorPayload),

    /// A gRPC status error that does not map to a specific variant above.
    Status {
        /// The gRPC status code as an integer.
        code: i32,
        /// The human-readable error message.
        message: ErrorPayload,
    },
}

impl SpiceDBError {
    /// The payload behind whichever variant this is.
    fn payload(&self) -> &ErrorPayload {
        match self {
            SpiceDBError::PermissionDenied(p)
            | SpiceDBError::Unauthenticated(p)
            | SpiceDBError::NotFound(p)
            | SpiceDBError::AlreadyExists(p)
            | SpiceDBError::InvalidArgument(p)
            | SpiceDBError::FailedPrecondition(p)
            | SpiceDBError::OutOfRange(p)
            | SpiceDBError::Unavailable(p)
            | SpiceDBError::Cancelled(p)
            | SpiceDBError::DeadlineExceeded(p)
            | SpiceDBError::ResourceExhausted(p)
            | SpiceDBError::Transport(p) => p,
            SpiceDBError::Status { message, .. } => message,
        }
    }

    /// The human-readable message, without the variant prefix `Display` adds.
    pub fn message(&self) -> &str {
        self.payload().message()
    }

    /// SpiceDB's structured explanation for this failure: the name of an
    /// `authzed.api.v1.ErrorReason` enum value, e.g.
    /// `"ERROR_REASON_MAXIMUM_DEPTH_EXCEEDED"`. `None` when the server attached
    /// no `ErrorInfo`.
    ///
    /// The value is surfaced exactly as the server sent it. A reason a newer
    /// server knows and this client does not is passed through unchanged rather
    /// than coerced or rejected: it is server-supplied, and root DESIGN.md's
    /// "RULE: A conversion that cannot preserve meaning must fail" requires
    /// server-supplied unknowns to degrade safely rather than fail.
    pub fn reason(&self) -> Option<&str> {
        self.detail()
            .map(|d| d.reason.as_str())
            .filter(|r| !r.is_empty())
    }

    /// Who produced the reason. SpiceDB uses `"authzed.com"`.
    pub fn reason_domain(&self) -> Option<&str> {
        self.detail()
            .map(|d| d.reason_domain.as_str())
            .filter(|d| !d.is_empty())
    }

    /// The specifics behind the reason -- which precondition failed, what depth
    /// limit was hit. Empty when the server attached no `ErrorInfo`.
    pub fn reason_metadata(&self) -> &HashMap<String, String> {
        static EMPTY: std::sync::OnceLock<HashMap<String, String>> = std::sync::OnceLock::new();
        self.detail()
            .map(|d| &d.reason_metadata)
            .unwrap_or_else(|| EMPTY.get_or_init(HashMap::new))
    }

    /// The originating [`tonic::Status`], for callers who want the whole thing:
    /// its trailers, its other `google.rpc` details, or its own error source.
    /// `None` for errors this client detected locally.
    pub fn status(&self) -> Option<&Status> {
        self.detail().map(|d| &d.status)
    }

    fn detail(&self) -> Option<&ServerDetail> {
        self.payload().detail.as_deref()
    }

    /// The prefix `Display` puts in front of the message.
    fn prefix(&self) -> &'static str {
        match self {
            SpiceDBError::PermissionDenied(_) => "permission denied",
            SpiceDBError::Unauthenticated(_) => "unauthenticated",
            SpiceDBError::NotFound(_) => "not found",
            SpiceDBError::AlreadyExists(_) => "already exists",
            SpiceDBError::InvalidArgument(_) => "invalid argument",
            SpiceDBError::FailedPrecondition(_) => "failed precondition",
            SpiceDBError::OutOfRange(_) => "out of range",
            SpiceDBError::Unavailable(_) => "unavailable",
            SpiceDBError::Cancelled(_) => "cancelled",
            SpiceDBError::DeadlineExceeded(_) => "deadline exceeded",
            SpiceDBError::ResourceExhausted(_) => "resource exhausted",
            SpiceDBError::Transport(_) => "transport error",
            SpiceDBError::Status { .. } => "grpc status",
        }
    }
}

impl fmt::Display for SpiceDBError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            SpiceDBError::Status { code, message } => {
                write!(f, "grpc status {code}: {message}")
            }
            other => write!(f, "{}: {}", other.prefix(), other.payload()),
        }
    }
}

impl Error for SpiceDBError {
    /// The originating [`tonic::Status`], so `source()` walks from a
    /// `SpiceDBError` down through tonic's own error chain -- e.g. to the
    /// `TimeoutExpired` behind a client-side deadline.
    ///
    /// `Display` and `Error` are written out here rather than derived, because
    /// `thiserror`'s derive would make the payload the source and add a hop
    /// that renders as the same message.
    fn source(&self) -> Option<&(dyn Error + 'static)> {
        self.status()
            .map(|s| s as &(dyn Error + 'static))
            .or_else(|| {
                self.payload()
                    .cause
                    .as_deref()
                    .map(|c| c as &(dyn Error + 'static))
            })
    }
}

/// gRPC status codes used for error mapping and retry logic.
/// These mirror tonic::Code values.
mod codes {
    /// Not currently produced by `from_grpc_status` (no server response maps to
    /// it today) — used by [`internal`] below for locally-detected protocol
    /// invariant violations, so those errors share the same code space as a
    /// real server-side INTERNAL status.
    pub const INTERNAL: i32 = 13;
    pub const ABORTED: i32 = 10;
}

/// Returns `true` if tonic's [`tonic::TimeoutExpired`] appears anywhere in
/// `status`'s error source chain.
///
/// The whole chain is walked rather than just the immediate source, because how
/// deeply the timeout is nested depends on who produced it. A `Status` built
/// straight from the timeout (`Status::from_error(TimeoutExpired)`) carries it
/// as its direct source; the one a real `Channel` produces carries a
/// `tonic::transport::Error` whose own source is the `TimeoutExpired`. Walking
/// is also what tonic itself does in `find_status_in_source_chain`, so this
/// follows the library rather than guessing at one fixed depth.
fn has_timeout_expired(status: &Status) -> bool {
    let mut current: Option<&(dyn Error + 'static)> = status.source();
    while let Some(err) = current {
        if err.downcast_ref::<tonic::TimeoutExpired>().is_some() {
            return true;
        }
        current = err.source();
    }
    false
}

/// Convert a [`tonic::Status`] to a [`SpiceDBError`].
///
/// This is used internally by the client to map tonic errors to idiomatic error
/// types. It takes the whole `Status` rather than a code and a message so that
/// nothing the server sent is thrown away before mapping runs: the status's
/// `ErrorInfo` detail becomes the error's reason, and the status itself stays
/// reachable via [`SpiceDBError::status`] and `source()`.
pub fn from_grpc_status(status: Status) -> SpiceDBError {
    let message = status.message().to_string();
    from_grpc_status_with_message(status, message)
}

/// Like [`from_grpc_status`], but with a caller-supplied message — used by the
/// bulk paths, which prefix the server's message with the failing item's index.
pub(crate) fn from_grpc_status_with_message(status: Status, message: String) -> SpiceDBError {
    // tonic's own per-call timeout enforcement -- driven by the `grpc-timeout`
    // header set via `tonic::Request::set_timeout`, and enforced client-side by
    // `tonic::transport::Channel`'s built-in `GrpcTimeout` middleware racing a
    // local `tokio::time::sleep` against the call -- surfaces as
    // `Status::cancelled("Timeout expired")`, NOT `Status::deadline_exceeded`.
    // Left unmapped, that would make `DeadlineExceeded` unreachable for exactly
    // the case a deadline exists to guard against: a server that never responds
    // at all.
    //
    // The check is structural, not textual. tonic's `try_from_error` keeps the
    // original error as the new `Status`'s source (`status.rs`: `status.source =
    // Some(err.into())` right after `find_status_in_source_chain` matched), and
    // `TimeoutExpired` is publicly exported by tonic, so the timeout is
    // identified by downcasting that source rather than by comparing against
    // `TimeoutExpired`'s rendered `Display` text. Taking the whole `Status` here
    // is what makes that possible -- the previous signature reduced it to a code
    // and a string before this function ran, leaving a message comparison as the
    // only option.
    let timed_out = status.code() == Code::Cancelled && has_timeout_expired(&status);

    let code = status.code();
    let numeric_code = code as i32;
    let payload = ErrorPayload::from_status(message, status);

    if timed_out {
        return SpiceDBError::DeadlineExceeded(payload);
    }

    match code {
        Code::PermissionDenied => SpiceDBError::PermissionDenied(payload),
        Code::Unauthenticated => SpiceDBError::Unauthenticated(payload),
        Code::NotFound => SpiceDBError::NotFound(payload),
        Code::AlreadyExists => SpiceDBError::AlreadyExists(payload),
        Code::InvalidArgument => SpiceDBError::InvalidArgument(payload),
        Code::FailedPrecondition => SpiceDBError::FailedPrecondition(payload),
        Code::OutOfRange => SpiceDBError::OutOfRange(payload),
        Code::Unavailable => SpiceDBError::Unavailable(payload),
        Code::Cancelled => SpiceDBError::Cancelled(payload),
        Code::DeadlineExceeded => SpiceDBError::DeadlineExceeded(payload),
        Code::ResourceExhausted => SpiceDBError::ResourceExhausted(payload),
        _ => SpiceDBError::Status {
            code: numeric_code,
            message: payload,
        },
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
        message: message.into(),
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

    fn status(code: Code, message: &str) -> Status {
        Status::new(code, message)
    }

    #[test]
    fn test_from_grpc_status_permission_denied() {
        let err = from_grpc_status(status(Code::PermissionDenied, "no access"));
        assert!(matches!(err, SpiceDBError::PermissionDenied(_)));
        assert_eq!(err.to_string(), "permission denied: no access");
    }

    #[test]
    fn test_from_grpc_status_not_found() {
        let err = from_grpc_status(status(Code::NotFound, "missing"));
        assert!(matches!(err, SpiceDBError::NotFound(_)));
    }

    #[test]
    fn test_from_grpc_status_already_exists() {
        let err = from_grpc_status(status(Code::AlreadyExists, "dup"));
        assert!(matches!(err, SpiceDBError::AlreadyExists(_)));
    }

    #[test]
    fn test_from_grpc_status_invalid_argument() {
        let err = from_grpc_status(status(Code::InvalidArgument, "bad arg"));
        assert!(matches!(err, SpiceDBError::InvalidArgument(_)));
    }

    #[test]
    fn test_from_grpc_status_failed_precondition() {
        let err = from_grpc_status(status(Code::FailedPrecondition, "precond"));
        assert!(matches!(err, SpiceDBError::FailedPrecondition(_)));
    }

    #[test]
    fn test_from_grpc_status_unavailable() {
        let err = from_grpc_status(status(Code::Unavailable, "down"));
        assert!(matches!(err, SpiceDBError::Unavailable(_)));
    }

    #[test]
    fn test_from_grpc_status_cancelled() {
        let err = from_grpc_status(status(Code::Cancelled, "stopped"));
        assert!(matches!(err, SpiceDBError::Cancelled(_)));
    }

    #[test]
    fn test_from_grpc_status_deadline_exceeded() {
        let err = from_grpc_status(status(Code::DeadlineExceeded, "timeout"));
        assert!(matches!(err, SpiceDBError::DeadlineExceeded(_)));
    }

    #[test]
    fn test_from_grpc_status_resource_exhausted() {
        let err = from_grpc_status(status(Code::ResourceExhausted, "quota"));
        assert!(matches!(err, SpiceDBError::ResourceExhausted(_)));
    }

    // OUT_OF_RANGE is SpiceDB's code for an expired or garbage-collected
    // ZedToken. Recovery is mechanical -- drop the token, re-read at full
    // consistency -- so it must be distinguishable by variant rather than by
    // message. See root DESIGN.md, "RULE: Error mapping must not lose the
    // server's detail".
    #[test]
    fn test_from_grpc_status_out_of_range() {
        let err = from_grpc_status(status(Code::OutOfRange, "revision no longer available"));
        assert!(matches!(err, SpiceDBError::OutOfRange(_)));
        assert!(!matches!(err, SpiceDBError::InvalidArgument(_)));
        assert_eq!(
            err.to_string(),
            "out of range: revision no longer available"
        );
        assert!(!is_transient(&err));
    }

    // A wrong, expired, or rotated token must be distinguishable from an
    // internal server fault, so a caller can refresh credentials on one and
    // page someone on the other.
    #[test]
    fn test_from_grpc_status_unauthenticated() {
        let err = from_grpc_status(status(Code::Unauthenticated, "bad token"));
        assert!(matches!(err, SpiceDBError::Unauthenticated(_)));
        assert!(!matches!(err, SpiceDBError::PermissionDenied(_)));
        assert!(!matches!(err, SpiceDBError::Status { .. }));
        assert!(!is_transient(&err));
    }

    #[test]
    fn test_from_grpc_status_unknown_code() {
        let err = from_grpc_status(status(Code::Unknown, "unknown"));
        assert!(matches!(err, SpiceDBError::Status { code: 2, .. }));
        assert_eq!(err.to_string(), "grpc status 2: unknown");
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

    /// tonic's client-side deadline surfaces as `Cancelled("Timeout expired")`
    /// with `TimeoutExpired` as the status's `source`. Mapping must recognise it
    /// by that source, not by the message text -- this test constructs the
    /// status exactly the way tonic does, then proves a `Cancelled` carrying a
    /// *different* source (or none) is left alone even when the message is
    /// identical.
    #[test]
    fn test_timeout_expired_is_detected_structurally() {
        let from_tonic = Status::from_error(Box::new(tonic::TimeoutExpired(())));
        assert_eq!(from_tonic.code(), Code::Cancelled);
        let err = from_grpc_status(from_tonic);
        assert!(
            matches!(err, SpiceDBError::DeadlineExceeded(_)),
            "expected DeadlineExceeded, got {err:?}"
        );
    }

    /// The shape a real `Channel` produces: the timeout is not the status's
    /// immediate source but one hop further down, behind the transport error.
    /// `tests/deadline_test.rs` exercises this end to end against a stub server
    /// that never responds; this reproduces the nesting without a socket.
    #[test]
    fn test_timeout_expired_is_found_when_wrapped_one_level_down() {
        #[derive(Debug)]
        struct Wrapper(tonic::TimeoutExpired);

        impl fmt::Display for Wrapper {
            fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
                write!(f, "transport error")
            }
        }

        impl Error for Wrapper {
            fn source(&self) -> Option<&(dyn Error + 'static)> {
                Some(&self.0)
            }
        }

        let mut status = Status::cancelled("Timeout expired");
        status.set_source(std::sync::Arc::new(Wrapper(tonic::TimeoutExpired(()))));

        let err = from_grpc_status(status);
        assert!(
            matches!(err, SpiceDBError::DeadlineExceeded(_)),
            "expected DeadlineExceeded, got {err:?}"
        );
    }

    #[test]
    fn test_cancelled_with_the_same_text_but_no_timeout_source_stays_cancelled() {
        // A server that genuinely cancels with this exact wording must not be
        // reclassified. Under the old message comparison it would have been.
        let err = from_grpc_status(Status::cancelled("Timeout expired"));
        assert!(
            matches!(err, SpiceDBError::Cancelled(_)),
            "expected Cancelled, got {err:?}"
        );
    }

    #[test]
    fn test_source_chain_reaches_timeout_expired() {
        let err = from_grpc_status(Status::from_error(Box::new(tonic::TimeoutExpired(()))));

        let status_source = err.source().expect("SpiceDBError should source its Status");
        let status = status_source
            .downcast_ref::<Status>()
            .expect("source should be the originating tonic::Status");
        assert_eq!(status.code(), Code::Cancelled);

        let inner = status
            .source()
            .expect("Status should source TimeoutExpired");
        assert!(inner.downcast_ref::<tonic::TimeoutExpired>().is_some());
    }

    #[test]
    fn test_status_is_reachable_on_a_mapped_error() {
        let err = from_grpc_status(status(Code::NotFound, "missing"));
        let status = err.status().expect("mapped errors keep their Status");
        assert_eq!(status.code(), Code::NotFound);
        assert_eq!(status.message(), "missing");
    }

    #[test]
    fn test_locally_detected_error_has_no_status_and_no_reason() {
        let err = internal("malformed response".into());
        assert!(err.status().is_none());
        assert!(err.source().is_none());
        assert!(err.reason().is_none());
        assert!(err.reason_metadata().is_empty());
    }

    #[test]
    fn test_message_accessor_excludes_the_display_prefix() {
        let err = from_grpc_status(status(Code::NotFound, "missing"));
        assert_eq!(err.message(), "missing");
        assert_eq!(err.to_string(), "not found: missing");
    }
}
