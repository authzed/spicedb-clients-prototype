//! Reusable in-process mock gRPC server harness for the integration tests.
//!
//! This module stands up real, in-process tonic servers bound to an ephemeral
//! loopback port so the idiomatic client can connect to them over gRPC exactly
//! as it would to a real SpiceDB. It exists primarily to exercise behaviors
//! that require a live server — most importantly, open-ended server streams
//! (watch) that must be delivered incrementally rather than buffered.
//!
//! ## Extending
//!
//! This harness is intended to grow. To add coverage for another service
//! (e.g. a mock `SchemaService` or `ExperimentalService`):
//!
//! 1. Add a `MockXxxService` struct here and implement the corresponding
//!    generated `xxx_service_server::XxxService` trait from
//!    `spicedb_proto::authzed::api::v1`.
//! 2. Build a router with `Server::builder().add_service(...)` (chain
//!    `.add_service()` for multiple services) and hand it to [`spawn_server`].
//!
//! [`spawn_server`] is service-agnostic on purpose so new services reuse the
//! same bind/spawn/address-readback plumbing. [`MockPermissionsService`] is a
//! worked example that mocks a subset of a multi-RPC service (only the RPCs
//! that have behavioral tests implement real logic; the rest `unimplemented!()`).

#![allow(dead_code)]

use std::collections::VecDeque;
use std::net::SocketAddr;
use std::pin::Pin;
use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::{Arc, Mutex};

use futures::Stream;
use tokio::net::TcpListener;
use tokio_stream::wrappers::TcpListenerStream;
use tonic::transport::server::Router;
use tonic::transport::Server;
use tonic::{Request, Response, Status};

use spicedb_proto::authzed::api::v1 as proto;
use spicedb_proto::authzed::api::v1::permissions_service_server::{
    PermissionsService, PermissionsServiceServer,
};
use spicedb_proto::authzed::api::v1::watch_service_server::{WatchService, WatchServiceServer};

/// Binds an ephemeral loopback port, serves `router` on a background task, and
/// returns the bound address.
///
/// The listener is bound *before* the serving task is spawned, so the OS accept
/// queue holds any incoming connection until the server begins accepting — this
/// removes the client-connects-before-server-ready race. Connect a client with
/// `SpiceDBClient::new_plaintext(addr.to_string(), "token")`.
pub async fn spawn_server(router: Router) -> SocketAddr {
    let listener = TcpListener::bind("127.0.0.1:0")
        .await
        .expect("bind ephemeral loopback port");
    let addr = listener.local_addr().expect("read bound local address");
    let incoming = TcpListenerStream::new(listener);
    tokio::spawn(async move {
        router
            .serve_with_incoming(incoming)
            .await
            .expect("mock gRPC server should serve");
    });
    addr
}

/// Convenience wrapper: serves a single [`MockWatchService`] and returns its
/// address.
pub async fn spawn_watch_server(mock: MockWatchService) -> SocketAddr {
    let router = Server::builder().add_service(WatchServiceServer::new(mock));
    spawn_server(router).await
}

/// Server-streaming response type for the mock watch method.
type WatchResponseStream = Pin<Box<dyn Stream<Item = Result<proto::WatchResponse, Status>> + Send>>;

/// A configurable mock implementation of SpiceDB's `WatchService`.
///
/// It emits a fixed set of [`proto::WatchResponse`]s in order. When
/// `keep_open` is set, the response stream never terminates after emitting
/// them — emulating a live, open-ended watch. That is precisely the condition
/// that proves the client yields updates incrementally instead of buffering
/// until the stream closes (which, for a live watch, is never).
///
/// `fail_establish_times` lets a test simulate transient failures *during
/// stream establishment* (the initial `Watch` call returning `Err` before any
/// stream is returned): the first N calls return the given `Status`, and the
/// (N+1)th call succeeds and streams `responses` as usual.
pub struct MockWatchService {
    responses: Vec<proto::WatchResponse>,
    keep_open: bool,
    fail_establish_times: usize,
    fail_establish_status: Status,
    establish_calls: Arc<AtomicUsize>,
}

impl MockWatchService {
    /// Creates a mock that emits `responses` in order.
    ///
    /// If `keep_open` is `true`, the stream stays open forever after emitting
    /// them; if `false`, the stream closes once they are exhausted.
    pub fn new(responses: Vec<proto::WatchResponse>, keep_open: bool) -> Self {
        Self {
            responses,
            keep_open,
            fail_establish_times: 0,
            fail_establish_status: Status::unavailable("unused"),
            establish_calls: Arc::new(AtomicUsize::new(0)),
        }
    }

    /// Makes the first `times` `Watch` calls fail immediately with `status`
    /// (i.e. establishment fails, no stream is returned); the call after that
    /// succeeds and streams `responses` as usual.
    pub fn fail_establish_times(mut self, times: usize, status: Status) -> Self {
        self.fail_establish_times = times;
        self.fail_establish_status = status;
        self
    }

    /// Returns a live handle to the number of `Watch` establishment attempts
    /// received so far (including failed ones). Grab this *before* moving the
    /// mock into [`spawn_watch_server`].
    pub fn establish_calls(&self) -> Arc<AtomicUsize> {
        self.establish_calls.clone()
    }
}

#[tonic::async_trait]
impl WatchService for MockWatchService {
    type WatchStream = WatchResponseStream;

    async fn watch(
        &self,
        _request: Request<proto::WatchRequest>,
    ) -> Result<Response<Self::WatchStream>, Status> {
        let call_index = self.establish_calls.fetch_add(1, Ordering::SeqCst);
        if call_index < self.fail_establish_times {
            return Err(self.fail_establish_status.clone());
        }

        let responses = self.responses.clone();
        let keep_open = self.keep_open;
        let stream = async_stream::stream! {
            for resp in responses {
                yield Ok(resp);
            }
            if keep_open {
                // Never terminate: emulate a live, open-ended watch stream.
                futures::future::pending::<()>().await;
            }
        };
        Ok(Response::new(Box::pin(stream)))
    }
}

/// Convenience wrapper: serves a single [`MockPermissionsService`] and
/// returns its address.
pub async fn spawn_permissions_server(mock: MockPermissionsService) -> SocketAddr {
    let router = Server::builder().add_service(PermissionsServiceServer::new(mock));
    spawn_server(router).await
}

/// Server-streaming response type shared by the mock's paginating RPCs.
type ProtoResponseStream<T> = Pin<Box<dyn Stream<Item = Result<T, Status>> + Send>>;

/// A configurable mock implementation of SpiceDB's `PermissionsService`.
///
/// Only the RPCs exercised by the behavioral test suites — `ReadRelationships`,
/// `LookupResources`, `LookupSubjects`, `ExportBulkRelationships` (all used by
/// `tests/read_stream_test.rs`), `DeleteRelationships` (used by
/// `tests/delete_relationships_test.rs`), and `CheckBulkPermissions` (used by
/// `tests/check_bulk_permissions_test.rs`) — have real behavior; the rest of
/// the trait is `unimplemented!()` since nothing in this harness calls them.
///
/// The paginating RPCs (`read_relationships`, `lookup_resources`,
/// `export_bulk_relationships`) are configured with a *queue of pages*: each
/// incoming request pops and streams the next page in the queue, and the
/// server-side call count is tracked so tests can prove the client only
/// issues a second request after the first page has been fully drained (i.e.
/// that pagination is real and lazy, not a client-side illusion over a
/// single buffered fetch).
pub struct MockPermissionsService {
    read_relationships_pages: Mutex<VecDeque<Vec<proto::ReadRelationshipsResponse>>>,
    read_relationships_calls: Arc<AtomicUsize>,
    read_relationships_cursors: Arc<Mutex<Vec<Option<proto::Cursor>>>>,

    lookup_resources_pages: Mutex<VecDeque<Vec<proto::LookupResourcesResponse>>>,
    lookup_resources_calls: Arc<AtomicUsize>,
    lookup_resources_cursors: Arc<Mutex<Vec<Option<proto::Cursor>>>>,

    lookup_subjects_responses: Mutex<Vec<proto::LookupSubjectsResponse>>,
    lookup_subjects_calls: Arc<AtomicUsize>,

    export_relationships_pages: Mutex<VecDeque<Vec<proto::ExportBulkRelationshipsResponse>>>,
    export_relationships_calls: Arc<AtomicUsize>,
    export_relationships_cursors: Arc<Mutex<Vec<Option<proto::Cursor>>>>,

    delete_relationships_responses: Mutex<VecDeque<proto::DeleteRelationshipsResponse>>,
    delete_relationships_calls: Arc<AtomicUsize>,
    delete_relationships_requests: Arc<Mutex<Vec<proto::DeleteRelationshipsRequest>>>,

    check_bulk_permissions_responses: Mutex<VecDeque<proto::CheckBulkPermissionsResponse>>,
    check_bulk_permissions_calls: Arc<AtomicUsize>,
    check_bulk_permissions_requests: Arc<Mutex<Vec<proto::CheckBulkPermissionsRequest>>>,
    check_bulk_permissions_fail: Mutex<VecDeque<Status>>,
    /// If set, `check_bulk_permissions` sleeps this long before responding --
    /// simulates a wedged server that accepts the call but never answers
    /// within any sensible deadline. Used by `tests/deadline_test.rs`.
    check_bulk_permissions_stall: Mutex<Option<std::time::Duration>>,

    write_relationships_calls: Arc<AtomicUsize>,
    write_relationships_fail: Mutex<VecDeque<Status>>,

    /// If set, `read_relationships` sleeps this long before streaming its
    /// page -- proves a streaming call outlives a tiny unary
    /// `default_timeout` instead of inheriting it. Used by
    /// `tests/deadline_test.rs`.
    read_relationships_stall: Mutex<Option<std::time::Duration>>,

    import_bulk_relationships_calls: Arc<AtomicUsize>,
    /// If set, `import_bulk_relationships` sleeps this long -- after fully
    /// draining the client-streaming request -- before responding. Proves
    /// `import_relationships` (client-streaming) is neither bound by the
    /// tiny unary `default_timeout` nor unresponsive to an explicit
    /// `import_relationships_with_timeout`. Used by `tests/deadline_test.rs`.
    import_bulk_relationships_stall: Mutex<Option<std::time::Duration>>,
}

impl MockPermissionsService {
    /// Creates an empty mock. Configure it with the `push_*`/`with_*` methods
    /// below before handing it to [`spawn_permissions_server`].
    pub fn new() -> Self {
        Self {
            read_relationships_pages: Mutex::new(VecDeque::new()),
            read_relationships_calls: Arc::new(AtomicUsize::new(0)),
            read_relationships_cursors: Arc::new(Mutex::new(Vec::new())),

            lookup_resources_pages: Mutex::new(VecDeque::new()),
            lookup_resources_calls: Arc::new(AtomicUsize::new(0)),
            lookup_resources_cursors: Arc::new(Mutex::new(Vec::new())),

            lookup_subjects_responses: Mutex::new(Vec::new()),
            lookup_subjects_calls: Arc::new(AtomicUsize::new(0)),

            export_relationships_pages: Mutex::new(VecDeque::new()),
            export_relationships_calls: Arc::new(AtomicUsize::new(0)),
            export_relationships_cursors: Arc::new(Mutex::new(Vec::new())),

            delete_relationships_responses: Mutex::new(VecDeque::new()),
            delete_relationships_calls: Arc::new(AtomicUsize::new(0)),
            delete_relationships_requests: Arc::new(Mutex::new(Vec::new())),

            check_bulk_permissions_responses: Mutex::new(VecDeque::new()),
            check_bulk_permissions_calls: Arc::new(AtomicUsize::new(0)),
            check_bulk_permissions_requests: Arc::new(Mutex::new(Vec::new())),
            check_bulk_permissions_fail: Mutex::new(VecDeque::new()),
            check_bulk_permissions_stall: Mutex::new(None),

            write_relationships_calls: Arc::new(AtomicUsize::new(0)),
            write_relationships_fail: Mutex::new(VecDeque::new()),

            read_relationships_stall: Mutex::new(None),

            import_bulk_relationships_calls: Arc::new(AtomicUsize::new(0)),
            import_bulk_relationships_stall: Mutex::new(None),
        }
    }

    /// Queues one page (i.e. one `ReadRelationships` server call's worth) of
    /// responses. Pages are popped and streamed in the order they were
    /// pushed.
    pub fn push_read_relationships_page(&self, page: Vec<proto::ReadRelationshipsResponse>) {
        self.read_relationships_pages
            .lock()
            .unwrap()
            .push_back(page);
    }

    /// Returns a live handle to the `ReadRelationships` call counter. Grab
    /// this *before* moving the mock into [`spawn_permissions_server`].
    pub fn read_relationships_calls(&self) -> Arc<AtomicUsize> {
        self.read_relationships_calls.clone()
    }

    /// Returns a live handle to the cursors received on each
    /// `ReadRelationships` call, in call order.
    pub fn read_relationships_cursors(&self) -> Arc<Mutex<Vec<Option<proto::Cursor>>>> {
        self.read_relationships_cursors.clone()
    }

    /// Queues one page of `LookupResources` responses.
    pub fn push_lookup_resources_page(&self, page: Vec<proto::LookupResourcesResponse>) {
        self.lookup_resources_pages.lock().unwrap().push_back(page);
    }

    /// Returns a live handle to the `LookupResources` call counter.
    pub fn lookup_resources_calls(&self) -> Arc<AtomicUsize> {
        self.lookup_resources_calls.clone()
    }

    /// Returns a live handle to the cursors received on each
    /// `LookupResources` call, in call order.
    pub fn lookup_resources_cursors(&self) -> Arc<Mutex<Vec<Option<proto::Cursor>>>> {
        self.lookup_resources_cursors.clone()
    }

    /// Sets the single, fixed set of `LookupSubjects` responses (no
    /// pagination exists for this RPC).
    pub fn set_lookup_subjects_responses(&self, responses: Vec<proto::LookupSubjectsResponse>) {
        *self.lookup_subjects_responses.lock().unwrap() = responses;
    }

    /// Returns a live handle to the `LookupSubjects` call counter.
    pub fn lookup_subjects_calls(&self) -> Arc<AtomicUsize> {
        self.lookup_subjects_calls.clone()
    }

    /// Queues one page of `ExportBulkRelationships` responses (a page may
    /// itself be split across multiple streamed messages).
    pub fn push_export_relationships_page(
        &self,
        page: Vec<proto::ExportBulkRelationshipsResponse>,
    ) {
        self.export_relationships_pages
            .lock()
            .unwrap()
            .push_back(page);
    }

    /// Returns a live handle to the `ExportBulkRelationships` call counter.
    pub fn export_relationships_calls(&self) -> Arc<AtomicUsize> {
        self.export_relationships_calls.clone()
    }

    /// Returns a live handle to the cursors received on each
    /// `ExportBulkRelationships` call, in call order.
    pub fn export_relationships_cursors(&self) -> Arc<Mutex<Vec<Option<proto::Cursor>>>> {
        self.export_relationships_cursors.clone()
    }

    /// Queues one `DeleteRelationships` response, popped and returned in the
    /// order pushed. If the queue is exhausted, the mock falls back to a
    /// default `DELETION_PROGRESS_COMPLETE` response — so single-call tests
    /// that don't care about the response body don't need to push one.
    pub fn push_delete_relationships_response(&self, resp: proto::DeleteRelationshipsResponse) {
        self.delete_relationships_responses
            .lock()
            .unwrap()
            .push_back(resp);
    }

    /// Returns a live handle to the `DeleteRelationships` call counter. Grab
    /// this *before* moving the mock into [`spawn_permissions_server`].
    pub fn delete_relationships_calls(&self) -> Arc<AtomicUsize> {
        self.delete_relationships_calls.clone()
    }

    /// Returns a live handle to the full `DeleteRelationshipsRequest` received
    /// on each call, in call order — lets tests assert on the filter,
    /// preconditions, and limit the client actually sent.
    pub fn delete_relationships_requests(
        &self,
    ) -> Arc<Mutex<Vec<proto::DeleteRelationshipsRequest>>> {
        self.delete_relationships_requests.clone()
    }

    /// Queues one `CheckBulkPermissions` response, popped and returned in the
    /// order pushed. If the queue is exhausted, the mock falls back to a
    /// default (empty-pairs) response.
    pub fn push_check_bulk_permissions_response(&self, resp: proto::CheckBulkPermissionsResponse) {
        self.check_bulk_permissions_responses
            .lock()
            .unwrap()
            .push_back(resp);
    }

    /// Returns a live handle to the `CheckBulkPermissions` call counter. Grab
    /// this *before* moving the mock into [`spawn_permissions_server`].
    pub fn check_bulk_permissions_calls(&self) -> Arc<AtomicUsize> {
        self.check_bulk_permissions_calls.clone()
    }

    /// Returns a live handle to the full `CheckBulkPermissionsRequest`
    /// received on each call, in call order.
    pub fn check_bulk_permissions_requests(
        &self,
    ) -> Arc<Mutex<Vec<proto::CheckBulkPermissionsRequest>>> {
        self.check_bulk_permissions_requests.clone()
    }

    /// Queues a `CheckBulkPermissions` failure: the next call returns `status`
    /// instead of a response. Queue multiple to fail several calls in a row;
    /// once exhausted, calls fall through to the normal response queue. Used
    /// to prove reads are retried on a transient error.
    pub fn queue_check_bulk_permissions_failure(&self, status: Status) {
        self.check_bulk_permissions_fail
            .lock()
            .unwrap()
            .push_back(status);
    }

    /// Makes every subsequent `CheckBulkPermissions` call sleep `duration`
    /// before responding -- simulates a wedged server. Used to prove a
    /// unary call actually times out instead of hanging.
    pub fn stall_check_bulk_permissions(&self, duration: std::time::Duration) {
        *self.check_bulk_permissions_stall.lock().unwrap() = Some(duration);
    }

    /// Makes every subsequent `ReadRelationships` call sleep `duration`
    /// before streaming its page -- proves a streaming call is not bound by
    /// the tiny unary default used to prove the unary case times out.
    pub fn stall_read_relationships(&self, duration: std::time::Duration) {
        *self.read_relationships_stall.lock().unwrap() = Some(duration);
    }

    /// Makes the next `ImportBulkRelationships` call sleep `duration` --
    /// after fully draining the client-streaming request -- before
    /// responding. Simulates a wedged server on the bulk-import path.
    pub fn stall_import_bulk_relationships(&self, duration: std::time::Duration) {
        *self.import_bulk_relationships_stall.lock().unwrap() = Some(duration);
    }

    /// Returns a live handle to the `ImportBulkRelationships` call counter.
    /// Grab this *before* moving the mock into [`spawn_permissions_server`].
    pub fn import_bulk_relationships_calls(&self) -> Arc<AtomicUsize> {
        self.import_bulk_relationships_calls.clone()
    }

    /// Returns a live handle to the `WriteRelationships` call counter. Grab
    /// this *before* moving the mock into [`spawn_permissions_server`].
    pub fn write_relationships_calls(&self) -> Arc<AtomicUsize> {
        self.write_relationships_calls.clone()
    }

    /// Queues a `WriteRelationships` failure: the next call returns `status`
    /// instead of a response. Used to prove mutations are attempted exactly
    /// once even on a retryable error.
    pub fn queue_write_relationships_failure(&self, status: Status) {
        self.write_relationships_fail
            .lock()
            .unwrap()
            .push_back(status);
    }
}

impl Default for MockPermissionsService {
    fn default() -> Self {
        Self::new()
    }
}

#[tonic::async_trait]
impl PermissionsService for MockPermissionsService {
    type ReadRelationshipsStream = ProtoResponseStream<proto::ReadRelationshipsResponse>;

    async fn read_relationships(
        &self,
        request: Request<proto::ReadRelationshipsRequest>,
    ) -> Result<Response<Self::ReadRelationshipsStream>, Status> {
        self.read_relationships_calls.fetch_add(1, Ordering::SeqCst);
        self.read_relationships_cursors
            .lock()
            .unwrap()
            .push(request.into_inner().optional_cursor);
        let page = self
            .read_relationships_pages
            .lock()
            .unwrap()
            .pop_front()
            .unwrap_or_default();
        let stall = *self.read_relationships_stall.lock().unwrap();
        let stream = async_stream::stream! {
            if let Some(d) = stall {
                tokio::time::sleep(d).await;
            }
            for resp in page {
                yield Ok(resp);
            }
        };
        Ok(Response::new(Box::pin(stream)))
    }

    async fn write_relationships(
        &self,
        _request: Request<proto::WriteRelationshipsRequest>,
    ) -> Result<Response<proto::WriteRelationshipsResponse>, Status> {
        self.write_relationships_calls
            .fetch_add(1, Ordering::SeqCst);
        if let Some(status) = self.write_relationships_fail.lock().unwrap().pop_front() {
            return Err(status);
        }
        Ok(Response::new(proto::WriteRelationshipsResponse {
            written_at: Some(proto::ZedToken {
                token: "rev-default".to_string(),
            }),
        }))
    }

    async fn delete_relationships(
        &self,
        request: Request<proto::DeleteRelationshipsRequest>,
    ) -> Result<Response<proto::DeleteRelationshipsResponse>, Status> {
        self.delete_relationships_calls
            .fetch_add(1, Ordering::SeqCst);
        self.delete_relationships_requests
            .lock()
            .unwrap()
            .push(request.into_inner());
        let resp = self
            .delete_relationships_responses
            .lock()
            .unwrap()
            .pop_front()
            .unwrap_or(proto::DeleteRelationshipsResponse {
                deleted_at: Some(proto::ZedToken {
                    token: "rev-default".to_string(),
                }),
                deletion_progress: proto::delete_relationships_response::DeletionProgress::Complete
                    as i32,
                relationships_deleted_count: 0,
            });
        Ok(Response::new(resp))
    }

    async fn check_permission(
        &self,
        _request: Request<proto::CheckPermissionRequest>,
    ) -> Result<Response<proto::CheckPermissionResponse>, Status> {
        unimplemented!("not exercised by the read-stream behavioral tests")
    }

    async fn check_bulk_permissions(
        &self,
        request: Request<proto::CheckBulkPermissionsRequest>,
    ) -> Result<Response<proto::CheckBulkPermissionsResponse>, Status> {
        self.check_bulk_permissions_calls
            .fetch_add(1, Ordering::SeqCst);
        self.check_bulk_permissions_requests
            .lock()
            .unwrap()
            .push(request.into_inner());
        let stall = *self.check_bulk_permissions_stall.lock().unwrap();
        if let Some(d) = stall {
            tokio::time::sleep(d).await;
        }
        if let Some(status) = self.check_bulk_permissions_fail.lock().unwrap().pop_front() {
            return Err(status);
        }
        let resp = self
            .check_bulk_permissions_responses
            .lock()
            .unwrap()
            .pop_front()
            .unwrap_or_default();
        Ok(Response::new(resp))
    }

    async fn expand_permission_tree(
        &self,
        _request: Request<proto::ExpandPermissionTreeRequest>,
    ) -> Result<Response<proto::ExpandPermissionTreeResponse>, Status> {
        unimplemented!("not exercised by the read-stream behavioral tests")
    }

    type LookupResourcesStream = ProtoResponseStream<proto::LookupResourcesResponse>;

    async fn lookup_resources(
        &self,
        request: Request<proto::LookupResourcesRequest>,
    ) -> Result<Response<Self::LookupResourcesStream>, Status> {
        self.lookup_resources_calls.fetch_add(1, Ordering::SeqCst);
        self.lookup_resources_cursors
            .lock()
            .unwrap()
            .push(request.into_inner().optional_cursor);
        let page = self
            .lookup_resources_pages
            .lock()
            .unwrap()
            .pop_front()
            .unwrap_or_default();
        let stream = async_stream::stream! {
            for resp in page {
                yield Ok(resp);
            }
        };
        Ok(Response::new(Box::pin(stream)))
    }

    type LookupSubjectsStream = ProtoResponseStream<proto::LookupSubjectsResponse>;

    async fn lookup_subjects(
        &self,
        _request: Request<proto::LookupSubjectsRequest>,
    ) -> Result<Response<Self::LookupSubjectsStream>, Status> {
        self.lookup_subjects_calls.fetch_add(1, Ordering::SeqCst);
        let responses = self.lookup_subjects_responses.lock().unwrap().clone();
        let stream = async_stream::stream! {
            for resp in responses {
                yield Ok(resp);
            }
        };
        Ok(Response::new(Box::pin(stream)))
    }

    async fn import_bulk_relationships(
        &self,
        request: Request<tonic::Streaming<proto::ImportBulkRelationshipsRequest>>,
    ) -> Result<Response<proto::ImportBulkRelationshipsResponse>, Status> {
        self.import_bulk_relationships_calls
            .fetch_add(1, Ordering::SeqCst);

        // Drain the client-streaming request fully before ever consulting
        // the stall -- a real server can't respond mid-stream, so a stall
        // here must be *after* the last chunk, not instead of reading it.
        let mut stream = request.into_inner();
        let mut num_loaded: u64 = 0;
        while let Some(chunk) = stream.message().await? {
            num_loaded += chunk.relationships.len() as u64;
        }

        let stall = *self.import_bulk_relationships_stall.lock().unwrap();
        if let Some(d) = stall {
            tokio::time::sleep(d).await;
        }

        Ok(Response::new(proto::ImportBulkRelationshipsResponse {
            num_loaded,
        }))
    }

    type ExportBulkRelationshipsStream =
        ProtoResponseStream<proto::ExportBulkRelationshipsResponse>;

    async fn export_bulk_relationships(
        &self,
        request: Request<proto::ExportBulkRelationshipsRequest>,
    ) -> Result<Response<Self::ExportBulkRelationshipsStream>, Status> {
        self.export_relationships_calls
            .fetch_add(1, Ordering::SeqCst);
        self.export_relationships_cursors
            .lock()
            .unwrap()
            .push(request.into_inner().optional_cursor);
        let page = self
            .export_relationships_pages
            .lock()
            .unwrap()
            .pop_front()
            .unwrap_or_default();
        let stream = async_stream::stream! {
            for resp in page {
                yield Ok(resp);
            }
        };
        Ok(Response::new(Box::pin(stream)))
    }
}
