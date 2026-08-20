//! A configurable in-process SpiceDB stand-in, shared by the examples that need
//! a server they can make misbehave on demand.
//!
//! It lives in `examples/common/` rather than `examples/common.rs` on purpose:
//! the integration runner globs `examples/*.rs`, so a top-level file here would
//! be picked up as an example in its own right and fail for having no `main`.
//!
//! Why a stand-in at all: a real SpiceDB cannot be asked to return
//! `OUT_OF_RANGE` on demand (a garbage ZedToken returns `INVALID_ARGUMENT`, and
//! the in-memory datastore never collects a revision), cannot be asked to reject
//! a token with `UNAUTHENTICATED` (a bad preshared key comes back
//! `PERMISSION_DENIED`), and cannot be asked to fail transiently so a retry is
//! observable. All three were verified against `authzed/spicedb:latest` rather
//! than assumed.

#![allow(dead_code)]

use std::net::SocketAddr;
use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::Arc;

use spicedb_proto::authzed::api::v1 as proto;
use spicedb_proto::authzed::api::v1::permissions_service_server::{
    PermissionsService, PermissionsServiceServer,
};
use tokio::net::TcpListener;
use tokio_stream::wrappers::TcpListenerStream;
use tonic::transport::Server;
use tonic::{Code, Request, Response, Status};

/// The ZedToken the stand-in treats as expired or garbage-collected.
pub const STALE_TOKEN: &str = "stale-zedtoken";

/// How the stand-in should answer a check.
#[derive(Clone, Copy, PartialEq, Eq)]
pub enum CheckBehavior {
    /// Grant, except that a read pinned to [`STALE_TOKEN`] is `OUT_OF_RANGE`.
    GrantUnlessStaleToken,
    /// Always reject the caller's credentials.
    Unauthenticated,
    /// Fail the first `failures` attempts with `code`, then grant.
    FailThenGrant { failures: usize, code: Code },
    /// Answer with a permissionship value this client has never seen.
    UnknownPermissionship,
}

/// Counts every attempt the client actually made, per RPC.
#[derive(Default)]
pub struct Counts {
    pub check: AtomicUsize,
    pub write: AtomicUsize,
}

impl Counts {
    pub fn check(&self) -> usize {
        self.check.load(Ordering::SeqCst)
    }

    pub fn write(&self) -> usize {
        self.write.load(Ordering::SeqCst)
    }
}

pub struct StandIn {
    behavior: CheckBehavior,
    counts: Arc<Counts>,
}

/// Binds an ephemeral loopback port, serves the stand-in on a background task,
/// and returns its address plus the attempt counters.
pub async fn spawn(behavior: CheckBehavior) -> (SocketAddr, Arc<Counts>) {
    let counts = Arc::new(Counts::default());
    let listener = TcpListener::bind("127.0.0.1:0").await.expect("bind");
    let addr = listener.local_addr().expect("local_addr");
    let service = StandIn {
        behavior,
        counts: Arc::clone(&counts),
    };
    tokio::spawn(async move {
        Server::builder()
            .add_service(PermissionsServiceServer::new(service))
            .serve_with_incoming(TcpListenerStream::new(listener))
            .await
            .ok();
    });
    (addr, counts)
}

impl StandIn {
    fn permissionship(&self) -> i32 {
        match self.behavior {
            CheckBehavior::UnknownPermissionship => 4242,
            // 2 is PERMISSIONSHIP_HAS_PERMISSION.
            _ => 2,
        }
    }

    /// Applies the configured behavior, returning an error status when the
    /// stand-in should fail this attempt.
    #[allow(clippy::result_large_err)] // tonic::Status is what the trait returns
    fn gate(&self, at_least_as_fresh: Option<&str>) -> Result<(), Status> {
        let attempt = self.counts.check.fetch_add(1, Ordering::SeqCst) + 1;
        match self.behavior {
            CheckBehavior::GrantUnlessStaleToken => {
                if at_least_as_fresh == Some(STALE_TOKEN) {
                    return Err(Status::new(
                        Code::OutOfRange,
                        "the specified revision has expired or been garbage collected",
                    ));
                }
                Ok(())
            }
            CheckBehavior::Unauthenticated => {
                Err(Status::new(Code::Unauthenticated, "invalid token"))
            }
            CheckBehavior::FailThenGrant { failures, code } => {
                if attempt <= failures {
                    return Err(Status::new(code, "transient, from the stand-in"));
                }
                Ok(())
            }
            CheckBehavior::UnknownPermissionship => Ok(()),
        }
    }
}

type ProtoStream<T> = tokio_stream::Iter<std::vec::IntoIter<Result<T, Status>>>;

#[tonic::async_trait]
impl PermissionsService for StandIn {
    async fn check_permission(
        &self,
        request: Request<proto::CheckPermissionRequest>,
    ) -> Result<Response<proto::CheckPermissionResponse>, Status> {
        let token = request
            .get_ref()
            .consistency
            .as_ref()
            .and_then(|c| c.requirement.as_ref())
            .and_then(|r| match r {
                proto::consistency::Requirement::AtLeastAsFresh(t) => Some(t.token.clone()),
                _ => None,
            });
        self.gate(token.as_deref())?;
        Ok(Response::new(proto::CheckPermissionResponse {
            permissionship: self.permissionship(),
            ..Default::default()
        }))
    }

    async fn check_bulk_permissions(
        &self,
        request: Request<proto::CheckBulkPermissionsRequest>,
    ) -> Result<Response<proto::CheckBulkPermissionsResponse>, Status> {
        let token = request
            .get_ref()
            .consistency
            .as_ref()
            .and_then(|c| c.requirement.as_ref())
            .and_then(|r| match r {
                proto::consistency::Requirement::AtLeastAsFresh(t) => Some(t.token.clone()),
                _ => None,
            });
        self.gate(token.as_deref())?;
        let permissionship = self.permissionship();
        let pairs = request
            .into_inner()
            .items
            .into_iter()
            .map(|_| proto::CheckBulkPermissionsPair {
                request: None,
                response: Some(proto::check_bulk_permissions_pair::Response::Item(
                    proto::CheckBulkPermissionsResponseItem {
                        permissionship,
                        ..Default::default()
                    },
                )),
            })
            .collect();
        Ok(Response::new(proto::CheckBulkPermissionsResponse {
            pairs,
            ..Default::default()
        }))
    }

    async fn write_relationships(
        &self,
        _r: Request<proto::WriteRelationshipsRequest>,
    ) -> Result<Response<proto::WriteRelationshipsResponse>, Status> {
        self.counts.write.fetch_add(1, Ordering::SeqCst);
        // Always fails, transiently. A retrying client would come back.
        Err(Status::new(Code::Unavailable, "transient, from the stand-in"))
    }

    type ReadRelationshipsStream = ProtoStream<proto::ReadRelationshipsResponse>;
    type LookupResourcesStream = ProtoStream<proto::LookupResourcesResponse>;
    type LookupSubjectsStream = ProtoStream<proto::LookupSubjectsResponse>;
    type ExportBulkRelationshipsStream = ProtoStream<proto::ExportBulkRelationshipsResponse>;

    async fn read_relationships(
        &self,
        _r: Request<proto::ReadRelationshipsRequest>,
    ) -> Result<Response<Self::ReadRelationshipsStream>, Status> {
        unimplemented!("not used by these examples")
    }
    async fn delete_relationships(
        &self,
        _r: Request<proto::DeleteRelationshipsRequest>,
    ) -> Result<Response<proto::DeleteRelationshipsResponse>, Status> {
        unimplemented!("not used by these examples")
    }
    async fn expand_permission_tree(
        &self,
        _r: Request<proto::ExpandPermissionTreeRequest>,
    ) -> Result<Response<proto::ExpandPermissionTreeResponse>, Status> {
        unimplemented!("not used by these examples")
    }
    async fn lookup_resources(
        &self,
        _r: Request<proto::LookupResourcesRequest>,
    ) -> Result<Response<Self::LookupResourcesStream>, Status> {
        unimplemented!("not used by these examples")
    }
    async fn lookup_subjects(
        &self,
        _r: Request<proto::LookupSubjectsRequest>,
    ) -> Result<Response<Self::LookupSubjectsStream>, Status> {
        unimplemented!("not used by these examples")
    }
    async fn import_bulk_relationships(
        &self,
        _r: Request<tonic::Streaming<proto::ImportBulkRelationshipsRequest>>,
    ) -> Result<Response<proto::ImportBulkRelationshipsResponse>, Status> {
        unimplemented!("not used by these examples")
    }
    async fn export_bulk_relationships(
        &self,
        _r: Request<proto::ExportBulkRelationshipsRequest>,
    ) -> Result<Response<Self::ExportBulkRelationshipsStream>, Status> {
        unimplemented!("not used by these examples")
    }
}
