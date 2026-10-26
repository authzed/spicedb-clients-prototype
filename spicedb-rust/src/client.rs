#[path = "operations.rs"]
mod operations;
use operations::remaining_timeout;

use std::time::Duration;

use crate::consistency::Strategy;
use crate::error::{self, SpiceDBError};
use crate::types::*;

use futures::Stream;

use spicedb_proto::authzed::api::materialize::v0 as materialize_proto;
use spicedb_proto::authzed::api::v1 as proto;
use spicedb_proto::SpiceDBProtoClient;

const DEFAULT_CHECK_BATCH_SIZE: usize = 1_000;
const DEFAULT_IMPORT_BATCH_SIZE: usize = 1_000;

const MAX_RETRIES: u32 = 3;

const BASE_RETRY_DELAY_MS: u64 = 100;

const DEFAULT_TIMEOUT: Duration = Duration::from_secs(30);

#[derive(Clone)]
#[non_exhaustive]
pub struct SpiceDBClient {
    // The proto client holds the gRPC channel and bearer-token interceptor.
    proto: SpiceDBProtoClient,
    // Applied to any unary call that doesn't specify its own timeout. See
    // [`DEFAULT_TIMEOUT`].
    default_timeout: Duration,
}

#[non_exhaustive]
pub struct SpiceDBClientBuilder {
    endpoint: String,
    token: String,
    plaintext: bool,
    remote_credentials_policy: RemoteCredentialsPolicy,
    default_timeout: Duration,
}

impl SpiceDBClientBuilder {
    pub fn plaintext(mut self) -> Self {
        self.plaintext = true;
        self
    }

    /// Explicitly allow credentials over remote plaintext only with `AllowInsecureRemote`.
    pub fn remote_credentials_policy(mut self, policy: RemoteCredentialsPolicy) -> Self {
        self.remote_credentials_policy = policy;
        self
    }

    pub fn default_timeout(mut self, timeout: Duration) -> Self {
        self.default_timeout = timeout;
        self
    }

    pub async fn build(self) -> Result<SpiceDBClient, SpiceDBError> {
        if self.default_timeout.is_zero() {
            return Err(invalid("default_timeout", "must be positive"));
        }
        let proto = SpiceDBProtoClient::new_with_options(
            &self.endpoint,
            &self.token,
            self.plaintext,
            self.remote_credentials_policy == RemoteCredentialsPolicy::AllowInsecureRemote,
        )
        .await
        .map_err(|e| match e {
            spicedb_proto::SpiceDBProtoClientError::InsecureRemoteHostNotAllowed(_) => invalid(
                "remote_credentials_policy",
                "remote plaintext credentials require RemoteCredentialsPolicy::AllowInsecureRemote",
            ),
            spicedb_proto::SpiceDBProtoClientError::UnixSocketNotSupported(msg)
            | spicedb_proto::SpiceDBProtoClientError::InvalidToken(msg) => {
                SpiceDBError::InvalidArgument(msg.into())
            }
            transport @ spicedb_proto::SpiceDBProtoClientError::Transport(_) => {
                // Keep the transport failure itself as the error's source, not
                // just its rendered text. `SpiceDBProtoClientError` implements
                // `source()` down to the `tonic::transport::Error`, so a caller
                // can walk from `SpiceDBError::Transport` to the underlying
                // connection or TLS failure -- the class of error where the
                // cause chain is most diagnostic, and previously the only one
                // in this hierarchy with no chain at all.
                SpiceDBError::Transport(error::ErrorPayload::with_cause(
                    transport.to_string(),
                    transport,
                ))
            }
        })?;

        Ok(SpiceDBClient {
            proto,
            default_timeout: self.default_timeout,
        })
    }
}

impl SpiceDBClient {
    /// Connect without TLS. Remote credentials require `AllowInsecureRemote`.
    pub async fn new_plaintext(
        endpoint: impl Into<String>,
        token: impl Into<String>,
        policy: RemoteCredentialsPolicy,
    ) -> Result<Self, SpiceDBError> {
        Self::builder(endpoint, token)
            .plaintext()
            .remote_credentials_policy(policy)
            .build()
            .await
    }

    pub async fn new_system_tls(
        endpoint: impl Into<String>,
        token: impl Into<String>,
    ) -> Result<Self, SpiceDBError> {
        Self::builder(endpoint, token).build().await
    }

    pub fn builder(endpoint: impl Into<String>, token: impl Into<String>) -> SpiceDBClientBuilder {
        SpiceDBClientBuilder {
            endpoint: endpoint.into(),
            token: token.into(),
            plaintext: false,
            remote_credentials_policy: RemoteCredentialsPolicy::LoopbackOnly,
            default_timeout: DEFAULT_TIMEOUT,
        }
    }

    /// Authenticated protocol escape hatch on this client's existing channel.
    /// Cloned handles own the channel independently and may outlive this client.
    /// Raw calls have caller-managed deadlines and retries.
    pub fn raw_proto(&self) -> &SpiceDBProtoClient {
        &self.proto
    }

    fn effective_timeout(&self, timeout: Option<Duration>) -> Duration {
        timeout.unwrap_or(self.default_timeout)
    }

    // -----------------------------------------------------------------------
    // Checks — all via BulkCheckPermissions
    // -----------------------------------------------------------------------

    async fn write_impl(
        &self,
        txn: &Transaction,
        timeout: Option<Duration>,
        metadata: Option<&Context>,
    ) -> Result<String, SpiceDBError> {
        let timeout = self.effective_timeout(timeout);
        let updates: Vec<proto::RelationshipUpdate> = txn
            .updates()
            .iter()
            .map(|(op, rel)| {
                let operation = match op {
                    UpdateOperation::Create => proto::relationship_update::Operation::Create as i32,
                    UpdateOperation::Touch => proto::relationship_update::Operation::Touch as i32,
                    UpdateOperation::Delete => proto::relationship_update::Operation::Delete as i32,
                    // Transaction's builder never produces Unspecified -- it is only ever
                    // produced by the watch stream for an operation this client does not
                    // recognize. Send it through as OPERATION_UNSPECIFIED so the server
                    // rejects the write, rather than guessing at a mutation the caller
                    // never asked for.
                    UpdateOperation::Unspecified => {
                        proto::relationship_update::Operation::Unspecified as i32
                    }
                };
                proto::RelationshipUpdate {
                    operation,
                    relationship: Some(rel.to_proto()),
                }
            })
            .collect();

        let preconditions = preconditions_to_proto(txn.preconditions())?;

        let resp = self
            .call_once(|| async {
                let mut request = tonic::Request::new(proto::WriteRelationshipsRequest {
                    updates: updates.clone(),
                    optional_preconditions: preconditions.clone(),
                    optional_transaction_metadata: metadata.map(check_context_to_proto),
                });
                request.set_timeout(remaining_timeout(timeout));
                self.proto
                    .permissions
                    .clone()
                    .write_relationships(request)
                    .await
            })
            .await?;

        Ok(resp
            .into_inner()
            .written_at
            .map(|z| z.token)
            .unwrap_or_default())
    }

    // -----------------------------------------------------------------------
    // Lookups
    // -----------------------------------------------------------------------

    // -----------------------------------------------------------------------
    // Schema
    // -----------------------------------------------------------------------

    async fn read_schema_impl(
        &self,
        timeout: Option<Duration>,
    ) -> Result<(String, String), SpiceDBError> {
        let timeout = self.effective_timeout(timeout);
        let resp = self
            .retry(|| async {
                let mut request = tonic::Request::new(proto::ReadSchemaRequest {});
                request.set_timeout(remaining_timeout(timeout));
                self.proto.schema.clone().read_schema(request).await
            })
            .await?;

        let inner = resp.into_inner();
        let revision = inner.read_at.map(|z| z.token).unwrap_or_default();
        Ok((inner.schema_text, revision))
    }

    async fn write_schema_impl(
        &self,
        schema: &str,
        timeout: Option<Duration>,
    ) -> Result<String, SpiceDBError> {
        let timeout = self.effective_timeout(timeout);
        let resp = self
            .call_once(|| async {
                let mut request = tonic::Request::new(proto::WriteSchemaRequest {
                    schema: schema.to_string(),
                });
                request.set_timeout(remaining_timeout(timeout));
                self.proto.schema.clone().write_schema(request).await
            })
            .await?;

        Ok(resp
            .into_inner()
            .written_at
            .map(|z| z.token)
            .unwrap_or_default())
    }

    async fn reflect_schema_impl(
        &self,
        consistency: &Strategy,
        timeout: Option<Duration>,
        filters: &[ReflectionFilter],
    ) -> Result<ReflectSchemaResult, SpiceDBError> {
        let timeout = self.effective_timeout(timeout);
        let resp = self
            .retry(|| async {
                let mut request = tonic::Request::new(proto::ReflectSchemaRequest {
                    consistency: Some(consistency.to_proto()),
                    optional_filters: filters
                        .iter()
                        .map(|f| proto::ReflectionSchemaFilter {
                            optional_definition_name_filter: f.definition_name_filter.clone(),
                            optional_caveat_name_filter: f.caveat_name_filter.clone(),
                            optional_relation_name_filter: f.relation_name_filter.clone(),
                            optional_permission_name_filter: f.permission_name_filter.clone(),
                        })
                        .collect(),
                });
                request.set_timeout(remaining_timeout(timeout));
                self.proto.schema.clone().reflect_schema(request).await
            })
            .await?;

        let inner = resp.into_inner();
        let revision = inner.read_at.map(|z| z.token).unwrap_or_default();

        let definitions = inner.definitions.iter().map(schema_definition).collect();
        let caveats = inner.caveats.iter().map(schema_caveat).collect();

        Ok(ReflectSchemaResult {
            definitions,
            caveats,
            revision: revision.into(),
        })
    }

    async fn computable_permissions_impl(
        &self,
        consistency: &Strategy,
        definition_name: &str,
        relation_name: &str,
        timeout: Option<Duration>,
        definition_filter: Option<&str>,
    ) -> Result<(Vec<RelationReference>, String), SpiceDBError> {
        let timeout = self.effective_timeout(timeout);
        let resp = self
            .retry(|| async {
                let mut request = tonic::Request::new(proto::ComputablePermissionsRequest {
                    consistency: Some(consistency.to_proto()),
                    definition_name: definition_name.to_string(),
                    relation_name: relation_name.to_string(),
                    optional_definition_name_filter: definition_filter.unwrap_or_default().into(),
                });
                request.set_timeout(remaining_timeout(timeout));
                self.proto
                    .schema
                    .clone()
                    .computable_permissions(request)
                    .await
            })
            .await?;

        let inner = resp.into_inner();
        let revision = inner.read_at.map(|z| z.token).unwrap_or_default();

        let refs = inner
            .permissions
            .iter()
            .map(|perm| RelationReference {
                definition_name: perm.definition_name.clone(),
                relation_name: perm.relation_name.clone(),
                is_permission: perm.is_permission,
            })
            .collect();

        Ok((refs, revision))
    }

    async fn dependent_relations_impl(
        &self,
        consistency: &Strategy,
        definition_name: &str,
        permission_name: &str,
        timeout: Option<Duration>,
    ) -> Result<(Vec<RelationReference>, String), SpiceDBError> {
        let timeout = self.effective_timeout(timeout);
        let resp = self
            .retry(|| async {
                let mut request = tonic::Request::new(proto::DependentRelationsRequest {
                    consistency: Some(consistency.to_proto()),
                    definition_name: definition_name.to_string(),
                    permission_name: permission_name.to_string(),
                });
                request.set_timeout(remaining_timeout(timeout));
                self.proto.schema.clone().dependent_relations(request).await
            })
            .await?;

        let inner = resp.into_inner();
        let revision = inner.read_at.map(|z| z.token).unwrap_or_default();

        let refs = inner
            .relations
            .iter()
            .map(|rel| RelationReference {
                definition_name: rel.definition_name.clone(),
                relation_name: rel.relation_name.clone(),
                is_permission: rel.is_permission,
            })
            .collect();

        Ok((refs, revision))
    }

    async fn diff_schema_impl(
        &self,
        consistency: &Strategy,
        comparison_schema: &str,
        timeout: Option<Duration>,
    ) -> Result<(Vec<SchemaDiff>, String), SpiceDBError> {
        let timeout = self.effective_timeout(timeout);
        let resp = self
            .retry(|| async {
                let mut request = tonic::Request::new(proto::DiffSchemaRequest {
                    consistency: Some(consistency.to_proto()),
                    comparison_schema: comparison_schema.to_string(),
                });
                request.set_timeout(remaining_timeout(timeout));
                self.proto.schema.clone().diff_schema(request).await
            })
            .await?;

        let inner = resp.into_inner();
        let revision = inner.read_at.map(|z| z.token).unwrap_or_default();

        let diffs = inner.diffs.iter().map(schema_diff_from_proto).collect();

        Ok((diffs, revision))
    }

    // -----------------------------------------------------------------------
    // Expand
    // -----------------------------------------------------------------------

    async fn expand_permission_tree_impl(
        &self,
        consistency: &Strategy,
        resource_type: &str,
        resource_id: &str,
        permission: &str,
        timeout: Option<Duration>,
    ) -> Result<ExpandPermissionTreeResult, SpiceDBError> {
        let timeout = self.effective_timeout(timeout);
        let resp = self
            .retry(|| async {
                let mut request = tonic::Request::new(proto::ExpandPermissionTreeRequest {
                    consistency: Some(consistency.to_proto()),
                    resource: Some(proto::ObjectReference {
                        object_type: resource_type.to_string(),
                        object_id: resource_id.to_string(),
                    }),
                    permission: permission.to_string(),
                });
                request.set_timeout(remaining_timeout(timeout));
                self.proto
                    .permissions
                    .clone()
                    .expand_permission_tree(request)
                    .await
            })
            .await?;

        let inner = resp.into_inner();
        let revision = inner.expanded_at.map(|z| z.token).unwrap_or_default();
        let tree = permission_tree_from_proto(inner.tree_root.as_ref());

        Ok(ExpandPermissionTreeResult {
            tree,
            revision: revision.into(),
        })
    }

    // -----------------------------------------------------------------------
    // Bulk
    // -----------------------------------------------------------------------

    // -----------------------------------------------------------------------
    // Watch
    // -----------------------------------------------------------------------

    // -----------------------------------------------------------------------
    // Experimental
    // -----------------------------------------------------------------------

    async fn experimental_register_relationship_counter_impl(
        &self,
        name: &str,
        filter: &Filter,
        timeout: Option<Duration>,
    ) -> Result<(), SpiceDBError> {
        let timeout = self.effective_timeout(timeout);
        let filter = filter.to_proto()?;
        self.call_once(|| async {
            let mut request =
                tonic::Request::new(proto::ExperimentalRegisterRelationshipCounterRequest {
                    name: name.to_string(),
                    relationship_filter: Some(filter.clone()),
                });
            request.set_timeout(remaining_timeout(timeout));
            self.proto
                .experimental
                .clone()
                .experimental_register_relationship_counter(request)
                .await
        })
        .await?;

        Ok(())
    }

    async fn experimental_count_relationships_impl(
        &self,
        name: &str,
        timeout: Option<Duration>,
    ) -> Result<ExperimentalCountRelationshipsResult, SpiceDBError> {
        let timeout = self.effective_timeout(timeout);
        let resp = self
            .retry(|| async {
                let mut request =
                    tonic::Request::new(proto::ExperimentalCountRelationshipsRequest {
                        name: name.to_string(),
                    });
                request.set_timeout(remaining_timeout(timeout));
                self.proto
                    .experimental
                    .clone()
                    .experimental_count_relationships(request)
                    .await
            })
            .await?;

        let inner = resp.into_inner();

        match inner.counter_result {
            Some(
                proto::experimental_count_relationships_response::CounterResult::CounterStillCalculating(
                    _,
                ),
            ) => Ok(ExperimentalCountRelationshipsResult::StillCalculating),
            Some(
                proto::experimental_count_relationships_response::CounterResult::ReadCounterValue(
                    cv,
                ),
            ) => {
                let revision = cv.read_at.map(|z| z.token).unwrap_or_default();
                Ok(ExperimentalCountRelationshipsResult::Count(CountResult {
                    relationship_count: cv.relationship_count,
                    revision: revision.into(),
                }))
            }
            None => Ok(ExperimentalCountRelationshipsResult::Unknown),
        }
    }

    async fn experimental_unregister_relationship_counter_impl(
        &self,
        name: &str,
        timeout: Option<Duration>,
    ) -> Result<(), SpiceDBError> {
        let timeout = self.effective_timeout(timeout);
        self.call_once(|| async {
            let mut request =
                tonic::Request::new(proto::ExperimentalUnregisterRelationshipCounterRequest {
                    name: name.to_string(),
                });
            request.set_timeout(remaining_timeout(timeout));
            self.proto
                .experimental
                .clone()
                .experimental_unregister_relationship_counter(request)
                .await
        })
        .await?;

        Ok(())
    }

    async fn experimental_roaring_lookup_resources_impl(
        &self,
        consistency: &Strategy,
        resource_type: &str,
        permission: &str,
        subject: &SubjectRef,
        timeout: Option<Duration>,
    ) -> Result<ExperimentalRoaringLookupResourcesResult, SpiceDBError> {
        let timeout = self.effective_timeout(timeout);
        let consistency = consistency.to_proto();
        let resp = self
            .retry(|| async {
                let mut request = tonic::Request::new(
                    materialize_proto::ExperimentalRoaringLookupResourcesRequest {
                        consistency: Some(consistency.clone()),
                        resource_object_type: resource_type.to_string(),
                        permission: permission.to_string(),
                        subject: Some(subject.to_proto()),
                    },
                );
                request.set_timeout(remaining_timeout(timeout));
                self.proto
                    .materialize
                    .clone()
                    .experimental_roaring_lookup_resources(request)
                    .await
            })
            .await?;

        let inner = resp.into_inner();
        Ok(ExperimentalRoaringLookupResourcesResult {
            bitmap: inner.bitmap,
            cardinality: inner.cardinality,
            at_revision: inner
                .at_revision
                .map(|z| z.token.into())
                .unwrap_or_default(),
        })
    }

    // -----------------------------------------------------------------------
    // Retry with exponential backoff
    // -----------------------------------------------------------------------

    async fn retry<F, Fut, T>(&self, f: F) -> Result<tonic::Response<T>, SpiceDBError>
    where
        F: Fn() -> Fut,
        Fut: std::future::Future<Output = Result<tonic::Response<T>, tonic::Status>>,
    {
        retry_call(f).await
    }

    async fn call_once<F, Fut, T>(&self, f: F) -> Result<tonic::Response<T>, SpiceDBError>
    where
        F: Fn() -> Fut,
        Fut: std::future::Future<Output = Result<tonic::Response<T>, tonic::Status>>,
    {
        f().await.map_err(error::from_grpc_status)
    }
}

fn jittered_backoff_ms(attempt: u32) -> u64 {
    let cap_ms = (BASE_RETRY_DELAY_MS * 2u64.pow(attempt)).min(5000);
    (rand::random::<f64>() * cap_ms as f64) as u64
}

pub(crate) async fn retry_call<F, Fut, T>(f: F) -> Result<tonic::Response<T>, SpiceDBError>
where
    F: Fn() -> Fut,
    Fut: std::future::Future<Output = Result<tonic::Response<T>, tonic::Status>>,
{
    let mut attempt = 0u32;
    loop {
        match f().await {
            Ok(resp) => return Ok(resp),
            Err(status) => {
                let err = error::from_grpc_status(status);
                if !error::is_transient(&err) || attempt >= MAX_RETRIES {
                    return Err(err);
                }
                let delay_ms = jittered_backoff_ms(attempt);
                tokio::time::sleep(tokio::time::Duration::from_millis(delay_ms)).await;
                attempt += 1;
            }
        }
    }
}

fn preconditions_to_proto(
    preconditions: &[Precondition],
) -> Result<Vec<proto::Precondition>, SpiceDBError> {
    preconditions
        .iter()
        .map(|pc| {
            let operation = match pc.operation {
                PreconditionOperation::MustNotMatch => {
                    proto::precondition::Operation::MustNotMatch as i32
                }
                PreconditionOperation::MustMatch => {
                    proto::precondition::Operation::MustMatch as i32
                }
            };
            Ok(proto::Precondition {
                operation,
                filter: Some(pc.filter.to_proto()?),
            })
        })
        .collect()
}

fn schema_diff_from_proto(d: &proto::ReflectionSchemaDiff) -> SchemaDiff {
    use proto::reflection_schema_diff::Diff;
    match &d.diff {
        Some(Diff::DefinitionAdded(value)) => SchemaDiff::DefinitionAdded(schema_definition(value)),
        Some(Diff::DefinitionRemoved(value)) => {
            SchemaDiff::DefinitionRemoved(schema_definition(value))
        }
        Some(Diff::DefinitionDocCommentChanged(value)) => {
            SchemaDiff::DefinitionDocCommentChanged(schema_definition(value))
        }
        Some(Diff::RelationAdded(value)) => SchemaDiff::RelationAdded(schema_relation(value)),
        Some(Diff::RelationRemoved(value)) => SchemaDiff::RelationRemoved(schema_relation(value)),
        Some(Diff::RelationDocCommentChanged(value)) => {
            SchemaDiff::RelationDocCommentChanged(schema_relation(value))
        }
        Some(Diff::RelationSubjectTypeAdded(value)) => {
            SchemaDiff::RelationSubjectTypeAdded(subject_type_change(value))
        }
        Some(Diff::RelationSubjectTypeRemoved(value)) => {
            SchemaDiff::RelationSubjectTypeRemoved(subject_type_change(value))
        }
        Some(Diff::PermissionAdded(value)) => SchemaDiff::PermissionAdded(schema_permission(value)),
        Some(Diff::PermissionRemoved(value)) => {
            SchemaDiff::PermissionRemoved(schema_permission(value))
        }
        Some(Diff::PermissionDocCommentChanged(value)) => {
            SchemaDiff::PermissionDocCommentChanged(schema_permission(value))
        }
        Some(Diff::PermissionExprChanged(value)) => {
            SchemaDiff::PermissionExprChanged(schema_permission(value))
        }
        Some(Diff::CaveatAdded(value)) => SchemaDiff::CaveatAdded(schema_caveat(value)),
        Some(Diff::CaveatRemoved(value)) => SchemaDiff::CaveatRemoved(schema_caveat(value)),
        Some(Diff::CaveatDocCommentChanged(value)) => {
            SchemaDiff::CaveatDocCommentChanged(schema_caveat(value))
        }
        Some(Diff::CaveatExprChanged(value)) => SchemaDiff::CaveatExprChanged(schema_caveat(value)),
        Some(Diff::CaveatParameterAdded(value)) => {
            SchemaDiff::CaveatParameterAdded(schema_parameter(value))
        }
        Some(Diff::CaveatParameterRemoved(value)) => {
            SchemaDiff::CaveatParameterRemoved(schema_parameter(value))
        }
        Some(Diff::CaveatParameterTypeChanged(value)) => {
            SchemaDiff::CaveatParameterTypeChanged(parameter_type_change(value))
        }
        None => SchemaDiff::Unknown,
    }
}

fn schema_type(r: &proto::ReflectionTypeReference) -> SchemaTypeReference {
    SchemaTypeReference {
        subject_definition_name: r.subject_definition_name.clone(),
        caveat_name: if r.optional_caveat_name.is_empty() {
            None
        } else {
            Some(r.optional_caveat_name.clone())
        },
        reference: r.typeref.as_ref().map(|t| match t {
            proto::reflection_type_reference::Typeref::IsTerminalSubject(v) => {
                SchemaSubjectType::Terminal(*v)
            }
            proto::reflection_type_reference::Typeref::OptionalRelationName(v) => {
                SchemaSubjectType::Relation(v.clone())
            }
            proto::reflection_type_reference::Typeref::IsPublicWildcard(v) => {
                SchemaSubjectType::PublicWildcard(*v)
            }
        }),
    }
}
fn schema_relation(r: &proto::ReflectionRelation) -> SchemaRelation {
    SchemaRelation {
        name: r.name.clone(),
        comment: r.comment.clone(),
        parent_definition_name: r.parent_definition_name.clone(),
        subject_types: r.subject_types.iter().map(schema_type).collect(),
    }
}
fn schema_permission(r: &proto::ReflectionPermission) -> SchemaPermission {
    SchemaPermission {
        name: r.name.clone(),
        comment: r.comment.clone(),
        parent_definition_name: r.parent_definition_name.clone(),
    }
}
fn schema_definition(r: &proto::ReflectionDefinition) -> SchemaDefinition {
    SchemaDefinition {
        name: r.name.clone(),
        comment: r.comment.clone(),
        relations: r.relations.iter().map(schema_relation).collect(),
        permissions: r.permissions.iter().map(schema_permission).collect(),
    }
}
fn schema_parameter(r: &proto::ReflectionCaveatParameter) -> SchemaCaveatParameter {
    SchemaCaveatParameter {
        name: r.name.clone(),
        type_name: r.r#type.clone(),
        parent_caveat_name: r.parent_caveat_name.clone(),
    }
}
fn schema_caveat(r: &proto::ReflectionCaveat) -> SchemaCaveat {
    SchemaCaveat {
        name: r.name.clone(),
        comment: r.comment.clone(),
        expression: r.expression.clone(),
        parameters: r.parameters.iter().map(schema_parameter).collect(),
    }
}
fn subject_type_change(
    r: &proto::ReflectionRelationSubjectTypeChange,
) -> RelationSubjectTypeChange {
    RelationSubjectTypeChange {
        relation: r.relation.as_ref().map(schema_relation),
        changed_subject_type: r.changed_subject_type.as_ref().map(schema_type),
    }
}
fn parameter_type_change(
    r: &proto::ReflectionCaveatParameterTypeChange,
) -> CaveatParameterTypeChange {
    CaveatParameterTypeChange {
        parameter: r.parameter.as_ref().map(schema_parameter),
        previous_type: r.previous_type.clone(),
    }
}
