//! Client-wide execution policy (retries, default deadline) and the per-call
//! transport escape hatch.
//!
//! See root DESIGN.md, "Deadlines, cancellation, and retry" and "Keep protocol
//! access secondary and ownership explicit".

use std::fmt;
use std::sync::{Arc, Mutex};
use std::time::Duration;

use tonic::metadata::MetadataMap;
use tonic::Code;

use crate::error::SpiceDBError;

/// A shared callback stored in an options value. Compares by identity so
/// options keep `PartialEq`.
#[derive(Clone)]
pub(crate) struct Callback<T: ?Sized>(pub(crate) Arc<dyn Fn(&T) + Send + Sync>);
impl<T: ?Sized> Callback<T> {
    pub(crate) fn new(f: impl Fn(&T) + Send + Sync + 'static) -> Self {
        Self(Arc::new(f))
    }
    pub(crate) fn call(&self, value: &T) {
        (self.0)(value)
    }
}
impl<T: ?Sized> fmt::Debug for Callback<T> {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str("Callback(..)")
    }
}
impl<T: ?Sized> PartialEq for Callback<T> {
    fn eq(&self, other: &Self) -> bool {
        Arc::ptr_eq(&self.0, &other.0)
    }
}
impl<T: ?Sized> Eq for Callback<T> {}
// A callback only ever receives a borrowed event and the client keeps no
// state that a panic inside it could leave half-updated (response-metadata
// capture recovers from poisoning), so options holding one stay unwind-safe
// like the rest of the options values.
impl<T: ?Sized> std::panic::UnwindSafe for Callback<T> {}
impl<T: ?Sized> std::panic::RefUnwindSafe for Callback<T> {}

/// A retry about to happen, passed to [`RetryPolicy::with_on_retry`].
#[derive(Debug, Clone)]
#[non_exhaustive]
pub struct RetryEvent {
    /// The attempt that failed, starting at 1.
    pub attempt: u32,
    /// That attempt's error.
    pub error: SpiceDBError,
    /// The backoff before the next attempt.
    pub delay: Duration,
}

/// Retry policy for idempotent reads and stream establishment.
///
/// Mutations (writes, deletes, imports, counter registration) are never
/// retried automatically. A caller may opt a single idempotent mutation into
/// retries with [`CallOptions::with_retry_policy`]: a write containing only
/// touches and no preconditions, or a schema write. The default is three retries of
/// `Unavailable` and `Aborted` with full-jitter exponential backoff starting at
/// 100 ms and capped at 5 s.
///
/// ```
/// use std::time::Duration;
/// use spicedb::RetryPolicy;
/// let policy = RetryPolicy::new()
///     .with_max_retries(2)
///     .with_retryable_codes(vec![tonic::Code::Unavailable, tonic::Code::ResourceExhausted])
///     .with_initial_backoff(Duration::from_millis(50))
///     .with_on_retry(|event| eprintln!("retrying after {:?}: {}", event.delay, event.error));
/// ```
#[derive(Debug, Clone, PartialEq, Eq)]
#[non_exhaustive]
pub struct RetryPolicy {
    pub(crate) max_retries: u32,
    pub(crate) retryable_codes: Vec<Code>,
    pub(crate) initial_backoff: Duration,
    pub(crate) max_backoff: Option<Duration>,
    pub(crate) on_retry: Option<Callback<RetryEvent>>,
}

impl Default for RetryPolicy {
    fn default() -> Self {
        Self {
            max_retries: 3,
            retryable_codes: vec![Code::Unavailable, Code::Aborted],
            initial_backoff: Duration::from_millis(100),
            max_backoff: Some(Duration::from_secs(5)),
            on_retry: None,
        }
    }
}

impl RetryPolicy {
    /// The built-in default policy.
    pub fn new() -> Self {
        Self::default()
    }
    /// Retries after the first attempt. Zero means a single attempt.
    pub fn with_max_retries(mut self, retries: u32) -> Self {
        self.max_retries = retries;
        self
    }
    /// The codes that are retried. The default excludes `ResourceExhausted`
    /// (SpiceDB load-shedding or a deterministic depth limit); a caller may add
    /// it deliberately.
    pub fn with_retryable_codes(mut self, codes: Vec<Code>) -> Self {
        self.retryable_codes = codes;
        self
    }
    /// The upper bound of the first full-jitter delay; each retry doubles it.
    pub fn with_initial_backoff(mut self, backoff: Duration) -> Self {
        self.initial_backoff = backoff;
        self
    }
    /// Caps the delay bound.
    pub fn with_max_backoff(mut self, backoff: Duration) -> Self {
        self.max_backoff = Some(backoff);
        self
    }
    /// Called before each backoff, for example to log retries.
    pub fn with_on_retry(mut self, f: impl Fn(&RetryEvent) + Send + Sync + 'static) -> Self {
        self.on_retry = Some(Callback::new(f));
        self
    }
    pub(crate) fn validate(&self) -> Result<(), SpiceDBError> {
        if self.max_retries > 0 && self.initial_backoff.is_zero() {
            return Err(crate::types::invalid(
                "retry_policy",
                "initial backoff must be positive when retries are enabled",
            ));
        }
        Ok(())
    }
    pub(crate) fn retryable(&self, code: Code) -> bool {
        self.retryable_codes.contains(&code)
    }
    pub(crate) fn delay(&self, attempt: u32) -> Duration {
        let bound = self
            .initial_backoff
            .checked_mul(1u32.checked_shl(attempt).unwrap_or(u32::MAX))
            .unwrap_or(Duration::MAX);
        let bound = self.max_backoff.map_or(bound, |m| bound.min(m));
        bound.mul_f64(rand::random::<f64>())
    }
}

/// Whether the client retries idempotent reads at all.
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq)]
#[non_exhaustive]
pub enum RetryMode {
    /// Use the configured [`RetryPolicy`] (the default policy unless one is set).
    #[default]
    Enabled,
    /// Every operation is a single attempt, for callers with their own policy.
    Disabled,
}

/// Whether opening a server stream (reads, lookups, exports, watch) is
/// retried before any item is exposed. A partially consumed stream is never
/// restarted.
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq)]
#[non_exhaustive]
pub enum StreamRetryMode {
    /// Retry establishment with the retry policy.
    #[default]
    Enabled,
    /// Open each stream once, e.g. for a watch the caller supervises.
    Disabled,
}

/// Whether unary operations get the client's default deadline when no
/// per-call timeout is set.
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq)]
#[non_exhaustive]
pub enum DefaultDeadline {
    /// Apply the default (30 seconds, or the builder's `default_timeout`).
    #[default]
    Enabled,
    /// Apply no client deadline. Per-call timeouts and dropping the future
    /// still bound the operation.
    Disabled,
}

/// Response headers and trailers captured by [`CallOptions::with_response_metadata`].
///
/// A cheap handle: keep a clone, pass one in the call options, and read it
/// after the operation. For operations making several RPCs (retries, chunks,
/// pages) the last RPC wins. For unary RPCs tonic delivers headers and
/// trailers merged into one map, so both accessors return that map; streams
/// report them separately. Data that SpiceDB versions before 1.30 send in
/// trailers is reachable here.
#[derive(Clone, Default)]
pub struct ResponseMetadata(Arc<Mutex<(MetadataMap, MetadataMap)>>);

impl ResponseMetadata {
    pub fn new() -> Self {
        Self::default()
    }
    /// The last RPC's response headers.
    pub fn headers(&self) -> MetadataMap {
        self.0.lock().unwrap_or_else(|e| e.into_inner()).0.clone()
    }
    /// The last RPC's trailers.
    pub fn trailers(&self) -> MetadataMap {
        self.0.lock().unwrap_or_else(|e| e.into_inner()).1.clone()
    }
    pub(crate) fn set_headers(&self, headers: &MetadataMap) {
        self.0.lock().unwrap_or_else(|e| e.into_inner()).0 = headers.clone();
    }
    pub(crate) fn set_trailers(&self, trailers: &MetadataMap) {
        self.0.lock().unwrap_or_else(|e| e.into_inner()).1 = trailers.clone();
    }
}
impl fmt::Debug for ResponseMetadata {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.debug_struct("ResponseMetadata")
            .field("headers", &self.headers())
            .field("trailers", &self.trailers())
            .finish()
    }
}
impl PartialEq for ResponseMetadata {
    fn eq(&self, other: &Self) -> bool {
        Arc::ptr_eq(&self.0, &other.0)
    }
}
impl Eq for ResponseMetadata {}

/// Per-call execution and transport settings, accepted by every operation's
/// options through `with_call_options`.
///
/// It is the supported route to transport-level data, so ordinary callers
/// never need [`raw_proto`](crate::SpiceDBClient::raw_proto) for it. Request
/// metadata is sent on every RPC the operation makes (each attempt, chunk and
/// page). The client's own `authorization` and `grpc-timeout` headers take
/// precedence over the same keys here, except that a client built with
/// [`SpiceDBClient::from_channel`](crate::SpiceDBClient::from_channel) without
/// a token sends a caller-supplied `authorization`.
///
/// ```
/// use spicedb::{CallOptions, ResponseMetadata};
/// let mut metadata = tonic::metadata::MetadataMap::new();
/// metadata.insert("x-request-id", "abc123".parse().unwrap());
/// let captured = ResponseMetadata::new();
/// let call = CallOptions::new()
///     .with_request_metadata(metadata)
///     .with_response_metadata(captured.clone());
/// let options = spicedb::ReadSchemaOptions::new().with_call_options(call);
/// ```
///
/// [`with_retry_policy`](Self::with_retry_policy) overrides the client's retry
/// policy for this one call, for example to open one supervised watch exactly
/// once while the client keeps retrying everything else:
///
/// ```
/// use spicedb::{CallOptions, RetryPolicy, WatchOptions};
/// let once = CallOptions::new().with_retry_policy(RetryPolicy::new().with_max_retries(0));
/// let options = WatchOptions::new().with_call_options(once);
/// ```
#[derive(Debug, Clone, Default)]
#[non_exhaustive]
pub struct CallOptions {
    pub(crate) metadata: MetadataMap,
    pub(crate) capture: Option<ResponseMetadata>,
    pub(crate) retry: Option<RetryPolicy>,
}

impl CallOptions {
    pub fn new() -> Self {
        Self::default()
    }
    /// Metadata (gRPC request headers) sent on every RPC of the operation.
    pub fn with_request_metadata(mut self, metadata: MetadataMap) -> Self {
        self.metadata = metadata;
        self
    }
    /// Captures response headers and trailers into `capture`.
    pub fn with_response_metadata(mut self, capture: ResponseMetadata) -> Self {
        self.capture = Some(capture);
        self
    }
    /// Overrides the client's retry policy (including
    /// [`RetryMode::Disabled`]) for this call. For a server stream it also
    /// governs reopening before the first item, whatever the client's
    /// [`StreamRetryMode`]; `RetryPolicy::new().with_max_retries(0)` exempts
    /// one call or stream from retries.
    ///
    /// Mutations are not retried by default. Two are idempotent on replay and
    /// may opt in here: a `write` whose transaction contains only touches and
    /// no preconditions, and `write_schema`. Any other mutation (creates,
    /// deletes, preconditioned writes, imports, counter registration,
    /// delete-all) given a policy with retries fails with `InvalidArgument`
    /// before any request.
    pub fn with_retry_policy(mut self, policy: RetryPolicy) -> Self {
        self.retry = Some(policy);
        self
    }
}
impl PartialEq for CallOptions {
    fn eq(&self, other: &Self) -> bool {
        self.metadata.clone().into_headers() == other.metadata.clone().into_headers()
            && self.capture == other.capture
            && self.retry == other.retry
    }
}
impl Eq for CallOptions {}

/// How [`check_bulk`](crate::SpiceDBClient::check_bulk) reports per-item
/// failures.
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq)]
#[non_exhaustive]
pub enum BulkItemErrors {
    /// The first failing item fails the whole call with
    /// [`SpiceDBError::BulkItem`].
    #[default]
    Fail,
    /// Every item gets a result; a failed item carries its
    /// [`SpiceDBError::BulkItem`] in [`CheckResult::error`](crate::CheckResult::error)
    /// and `Permissionship::Unspecified`, so it never grants.
    Returned,
}

/// Whether a bounded delete may delete only part of the matching
/// relationships.
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq)]
#[non_exhaustive]
pub enum DeletePartial {
    /// Delete up to the limit and report whether deletion completed.
    #[default]
    Allowed,
    /// All-or-nothing: the server rejects a delete that would exceed the limit
    /// instead of deleting part of it. The limit may then be omitted
    /// ([`DeleteRequest::server_default_limit`](crate::DeleteRequest::server_default_limit))
    /// to use the server's configured default.
    Disallowed,
}

/// How [`read_relationships`](crate::SpiceDBClient::read_relationships) and
/// [`lookup_resources`](crate::SpiceDBClient::lookup_resources) fetch results.
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq)]
#[non_exhaustive]
pub enum Paging {
    /// Fetch page after page (page size from the options' `with_page_size`)
    /// until results or the limit run out.
    #[default]
    Automatic,
    /// Send exactly one request, as the raw API does: its limit is the
    /// options' `with_limit` (or unbounded without one), the page size is
    /// ignored, and no follow-up page is requested. Each item still carries
    /// its after-result cursor.
    SingleRequest,
}
