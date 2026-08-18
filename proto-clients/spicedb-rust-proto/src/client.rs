use tonic::metadata::MetadataValue;
use tonic::service::Interceptor;
use tonic::transport::{Channel, ClientTlsConfig, Endpoint};

use crate::authzed::api::v1;

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
    /// * `insecure` - If true, disables TLS (for local testing)
    pub async fn new(
        endpoint: &str,
        token: &str,
        insecure: bool,
    ) -> Result<Self, tonic::transport::Error> {
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
