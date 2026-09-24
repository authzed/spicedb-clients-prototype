//! Demonstrates `SpiceDBClient::experimental_roaring_lookup_resources` -- the
//! wrapper around the materialize-tier `RoaringLookupResourcesService`: a
//! roaring64 bitmap (RoaringFormatSpec 64-bit portable format) of the resource
//! object IDs a subject has a permission on, meant to be consumed directly by
//! a search index (e.g. OpenSearch's `bitmap` term query against a `long`
//! field) rather than decoded by this client.
//!
//! Why this example stands up its own server, like `error_mapping` and
//! `call_deadlines` do: `authzed/spicedb:latest`, the image the integration
//! job starts, does not serve
//! `authzed.api.materialize.v0.RoaringLookupResourcesService` -- verified
//! against the running container rather than assumed. A direct RPC call
//! against it comes back
//! `Unimplemented("unknown service authzed.api.materialize.v0.RoaringLookupResourcesService")`.
//! A stand-in implementing the generated `RoaringLookupResourcesService` trait
//! exercises the real request/response mapping this client added --
//! consistency/resource type/permission/subject on the way out, bitmap
//! bytes/cardinality/ZedToken on the way back -- without depending on
//! upstream server support that does not exist yet.
//!
//! Run with: `cargo run --example roaring_lookup_resources`

use std::net::SocketAddr;
use std::sync::{Arc, Mutex};
use std::time::Duration;

use spicedb::client::SpiceDBClient;
use spicedb::consistency;
use spicedb::error::SpiceDBError;

use spicedb_proto::authzed::api::materialize::v0 as materialize_proto;
use spicedb_proto::authzed::api::materialize::v0::roaring_lookup_resources_service_server::{
    RoaringLookupResourcesService, RoaringLookupResourcesServiceServer,
};
use spicedb_proto::authzed::api::v1::ZedToken;
use tokio::net::TcpListener;
use tokio_stream::wrappers::TcpListenerStream;
use tonic::transport::Server;
use tonic::{Code, Request, Response, Status};

type CapturedRequest =
    Arc<Mutex<Option<materialize_proto::ExperimentalRoaringLookupResourcesRequest>>>;

/// A minimal `RoaringLookupResourcesService` that answers only what this
/// example asks of it, and records the last request it was called with so
/// the example can assert the client built it correctly.
struct StandIn {
    last_request: CapturedRequest,
}

#[tonic::async_trait]
impl RoaringLookupResourcesService for StandIn {
    async fn experimental_roaring_lookup_resources(
        &self,
        request: Request<materialize_proto::ExperimentalRoaringLookupResourcesRequest>,
    ) -> Result<Response<materialize_proto::ExperimentalRoaringLookupResourcesResponse>, Status>
    {
        let req = request.into_inner();

        // Mirrors the real service's FAILED_PRECONDITION for a resource type
        // whose relationships carry a non-canonical object ID (e.g. "007"
        // instead of "7") -- triggered here by a sentinel permission name,
        // since this stand-in has no relationships to violate the rule for
        // real.
        if req.permission == "non-canonical-ids" {
            return Err(Status::new(
                Code::FailedPrecondition,
                "resource object id \"007\" is not a canonical 44-bit integer",
            ));
        }

        *self.last_request.lock().unwrap() = Some(req);

        Ok(Response::new(
            materialize_proto::ExperimentalRoaringLookupResourcesResponse {
                // Not a real roaring64 payload -- this stand-in only proves
                // the bytes round-trip through `bitmap` unchanged, which is
                // all this client does with them (it does not decode roaring
                // itself).
                bitmap: vec![1, 2, 3, 4],
                cardinality: 2,
                at_revision: Some(ZedToken {
                    token: "rev-1".to_string(),
                }),
            },
        ))
    }
}

async fn spawn() -> (SocketAddr, CapturedRequest) {
    let captured: CapturedRequest = Arc::new(Mutex::new(None));
    let listener = TcpListener::bind("127.0.0.1:0").await.expect("bind");
    let addr = listener.local_addr().expect("local_addr");
    let service = StandIn {
        last_request: Arc::clone(&captured),
    };
    tokio::spawn(async move {
        Server::builder()
            .add_service(RoaringLookupResourcesServiceServer::new(service))
            .serve_with_incoming(TcpListenerStream::new(listener))
            .await
            .ok();
    });
    (addr, captured)
}

#[tokio::main]
async fn main() {
    let (addr, captured) = spawn().await;
    let client = SpiceDBClient::new_plaintext(format!("{addr}"), "some-token")
        .await
        .expect("connect to the stand-in");

    let result = client
        .experimental_roaring_lookup_resources(
            &consistency::full(),
            "document",
            "view",
            "user",
            "alice",
        )
        .await
        .expect("roaring lookup failed");

    // Bytes must round-trip unchanged -- this client does not decode the
    // roaring bitmap, only passes it through.
    assert_eq!(
        result.bitmap,
        vec![1, 2, 3, 4],
        "bitmap bytes must round-trip unchanged"
    );
    assert_eq!(
        result.cardinality, 2,
        "cardinality must come from the response, not be recomputed from the bitmap"
    );
    assert_eq!(
        result.at_revision, "rev-1",
        "at_revision must be the response's ZedToken token, not the proto message itself"
    );
    println!(
        "roaring lookup: bitmap={:?}, cardinality={}, at_revision={}",
        result.bitmap, result.cardinality, result.at_revision
    );

    // What actually reached the stand-in -- proves the request was built
    // correctly, not merely that some response came back.
    let req = captured
        .lock()
        .unwrap()
        .take()
        .expect("stand-in was never called");
    assert_eq!(req.resource_object_type, "document");
    assert_eq!(req.permission, "view");
    let subject = req.subject.expect("subject must be set on the request");
    let subject_object = subject
        .object
        .expect("subject.object must be set on the request");
    assert_eq!(subject_object.object_type, "user");
    assert_eq!(subject_object.object_id, "alice");
    println!("request reached the stand-in with the fields the client built");

    // For this API to be usable, every resource object ID of the requested
    // type must be a canonical decimal integer that fits in 44 bits. A
    // resource type with a non-canonical ID fails the whole call with
    // FAILED_PRECONDITION rather than returning a partial (and therefore
    // wrong) bitmap.
    let err = client
        .experimental_roaring_lookup_resources(
            &consistency::full(),
            "document",
            "non-canonical-ids",
            "user",
            "alice",
        )
        .await
        .expect_err("a non-canonical resource object id must fail, not return a partial bitmap");
    assert!(
        matches!(err, SpiceDBError::FailedPrecondition(_)),
        "must surface as FailedPrecondition, not a generic failure: {err:?}"
    );
    println!("non-canonical resource object id: SpiceDBError::FailedPrecondition");

    // The `_with_timeout` sibling reaches the same RPC with a per-call
    // deadline, per root DESIGN.md, "RULE: A unary call must have a
    // deadline".
    let result = client
        .experimental_roaring_lookup_resources_with_timeout(
            &consistency::full(),
            "document",
            "view",
            "user",
            "alice",
            Duration::from_secs(5),
        )
        .await
        .expect("with_timeout variant failed");
    assert_eq!(result.cardinality, 2);
    println!("experimental_roaring_lookup_resources_with_timeout: works the same, with a deadline");

    println!(
        "roaring_lookup_resources: request/response mapping verified against a stand-in \
         RoaringLookupResourcesService"
    );
}
