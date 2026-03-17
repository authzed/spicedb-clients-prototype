//! The SpiceDB client and all operations.
//!
//! Use [`SpiceDBClient::new_plaintext`], [`SpiceDBClient::new_system_tls`], or
//! [`SpiceDBClient::builder`] to create a client.
//!
//! # Connection Types
//!
//! - **Plaintext** — insecure, for testing only. The constructor name makes the
//!   lack of TLS obvious.
//! - **System TLS** — uses the system's TLS certificate pool. Recommended for
//!   production.
//! - **Builder** — full control over TLS configuration and other options.

use crate::consistency::Strategy;
use crate::error::SpiceDBError;
use crate::types::{
    CheckResult, CountResult, ExpandResult, Filter, ReflectSchemaResult, RelationReference,
    Relationship, SchemaDiff, Transaction, Update,
};

// TODO: When spicedb-proto is available, uncomment these imports:
// use spicedb_proto::authzed::api::v1 as proto;
// use tonic::transport::{Channel, ClientTlsConfig};
// use tokio_stream::Stream;

/// Default page sizes for transparent cursor-based pagination.
const DEFAULT_READ_PAGE_SIZE: u32 = 512;
const DEFAULT_LOOKUP_PAGE_SIZE: u32 = 512;
const DEFAULT_EXPORT_PAGE_SIZE: u32 = 512;
const DEFAULT_DELETE_PAGE_SIZE: u32 = 10_000;
const DEFAULT_CHECK_BATCH_SIZE: usize = 1_000;
const DEFAULT_IMPORT_BATCH_SIZE: usize = 1_000;

/// The idiomatic SpiceDB client.
///
/// All methods are async and require a tokio runtime. Read operations take an
/// explicit [`Strategy`] parameter — consistency is never silently defaulted.
///
/// Streaming operations return `impl Stream<Item = Result<T, SpiceDBError>>`.
/// Cursor-based pagination is handled transparently within the client.
///
/// Transient gRPC errors (UNAVAILABLE, DEADLINE_EXCEEDED, RESOURCE_EXHAUSTED)
/// are automatically retried with exponential backoff.
pub struct SpiceDBClient {
    // TODO: When spicedb-proto is available, store gRPC service clients:
    // permissions_client: proto::permissions_service_client::PermissionsServiceClient<Channel>,
    // schema_client: proto::schema_service_client::SchemaServiceClient<Channel>,
    // watch_client: proto::watch_service_client::WatchServiceClient<Channel>,
    // experimental_client: proto::experimental_service_client::ExperimentalServiceClient<Channel>,
    _endpoint: String,
    _token: String,
}

/// Builder for configuring a [`SpiceDBClient`] with custom TLS and connection
/// options.
pub struct SpiceDBClientBuilder {
    endpoint: String,
    token: String,
    plaintext: bool,
    // TODO: When tonic is wired up:
    // tls_config: Option<ClientTlsConfig>,
}

impl SpiceDBClientBuilder {
    /// Disables TLS for the connection. Use only for testing.
    pub fn plaintext(mut self) -> Self {
        self.plaintext = true;
        self
    }

    // TODO: When tonic is wired up:
    // /// Sets a custom TLS configuration.
    // pub fn tls_config(mut self, config: ClientTlsConfig) -> Self {
    //     self.tls_config = Some(config);
    //     self
    // }

    /// Builds the client, establishing the gRPC connection.
    pub async fn build(self) -> Result<SpiceDBClient, SpiceDBError> {
        // TODO: When spicedb-proto is available, establish the actual gRPC connection.
        // For now, return a placeholder client.
        Ok(SpiceDBClient {
            _endpoint: self.endpoint,
            _token: self.token,
        })
    }
}

impl SpiceDBClient {
    /// Creates a client with an insecure (plaintext) connection.
    ///
    /// Use this for testing only — the lack of TLS is made obvious by the name.
    pub async fn new_plaintext(
        endpoint: impl Into<String>,
        token: impl Into<String>,
    ) -> Result<Self, SpiceDBError> {
        Self::builder(endpoint, token).plaintext().build().await
    }

    /// Creates a client using the system's TLS certificate pool.
    ///
    /// Use this for production connections.
    pub async fn new_system_tls(
        endpoint: impl Into<String>,
        token: impl Into<String>,
    ) -> Result<Self, SpiceDBError> {
        Self::builder(endpoint, token).build().await
    }

    /// Returns a builder for configuring the client with custom options.
    pub fn builder(
        endpoint: impl Into<String>,
        token: impl Into<String>,
    ) -> SpiceDBClientBuilder {
        SpiceDBClientBuilder {
            endpoint: endpoint.into(),
            token: token.into(),
            plaintext: false,
        }
    }

    // -----------------------------------------------------------------------
    // Checks — all via BulkCheckPermissions
    // -----------------------------------------------------------------------

    /// Checks a single permission and returns a [`CheckResult`].
    ///
    /// Uses `BulkCheckPermissions` under the hood.
    ///
    /// # Errors
    ///
    /// Returns [`SpiceDBError`] if the gRPC call fails.
    #[must_use = "check results should not be silently discarded"]
    pub async fn check_permission(
        &self,
        _consistency: &Strategy,
        _permission: &str,
        _relationship: &Relationship,
    ) -> Result<CheckResult, SpiceDBError> {
        // TODO: Implement when spicedb-proto is available.
        // Internally calls check_permissions with a single item.
        todo!("requires spicedb-proto types")
    }

    /// Checks permissions on multiple relationships and returns a `Vec<bool>`
    /// indicating whether each is granted.
    ///
    /// Uses `BulkCheckPermissions` under the hood. Large batches are
    /// automatically split into chunks of 1,000.
    pub async fn check_permissions(
        &self,
        _consistency: &Strategy,
        _permission: &str,
        _relationships: &[Relationship],
    ) -> Result<Vec<bool>, SpiceDBError> {
        // TODO: Implement when spicedb-proto is available.
        todo!("requires spicedb-proto types")
    }

    /// Returns `true` if **any** of the given relationships have the permission.
    pub async fn check_any(
        &self,
        _consistency: &Strategy,
        _permission: &str,
        _relationships: &[Relationship],
    ) -> Result<bool, SpiceDBError> {
        // TODO: Implement when spicedb-proto is available.
        // Calls check_permissions, returns true if any element is true.
        todo!("requires spicedb-proto types")
    }

    /// Returns `true` if **all** of the given relationships have the permission.
    pub async fn check_all(
        &self,
        _consistency: &Strategy,
        _permission: &str,
        _relationships: &[Relationship],
    ) -> Result<bool, SpiceDBError> {
        // TODO: Implement when spicedb-proto is available.
        // Calls check_permissions, returns true only if all elements are true.
        todo!("requires spicedb-proto types")
    }

    // -----------------------------------------------------------------------
    // Relationships
    // -----------------------------------------------------------------------

    /// Commits a transaction of relationship mutations to SpiceDB.
    ///
    /// Returns the revision (ZedToken) at which the write occurred.
    pub async fn write(&self, _txn: &Transaction) -> Result<String, SpiceDBError> {
        // TODO: Implement when spicedb-proto is available.
        todo!("requires spicedb-proto types")
    }

    /// Returns a stream of relationships matching the given filter.
    ///
    /// Cursors are handled transparently — the client automatically re-fetches
    /// pages of 512 relationships using the `AfterResultCursor`.
    ///
    /// # Streaming
    ///
    /// Returns `impl Stream<Item = Result<Relationship, SpiceDBError>>`.
    pub async fn read_relationships(
        &self,
        _consistency: &Strategy,
        _filter: &Filter,
    ) -> Result<
        // TODO: Return impl Stream when spicedb-proto is available.
        // impl Stream<Item = Result<Relationship, SpiceDBError>>,
        Vec<Relationship>,
        SpiceDBError,
    > {
        let _ = DEFAULT_READ_PAGE_SIZE;
        // TODO: Implement with transparent cursor pagination (512-item pages).
        todo!("requires spicedb-proto types")
    }

    /// Deletes all relationships matching the given filter.
    ///
    /// Large result sets are automatically paged in batches of 10,000. Repeats
    /// until the server reports all matching relationships are deleted.
    ///
    /// Returns the revision of the final deletion.
    pub async fn delete_relationships(
        &self,
        _filter: &Filter,
    ) -> Result<String, SpiceDBError> {
        let _ = DEFAULT_DELETE_PAGE_SIZE;
        // TODO: Implement with auto-paging (10,000-item batches).
        todo!("requires spicedb-proto types")
    }

    // -----------------------------------------------------------------------
    // Lookups
    // -----------------------------------------------------------------------

    /// Returns a stream of resource IDs that the subject has the given
    /// permission on.
    ///
    /// Cursors are handled transparently — the client automatically re-fetches
    /// pages of 512 results.
    pub async fn lookup_resources(
        &self,
        _consistency: &Strategy,
        _resource_type: &str,
        _permission: &str,
        _subject_type: &str,
        _subject_id: &str,
    ) -> Result<
        // TODO: Return impl Stream when spicedb-proto is available.
        Vec<String>,
        SpiceDBError,
    > {
        let _ = DEFAULT_LOOKUP_PAGE_SIZE;
        // TODO: Implement with transparent cursor pagination (512-item pages).
        todo!("requires spicedb-proto types")
    }

    /// Returns a stream of subject IDs that have the given permission on the
    /// resource.
    ///
    /// Unlike `lookup_resources`, `lookup_subjects` does not currently support
    /// cursor-based pagination in SpiceDB — all results stream in a single
    /// server-streaming call.
    pub async fn lookup_subjects(
        &self,
        _consistency: &Strategy,
        _resource_type: &str,
        _resource_id: &str,
        _permission: &str,
        _subject_type: &str,
    ) -> Result<
        // TODO: Return impl Stream when spicedb-proto is available.
        Vec<String>,
        SpiceDBError,
    > {
        // TODO: Implement as single server-streaming call (no cursor support yet).
        todo!("requires spicedb-proto types")
    }

    // -----------------------------------------------------------------------
    // Schema
    // -----------------------------------------------------------------------

    /// Returns the current SpiceDB schema and the revision at which it was read.
    pub async fn read_schema(&self) -> Result<(String, String), SpiceDBError> {
        // TODO: Implement when spicedb-proto is available.
        todo!("requires spicedb-proto types")
    }

    /// Writes a new schema to SpiceDB.
    ///
    /// Returns the revision at which the schema was written.
    pub async fn write_schema(&self, _schema: &str) -> Result<String, SpiceDBError> {
        // TODO: Implement when spicedb-proto is available.
        todo!("requires spicedb-proto types")
    }

    /// Returns the definitions and caveats in the current schema via reflection.
    pub async fn reflect_schema(
        &self,
        _consistency: &Strategy,
    ) -> Result<ReflectSchemaResult, SpiceDBError> {
        // TODO: Implement when spicedb-proto is available.
        todo!("requires spicedb-proto types")
    }

    /// Returns the permissions that are computable for the given relation on a
    /// definition.
    pub async fn computable_permissions(
        &self,
        _consistency: &Strategy,
        _definition_name: &str,
        _relation_name: &str,
    ) -> Result<(Vec<RelationReference>, String), SpiceDBError> {
        // TODO: Implement when spicedb-proto is available.
        todo!("requires spicedb-proto types")
    }

    /// Returns the relations that the given permission depends on.
    pub async fn dependent_relations(
        &self,
        _consistency: &Strategy,
        _definition_name: &str,
        _permission_name: &str,
    ) -> Result<(Vec<RelationReference>, String), SpiceDBError> {
        // TODO: Implement when spicedb-proto is available.
        todo!("requires spicedb-proto types")
    }

    /// Compares the current schema against the given comparison schema, returning
    /// the differences.
    pub async fn diff_schema(
        &self,
        _consistency: &Strategy,
        _comparison_schema: &str,
    ) -> Result<(Vec<SchemaDiff>, String), SpiceDBError> {
        // TODO: Implement when spicedb-proto is available.
        todo!("requires spicedb-proto types")
    }

    // -----------------------------------------------------------------------
    // Expand
    // -----------------------------------------------------------------------

    /// Expands the permission tree for the given resource and permission,
    /// returning the full tree of subjects with access.
    pub async fn expand_permission_tree(
        &self,
        _consistency: &Strategy,
        _resource_type: &str,
        _resource_id: &str,
        _permission: &str,
    ) -> Result<ExpandResult, SpiceDBError> {
        // TODO: Implement when spicedb-proto is available.
        todo!("requires spicedb-proto types")
    }

    // -----------------------------------------------------------------------
    // Bulk
    // -----------------------------------------------------------------------

    /// Streams relationships to SpiceDB for bulk import.
    ///
    /// Accepts `impl Stream<Item = Relationship>` and automatically batches
    /// into chunks of 1,000.
    ///
    /// Returns the number of relationships loaded.
    pub async fn import_relationships(
        &self,
        _relationships: Vec<Relationship>,
        // TODO: When spicedb-proto is available, accept impl Stream:
        // _relationships: impl Stream<Item = Relationship>,
    ) -> Result<u64, SpiceDBError> {
        let _ = DEFAULT_IMPORT_BATCH_SIZE;
        // TODO: Implement with 1,000-item batching.
        todo!("requires spicedb-proto types")
    }

    /// Returns a stream of all relationships matching the optional filter,
    /// streamed from SpiceDB in bulk.
    ///
    /// Cursors are handled transparently — the client automatically re-fetches
    /// pages of 512 relationships.
    pub async fn export_relationships(
        &self,
        _consistency: &Strategy,
        _filter: Option<&Filter>,
    ) -> Result<
        // TODO: Return impl Stream when spicedb-proto is available.
        Vec<Relationship>,
        SpiceDBError,
    > {
        let _ = DEFAULT_EXPORT_PAGE_SIZE;
        // TODO: Implement with transparent cursor pagination (512-item pages).
        todo!("requires spicedb-proto types")
    }

    // -----------------------------------------------------------------------
    // Watch
    // -----------------------------------------------------------------------

    /// Returns a stream of relationship changes from SpiceDB's watch API,
    /// starting from the given revision.
    ///
    /// Each yielded [`Update`] contains the operation (create/touch/delete)
    /// and the affected relationship.
    pub async fn updates(
        &self,
        _object_types: &[String],
        _start_revision: Option<&str>,
    ) -> Result<
        // TODO: Return impl Stream when spicedb-proto is available.
        Vec<Update>,
        SpiceDBError,
    > {
        // TODO: Implement as server-streaming call.
        todo!("requires spicedb-proto types")
    }

    // -----------------------------------------------------------------------
    // Experimental
    // -----------------------------------------------------------------------

    /// Registers a named counter that tracks relationships matching the given
    /// filter. The counter is computed asynchronously by SpiceDB.
    ///
    /// # Experimental
    ///
    /// This API is experimental and may change without following the backwards
    /// compatibility mandate.
    pub async fn experimental_register_relationship_counter(
        &self,
        _name: &str,
        _filter: &Filter,
    ) -> Result<(), SpiceDBError> {
        // TODO: Implement when spicedb-proto is available.
        todo!("requires spicedb-proto types")
    }

    /// Reads the value of a previously registered relationship counter.
    ///
    /// Returns `Ok(Some(result))` if the count is ready, or `Ok(None)` if the
    /// counter is still being calculated.
    ///
    /// # Experimental
    ///
    /// This API is experimental and may change without following the backwards
    /// compatibility mandate.
    pub async fn experimental_count_relationships(
        &self,
        _name: &str,
    ) -> Result<Option<CountResult>, SpiceDBError> {
        // TODO: Implement when spicedb-proto is available.
        todo!("requires spicedb-proto types")
    }

    /// Removes a previously registered relationship counter.
    ///
    /// # Experimental
    ///
    /// This API is experimental and may change without following the backwards
    /// compatibility mandate.
    pub async fn experimental_unregister_relationship_counter(
        &self,
        _name: &str,
    ) -> Result<(), SpiceDBError> {
        // TODO: Implement when spicedb-proto is available.
        todo!("requires spicedb-proto types")
    }
}

// TODO: When spicedb-proto is available, add:
// - Actual gRPC connection establishment in constructors
// - Proto type conversions in each method
// - Transparent cursor pagination for streaming methods
// - Exponential backoff retry for transient errors
// - S2 compression by default

#[cfg(test)]
mod tests {
    use super::*;

    #[tokio::test]
    async fn test_new_plaintext_creates_client() {
        let client = SpiceDBClient::new_plaintext("localhost:50051", "testtoken").await;
        assert!(client.is_ok());
    }

    #[tokio::test]
    async fn test_new_system_tls_creates_client() {
        let client = SpiceDBClient::new_system_tls("grpc.example.com:443", "my-token").await;
        assert!(client.is_ok());
    }

    #[tokio::test]
    async fn test_builder_creates_client() {
        let client = SpiceDBClient::builder("endpoint", "token")
            .plaintext()
            .build()
            .await;
        assert!(client.is_ok());
    }

    #[test]
    fn test_page_size_constants() {
        assert_eq!(DEFAULT_READ_PAGE_SIZE, 512);
        assert_eq!(DEFAULT_LOOKUP_PAGE_SIZE, 512);
        assert_eq!(DEFAULT_EXPORT_PAGE_SIZE, 512);
        assert_eq!(DEFAULT_DELETE_PAGE_SIZE, 10_000);
        assert_eq!(DEFAULT_CHECK_BATCH_SIZE, 1_000);
        assert_eq!(DEFAULT_IMPORT_BATCH_SIZE, 1_000);
    }
}
