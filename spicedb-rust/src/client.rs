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
use crate::error::{self, SpiceDBError};
use crate::types::{
    CheckResult, CountResult, ExpandResult, Filter, PreconditionOperation, ReflectSchemaResult,
    RelationReference, Relationship, SchemaCaveat, SchemaCaveatParameter, SchemaDefinition,
    SchemaDiff, SchemaPermission, SchemaRelation, Transaction, Update, UpdateOperation,
};

// TODO: When spicedb-proto types are generated, uncomment these imports and
// remove the `#[cfg(feature = "proto")]` guards from method bodies.
//
// use spicedb_proto::authzed::api::v1 as proto;
// use spicedb_proto::authzed::api::v1::{
//     permissions_service_client::PermissionsServiceClient,
//     schema_service_client::SchemaServiceClient,
//     watch_service_client::WatchServiceClient,
//     experimental_service_client::ExperimentalServiceClient,
// };
// use tonic::transport::{Channel, ClientTlsConfig};

use spicedb_proto::SpiceDBProtoClient;

/// Default page sizes for transparent cursor-based pagination.
const DEFAULT_READ_PAGE_SIZE: u32 = 512;
const DEFAULT_LOOKUP_PAGE_SIZE: u32 = 512;
const DEFAULT_EXPORT_PAGE_SIZE: u32 = 512;
const DEFAULT_DELETE_PAGE_SIZE: u32 = 10_000;
const DEFAULT_CHECK_BATCH_SIZE: usize = 1_000;
const DEFAULT_IMPORT_BATCH_SIZE: usize = 1_000;

/// Maximum number of retry attempts for transient gRPC errors.
const MAX_RETRIES: u32 = 5;

/// Base delay for exponential backoff (in milliseconds).
const BASE_RETRY_DELAY_MS: u64 = 100;

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
    // The proto client holds the gRPC channel and bearer-token interceptor.
    // Once proto types are generated, the individual service clients
    // (permissions, schema, watch, experimental) will be extracted from it.
    _proto: SpiceDBProtoClient,
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
        let proto = SpiceDBProtoClient::new(&self.endpoint, &self.token, self.plaintext)
            .await
            .map_err(|e| SpiceDBError::Transport(e.to_string()))?;

        Ok(SpiceDBClient {
            _proto: proto,
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
        consistency: &Strategy,
        permission: &str,
        relationship: &Relationship,
    ) -> Result<CheckResult, SpiceDBError> {
        let results = self
            .check_permissions(consistency, permission, &[relationship.clone()])
            .await?;
        Ok(CheckResult {
            has_permission: results[0],
        })
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
        if _relationships.is_empty() {
            return Ok(Vec::new());
        }

        // TODO: uncomment when proto types are generated
        //
        // Process in batches of DEFAULT_CHECK_BATCH_SIZE
        // let mut all_results = Vec::with_capacity(_relationships.len());
        //
        // for chunk in _relationships.chunks(DEFAULT_CHECK_BATCH_SIZE) {
        //     let items: Vec<proto::CheckBulkPermissionsRequestItem> = chunk
        //         .iter()
        //         .map(|r| proto::CheckBulkPermissionsRequestItem {
        //             resource: Some(proto::ObjectReference {
        //                 object_type: r.resource_type.clone(),
        //                 object_id: r.resource_id.clone(),
        //             }),
        //             permission: _permission.to_string(),
        //             subject: Some(proto::SubjectReference {
        //                 object: Some(proto::ObjectReference {
        //                     object_type: r.subject_type.clone(),
        //                     object_id: r.subject_id.clone(),
        //                 }),
        //                 optional_relation: r.subject_relation.clone(),
        //             }),
        //             context: None,
        //         })
        //         .collect();
        //
        //     let resp = self.retry(|| async {
        //         self._proto.permissions.clone()
        //             .check_bulk_permissions(proto::CheckBulkPermissionsRequest {
        //                 consistency: Some(_consistency.to_proto()),
        //                 items: items.clone(),
        //             })
        //             .await
        //     }).await?;
        //
        //     let inner = resp.into_inner();
        //     for (i, pair) in inner.pairs.iter().enumerate() {
        //         if let Some(proto::check_bulk_permissions_pair::Response::Error(err_resp)) =
        //             &pair.response
        //         {
        //             return Err(SpiceDBError::InvalidArgument(
        //                 format!("check item {}: {}", i, err_resp.message),
        //             ));
        //         }
        //         if let Some(proto::check_bulk_permissions_pair::Response::Item(item)) =
        //             &pair.response
        //         {
        //             all_results.push(
        //                 item.permissionship
        //                     == proto::CheckPermissionResponse::PERMISSIONSHIP_HAS_PERMISSION as i32,
        //             );
        //         }
        //     }
        // }
        //
        // Ok(all_results)

        let _ = DEFAULT_CHECK_BATCH_SIZE;
        let _ = &self._proto;
        todo!("requires proto types to be generated")
    }

    /// Returns `true` if **any** of the given relationships have the permission.
    pub async fn check_any(
        &self,
        consistency: &Strategy,
        permission: &str,
        relationships: &[Relationship],
    ) -> Result<bool, SpiceDBError> {
        let results = self
            .check_permissions(consistency, permission, relationships)
            .await?;
        Ok(results.iter().any(|&r| r))
    }

    /// Returns `true` if **all** of the given relationships have the permission.
    pub async fn check_all(
        &self,
        consistency: &Strategy,
        permission: &str,
        relationships: &[Relationship],
    ) -> Result<bool, SpiceDBError> {
        let results = self
            .check_permissions(consistency, permission, relationships)
            .await?;
        Ok(results.iter().all(|&r| r))
    }

    // -----------------------------------------------------------------------
    // Relationships
    // -----------------------------------------------------------------------

    /// Commits a transaction of relationship mutations to SpiceDB.
    ///
    /// Returns the revision (ZedToken) at which the write occurred.
    pub async fn write(&self, _txn: &Transaction) -> Result<String, SpiceDBError> {
        // TODO: uncomment when proto types are generated
        //
        // let updates: Vec<proto::RelationshipUpdate> = _txn
        //     .updates()
        //     .iter()
        //     .map(|(op, rel)| {
        //         let operation = match op {
        //             UpdateOperation::Create => {
        //                 proto::relationship_update::Operation::Create as i32
        //             }
        //             UpdateOperation::Touch => {
        //                 proto::relationship_update::Operation::Touch as i32
        //             }
        //             UpdateOperation::Delete => {
        //                 proto::relationship_update::Operation::Delete as i32
        //             }
        //         };
        //         proto::RelationshipUpdate {
        //             operation,
        //             relationship: Some(rel.to_proto()),
        //         }
        //     })
        //     .collect();
        //
        // let mut preconditions = Vec::new();
        // for pc in _txn.preconditions() {
        //     let operation = match pc.operation {
        //         PreconditionOperation::MustNotMatch => {
        //             proto::precondition::Operation::MustNotMatch as i32
        //         }
        //         PreconditionOperation::MustMatch => {
        //             proto::precondition::Operation::MustMatch as i32
        //         }
        //     };
        //     preconditions.push(proto::Precondition {
        //         operation,
        //         filter: Some(pc.filter.to_proto()),
        //     });
        // }
        //
        // let resp = self.retry(|| async {
        //     self._proto.permissions.clone()
        //         .write_relationships(proto::WriteRelationshipsRequest {
        //             updates: updates.clone(),
        //             optional_preconditions: preconditions.clone(),
        //         })
        //         .await
        // }).await?;
        //
        // Ok(resp
        //     .into_inner()
        //     .written_at
        //     .map(|z| z.token)
        //     .unwrap_or_default())

        let _ = &self._proto;
        let _ = _txn;
        todo!("requires proto types to be generated")
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
        // TODO: Return impl Stream when proto types are generated.
        // impl futures::Stream<Item = Result<Relationship, SpiceDBError>>,
        Vec<Relationship>,
        SpiceDBError,
    > {
        // TODO: uncomment when proto types are generated
        //
        // Transparent cursor pagination with DEFAULT_READ_PAGE_SIZE pages:
        //
        // let mut results = Vec::new();
        // let mut cursor: Option<proto::Cursor> = None;
        //
        // loop {
        //     let resp_stream = self.retry(|| async {
        //         self._proto.permissions.clone()
        //             .read_relationships(proto::ReadRelationshipsRequest {
        //                 consistency: Some(_consistency.to_proto()),
        //                 relationship_filter: Some(_filter.to_proto()),
        //                 optional_limit: DEFAULT_READ_PAGE_SIZE,
        //                 optional_cursor: cursor.clone(),
        //             })
        //             .await
        //     }).await?;
        //
        //     let mut stream = resp_stream.into_inner();
        //     let mut count: u32 = 0;
        //
        //     while let Some(resp) = stream.message().await.map_err(|s| {
        //         error::from_grpc_status(s.code() as i32, s.message().to_string())
        //     })? {
        //         count += 1;
        //         if let Some(c) = resp.after_result_cursor {
        //             cursor = Some(c);
        //         }
        //         if let Some(rel) = resp.relationship {
        //             results.push(Relationship::from_proto(&rel));
        //         }
        //     }
        //
        //     // If we got fewer than the page size, we've read everything.
        //     if count < DEFAULT_READ_PAGE_SIZE {
        //         break;
        //     }
        // }
        //
        // Ok(results)

        let _ = DEFAULT_READ_PAGE_SIZE;
        let _ = &self._proto;
        todo!("requires proto types to be generated")
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
        // TODO: uncomment when proto types are generated
        //
        // Auto-paging with DEFAULT_DELETE_PAGE_SIZE batches:
        //
        // loop {
        //     let resp = self.retry(|| async {
        //         self._proto.permissions.clone()
        //             .delete_relationships(proto::DeleteRelationshipsRequest {
        //                 relationship_filter: Some(_filter.to_proto()),
        //                 optional_limit: DEFAULT_DELETE_PAGE_SIZE,
        //                 optional_allow_partial_deletions: true,
        //                 ..Default::default()
        //             })
        //             .await
        //     }).await?;
        //
        //     let inner = resp.into_inner();
        //     let revision = inner
        //         .deleted_at
        //         .map(|z| z.token)
        //         .unwrap_or_default();
        //
        //     // DELETION_PROGRESS_COMPLETE = 1
        //     if inner.deletion_progress == 1 {
        //         return Ok(revision);
        //     }
        // }

        let _ = DEFAULT_DELETE_PAGE_SIZE;
        let _ = &self._proto;
        todo!("requires proto types to be generated")
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
        // TODO: Return impl Stream when proto types are generated.
        Vec<String>,
        SpiceDBError,
    > {
        // TODO: uncomment when proto types are generated
        //
        // Transparent cursor pagination with DEFAULT_LOOKUP_PAGE_SIZE pages:
        //
        // let mut results = Vec::new();
        // let mut cursor: Option<proto::Cursor> = None;
        //
        // loop {
        //     let resp_stream = self.retry(|| async {
        //         self._proto.permissions.clone()
        //             .lookup_resources(proto::LookupResourcesRequest {
        //                 consistency: Some(_consistency.to_proto()),
        //                 resource_object_type: _resource_type.to_string(),
        //                 permission: _permission.to_string(),
        //                 subject: Some(proto::SubjectReference {
        //                     object: Some(proto::ObjectReference {
        //                         object_type: _subject_type.to_string(),
        //                         object_id: _subject_id.to_string(),
        //                     }),
        //                     optional_relation: String::new(),
        //                 }),
        //                 optional_limit: DEFAULT_LOOKUP_PAGE_SIZE,
        //                 optional_cursor: cursor.clone(),
        //                 ..Default::default()
        //             })
        //             .await
        //     }).await?;
        //
        //     let mut stream = resp_stream.into_inner();
        //     let mut count: u32 = 0;
        //
        //     while let Some(resp) = stream.message().await.map_err(|s| {
        //         error::from_grpc_status(s.code() as i32, s.message().to_string())
        //     })? {
        //         count += 1;
        //         if let Some(c) = resp.after_result_cursor {
        //             cursor = Some(c);
        //         }
        //         results.push(resp.resource_object_id);
        //     }
        //
        //     if count < DEFAULT_LOOKUP_PAGE_SIZE {
        //         break;
        //     }
        // }
        //
        // Ok(results)

        let _ = DEFAULT_LOOKUP_PAGE_SIZE;
        let _ = &self._proto;
        todo!("requires proto types to be generated")
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
        // TODO: Return impl Stream when proto types are generated.
        Vec<String>,
        SpiceDBError,
    > {
        // TODO: uncomment when proto types are generated
        //
        // Single server-streaming call (no cursor support):
        //
        // let resp_stream = self.retry(|| async {
        //     self._proto.permissions.clone()
        //         .lookup_subjects(proto::LookupSubjectsRequest {
        //             consistency: Some(_consistency.to_proto()),
        //             resource: Some(proto::ObjectReference {
        //                 object_type: _resource_type.to_string(),
        //                 object_id: _resource_id.to_string(),
        //             }),
        //             permission: _permission.to_string(),
        //             subject_object_type: _subject_type.to_string(),
        //             ..Default::default()
        //         })
        //         .await
        // }).await?;
        //
        // let mut stream = resp_stream.into_inner();
        // let mut results = Vec::new();
        //
        // while let Some(resp) = stream.message().await.map_err(|s| {
        //     error::from_grpc_status(s.code() as i32, s.message().to_string())
        // })? {
        //     // Prefer the nested subject field; fall back to deprecated top-level field.
        //     let subject_id = resp
        //         .subject
        //         .as_ref()
        //         .map(|s| s.subject_object_id.clone())
        //         .unwrap_or(resp.subject_object_id);
        //     results.push(subject_id);
        // }
        //
        // Ok(results)

        let _ = &self._proto;
        todo!("requires proto types to be generated")
    }

    // -----------------------------------------------------------------------
    // Schema
    // -----------------------------------------------------------------------

    /// Returns the current SpiceDB schema and the revision at which it was read.
    pub async fn read_schema(&self) -> Result<(String, String), SpiceDBError> {
        // TODO: uncomment when proto types are generated
        //
        // let resp = self.retry(|| async {
        //     self._proto.schema.clone()
        //         .read_schema(proto::ReadSchemaRequest {})
        //         .await
        // }).await?;
        //
        // let inner = resp.into_inner();
        // let revision = inner.read_at.map(|z| z.token).unwrap_or_default();
        // Ok((inner.schema_text, revision))

        let _ = &self._proto;
        todo!("requires proto types to be generated")
    }

    /// Writes a new schema to SpiceDB.
    ///
    /// Returns the revision at which the schema was written.
    pub async fn write_schema(&self, _schema: &str) -> Result<String, SpiceDBError> {
        // TODO: uncomment when proto types are generated
        //
        // let resp = self.retry(|| async {
        //     self._proto.schema.clone()
        //         .write_schema(proto::WriteSchemaRequest {
        //             schema: _schema.to_string(),
        //         })
        //         .await
        // }).await?;
        //
        // Ok(resp
        //     .into_inner()
        //     .written_at
        //     .map(|z| z.token)
        //     .unwrap_or_default())

        let _ = &self._proto;
        todo!("requires proto types to be generated")
    }

    /// Returns the definitions and caveats in the current schema via reflection.
    pub async fn reflect_schema(
        &self,
        _consistency: &Strategy,
    ) -> Result<ReflectSchemaResult, SpiceDBError> {
        // TODO: uncomment when proto types are generated
        //
        // let resp = self.retry(|| async {
        //     self._proto.schema.clone()
        //         .reflect_schema(proto::ReflectSchemaRequest {
        //             consistency: Some(_consistency.to_proto()),
        //             ..Default::default()
        //         })
        //         .await
        // }).await?;
        //
        // let inner = resp.into_inner();
        // let revision = inner.read_at.map(|z| z.token).unwrap_or_default();
        //
        // let definitions = inner
        //     .definitions
        //     .iter()
        //     .map(|def| SchemaDefinition {
        //         name: def.name.clone(),
        //         comment: def.comment.clone(),
        //         relations: def
        //             .relations
        //             .iter()
        //             .map(|r| SchemaRelation {
        //                 name: r.name.clone(),
        //                 comment: r.comment.clone(),
        //                 parent_definition_name: r.parent_definition_name.clone(),
        //             })
        //             .collect(),
        //         permissions: def
        //             .permissions
        //             .iter()
        //             .map(|p| SchemaPermission {
        //                 name: p.name.clone(),
        //                 comment: p.comment.clone(),
        //                 parent_definition_name: p.parent_definition_name.clone(),
        //             })
        //             .collect(),
        //     })
        //     .collect();
        //
        // let caveats = inner
        //     .caveats
        //     .iter()
        //     .map(|cav| SchemaCaveat {
        //         name: cav.name.clone(),
        //         comment: cav.comment.clone(),
        //         expression: cav.expression.clone(),
        //         parameters: cav
        //             .parameters
        //             .iter()
        //             .map(|p| SchemaCaveatParameter {
        //                 name: p.name.clone(),
        //                 type_name: p.r#type.clone(),
        //                 parent_caveat_name: p.parent_caveat_name.clone(),
        //             })
        //             .collect(),
        //     })
        //     .collect();
        //
        // Ok(ReflectSchemaResult {
        //     definitions,
        //     caveats,
        //     revision,
        // })

        let _ = &self._proto;
        todo!("requires proto types to be generated")
    }

    /// Returns the permissions that are computable for the given relation on a
    /// definition.
    pub async fn computable_permissions(
        &self,
        _consistency: &Strategy,
        _definition_name: &str,
        _relation_name: &str,
    ) -> Result<(Vec<RelationReference>, String), SpiceDBError> {
        // TODO: uncomment when proto types are generated
        //
        // let resp = self.retry(|| async {
        //     self._proto.schema.clone()
        //         .computable_permissions(proto::ComputablePermissionsRequest {
        //             consistency: Some(_consistency.to_proto()),
        //             definition_name: _definition_name.to_string(),
        //             relation_name: _relation_name.to_string(),
        //         })
        //         .await
        // }).await?;
        //
        // let inner = resp.into_inner();
        // let revision = inner.read_at.map(|z| z.token).unwrap_or_default();
        //
        // let refs = inner
        //     .permissions
        //     .iter()
        //     .map(|perm| RelationReference {
        //         definition_name: perm.definition_name.clone(),
        //         relation_name: perm.relation_name.clone(),
        //         is_permission: perm.is_permission,
        //     })
        //     .collect();
        //
        // Ok((refs, revision))

        let _ = &self._proto;
        todo!("requires proto types to be generated")
    }

    /// Returns the relations that the given permission depends on.
    pub async fn dependent_relations(
        &self,
        _consistency: &Strategy,
        _definition_name: &str,
        _permission_name: &str,
    ) -> Result<(Vec<RelationReference>, String), SpiceDBError> {
        // TODO: uncomment when proto types are generated
        //
        // let resp = self.retry(|| async {
        //     self._proto.schema.clone()
        //         .dependent_relations(proto::DependentRelationsRequest {
        //             consistency: Some(_consistency.to_proto()),
        //             definition_name: _definition_name.to_string(),
        //             permission_name: _permission_name.to_string(),
        //         })
        //         .await
        // }).await?;
        //
        // let inner = resp.into_inner();
        // let revision = inner.read_at.map(|z| z.token).unwrap_or_default();
        //
        // let refs = inner
        //     .relations
        //     .iter()
        //     .map(|rel| RelationReference {
        //         definition_name: rel.definition_name.clone(),
        //         relation_name: rel.relation_name.clone(),
        //         is_permission: rel.is_permission,
        //     })
        //     .collect();
        //
        // Ok((refs, revision))

        let _ = &self._proto;
        todo!("requires proto types to be generated")
    }

    /// Compares the current schema against the given comparison schema, returning
    /// the differences.
    pub async fn diff_schema(
        &self,
        _consistency: &Strategy,
        _comparison_schema: &str,
    ) -> Result<(Vec<SchemaDiff>, String), SpiceDBError> {
        // TODO: uncomment when proto types are generated
        //
        // let resp = self.retry(|| async {
        //     self._proto.schema.clone()
        //         .diff_schema(proto::DiffSchemaRequest {
        //             consistency: Some(_consistency.to_proto()),
        //             comparison_schema: _comparison_schema.to_string(),
        //         })
        //         .await
        // }).await?;
        //
        // let inner = resp.into_inner();
        // let revision = inner.read_at.map(|z| z.token).unwrap_or_default();
        //
        // let diffs = inner
        //     .diffs
        //     .iter()
        //     .map(|d| schema_diff_from_proto(d))
        //     .collect();
        //
        // Ok((diffs, revision))

        let _ = &self._proto;
        todo!("requires proto types to be generated")
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
        // TODO: uncomment when proto types are generated
        //
        // let resp = self.retry(|| async {
        //     self._proto.permissions.clone()
        //         .expand_permission_tree(proto::ExpandPermissionTreeRequest {
        //             consistency: Some(_consistency.to_proto()),
        //             resource: Some(proto::ObjectReference {
        //                 object_type: _resource_type.to_string(),
        //                 object_id: _resource_id.to_string(),
        //             }),
        //             permission: _permission.to_string(),
        //         })
        //         .await
        // }).await?;
        //
        // let inner = resp.into_inner();
        // let revision = inner.expanded_at.map(|z| z.token).unwrap_or_default();
        //
        // Ok(ExpandResult {
        //     revision,
        //     // tree_root: inner.tree_root,
        // })

        let _ = &self._proto;
        todo!("requires proto types to be generated")
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
        // TODO: When proto types are generated, accept impl Stream:
        // _relationships: impl futures::Stream<Item = Relationship>,
    ) -> Result<u64, SpiceDBError> {
        // TODO: uncomment when proto types are generated
        //
        // Batch into DEFAULT_IMPORT_BATCH_SIZE chunks using client-streaming:
        //
        // let (tx, rx) = tokio::sync::mpsc::channel(4);
        //
        // // Spawn a task to batch and send relationships
        // let batch_task = tokio::spawn(async move {
        //     for chunk in _relationships.chunks(DEFAULT_IMPORT_BATCH_SIZE) {
        //         let proto_rels: Vec<proto::Relationship> = chunk
        //             .iter()
        //             .map(|r| r.to_proto())
        //             .collect();
        //         if tx
        //             .send(proto::ImportBulkRelationshipsRequest {
        //                 relationships: proto_rels,
        //             })
        //             .await
        //             .is_err()
        //         {
        //             break;
        //         }
        //     }
        // });
        //
        // let stream = tokio_stream::wrappers::ReceiverStream::new(rx);
        // let resp = self._proto.permissions.clone()
        //     .import_bulk_relationships(stream)
        //     .await
        //     .map_err(|s| error::from_grpc_status(s.code() as i32, s.message().to_string()))?;
        //
        // batch_task.await.ok();
        // Ok(resp.into_inner().num_loaded)

        let _ = DEFAULT_IMPORT_BATCH_SIZE;
        let _ = &self._proto;
        todo!("requires proto types to be generated")
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
        // TODO: Return impl Stream when proto types are generated.
        Vec<Relationship>,
        SpiceDBError,
    > {
        // TODO: uncomment when proto types are generated
        //
        // Transparent cursor pagination with DEFAULT_EXPORT_PAGE_SIZE pages:
        //
        // let mut results = Vec::new();
        // let mut cursor: Option<proto::Cursor> = None;
        //
        // loop {
        //     let mut req = proto::ExportBulkRelationshipsRequest {
        //         consistency: Some(_consistency.to_proto()),
        //         optional_limit: DEFAULT_EXPORT_PAGE_SIZE,
        //         optional_cursor: cursor.clone(),
        //         ..Default::default()
        //     };
        //     if let Some(f) = _filter {
        //         req.optional_relationship_filter = Some(f.to_proto());
        //     }
        //
        //     let resp_stream = self.retry(|| async {
        //         self._proto.permissions.clone()
        //             .export_bulk_relationships(req.clone())
        //             .await
        //     }).await?;
        //
        //     let mut stream = resp_stream.into_inner();
        //     let mut page_count: u32 = 0;
        //
        //     while let Some(resp) = stream.message().await.map_err(|s| {
        //         error::from_grpc_status(s.code() as i32, s.message().to_string())
        //     })? {
        //         if let Some(c) = resp.after_result_cursor {
        //             cursor = Some(c);
        //         }
        //         for rel in &resp.relationships {
        //             page_count += 1;
        //             results.push(Relationship::from_proto(rel));
        //         }
        //     }
        //
        //     if page_count < DEFAULT_EXPORT_PAGE_SIZE {
        //         break;
        //     }
        // }
        //
        // Ok(results)

        let _ = DEFAULT_EXPORT_PAGE_SIZE;
        let _ = &self._proto;
        todo!("requires proto types to be generated")
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
        // TODO: Return impl Stream when proto types are generated.
        Vec<Update>,
        SpiceDBError,
    > {
        // TODO: uncomment when proto types are generated
        //
        // Server-streaming call:
        //
        // let mut req = proto::WatchRequest {
        //     optional_object_types: _object_types.to_vec(),
        //     ..Default::default()
        // };
        // if let Some(rev) = _start_revision {
        //     req.optional_start_cursor = Some(proto::ZedToken {
        //         token: rev.to_string(),
        //     });
        // }
        //
        // let resp_stream = self._proto.watch.clone()
        //     .watch(req)
        //     .await
        //     .map_err(|s| error::from_grpc_status(s.code() as i32, s.message().to_string()))?;
        //
        // let mut stream = resp_stream.into_inner();
        // let mut results = Vec::new();
        //
        // while let Some(resp) = stream.message().await.map_err(|s| {
        //     error::from_grpc_status(s.code() as i32, s.message().to_string())
        // })? {
        //     for update in &resp.updates {
        //         let operation = match update.operation {
        //             1 => UpdateOperation::Create,
        //             2 => UpdateOperation::Touch,
        //             3 => UpdateOperation::Delete,
        //             _ => continue,
        //         };
        //         if let Some(rel) = &update.relationship {
        //             results.push(Update {
        //                 operation,
        //                 relationship: Relationship::from_proto(rel),
        //             });
        //         }
        //     }
        // }
        //
        // Ok(results)

        let _ = &self._proto;
        todo!("requires proto types to be generated")
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
        // TODO: uncomment when proto types are generated
        //
        // self.retry(|| async {
        //     self._proto.experimental.clone()
        //         .experimental_register_relationship_counter(
        //             proto::ExperimentalRegisterRelationshipCounterRequest {
        //                 name: _name.to_string(),
        //                 relationship_filter: Some(_filter.to_proto()),
        //             },
        //         )
        //         .await
        // }).await?;
        //
        // Ok(())

        let _ = &self._proto;
        todo!("requires proto types to be generated")
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
        // TODO: uncomment when proto types are generated
        //
        // let resp = self.retry(|| async {
        //     self._proto.experimental.clone()
        //         .experimental_count_relationships(
        //             proto::ExperimentalCountRelationshipsRequest {
        //                 name: _name.to_string(),
        //             },
        //         )
        //         .await
        // }).await?;
        //
        // let inner = resp.into_inner();
        //
        // if inner.counter_still_calculating {
        //     return Ok(None);
        // }
        //
        // if let Some(cv) = inner.read_counter_value {
        //     let revision = cv.read_at.map(|z| z.token).unwrap_or_default();
        //     Ok(Some(CountResult {
        //         relationship_count: cv.relationship_count,
        //         revision,
        //     }))
        // } else {
        //     Ok(None)
        // }

        let _ = &self._proto;
        todo!("requires proto types to be generated")
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
        // TODO: uncomment when proto types are generated
        //
        // self.retry(|| async {
        //     self._proto.experimental.clone()
        //         .experimental_unregister_relationship_counter(
        //             proto::ExperimentalUnregisterRelationshipCounterRequest {
        //                 name: _name.to_string(),
        //             },
        //         )
        //         .await
        // }).await?;
        //
        // Ok(())

        let _ = &self._proto;
        todo!("requires proto types to be generated")
    }

    // -----------------------------------------------------------------------
    // Retry with exponential backoff
    // -----------------------------------------------------------------------

    /// Retries a gRPC call with exponential backoff for transient errors.
    ///
    /// Retries on UNAVAILABLE, DEADLINE_EXCEEDED, and RESOURCE_EXHAUSTED up to
    /// MAX_RETRIES times with exponentially increasing delays starting at
    /// BASE_RETRY_DELAY_MS.
    #[allow(dead_code)]
    async fn retry<F, Fut, T>(&self, f: F) -> Result<tonic::Response<T>, SpiceDBError>
    where
        F: Fn() -> Fut,
        Fut: std::future::Future<Output = Result<tonic::Response<T>, tonic::Status>>,
    {
        let mut attempt = 0u32;
        loop {
            match f().await {
                Ok(resp) => return Ok(resp),
                Err(status) => {
                    let err =
                        error::from_grpc_status(status.code() as i32, status.message().to_string());
                    if !error::is_transient(&err) || attempt >= MAX_RETRIES {
                        return Err(err);
                    }
                    let delay_ms = BASE_RETRY_DELAY_MS * 2u64.pow(attempt);
                    tokio::time::sleep(tokio::time::Duration::from_millis(delay_ms)).await;
                    attempt += 1;
                }
            }
        }
    }
}

// TODO: uncomment when proto types are generated
//
// /// Converts a proto ReflectionSchemaDiff into an idiomatic SchemaDiff.
// fn schema_diff_from_proto(d: &proto::ReflectionSchemaDiff) -> SchemaDiff {
//     // The proto uses a oneof for the diff type. Match on the variant to
//     // extract the kind and associated fields.
//     if let Some(diff) = &d.diff {
//         use proto::reflection_schema_diff::Diff;
//         match diff {
//             Diff::DefinitionAdded(def) => SchemaDiff {
//                 kind: "definition_added".into(),
//                 definition_name: def.name.clone(),
//                 ..Default::default()
//             },
//             Diff::DefinitionRemoved(def) => SchemaDiff {
//                 kind: "definition_removed".into(),
//                 definition_name: def.name.clone(),
//                 ..Default::default()
//             },
//             Diff::DefinitionDocCommentChanged(def) => SchemaDiff {
//                 kind: "definition_doc_comment_changed".into(),
//                 definition_name: def.name.clone(),
//                 ..Default::default()
//             },
//             Diff::RelationAdded(rel) => SchemaDiff {
//                 kind: "relation_added".into(),
//                 definition_name: rel.parent_definition_name.clone(),
//                 relation_name: rel.name.clone(),
//                 ..Default::default()
//             },
//             Diff::RelationRemoved(rel) => SchemaDiff {
//                 kind: "relation_removed".into(),
//                 definition_name: rel.parent_definition_name.clone(),
//                 relation_name: rel.name.clone(),
//                 ..Default::default()
//             },
//             Diff::RelationDocCommentChanged(rel) => SchemaDiff {
//                 kind: "relation_doc_comment_changed".into(),
//                 definition_name: rel.parent_definition_name.clone(),
//                 relation_name: rel.name.clone(),
//                 ..Default::default()
//             },
//             Diff::RelationSubjectTypeAdded(change) => {
//                 let rel = change.relation.as_ref();
//                 SchemaDiff {
//                     kind: "relation_subject_type_added".into(),
//                     definition_name: rel.map(|r| r.parent_definition_name.clone()).unwrap_or_default(),
//                     relation_name: rel.map(|r| r.name.clone()).unwrap_or_default(),
//                     ..Default::default()
//                 }
//             },
//             Diff::RelationSubjectTypeRemoved(change) => {
//                 let rel = change.relation.as_ref();
//                 SchemaDiff {
//                     kind: "relation_subject_type_removed".into(),
//                     definition_name: rel.map(|r| r.parent_definition_name.clone()).unwrap_or_default(),
//                     relation_name: rel.map(|r| r.name.clone()).unwrap_or_default(),
//                     ..Default::default()
//                 }
//             },
//             Diff::PermissionAdded(perm) => SchemaDiff {
//                 kind: "permission_added".into(),
//                 definition_name: perm.parent_definition_name.clone(),
//                 permission_name: perm.name.clone(),
//                 ..Default::default()
//             },
//             Diff::PermissionRemoved(perm) => SchemaDiff {
//                 kind: "permission_removed".into(),
//                 definition_name: perm.parent_definition_name.clone(),
//                 permission_name: perm.name.clone(),
//                 ..Default::default()
//             },
//             Diff::PermissionDocCommentChanged(perm) => SchemaDiff {
//                 kind: "permission_doc_comment_changed".into(),
//                 definition_name: perm.parent_definition_name.clone(),
//                 permission_name: perm.name.clone(),
//                 ..Default::default()
//             },
//             Diff::PermissionExprChanged(perm) => SchemaDiff {
//                 kind: "permission_expr_changed".into(),
//                 definition_name: perm.parent_definition_name.clone(),
//                 permission_name: perm.name.clone(),
//                 ..Default::default()
//             },
//             Diff::CaveatAdded(cav) => SchemaDiff {
//                 kind: "caveat_added".into(),
//                 caveat_name: cav.name.clone(),
//                 ..Default::default()
//             },
//             Diff::CaveatRemoved(cav) => SchemaDiff {
//                 kind: "caveat_removed".into(),
//                 caveat_name: cav.name.clone(),
//                 ..Default::default()
//             },
//             Diff::CaveatDocCommentChanged(cav) => SchemaDiff {
//                 kind: "caveat_doc_comment_changed".into(),
//                 caveat_name: cav.name.clone(),
//                 ..Default::default()
//             },
//             Diff::CaveatExprChanged(cav) => SchemaDiff {
//                 kind: "caveat_expr_changed".into(),
//                 caveat_name: cav.name.clone(),
//                 ..Default::default()
//             },
//             Diff::CaveatParameterAdded(param) => SchemaDiff {
//                 kind: "caveat_parameter_added".into(),
//                 caveat_name: param.parent_caveat_name.clone(),
//                 ..Default::default()
//             },
//             Diff::CaveatParameterRemoved(param) => SchemaDiff {
//                 kind: "caveat_parameter_removed".into(),
//                 caveat_name: param.parent_caveat_name.clone(),
//                 ..Default::default()
//             },
//             Diff::CaveatParameterTypeChanged(change) => {
//                 let param = change.parameter.as_ref();
//                 SchemaDiff {
//                     kind: "caveat_parameter_type_changed".into(),
//                     caveat_name: param.map(|p| p.parent_caveat_name.clone()).unwrap_or_default(),
//                     ..Default::default()
//                 }
//             },
//         }
//     } else {
//         SchemaDiff {
//             kind: "unknown".into(),
//             ..Default::default()
//         }
//     }
// }

#[cfg(test)]
mod tests {
    use super::*;

    // Note: constructor tests are removed because SpiceDBProtoClient::new
    // now attempts a real gRPC connection, which will fail in unit tests
    // without a running SpiceDB server.

    #[test]
    fn test_page_size_constants() {
        assert_eq!(DEFAULT_READ_PAGE_SIZE, 512);
        assert_eq!(DEFAULT_LOOKUP_PAGE_SIZE, 512);
        assert_eq!(DEFAULT_EXPORT_PAGE_SIZE, 512);
        assert_eq!(DEFAULT_DELETE_PAGE_SIZE, 10_000);
        assert_eq!(DEFAULT_CHECK_BATCH_SIZE, 1_000);
        assert_eq!(DEFAULT_IMPORT_BATCH_SIZE, 1_000);
    }

    #[test]
    fn test_retry_constants() {
        assert_eq!(MAX_RETRIES, 5);
        assert_eq!(BASE_RETRY_DELAY_MS, 100);
    }
}
