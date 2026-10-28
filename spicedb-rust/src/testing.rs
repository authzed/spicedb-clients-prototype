//! An in-memory SpiceDB double for tests (feature `testing`).
//!
//! [`InMemorySpiceDB`] serves SpiceDB's `PermissionsService` over an
//! in-memory channel, and [`InMemorySpiceDB::client`] returns a real
//! [`SpiceDBClient`] connected to it through
//! [`SpiceDBClient::from_channel`]. The test double therefore has exactly the
//! real client's surface: code under test takes a `SpiceDBClient` and needs no
//! trait or adapter.
//!
//! It applies SpiceDB's relationship semantics — filter matching (via
//! [`Filter::matches`]), preconditions, `CREATE` conflicts and atomic
//! transactions (an empty write checks its preconditions and returns the
//! current revision), paged reads and exports with cursors, limits and the
//! export filter, all-or-nothing imports, bounded and all-or-nothing deletes —
//! and serves `SchemaService` reads and writes of the schema text. It
//! evaluates no schema: permission checks return results registered with
//! [`InMemorySpiceDB::set_check_result`] (default `NoPermission`), and
//! `reflect_schema` returns the result registered with
//! [`InMemorySpiceDB::set_reflect_schema_result`]. RPCs it does not model
//! return `UNIMPLEMENTED`.
//!
//! Because the double is a server behind a real client, every option is
//! honored exactly as the client sends it (cursors, limits, page sizes,
//! filters, per-call retry policies, request metadata), and errors have the
//! real client's codes and shape: server failures are mapped by the client
//! with [`SpiceDBError::server_message`] set, and local validation fails in
//! the client before any request, as it does against SpiceDB. Headers
//! registered with [`InMemorySpiceDB::set_response_metadata`] are sent on
//! every successful response, so [`ResponseMetadata`](crate::ResponseMetadata)
//! captures them.
//!
//! ```
//! # #[tokio::main(flavor = "current_thread")]
//! # async fn main() -> Result<(), spicedb::SpiceDBError> {
//! use spicedb::testing::InMemorySpiceDB;
//! use spicedb::{consistency, PermissionCheck, Permissionship, Relationship, Transaction, WriteRequest};
//!
//! let fake = InMemorySpiceDB::new();
//! let client = fake.client()?;
//! let viewer: Relationship = "document:readme#viewer@user:alice".parse()?;
//! let mut txn = Transaction::new();
//! txn.create(&viewer);
//! client.write(WriteRequest::new(txn)).await?;
//! assert_eq!(fake.relationships(), vec![viewer.clone()]);
//!
//! fake.set_check_result(viewer.resource(), "view", viewer.subject(), Permissionship::HasPermission);
//! let check = PermissionCheck::new(viewer.resource().clone(), "view", viewer.subject().clone(), consistency::full());
//! assert!(client.check(check).await?.has_permission());
//! # Ok(()) }
//! ```

// tonic services return `Status` errors by design.
#![allow(clippy::result_large_err)]

use std::collections::HashMap;
use std::pin::Pin;
use std::sync::{Arc, Mutex};

use futures::Stream;
use spicedb_proto::authzed::api::v1 as proto;
use spicedb_proto::authzed::api::v1::permissions_service_server::{
    PermissionsService, PermissionsServiceServer,
};
use spicedb_proto::authzed::api::v1::schema_service_server::{SchemaService, SchemaServiceServer};
use tonic::metadata::MetadataMap;
use tonic::transport::{Channel, Endpoint, Server};
use tonic::{Code, Request, Response, Status};
use tonic_types::{ErrorDetails, StatusExt};

use crate::error::SpiceDBError;
use crate::protoconv;
use crate::types::*;
use crate::SpiceDBClient;

const DEFAULT_DELETE_LIMIT: u32 = 1_000;

#[derive(Default)]
struct State {
    relationships: Vec<Relationship>,
    revision: u64,
    checks: HashMap<String, Permissionship>,
    schema: Option<String>,
    reflection: Option<ReflectSchemaResult>,
    headers: MetadataMap,
}

impl State {
    /// A successful response carrying the registered headers.
    fn respond<T>(&self, message: T) -> Response<T> {
        let mut response = Response::new(message);
        *response.metadata_mut() = self.headers.clone();
        response
    }
    fn token(&self) -> proto::ZedToken {
        proto::ZedToken {
            token: self.revision.to_string(),
        }
    }
    fn index(&self, r: &Relationship) -> Option<usize> {
        let identity = r.filter();
        self.relationships.iter().position(|e| identity.matches(e))
    }
    fn upsert(&mut self, r: Relationship) {
        match self.index(&r) {
            Some(i) => self.relationships[i] = r,
            None => self.relationships.push(r),
        }
    }
    fn check_preconditions(&self, preconditions: &[proto::Precondition]) -> Result<(), Status> {
        for p in preconditions {
            let p = protoconv::precondition_from_proto(p).map_err(Status::from)?;
            let matched = self.relationships.iter().any(|r| p.filter.matches(r));
            let ok = match p.operation {
                PreconditionOperation::MustMatch => matched,
                _ => !matched,
            };
            if !ok {
                return Err(reasoned(
                    Code::FailedPrecondition,
                    format!("unable to satisfy write precondition `{p}`"),
                    "ERROR_REASON_WRITE_OR_DELETE_PRECONDITION_FAILURE",
                ));
            }
        }
        Ok(())
    }
}

fn reasoned(code: Code, message: String, reason: &str) -> Status {
    Status::with_error_details(
        code,
        message,
        ErrorDetails::with_error_info(reason, "authzed.com", HashMap::new()),
    )
}

fn check_key(resource: &ObjectRef, permission: &str, subject: &SubjectRef) -> String {
    format!("{resource}#{permission}@{subject}")
}

/// An in-memory SpiceDB double. Cheap to clone; clones share one store.
#[derive(Clone, Default)]
pub struct InMemorySpiceDB {
    state: Arc<Mutex<State>>,
}

impl std::fmt::Debug for InMemorySpiceDB {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.debug_struct("InMemorySpiceDB")
            .field("relationships", &self.relationships())
            .finish()
    }
}

impl InMemorySpiceDB {
    /// An empty store.
    pub fn new() -> Self {
        Self::default()
    }

    /// A store holding `relationships`.
    pub fn with_relationships(relationships: impl IntoIterator<Item = Relationship>) -> Self {
        let fake = Self::new();
        {
            let mut state = fake.lock();
            for r in relationships {
                state.upsert(r);
            }
        }
        fake
    }

    fn lock(&self) -> std::sync::MutexGuard<'_, State> {
        self.state.lock().unwrap_or_else(|e| e.into_inner())
    }

    /// The stored relationships, in insertion order.
    pub fn relationships(&self) -> Vec<Relationship> {
        self.lock().relationships.clone()
    }

    /// Registers the permissionship `check` and `check_bulk` return for this
    /// exact question. Unregistered questions are `NoPermission`.
    pub fn set_check_result(
        &self,
        resource: &ObjectRef,
        permission: &str,
        subject: &SubjectRef,
        permissionship: Permissionship,
    ) {
        self.lock()
            .checks
            .insert(check_key(resource, permission, subject), permissionship);
    }

    /// Registers the result `reflect_schema` returns (its revision is
    /// replaced with the store's current one). Without one, `reflect_schema`
    /// fails with `NotFound`. Build it with field assignment:
    ///
    /// ```
    /// use spicedb::{ReflectSchemaResult, SchemaDefinition};
    /// let mut user = SchemaDefinition::default();
    /// user.name = "user".into();
    /// let mut reflection = ReflectSchemaResult::default();
    /// reflection.definitions.push(user);
    /// spicedb::testing::InMemorySpiceDB::new().set_reflect_schema_result(reflection);
    /// ```
    pub fn set_reflect_schema_result(&self, result: ReflectSchemaResult) {
        self.lock().reflection = Some(result);
    }

    /// The schema text last written with `write_schema`.
    pub fn schema(&self) -> Option<String> {
        self.lock().schema.clone()
    }

    /// Headers sent on every successful response, for code that reads them
    /// through [`ResponseMetadata`](crate::ResponseMetadata).
    pub fn set_response_metadata(&self, headers: MetadataMap) {
        self.lock().headers = headers;
    }

    /// An in-memory channel to this double. Each connection the channel makes
    /// is served on the current Tokio runtime.
    pub fn channel(&self) -> Channel {
        let service = PermissionsServiceServer::new(Service(self.clone()));
        let schema = SchemaServiceServer::new(Service(self.clone()));
        Endpoint::from_static("http://in-memory.spicedb").connect_with_connector_lazy(
            tower::service_fn(move |_: tonic::codegen::http::Uri| {
                let (client_io, server_io) = tokio::io::duplex(64 * 1024);
                let service = service.clone();
                let schema = schema.clone();
                tokio::spawn(async move {
                    let _ = Server::builder()
                        .add_service(service)
                        .add_service(schema)
                        .serve_with_incoming(futures::stream::iter([Ok::<_, std::io::Error>(
                            server_io,
                        )]))
                        .await;
                });
                async move { Ok::<_, std::io::Error>(hyper_util::rt::TokioIo::new(client_io)) }
            }),
        )
    }

    /// A real [`SpiceDBClient`] connected to this double.
    pub fn client(&self) -> Result<SpiceDBClient, SpiceDBError> {
        SpiceDBClient::from_channel(self.channel()).build()
    }
}

struct Service(InMemorySpiceDB);

type BoxStream<T> = Pin<Box<dyn Stream<Item = Result<T, Status>> + Send>>;

fn unimplemented<T>(rpc: &str) -> Result<T, Status> {
    Err(Status::unimplemented(format!(
        "{rpc} is not modeled by spicedb::testing::InMemorySpiceDB"
    )))
}

fn required<'a, T>(value: Option<&'a T>, field: &str) -> Result<&'a T, Status> {
    value.ok_or_else(|| Status::invalid_argument(format!("{field} is required")))
}

#[tonic::async_trait]
impl PermissionsService for Service {
    type ReadRelationshipsStream = BoxStream<proto::ReadRelationshipsResponse>;
    type LookupResourcesStream = BoxStream<proto::LookupResourcesResponse>;
    type LookupSubjectsStream = BoxStream<proto::LookupSubjectsResponse>;
    type ExportBulkRelationshipsStream = BoxStream<proto::ExportBulkRelationshipsResponse>;

    async fn read_relationships(
        &self,
        request: Request<proto::ReadRelationshipsRequest>,
    ) -> Result<Response<Self::ReadRelationshipsStream>, Status> {
        let request = request.into_inner();
        let filter = protoconv::filter_from_proto(required(
            request.relationship_filter.as_ref(),
            "relationship_filter",
        )?);
        filter.to_proto().map_err(Status::from)?;
        let start = parse_cursor(request.optional_cursor.as_ref())?;
        let limit = match request.optional_limit {
            0 => usize::MAX,
            n => n as usize,
        };
        let state = self.0.lock();
        let read_at = state.token();
        let items: Vec<_> = state
            .relationships
            .iter()
            .enumerate()
            .filter(|(_, r)| filter.matches(r))
            .filter(|(i, _)| *i >= start)
            .take(limit)
            .map(|(i, r)| {
                Ok(proto::ReadRelationshipsResponse {
                    read_at: Some(read_at.clone()),
                    relationship: Some(r.to_proto()),
                    after_result_cursor: Some(proto::Cursor {
                        token: (i + 1).to_string(),
                    }),
                })
            })
            .collect();
        Ok(state.respond(Box::pin(futures::stream::iter(items))))
    }

    async fn write_relationships(
        &self,
        request: Request<proto::WriteRelationshipsRequest>,
    ) -> Result<Response<proto::WriteRelationshipsResponse>, Status> {
        let request = request.into_inner();
        let mut state = self.0.lock();
        state.check_preconditions(&request.optional_preconditions)?;
        if request.updates.is_empty() {
            // SpiceDB accepts an empty write and returns the current revision.
            return Ok(state.respond(proto::WriteRelationshipsResponse {
                written_at: Some(state.token()),
            }));
        }
        let mut staged = State {
            relationships: state.relationships.clone(),
            ..State::default()
        };
        for update in &request.updates {
            let update = protoconv::update_from_proto(update).map_err(Status::from)?;
            match update.operation {
                UpdateOperation::Create => {
                    if staged.index(&update.relationship).is_some() {
                        return Err(reasoned(
                            Code::AlreadyExists,
                            format!(
                                "could not CREATE relationship `{}`, as it already existed",
                                update.relationship
                            ),
                            "ERROR_REASON_ATTEMPT_TO_RECREATE_RELATIONSHIP",
                        ));
                    }
                    staged.upsert(update.relationship);
                }
                UpdateOperation::Touch => staged.upsert(update.relationship),
                UpdateOperation::Delete => {
                    if let Some(i) = staged.index(&update.relationship) {
                        staged.relationships.remove(i);
                    }
                }
                _ => return Err(Status::invalid_argument("unknown update operation")),
            }
        }
        state.relationships = staged.relationships;
        state.revision += 1;
        Ok(state.respond(proto::WriteRelationshipsResponse {
            written_at: Some(state.token()),
        }))
    }

    async fn delete_relationships(
        &self,
        request: Request<proto::DeleteRelationshipsRequest>,
    ) -> Result<Response<proto::DeleteRelationshipsResponse>, Status> {
        let request = request.into_inner();
        let filter = protoconv::filter_from_proto(required(
            request.relationship_filter.as_ref(),
            "relationship_filter",
        )?);
        filter.to_proto().map_err(Status::from)?;
        let limit = match request.optional_limit {
            0 => DEFAULT_DELETE_LIMIT,
            n => n,
        } as usize;
        let mut state = self.0.lock();
        state.check_preconditions(&request.optional_preconditions)?;
        let matching = state
            .relationships
            .iter()
            .filter(|r| filter.matches(r))
            .count();
        if matching > limit && !request.optional_allow_partial_deletions {
            return Err(reasoned(
                Code::InvalidArgument,
                format!(
                    "found more than {limit} relationships to be deleted and partial deletion was not requested"
                ),
                "ERROR_REASON_TOO_MANY_RELATIONSHIPS_FOR_TRANSACTIONAL_DELETE",
            ));
        }
        let mut deleted = 0usize;
        state.relationships.retain(|r| {
            if deleted < limit && filter.matches(r) {
                deleted += 1;
                false
            } else {
                true
            }
        });
        state.revision += 1;
        let complete = deleted == matching;
        Ok(state.respond(proto::DeleteRelationshipsResponse {
            deleted_at: Some(state.token()),
            deletion_progress: if complete {
                proto::delete_relationships_response::DeletionProgress::Complete as i32
            } else {
                proto::delete_relationships_response::DeletionProgress::Partial as i32
            },
            relationships_deleted_count: deleted as u64,
            after_result_cursor: None,
        }))
    }

    async fn check_permission(
        &self,
        request: Request<proto::CheckPermissionRequest>,
    ) -> Result<Response<proto::CheckPermissionResponse>, Status> {
        let request = request.into_inner();
        let state = self.0.lock();
        let permissionship = lookup_check(
            &state,
            &request.resource,
            &request.permission,
            &request.subject,
        )?;
        Ok(state.respond(proto::CheckPermissionResponse {
            checked_at: Some(state.token()),
            permissionship,
            ..Default::default()
        }))
    }

    async fn check_bulk_permissions(
        &self,
        request: Request<proto::CheckBulkPermissionsRequest>,
    ) -> Result<Response<proto::CheckBulkPermissionsResponse>, Status> {
        let request = request.into_inner();
        let state = self.0.lock();
        let mut pairs = Vec::with_capacity(request.items.len());
        for item in request.items {
            let response =
                match lookup_check(&state, &item.resource, &item.permission, &item.subject) {
                    Ok(permissionship) => proto::check_bulk_permissions_pair::Response::Item(
                        proto::CheckBulkPermissionsResponseItem {
                            permissionship,
                            ..Default::default()
                        },
                    ),
                    Err(status) => proto::check_bulk_permissions_pair::Response::Error(
                        spicedb_proto::google::rpc::Status {
                            code: status.code() as i32,
                            message: status.message().to_string(),
                            details: Vec::new(),
                        },
                    ),
                };
            pairs.push(proto::CheckBulkPermissionsPair {
                request: Some(item),
                response: Some(response),
            });
        }
        Ok(state.respond(proto::CheckBulkPermissionsResponse {
            checked_at: Some(state.token()),
            pairs,
        }))
    }

    async fn expand_permission_tree(
        &self,
        _: Request<proto::ExpandPermissionTreeRequest>,
    ) -> Result<Response<proto::ExpandPermissionTreeResponse>, Status> {
        unimplemented("ExpandPermissionTree")
    }

    async fn lookup_resources(
        &self,
        _: Request<proto::LookupResourcesRequest>,
    ) -> Result<Response<Self::LookupResourcesStream>, Status> {
        unimplemented("LookupResources")
    }

    async fn lookup_subjects(
        &self,
        _: Request<proto::LookupSubjectsRequest>,
    ) -> Result<Response<Self::LookupSubjectsStream>, Status> {
        unimplemented("LookupSubjects")
    }

    /// One transaction, as SpiceDB does: a relationship that already exists
    /// (or repeats within the import) fails it with `ALREADY_EXISTS` and
    /// nothing is added.
    async fn import_bulk_relationships(
        &self,
        request: Request<tonic::Streaming<proto::ImportBulkRelationshipsRequest>>,
    ) -> Result<Response<proto::ImportBulkRelationshipsResponse>, Status> {
        let mut stream = request.into_inner();
        let mut batch = Vec::new();
        while let Some(message) = stream.message().await? {
            for r in &message.relationships {
                batch.push(protoconv::relationship_from_proto(r).map_err(Status::from)?);
            }
        }
        let mut state = self.0.lock();
        let mut staged = State {
            relationships: state.relationships.clone(),
            ..State::default()
        };
        for r in &batch {
            if staged.index(r).is_some() {
                return Err(Status::already_exists(format!(
                    "found duplicate relationship `{r}` while importing"
                )));
            }
            staged.relationships.push(r.clone());
        }
        state.relationships = staged.relationships;
        state.revision += 1;
        Ok(state.respond(proto::ImportBulkRelationshipsResponse {
            num_loaded: batch.len() as u64,
        }))
    }

    /// Every relationship (or those matching the request's filter), one per
    /// response so each carries its own cursor, honoring cursor and limit.
    async fn export_bulk_relationships(
        &self,
        request: Request<proto::ExportBulkRelationshipsRequest>,
    ) -> Result<Response<Self::ExportBulkRelationshipsStream>, Status> {
        let request = request.into_inner();
        let filter = request
            .optional_relationship_filter
            .as_ref()
            .map(protoconv::filter_from_proto);
        if let Some(f) = &filter {
            f.to_proto().map_err(Status::from)?;
        }
        let start = parse_cursor(request.optional_cursor.as_ref())?;
        let limit = match request.optional_limit {
            0 => usize::MAX,
            n => n as usize,
        };
        let state = self.0.lock();
        let items: Vec<_> = state
            .relationships
            .iter()
            .enumerate()
            .filter(|(_, r)| filter.as_ref().is_none_or(|f| f.matches(r)))
            .filter(|(i, _)| *i >= start)
            .take(limit)
            .map(|(i, r)| {
                Ok(proto::ExportBulkRelationshipsResponse {
                    after_result_cursor: Some(proto::Cursor {
                        token: (i + 1).to_string(),
                    }),
                    relationships: vec![r.to_proto()],
                })
            })
            .collect();
        Ok(state.respond(Box::pin(futures::stream::iter(items))))
    }
}

fn parse_cursor(cursor: Option<&proto::Cursor>) -> Result<usize, Status> {
    match cursor {
        Some(c) => c
            .token
            .parse::<usize>()
            .map_err(|_| Status::invalid_argument("invalid cursor")),
        None => Ok(0),
    }
}

const NO_SCHEMA: &str = "No schema has been defined; please call WriteSchema to start";

#[tonic::async_trait]
impl SchemaService for Service {
    /// The schema last written, or `NOT_FOUND` as SpiceDB returns.
    async fn read_schema(
        &self,
        _: Request<proto::ReadSchemaRequest>,
    ) -> Result<Response<proto::ReadSchemaResponse>, Status> {
        let state = self.0.lock();
        let schema = state
            .schema
            .clone()
            .ok_or_else(|| Status::not_found(NO_SCHEMA))?;
        Ok(state.respond(proto::ReadSchemaResponse {
            schema_text: schema,
            read_at: Some(state.token()),
        }))
    }

    /// Stores the schema text. It is not compiled or validated.
    async fn write_schema(
        &self,
        request: Request<proto::WriteSchemaRequest>,
    ) -> Result<Response<proto::WriteSchemaResponse>, Status> {
        let mut state = self.0.lock();
        state.schema = Some(request.into_inner().schema);
        state.revision += 1;
        Ok(state.respond(proto::WriteSchemaResponse {
            written_at: Some(state.token()),
        }))
    }

    /// The registered reflection at the current revision, or `NOT_FOUND`.
    async fn reflect_schema(
        &self,
        _: Request<proto::ReflectSchemaRequest>,
    ) -> Result<Response<proto::ReflectSchemaResponse>, Status> {
        let state = self.0.lock();
        let reflection = state
            .reflection
            .as_ref()
            .ok_or_else(|| Status::not_found(NO_SCHEMA))?;
        let mut response = protoconv::reflect_schema_result_to_proto(reflection);
        response.read_at = Some(state.token());
        Ok(state.respond(response))
    }

    async fn computable_permissions(
        &self,
        _: Request<proto::ComputablePermissionsRequest>,
    ) -> Result<Response<proto::ComputablePermissionsResponse>, Status> {
        unimplemented("ComputablePermissions")
    }

    async fn dependent_relations(
        &self,
        _: Request<proto::DependentRelationsRequest>,
    ) -> Result<Response<proto::DependentRelationsResponse>, Status> {
        unimplemented("DependentRelations")
    }

    async fn diff_schema(
        &self,
        _: Request<proto::DiffSchemaRequest>,
    ) -> Result<Response<proto::DiffSchemaResponse>, Status> {
        unimplemented("DiffSchema")
    }
}

fn lookup_check(
    state: &State,
    resource: &Option<proto::ObjectReference>,
    permission: &str,
    subject: &Option<proto::SubjectReference>,
) -> Result<i32, Status> {
    let resource = protoconv::object_from_proto(required(resource.as_ref(), "resource")?)
        .map_err(Status::from)?;
    let subject = protoconv::subject_from_proto(required(subject.as_ref(), "subject")?)
        .map_err(Status::from)?;
    let permissionship = state
        .checks
        .get(&check_key(&resource, permission, &subject))
        .cloned()
        .unwrap_or(Permissionship::NoPermission);
    use proto::check_permission_response::Permissionship as P;
    Ok(match permissionship {
        Permissionship::HasPermission => P::HasPermission,
        Permissionship::ConditionalPermission => P::ConditionalPermission,
        Permissionship::NoPermission => P::NoPermission,
        _ => P::Unspecified,
    } as i32)
}
