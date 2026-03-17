use tonic::transport::{Channel, ClientTlsConfig, Endpoint};
use tonic::metadata::MetadataValue;
use tonic::service::Interceptor;

/// Bearer token interceptor that injects an `authorization` header into every
/// gRPC request.
#[derive(Clone)]
struct BearerTokenInterceptor {
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
    // Once proto/ is populated and tonic-build generates the service clients,
    // these fields will hold the generated client types:
    //
    // pub permissions: authzed::api::v1::permissions_service_client::PermissionsServiceClient<
    //     tonic::service::interceptor::InterceptedService<Channel, BearerTokenInterceptor>,
    // >,
    // pub schema: authzed::api::v1::schema_service_client::SchemaServiceClient<
    //     tonic::service::interceptor::InterceptedService<Channel, BearerTokenInterceptor>,
    // >,
    // pub watch: authzed::api::v1::watch_service_client::WatchServiceClient<
    //     tonic::service::interceptor::InterceptedService<Channel, BearerTokenInterceptor>,
    // >,
    // pub experimental: authzed::api::v1::experimental_service_client::ExperimentalServiceClient<
    //     tonic::service::interceptor::InterceptedService<Channel, BearerTokenInterceptor>,
    // >,
    _channel: Channel,
    _interceptor: BearerTokenInterceptor,
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
            ep = ep.tls_config(ClientTlsConfig::new())?;
        }

        let channel = ep.connect().await?;

        let bearer = format!("Bearer {}", token)
            .parse::<MetadataValue<tonic::metadata::Ascii>>()
            .expect("valid bearer token");

        let interceptor = BearerTokenInterceptor { token: bearer };

        // Once the generated service clients are available, construct them:
        //
        // let svc = tonic::service::interceptor::InterceptedService::new(
        //     channel.clone(), interceptor.clone(),
        // );
        // let permissions = authzed::api::v1::permissions_service_client::PermissionsServiceClient::new(svc.clone());
        // let schema = authzed::api::v1::schema_service_client::SchemaServiceClient::new(svc.clone());
        // let watch = authzed::api::v1::watch_service_client::WatchServiceClient::new(svc.clone());
        // let experimental = authzed::api::v1::experimental_service_client::ExperimentalServiceClient::new(svc);

        Ok(Self {
            _channel: channel,
            _interceptor: interceptor,
        })
    }
}
