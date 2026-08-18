use std::fmt;
use std::net::IpAddr;

use tonic::metadata::MetadataValue;
use tonic::service::Interceptor;
use tonic::transport::{Channel, ClientTlsConfig, Endpoint};

use crate::authzed::api::v1;

/// Errors returned by [`SpiceDBProtoClient::new`] / [`SpiceDBProtoClient::new_with_options`].
#[derive(Debug)]
pub enum SpiceDBProtoClientError {
    /// See root DESIGN.md, "RULE: Credentials over insecure transport
    /// require an explicit opt-in": refused because `insecure` was `true`,
    /// `endpoint` was not loopback, and `allow_insecure_remote_credentials`
    /// was `false`. Raised before any [`Endpoint`], TLS config, or channel
    /// is created, so the token can never reach the wire for a rejected
    /// combination.
    InsecureRemoteHostNotAllowed(String),
    /// A tonic transport-level error (invalid URI, TLS/connect failure).
    Transport(tonic::transport::Error),
}

impl fmt::Display for SpiceDBProtoClientError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Self::InsecureRemoteHostNotAllowed(msg) => write!(f, "{msg}"),
            Self::Transport(e) => write!(f, "{e}"),
        }
    }
}

impl std::error::Error for SpiceDBProtoClientError {
    fn source(&self) -> Option<&(dyn std::error::Error + 'static)> {
        match self {
            Self::InsecureRemoteHostNotAllowed(_) => None,
            Self::Transport(e) => Some(e),
        }
    }
}

impl From<tonic::transport::Error> for SpiceDBProtoClientError {
    fn from(e: tonic::transport::Error) -> Self {
        Self::Transport(e)
    }
}

/// Reports whether a gRPC target string names a loopback destination: the
/// literal hostname "localhost", an IP in 127.0.0.0/8, the IPv6 loopback
/// ::1, or a unix domain socket target (a "unix:" prefix). A unix socket
/// never leaves the host's kernel, so it is loopback for this check even
/// though it has no IP at all.
///
/// This is the exemption in root DESIGN.md, "RULE: Credentials over
/// insecure transport require an explicit opt-in": loopback is the reason
/// `insecure` exists at all (local development, docker-compose, CI), so it
/// must keep working with no extra ceremony. Anything else requires
/// `allow_insecure_remote_credentials: true` -- see
/// [`SpiceDBProtoClient::new_with_options`].
///
/// Never performs a DNS lookup: `str::parse::<IpAddr>` is pure parsing (no
/// I/O either way), so a real remote hostname is simply rejected as "not
/// an IP" and treated as non-loopback.
pub fn is_loopback_endpoint(endpoint: &str) -> bool {
    if endpoint.starts_with("unix:") {
        return true;
    }

    let host: &str = if let Some(rest) = endpoint.strip_prefix('[') {
        // "[::1]:50051" or "[::1]" -> "::1"
        rest.split(']').next().unwrap_or(rest)
    } else if endpoint.matches(':').count() > 1 {
        // A bare IPv6 literal (e.g. "::1") -- no port is possible without
        // brackets, so the whole string is the host.
        endpoint
    } else if let Some(idx) = endpoint.rfind(':') {
        &endpoint[..idx]
    } else {
        endpoint
    };

    if host.eq_ignore_ascii_case("localhost") {
        return true;
    }

    host.parse::<IpAddr>()
        .map(|ip| ip.is_loopback())
        .unwrap_or(false)
}

/// Bearer token interceptor that injects an `authorization` header into every
/// gRPC request.
#[derive(Clone)]
pub struct BearerTokenInterceptor {
    token: MetadataValue<tonic::metadata::Ascii>,
}

impl Interceptor for BearerTokenInterceptor {
    fn call(
        &mut self,
        mut request: tonic::Request<()>,
    ) -> Result<tonic::Request<()>, tonic::Status> {
        request
            .metadata_mut()
            .insert("authorization", self.token.clone());
        Ok(request)
    }
}

/// The intercepted service type used by all service clients.
pub type InterceptedService =
    tonic::service::interceptor::InterceptedService<Channel, BearerTokenInterceptor>;

/// A thin wrapper over tonic-generated gRPC service clients for SpiceDB.
///
/// Provides access to all SpiceDB API services:
/// - `permissions` — PermissionsService (CheckPermission, LookupResources, etc.)
/// - `schema` — SchemaService (ReadSchema, WriteSchema)
/// - `watch` — WatchService (Watch)
/// - `experimental` — ExperimentalService (BulkCheckPermission, etc.)
///
/// # Example
///
/// ```rust,no_run
/// use spicedb_proto::SpiceDBProtoClient;
///
/// #[tokio::main]
/// async fn main() -> Result<(), Box<dyn std::error::Error>> {
///     let client = SpiceDBProtoClient::new("grpc.authzed.com:443", "my-token", false).await?;
///     Ok(())
/// }
/// ```
pub struct SpiceDBProtoClient {
    pub permissions: v1::permissions_service_client::PermissionsServiceClient<InterceptedService>,
    pub schema: v1::schema_service_client::SchemaServiceClient<InterceptedService>,
    pub watch: v1::watch_service_client::WatchServiceClient<InterceptedService>,
    pub experimental:
        v1::experimental_service_client::ExperimentalServiceClient<InterceptedService>,
}

impl SpiceDBProtoClient {
    /// Creates a new SpiceDB proto client connected to the given endpoint,
    /// authenticated with the given bearer token.
    ///
    /// # Arguments
    ///
    /// * `endpoint` - The gRPC endpoint (e.g., "grpc.authzed.com:443")
    /// * `token` - Bearer token for authentication
    /// * `insecure` - If true, disables TLS (for local testing). By itself,
    ///   this only permits a plaintext connection to a loopback endpoint
    ///   (localhost, 127.0.0.0/8, ::1, or a unix socket target) -- see
    ///   [`new_with_options`](Self::new_with_options) for a non-loopback
    ///   endpoint.
    pub async fn new(
        endpoint: &str,
        token: &str,
        insecure: bool,
    ) -> Result<Self, SpiceDBProtoClientError> {
        Self::new_with_options(endpoint, token, insecure, false).await
    }

    /// As [`new`](Self::new), with an explicit opt-in permitting a
    /// non-loopback `endpoint` when `insecure` is `true`.
    ///
    /// # Arguments
    ///
    /// * `endpoint` - The gRPC endpoint (e.g., "grpc.authzed.com:443")
    /// * `token` - Bearer token for authentication
    /// * `insecure` - If true, disables TLS (for local testing)
    /// * `allow_insecure_remote_credentials` - Explicit, separately named
    ///   opt-in required by root DESIGN.md, "RULE: Credentials over
    ///   insecure transport require an explicit opt-in" before `insecure`
    ///   may be combined with a non-loopback `endpoint`. Named and separate
    ///   from `insecure` on purpose: the rule requires an option a reader
    ///   cannot mistake for a default, not a boolean that does double duty
    ///   as the plaintext-transport switch. Pass `true` only if you
    ///   genuinely mean to send a bearer token in cleartext to a remote
    ///   host.
    pub async fn new_with_options(
        endpoint: &str,
        token: &str,
        insecure: bool,
        allow_insecure_remote_credentials: bool,
    ) -> Result<Self, SpiceDBProtoClientError> {
        // See root DESIGN.md, "RULE: Credentials over insecure transport
        // require an explicit opt-in". This is the guard for
        // BearerTokenInterceptor above: tonic's Interceptor trait has no
        // built-in "refuse over an insecure channel" check the way some
        // other language bindings do, so nothing else here stops a bearer
        // token from reaching an arbitrary insecure host. Refuse before any
        // Endpoint, TLS config, or channel is created.
        if insecure && !allow_insecure_remote_credentials && !is_loopback_endpoint(endpoint) {
            return Err(SpiceDBProtoClientError::InsecureRemoteHostNotAllowed(format!(
                "spicedb: refusing to send credentials over an insecure (plaintext) connection to \
                 non-loopback endpoint \"{endpoint}\": use TLS (insecure: false), or pass \
                 allow_insecure_remote_credentials: true if you intend to send a bearer token in \
                 cleartext to a remote host"
            )));
        }

        let uri = if insecure {
            format!("http://{}", endpoint)
        } else {
            format!("https://{}", endpoint)
        };

        let mut ep = Endpoint::from_shared(uri)?;

        if !insecure {
            // Platform trust store. `ClientTlsConfig::new()` alone carries an EMPTY
            // trust-anchor set, so every handshake fails `UnknownIssuer` — see the
            // root DESIGN.md rule "A system-TLS constructor must reach a real server".
            // Do not swap in `with_enabled_roots()`: it discards `self`.
            ep = ep.tls_config(ClientTlsConfig::new().with_native_roots())?;
        }

        // Use connect_lazy for insecure (plaintext) connections so that client
        // construction succeeds even when no server is running.  For TLS
        // connections, a real handshake is needed to validate certs, so we
        // keep the eager connect() path there.
        let channel = if insecure {
            ep.connect_lazy()
        } else {
            ep.connect().await?
        };

        let bearer = format!("Bearer {}", token)
            .parse::<MetadataValue<tonic::metadata::Ascii>>()
            .expect("valid bearer token");

        let interceptor = BearerTokenInterceptor { token: bearer };

        let svc = tonic::service::interceptor::InterceptedService::new(
            channel.clone(),
            interceptor.clone(),
        );
        let permissions =
            v1::permissions_service_client::PermissionsServiceClient::new(svc.clone());
        let schema = v1::schema_service_client::SchemaServiceClient::new(svc.clone());
        let watch = v1::watch_service_client::WatchServiceClient::new(svc.clone());
        let experimental = v1::experimental_service_client::ExperimentalServiceClient::new(svc);

        Ok(Self {
            permissions,
            schema,
            watch,
            experimental,
        })
    }
}
