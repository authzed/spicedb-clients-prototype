//! The per-page default deadline bounds only waiting on the server, never the
//! caller's processing time. Each test pairs a healthy fake that answers at
//! once with a caller that spends longer on the items (5 x 100 ms) than the
//! 200 ms per-page default; the read must succeed.
//!
//! Each item carries a 3 MiB ID, larger than the client's HTTP/2 stream
//! window, so the server can only send the rest of the page as
//! the client reads, as with a large real page. With small items the whole
//! page would already sit in the client's transport buffer, and a deadline
//! that wrongly counted the caller's time would never be observed.

#![allow(clippy::result_large_err)] // tonic Status is the generated service error type.

mod support;

use std::pin::Pin;
use std::time::Duration;

use futures::{Stream, StreamExt};
use spicedb::client::SpiceDBClient;
use spicedb::consistency;
use spicedb::types::Filter;
use spicedb::SpiceDBError;
use spicedb_proto::authzed::api::materialize::v0 as materialize;
use spicedb_proto::authzed::api::v1 as proto;
use tonic::{Request, Response, Status};

use support::{spawn_permissions_server, spawn_server, MockPermissionsService};

const PAGE_DEFAULT: Duration = Duration::from_millis(200);
const PER_ITEM_WORK: Duration = Duration::from_millis(100);
const ITEMS: usize = 5;

/// An object ID of about 3 MiB, unique per item.
fn big_id(i: usize) -> String {
    format!("{i}-{}", "x".repeat(3 << 20))
}
const WATCHDOG: Duration = Duration::from_secs(10);

async fn client_for(addr: std::net::SocketAddr) -> SpiceDBClient {
    SpiceDBClient::builder(addr.to_string(), "token")
        .plaintext()
        // Connect at build, untimed: a short allowance below covers the call, not connection setup.
        .connect_mode(spicedb::ConnectMode::Eager)
        .default_timeout(PAGE_DEFAULT)
        .build()
        .await
        .expect("client should connect to mock server")
}

/// Consumes every item, sleeping between them like a caller doing work, and
/// returns how many succeeded. Panics on any error.
async fn consume_slowly<T: std::fmt::Debug>(
    stream: impl Stream<Item = Result<T, SpiceDBError>>,
) -> usize {
    let started = std::time::Instant::now();
    let count = tokio::time::timeout(WATCHDOG, async {
        tokio::pin!(stream);
        let mut count = 0;
        while let Some(item) = stream.next().await {
            item.unwrap_or_else(|e| {
                panic!("the caller's processing time must not count against the page: {e:?}")
            });
            count += 1;
            tokio::time::sleep(PER_ITEM_WORK).await;
        }
        count
    })
    .await
    .expect("watchdog");
    assert!(
        started.elapsed() > PAGE_DEFAULT * 2,
        "the caller's work must outlast the per-page default for this test to mean anything"
    );
    count
}

fn relationship(id: &str) -> proto::Relationship {
    proto::Relationship {
        resource: Some(proto::ObjectReference {
            object_type: "document".into(),
            object_id: id.into(),
        }),
        relation: "viewer".into(),
        subject: Some(proto::SubjectReference {
            object: Some(proto::ObjectReference {
                object_type: "user".into(),
                object_id: "alice".into(),
            }),
            optional_relation: String::new(),
        }),
        optional_caveat: None,
        optional_expires_at: None,
    }
}

fn cursor(i: usize) -> Option<proto::Cursor> {
    Some(proto::Cursor {
        token: format!("after-{i}"),
    })
}

#[tokio::test]
async fn slow_caller_does_not_fail_a_relationship_read() {
    let mock = MockPermissionsService::new();
    mock.push_read_relationships_page(
        (0..ITEMS)
            .map(|i| proto::ReadRelationshipsResponse {
                read_at: None,
                relationship: Some(relationship(&big_id(i))),
                after_result_cursor: cursor(i),
            })
            .collect(),
    );
    let client = client_for(spawn_permissions_server(mock).await).await;
    let stream = client.read_relationships(spicedb::ReadRelationshipsRequest::new(
        Filter::new("document"),
        consistency::full(),
    ));
    assert_eq!(consume_slowly(stream).await, ITEMS);
}

#[tokio::test]
async fn slow_caller_does_not_fail_a_resource_lookup() {
    let mock = MockPermissionsService::new();
    mock.push_lookup_resources_page(
        (0..ITEMS)
            .map(|i| proto::LookupResourcesResponse {
                resource_object_id: big_id(i),
                permissionship: proto::LookupPermissionship::HasPermission as i32,
                after_result_cursor: cursor(i),
                ..Default::default()
            })
            .collect(),
    );
    let client = client_for(spawn_permissions_server(mock).await).await;
    let stream = client.lookup_resources(spicedb::LookupResourcesRequest::new(
        "document",
        "view",
        spicedb::SubjectRef::direct(spicedb::ObjectRef::new("user", "alice").unwrap()),
        consistency::full(),
    ));
    assert_eq!(consume_slowly(stream).await, ITEMS);
}

#[tokio::test]
async fn slow_caller_does_not_fail_a_subject_lookup() {
    let mock = MockPermissionsService::new();
    mock.set_lookup_subjects_responses(
        (0..ITEMS)
            .map(|i| proto::LookupSubjectsResponse {
                subject: Some(proto::ResolvedSubject {
                    subject_object_id: big_id(i),
                    permissionship: proto::LookupPermissionship::HasPermission as i32,
                    partial_caveat_info: None,
                }),
                ..Default::default()
            })
            .collect(),
    );
    let client = client_for(spawn_permissions_server(mock).await).await;
    let stream = client.lookup_subjects(spicedb::LookupSubjectsRequest::new(
        spicedb::ObjectRef::new("document", "a").unwrap(),
        "view",
        "user",
        consistency::full(),
    ));
    assert_eq!(consume_slowly(stream).await, ITEMS);
}

#[tokio::test]
async fn slow_caller_does_not_fail_an_export() {
    let mock = MockPermissionsService::new();
    // One relationship per message, so every item is a separate server read.
    mock.push_export_relationships_page(
        (0..ITEMS)
            .map(|i| proto::ExportBulkRelationshipsResponse {
                after_result_cursor: cursor(i),
                relationships: vec![relationship(&big_id(i))],
            })
            .collect(),
    );
    let client = client_for(spawn_permissions_server(mock).await).await;
    let stream =
        client.export_relationships(spicedb::ExportRelationshipsRequest::new(consistency::full()));
    assert_eq!(consume_slowly(stream).await, ITEMS);
}

type Events<T> = Pin<Box<dyn Stream<Item = Result<T, Status>> + Send>>;

/// A permission-set service whose lookup answers `ITEMS` changes at once.
struct PermissionSets;

#[tonic::async_trait]
impl materialize::watch_permission_sets_service_server::WatchPermissionSetsService
    for PermissionSets
{
    type WatchPermissionSetsStream = Events<materialize::WatchPermissionSetsResponse>;
    async fn watch_permission_sets(
        &self,
        _: Request<materialize::WatchPermissionSetsRequest>,
    ) -> Result<Response<Self::WatchPermissionSetsStream>, Status> {
        Err(Status::unimplemented("not used"))
    }
    type LookupPermissionSetsStream = Events<materialize::LookupPermissionSetsResponse>;
    async fn lookup_permission_sets(
        &self,
        _: Request<materialize::LookupPermissionSetsRequest>,
    ) -> Result<Response<Self::LookupPermissionSetsStream>, Status> {
        let items = (0..ITEMS).map(|i| {
            Ok(materialize::LookupPermissionSetsResponse {
                change: Some(materialize::PermissionSetChange {
                    at_revision: Some(proto::ZedToken {
                        token: "revision".into(),
                    }),
                    operation: 1,
                    parent_set: Some(materialize::SetReference {
                        object_type: "document".into(),
                        object_id: big_id(i),
                        permission_or_relation: "view".into(),
                    }),
                    child: None,
                }),
                cursor: None,
            })
        });
        Ok(Response::new(Box::pin(futures::stream::iter(
            items.collect::<Vec<_>>(),
        ))))
    }
    async fn download_permission_sets(
        &self,
        _: Request<materialize::DownloadPermissionSetsRequest>,
    ) -> Result<Response<materialize::DownloadPermissionSetsResponse>, Status> {
        Err(Status::unimplemented("not used"))
    }
}

#[tokio::test]
async fn slow_caller_does_not_fail_a_permission_set_lookup() {
    let router = tonic::transport::Server::builder().add_service(
        materialize::watch_permission_sets_service_server::WatchPermissionSetsServiceServer::new(
            PermissionSets,
        ),
    );
    let client = client_for(spawn_server(router).await).await;
    let stream = client.lookup_permission_sets(spicedb::LookupPermissionSetsRequest::new(
        std::num::NonZeroU32::new(10).unwrap(),
    ));
    assert_eq!(consume_slowly(stream).await, ITEMS);
}
