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
//! (e.g. a mock `PermissionsService`, `SchemaService`, or `ExperimentalService`):
//!
//! 1. Add a `MockXxxService` struct here and implement the corresponding
//!    generated `xxx_service_server::XxxService` trait from
//!    `spicedb_proto::authzed::api::v1`.
//! 2. Build a router with `Server::builder().add_service(...)` (chain
//!    `.add_service()` for multiple services) and hand it to [`spawn_server`].
//!
//! [`spawn_server`] is service-agnostic on purpose so new services reuse the
//! same bind/spawn/address-readback plumbing.

#![allow(dead_code)]

use std::net::SocketAddr;
use std::pin::Pin;

use futures::Stream;
use tokio::net::TcpListener;
use tokio_stream::wrappers::TcpListenerStream;
use tonic::transport::server::Router;
use tonic::transport::Server;
use tonic::{Request, Response, Status};

use spicedb_proto::authzed::api::v1 as proto;
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
pub struct MockWatchService {
    responses: Vec<proto::WatchResponse>,
    keep_open: bool,
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
        }
    }
}

#[tonic::async_trait]
impl WatchService for MockWatchService {
    type WatchStream = WatchResponseStream;

    async fn watch(
        &self,
        _request: Request<proto::WatchRequest>,
    ) -> Result<Response<Self::WatchStream>, Status> {
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
