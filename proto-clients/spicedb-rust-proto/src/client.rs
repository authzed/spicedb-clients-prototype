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
    /// Refused because `endpoint` names a unix domain socket, which this
    /// client's transport cannot dial. tonic's `Channel` takes a URI, and
    /// `"http://unix:/var/run/spicedb.sock".parse::<Uri>()` has host `"unix"`
    /// -- so accepting such an endpoint would resolve the DNS name `unix` and
    /// connect there, not to the socket path. Raised unconditionally, before
    /// the credential guard and regardless of TLS, since no combination of
    /// options makes that the thing the caller asked for.
    UnixSocketNotSupported(String),
    /// Refused because `token` cannot be carried in an HTTP header. A gRPC
    /// `authorization` metadata value is an ASCII header value, so a control
    /// character -- most commonly a trailing newline on a token read from a
    /// file or a mounted secret -- has no valid encoding. Raised instead of
    /// panicking: this is a `Result`-returning async constructor, and an
    /// unwind here aborts the task carrying it (or the process, under
    /// `panic = "abort"`).
    InvalidToken(String),
    /// A tonic transport-level error (invalid URI, TLS/connect failure).
    Transport(tonic::transport::Error),
}

impl fmt::Display for SpiceDBProtoClientError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Self::InsecureRemoteHostNotAllowed(msg) => write!(f, "{msg}"),
            Self::UnixSocketNotSupported(msg) => write!(f, "{msg}"),
            Self::InvalidToken(msg) => write!(f, "{msg}"),
            Self::Transport(e) => write!(f, "{e}"),
        }
    }
}

impl std::error::Error for SpiceDBProtoClientError {
    fn source(&self) -> Option<&(dyn std::error::Error + 'static)> {
        match self {
            Self::InsecureRemoteHostNotAllowed(_)
            | Self::UnixSocketNotSupported(_)
            | Self::InvalidToken(_) => None,
            Self::Transport(e) => Some(e),
        }
    }
}

impl From<tonic::transport::Error> for SpiceDBProtoClientError {
    fn from(e: tonic::transport::Error) -> Self {
        Self::Transport(e)
    }
}

/// Returns the URI authority the transport dials for `endpoint`.
///
/// This exists so [`is_loopback_endpoint`] and
/// [`SpiceDBProtoClient::new_with_options`] cannot disagree about what is
/// being connected to. It brackets a bare IPv6 literal: `"::1"` is a perfectly
/// ordinary way to name the loopback host and is an explicit part of this
/// client's supported set, but it is *not* a legal URI authority, so
/// `"http://::1".parse::<Uri>()` fails with `InvalidAuthority`. The guard used
/// to bracket it for its own parse while the constructor built the URI from
/// the raw endpoint, which meant `"::1"` passed the guard and then failed to
/// construct a client at all.
///
/// Anything already bracketed, or not an IPv6 literal, is returned unchanged.
fn transport_authority(endpoint: &str) -> String {
    match endpoint.parse::<IpAddr>() {
        Ok(IpAddr::V6(_)) if !endpoint.starts_with('[') => format!("[{endpoint}]"),
        _ => endpoint.to_string(),
    }
}

/// Reports whether the connection this client would actually open for
/// `endpoint` terminates on a loopback destination: the literal hostname
/// "localhost", an IP in 127.0.0.0/8, or the IPv6 loopback ::1.
///
/// Unix-domain-socket targets are NOT in that list, unlike the equivalent
/// guard in the Go, Python and Ruby clients. Those clients' transports
/// genuinely dial a UDS path; this one cannot.
/// `"http://unix:/var/run/spicedb.sock".parse::<Uri>()` has host `"unix"`, so
/// a "unix:" endpoint here would resolve that DNS name instead.
/// [`SpiceDBProtoClient::new_with_options`] refuses such an endpoint outright
/// with [`SpiceDBProtoClientError::UnixSocketNotSupported`], rather than
/// letting this function call it loopback.
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
    // There is deliberately no "unix:" exemption here -- see the doc comment
    // above, and the unconditional refusal in new_with_options below.

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

    // Exactly the authority the transport will dial -- see
    // transport_authority, which new_with_options uses to build its URI. Using
    // one function for both is the point: if this bracketed a bare IPv6
    // literal and the constructor did not, the guard would vet an address the
    // transport never sees.
    let authority = transport_authority(endpoint);

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
    ///   (localhost, 127.0.0.0/8, or ::1; a `unix:` target is NOT loopback
    ///   here and is refused outright, since tonic would resolve the DNS
    ///   name `unix` instead of a socket path) -- see
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
        // Refused unconditionally -- ahead of the credential guard below, and
        // regardless of TLS or of allow_insecure_remote_credentials -- because
        // this transport cannot do what such an endpoint asks for. tonic's
        // Channel takes a URI, and "http://unix:/var/run/spicedb.sock" parses
        // with host "unix", so the endpoint that looks local would resolve
        // that DNS name and ship the bearer token there. Failing loudly is the
        // only honest answer; silently dialing a host called "unix" is not.
        // Matched case-insensitively because a URI scheme is case-insensitive.
        //
        // endpoint.get(..5), not endpoint[..5]: the latter is a BYTE slice on a
        // &str and panics when byte 5 falls inside a multi-byte character, so
        // an IDN hostname like "abcdé.example.com:443" unwound out of this
        // async constructor instead of returning Err -- aborting the Tokio task
        // carrying it, or the process under panic=abort. get() returns None
        // there instead.
        if endpoint
            .get(..5)
            .is_some_and(|p| p.eq_ignore_ascii_case("unix:"))
        {
            return Err(SpiceDBProtoClientError::UnixSocketNotSupported(format!(
                "spicedb: unix-domain-socket targets are not supported by this client's \
                 transport: \"{endpoint}\". tonic would parse \"unix\" as a DNS hostname and \
                 connect there, not to the socket path. Use a \"host:port\" endpoint instead."
            )));
        }

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

        // transport_authority, not the raw endpoint: a bare IPv6 literal must
        // be bracketed to be a legal URI authority, and it is the same
        // function the guard above vetted, so the two cannot disagree about
        // where this connection goes.
        let authority = transport_authority(endpoint);
        let uri = if insecure {
            format!("http://{}", authority)
        } else {
            format!("https://{}", authority)
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

        // Not .expect(): a gRPC metadata value is an ASCII header value, so a
        // token holding a control character has no valid encoding -- and the
        // overwhelmingly common way to get one is a trailing newline on a
        // secret read from a file or a mounted k8s secret. Panicking there
        // unwinds out of a Result-returning async fn, aborting the Tokio task
        // carrying it, or the process under panic = "abort". Note that
        // non-ASCII text such as "tokén" is NOT affected, and neither is a
        // horizontal tab: bytes >= 0x80 are legal obs-text header octets and
        // HTAB is legal field-value whitespace, which is what makes the
        // surviving control-character case easy to miss.
        let bearer = format!("Bearer {}", token)
            .parse::<MetadataValue<tonic::metadata::Ascii>>()
            .map_err(|_| {
                SpiceDBProtoClientError::InvalidToken(
                    "spicedb: token is not a valid gRPC metadata value: it contains a character \
                     that cannot appear in an HTTP header (most often a trailing newline on a \
                     token read from a file). Strip surrounding whitespace before passing it."
                        .to_string(),
                )
            })?;

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
