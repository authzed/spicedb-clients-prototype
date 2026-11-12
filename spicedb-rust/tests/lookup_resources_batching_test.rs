//! `LookupResourcesOptions::with_response_batch_size` is a wire-level grouping
//! hint: the server may return `LookupResourcesResponse.batched` instead of
//! the singular per-message fields, but this client still yields one
//! `LookupResource` per result, with every item of one batch sharing that
//! batch's cursor (the same way `export_relationships` shares one cursor
//! across a response's relationships).

mod support;

use futures::StreamExt;
use spicedb::consistency;
use spicedb_proto::authzed::api::v1 as proto;

use support::{spawn_permissions_server, MockPermissionsService};

async fn connect(mock: MockPermissionsService) -> spicedb::client::SpiceDBClient {
    let addr = spawn_permissions_server(mock).await;
    spicedb::client::SpiceDBClient::new_plaintext(
        addr.to_string(),
        "token",
        spicedb::RemoteCredentialsPolicy::LoopbackOnly,
    )
    .await
    .expect("client should connect to mock server")
}

fn batch_item(
    id: &str,
    permissionship: proto::LookupPermissionship,
) -> proto::LookupResourcesResult {
    proto::LookupResourcesResult {
        resource_object_id: id.into(),
        permissionship: permissionship as i32,
        partial_caveat_info: None,
    }
}

#[tokio::test]
async fn lookup_resources_unbatches_grouped_results_sharing_one_cursor() {
    let mock = MockPermissionsService::new();
    mock.push_lookup_resources_page(vec![proto::LookupResourcesResponse {
        looked_up_at: Some(proto::ZedToken {
            token: "rev-1".into(),
        }),
        batched: Some(proto::LookupResourcesBatch {
            results: vec![
                batch_item("a", proto::LookupPermissionship::HasPermission),
                batch_item("b", proto::LookupPermissionship::HasPermission),
                batch_item("c", proto::LookupPermissionship::HasPermission),
            ],
            after_cursor: Some(proto::Cursor {
                token: "batch-1-end".into(),
            }),
        }),
        ..Default::default()
    }]);
    let client = connect(mock).await;

    let stream = client.lookup_resources(spicedb::LookupResourcesRequest::new(
        "document",
        "view",
        spicedb::SubjectRef::direct(spicedb::ObjectRef::new("user", "alice").unwrap()),
        consistency::full(),
    ));
    tokio::pin!(stream);

    let mut items = Vec::new();
    while let Some(result) = stream.next().await {
        items.push(result.expect("unbatched result should not fail"));
    }

    assert_eq!(
        items
            .iter()
            .map(|i| i.resource_id.as_str())
            .collect::<Vec<_>>(),
        vec!["a", "b", "c"]
    );
    for item in &items {
        assert_eq!(item.looked_up_at.as_ref(), "rev-1", "{item:?}");
        assert_eq!(
            item.after_result_cursor.as_deref(),
            Some("batch-1-end"),
            "every item of one batch shares its cursor: {item:?}"
        );
    }
}

#[tokio::test]
async fn lookup_resources_permissionship_filter_applies_within_a_batch() {
    let mock = MockPermissionsService::new();
    mock.push_lookup_resources_page(vec![proto::LookupResourcesResponse {
        batched: Some(proto::LookupResourcesBatch {
            results: vec![
                batch_item("a", proto::LookupPermissionship::HasPermission),
                batch_item("b", proto::LookupPermissionship::ConditionalPermission),
            ],
            after_cursor: Some(proto::Cursor {
                token: "batch-1-end".into(),
            }),
        }),
        ..Default::default()
    }]);
    let client = connect(mock).await;

    let stream = client.lookup_resources(
        spicedb::LookupResourcesRequest::new(
            "document",
            "view",
            spicedb::SubjectRef::direct(spicedb::ObjectRef::new("user", "alice").unwrap()),
            consistency::full(),
        )
        .with_options(
            spicedb::LookupResourcesOptions::new()
                .with_permissionship_filter(spicedb::LookupPermissionshipFilter::HasPermissionOnly),
        ),
    );
    tokio::pin!(stream);

    let mut ids = Vec::new();
    while let Some(result) = stream.next().await {
        ids.push(result.expect("filtered result should not fail").resource_id);
    }
    assert_eq!(ids, vec!["a"]);
}

#[tokio::test]
async fn lookup_resources_fails_on_a_batch_item_without_a_resource_id() {
    let mock = MockPermissionsService::new();
    mock.push_lookup_resources_page(vec![proto::LookupResourcesResponse {
        batched: Some(proto::LookupResourcesBatch {
            results: vec![
                batch_item("a", proto::LookupPermissionship::HasPermission),
                batch_item("", proto::LookupPermissionship::HasPermission),
            ],
            after_cursor: Some(proto::Cursor {
                token: "batch-1-end".into(),
            }),
        }),
        ..Default::default()
    }]);
    let client = connect(mock).await;

    let stream = client.lookup_resources(spicedb::LookupResourcesRequest::new(
        "document",
        "view",
        spicedb::SubjectRef::direct(spicedb::ObjectRef::new("user", "alice").unwrap()),
        consistency::full(),
    ));
    tokio::pin!(stream);

    let mut items = 0;
    let mut saw_error = false;
    while let Some(result) = stream.next().await {
        match result {
            Ok(_) => items += 1,
            Err(err) => {
                assert_eq!(err.code(), tonic::Code::Internal, "{err}");
                saw_error = true;
                break;
            }
        }
    }
    assert_eq!(items, 1);
    assert!(
        saw_error,
        "a batch item without a resource ID must fail the call"
    );
}
