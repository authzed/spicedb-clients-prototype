use std::fmt;
use std::net::IpAddr;

use tonic::metadata::MetadataValue;
use tonic::service::Interceptor;
use tonic::transport::{Channel, ClientTlsConfig, Endpoint, Uri};

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

/// Reports whether the connection this client would actually open for
/// `endpoint` terminates on a loopback destination: the literal hostname
/// "localhost", an IP in 127.0.0.0/8, the IPv6 loopback ::1, or a unix
/// domain socket target (a "unix:" prefix). A unix socket never leaves the
/// host's kernel, so it is loopback for this check even though it has no IP
/// at all.
///
/// That wording is deliberate. This function does not answer "does this
/// string look like it names a loopback host"; it answers "will the
/// transport dial loopback". Those are the same question only if this
/// function and the transport agree on where the host ends and the rest of
/// the target begins -- and a hand-rolled string split will always disagree
/// with a URI parser somewhere. It used to: given
/// `"127.0.0.1:443@evil.com"` a last-colon split yields host "127.0.0.1"
/// and reports loopback, while `Endpoint::from_shared("http://…")` parses
/// the same string as a URI, reads "127.0.0.1:443" as *userinfo*, and
/// reports `uri.host() == Some("evil.com")` -- so the bearer token went to
/// evil.com in cleartext with this function reporting "loopback".
/// `"[::1]:443@evil.com"` did the same through the bracketed branch, which
/// never validated what followed the `]`.
///
/// So the host is derived by building the exact URI
/// [`SpiceDBProtoClient::new_with_options`] dials (`"http://" + endpoint`),
/// parsing it with the same [`Uri`] type tonic's [`Endpoint`] is built from,
/// and asking IT for the host. There is one parse, so guard and transport
/// cannot disagree. Before that, anything that could move the authority
/// under URI parsing (`@`, `/`, `?`, `#`, whitespace) is refused outright: a
/// legitimate SpiceDB target contains none of those, and failing closed on a
/// weird endpoint is the correct trade for a credential leak.
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
    // Checked first, and only on the raw string: a unix target is not a URI
    // authority at all (it carries a filesystem path, so it legitimately
    // contains the '/' the reserved-character check below refuses), and it
    // never leaves the host's kernel regardless of what the path says.
    if endpoint.starts_with("unix:") {
        return true;
    }

    // Fail closed on any character that can shift which part of the string
    // the URI parser treats as the authority: '@' (userinfo), '/' (path),
    // '?' (query), '#' (fragment), whitespace. Redundant with the Uri parse
    // below -- deliberately so. The parse is what makes this function
    // correct; this is what keeps it correct if some future edit ever
    // reaches for a manual split again.
    if endpoint
        .chars()
        .any(|c| matches!(c, '@' | '/' | '?' | '#') || c.is_whitespace())
    {
        return false;
    }

    // A bare IPv6 literal ("::1") is not a legal URI authority -- brackets
    // are the only form the transport can dial -- so bracket it and let the
    // one parser below judge it, rather than special-casing it out of the
    // parse entirely.
    let authority = match endpoint.parse::<IpAddr>() {
        Ok(IpAddr::V6(_)) if !endpoint.starts_with('[') => format!("[{endpoint}]"),
        _ => endpoint.to_string(),
    };

    // The scheme is "http" because this guard only ever gates the insecure
    // path; either way, scheme does not affect how the authority is parsed.
    let uri: Uri = match format!("http://{authority}").parse() {
        Ok(uri) => uri,
        Err(_) => return false,
    };
    let Some(host) = uri.host() else {
        return false;
    };

    // Uri::host keeps the brackets on an IPv6 literal; IpAddr::from_str does
    // not accept them.
    let host = host
        .strip_prefix('[')
        .and_then(|h| h.strip_suffix(']'))
        .unwrap_or(host);

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
