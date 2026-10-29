//! Paged reads, lookups and exports must fail with INTERNAL when a full page
//! ends with a cursor that is missing, empty, or equal to the cursor sent,
//! instead of re-requesting the same page forever.

mod support;

use std::sync::atomic::Ordering;

use futures::StreamExt;
use spicedb::client::SpiceDBClient;
use spicedb::consistency;
use spicedb::types::Filter;
use spicedb_proto::authzed::api::v1 as proto;

use support::{spawn_permissions_server, MockPermissionsService};

fn cursor(token: &str) -> proto::Cursor {
    proto::Cursor {
        token: token.to_string(),
    }
}

fn rel_proto(resource_id: &str) -> proto::Relationship {
    proto::Relationship {
        resource: Some(proto::ObjectReference {
            object_type: "document".into(),
            object_id: resource_id.into(),
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

/// The stalled cursors a full page can end with when the start cursor is
/// `"start"`: missing, empty, and unchanged.
fn stalled_cursors() -> Vec<Option<proto::Cursor>> {
    vec![None, Some(cursor("")), Some(cursor("start"))]
}

fn page_size() -> std::num::NonZeroU32 {
    std::num::NonZeroU32::new(2).unwrap()
}

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

/// Drains a stream, returning the number of items yielded before its error.
async fn drain_error<T>(
    stream: impl futures::Stream<Item = Result<T, spicedb::SpiceDBError>>,
) -> (usize, spicedb::SpiceDBError) {
    tokio::pin!(stream);
    let mut items = 0;
    while let Some(result) = stream.next().await {
        match result {
            Ok(_) => items += 1,
            Err(err) => return (items, err),
        }
    }
    panic!("stream ended without the cursor-advance error");
}

fn assert_stalled(
    items: usize,
    err: &spicedb::SpiceDBError,
    calls: usize,
    stalled: &Option<proto::Cursor>,
) {
    assert_eq!(items, 2, "stalled cursor {stalled:?}");
    assert_eq!(
        err.code(),
        tonic::Code::Internal,
        "stalled cursor {stalled:?}: {err}"
    );
    assert!(err.to_string().contains("did not advance"), "{err}");
    assert_eq!(calls, 1, "stalled cursor {stalled:?}");
}

#[tokio::test]
async fn read_relationships_fails_when_a_full_page_does_not_advance_the_cursor() {
    for stalled in stalled_cursors() {
        let mock = MockPermissionsService::new();
        let row = |id: &str| proto::ReadRelationshipsResponse {
            read_at: None,
            relationship: Some(rel_proto(id)),
            after_result_cursor: stalled.clone(),
        };
        mock.push_read_relationships_page(vec![row("a"), row("b")]);
        mock.push_read_relationships_page(vec![row("a"), row("b")]);
        let calls = mock.read_relationships_calls();
        let client = connect(mock).await;
        let request =
            spicedb::ReadRelationshipsRequest::new(Filter::new("document"), consistency::full())
                .with_options(
                    spicedb::ReadRelationshipsOptions::new()
                        .with_page_size(page_size())
                        .with_cursor("start"),
                );
        let (items, err) = drain_error(client.read_relationships(request)).await;
        assert_stalled(items, &err, calls.load(Ordering::SeqCst), &stalled);
    }
}

#[tokio::test]
async fn lookup_resources_fails_when_a_full_page_does_not_advance_the_cursor() {
    for stalled in stalled_cursors() {
        let mock = MockPermissionsService::new();
        let row = |id: &str| proto::LookupResourcesResponse {
            resource_object_id: id.into(),
            permissionship: 1,
            after_result_cursor: stalled.clone(),
            ..Default::default()
        };
        mock.push_lookup_resources_page(vec![row("a"), row("b")]);
        mock.push_lookup_resources_page(vec![row("a"), row("b")]);
        let calls = mock.lookup_resources_calls();
        let client = connect(mock).await;
        let request = spicedb::LookupResourcesRequest::new(
            "document",
            "view",
            spicedb::SubjectRef::new(spicedb::ObjectRef::new("user", "alice").unwrap(), None)
                .unwrap(),
            consistency::full(),
        )
        .with_options(
            spicedb::LookupResourcesOptions::new()
                .with_page_size(page_size())
                .with_cursor("start"),
        );
        let (items, err) = drain_error(client.lookup_resources(request)).await;
        assert_stalled(items, &err, calls.load(Ordering::SeqCst), &stalled);
    }
}

#[tokio::test]
async fn export_relationships_fails_when_a_full_page_does_not_advance_the_cursor() {
    for stalled in stalled_cursors() {
        let mock = MockPermissionsService::new();
        let page = || {
            vec![proto::ExportBulkRelationshipsResponse {
                after_result_cursor: stalled.clone(),
                relationships: vec![rel_proto("a"), rel_proto("b")],
            }]
        };
        mock.push_export_relationships_page(page());
        mock.push_export_relationships_page(page());
        let calls = mock.export_relationships_calls();
        let client = connect(mock).await;
        let request = spicedb::ExportRelationshipsRequest::new(consistency::full()).with_options(
            spicedb::ExportRelationshipsOptions::new()
                .with_page_size(page_size())
                .with_cursor("start"),
        );
        let (items, err) = drain_error(client.export_relationships(request)).await;
        assert_stalled(items, &err, calls.load(Ordering::SeqCst), &stalled);
    }
}
