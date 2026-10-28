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
//! transactions, paged reads with cursors, bounded and all-or-nothing deletes —
//! but evaluates no schema: permission checks return results registered with
//! [`InMemorySpiceDB::set_check_result`] (default `NoPermission`). RPCs it does
//! not model return `UNIMPLEMENTED`.
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
}

impl State {
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

    /// An in-memory channel to this double. Each connection the channel makes
    /// is served on the current Tokio runtime.
    pub fn channel(&self) -> Channel {
        let service = PermissionsServiceServer::new(Service(self.clone()));
        Endpoint::from_static("http://in-memory.spicedb").connect_with_connector_lazy(
            tower::service_fn(move |_: tonic::codegen::http::Uri| {
                let (client_io, server_io) = tokio::io::duplex(64 * 1024);
                let service = service.clone();
                tokio::spawn(async move {
                    let _ = Server::builder()
                        .add_service(service)
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
        let start = match &request.optional_cursor {
            Some(c) => c
                .token
                .parse::<usize>()
                .map_err(|_| Status::invalid_argument("invalid cursor"))?,
            None => 0,
        };
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
        Ok(Response::new(Box::pin(futures::stream::iter(items))))
    }

    async fn write_relationships(
        &self,
        request: Request<proto::WriteRelationshipsRequest>,
    ) -> Result<Response<proto::WriteRelationshipsResponse>, Status> {
        let request = request.into_inner();
        let mut state = self.0.lock();
        state.check_preconditions(&request.optional_preconditions)?;
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
        Ok(Response::new(proto::WriteRelationshipsResponse {
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
        Ok(Response::new(proto::DeleteRelationshipsResponse {
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
        Ok(Response::new(proto::CheckPermissionResponse {
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
        Ok(Response::new(proto::CheckBulkPermissionsResponse {
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

    async fn import_bulk_relationships(
        &self,
        _: Request<tonic::Streaming<proto::ImportBulkRelationshipsRequest>>,
    ) -> Result<Response<proto::ImportBulkRelationshipsResponse>, Status> {
        unimplemented("ImportBulkRelationships")
    }

    async fn export_bulk_relationships(
        &self,
        _: Request<proto::ExportBulkRelationshipsRequest>,
    ) -> Result<Response<Self::ExportBulkRelationshipsStream>, Status> {
        unimplemented("ExportBulkRelationships")
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
