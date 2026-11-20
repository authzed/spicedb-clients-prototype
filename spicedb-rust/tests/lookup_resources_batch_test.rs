//! `LookupResourcesOptions::with_response_batch_size` wraps the proto's
//! `optional_response_batch_size` request field and the
//! `LookupResourcesResponse.batched`/`LookupResourcesBatch`/
//! `LookupResourcesResult` messages: a batched response is flattened into the
//! same `LookupResource` sequence a one-resource-per-message response would
//! yield, sharing the batch's single cursor, and a malformed result inside a
//! batch fails the call exactly like a malformed singular response.

mod support;

use std::num::NonZeroU32;

use futures::StreamExt;
use spicedb::client::SpiceDBClient;
use spicedb::consistency;
use spicedb::types::Permissionship;
use spicedb::SpiceDBError;
use spicedb_proto::authzed::api::v1 as proto;

use support::{spawn_permissions_server, MockPermissionsService};

async fn connect(mock: MockPermissionsService) -> SpiceDBClient {
    let addr = spawn_permissions_server(mock).await;
    SpiceDBClient::new_plaintext(
        addr.to_string(),
        "token",
        spicedb::RemoteCredentialsPolicy::LoopbackOnly,
    )
    .await
    .expect("client should connect to mock server")
}

fn request(options: spicedb::LookupResourcesOptions) -> spicedb::LookupResourcesRequest {
    spicedb::LookupResourcesRequest::new(
        "document",
        "view",
        spicedb::SubjectRef::direct(spicedb::ObjectRef::new("user", "alice").unwrap()),
        consistency::full(),
    )
    .with_options(options)
}

#[tokio::test]
async fn response_batch_size_is_unset_by_default_and_reaches_the_wire_when_set() {
    let mock = MockPermissionsService::new();
    mock.push_lookup_resources_page(Vec::new());
    mock.push_lookup_resources_page(Vec::new());
    let batch_sizes = mock.lookup_resources_batch_sizes();
    let client = connect(mock).await;

    let stream = client.lookup_resources(request(spicedb::LookupResourcesOptions::new()));
    tokio::pin!(stream);
    assert!(stream.next().await.is_none());

    let stream = client.lookup_resources(request(
        spicedb::LookupResourcesOptions::new()
            .with_response_batch_size(NonZeroU32::new(1000).unwrap()),
    ));
    tokio::pin!(stream);
    assert!(stream.next().await.is_none());

    assert_eq!(*batch_sizes.lock().unwrap(), vec![0, 1000]);
}

#[tokio::test]
async fn batched_response_flattens_into_results_sharing_one_cursor() {
    let mock = MockPermissionsService::new();
    mock.push_lookup_resources_page(vec![proto::LookupResourcesResponse {
        looked_up_at: Some(proto::ZedToken {
            token: "snapshot".into(),
        }),
        batched: Some(proto::LookupResourcesBatch {
            results: vec![
                proto::LookupResourcesResult {
                    resource_object_id: "doc1".into(),
                    permissionship: proto::LookupPermissionship::HasPermission as i32,
                    partial_caveat_info: None,
                },
                proto::LookupResourcesResult {
                    resource_object_id: "doc2".into(),
                    permissionship: proto::LookupPermissionship::ConditionalPermission as i32,
                    partial_caveat_info: Some(proto::PartialCaveatInfo {
                        missing_required_context: vec!["ip_address".into()],
                    }),
                },
            ],
            after_cursor: Some(proto::Cursor {
                token: "batch-1".into(),
            }),
        }),
        ..Default::default()
    }]);
    let client = connect(mock).await;

    let stream = client.lookup_resources(request(
        spicedb::LookupResourcesOptions::new()
            .with_response_batch_size(NonZeroU32::new(1000).unwrap()),
    ));
    tokio::pin!(stream);
    let got: Vec<_> = stream.map(|r| r.unwrap()).collect().await;

    assert_eq!(got.len(), 2);
    assert_eq!(got[0].resource_id, "doc1");
    assert_eq!(got[0].permissionship, Permissionship::HasPermission);
    assert_eq!(got[0].partial_caveat, None);
    assert_eq!(got[0].after_result_cursor.as_deref(), Some("batch-1"));

    assert_eq!(got[1].resource_id, "doc2");
    assert_eq!(got[1].permissionship, Permissionship::ConditionalPermission);
    assert_eq!(
        got[1]
            .partial_caveat
            .as_ref()
            .unwrap()
            .missing_required_context,
        vec!["ip_address".to_string()]
    );
    assert_eq!(got[1].after_result_cursor.as_deref(), Some("batch-1"));
}

#[tokio::test]
async fn a_malformed_result_inside_a_batch_fails_the_whole_call() {
    let mock = MockPermissionsService::new();
    mock.push_lookup_resources_page(vec![proto::LookupResourcesResponse {
        batched: Some(proto::LookupResourcesBatch {
            results: vec![
                proto::LookupResourcesResult {
                    resource_object_id: "doc1".into(),
                    permissionship: proto::LookupPermissionship::HasPermission as i32,
                    partial_caveat_info: None,
                },
                proto::LookupResourcesResult {
                    resource_object_id: String::new(),
                    permissionship: proto::LookupPermissionship::HasPermission as i32,
                    partial_caveat_info: None,
                },
            ],
            after_cursor: Some(proto::Cursor {
                token: "batch-1".into(),
            }),
        }),
        ..Default::default()
    }]);
    let client = connect(mock).await;

    let stream = client.lookup_resources(request(
        spicedb::LookupResourcesOptions::new()
            .with_response_batch_size(NonZeroU32::new(1000).unwrap()),
    ));
    tokio::pin!(stream);
    let mut yields = 0;
    let mut err: Option<SpiceDBError> = None;
    while let Some(result) = stream.next().await {
        match result {
            Ok(_) => yields += 1,
            Err(e) => {
                err = Some(e);
                break;
            }
        }
    }
    assert_eq!(yields, 1);
    let err = err.expect("call should fail on the malformed second result");
    assert_eq!(err.code(), tonic::Code::Internal, "{err}");
    assert!(format!("{err}").contains("resource object ID"), "{err}");
}
