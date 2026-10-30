//! A response the client cannot interpret fails with INTERNAL, which no
//! retry policy treats as retryable by default. It is never skipped (a
//! skipped item makes a full page look short and silently ends a paged read
//! early) and its missing fields are never filled with empty values.

mod support;

use std::sync::atomic::Ordering;

use futures::StreamExt;
use spicedb::client::SpiceDBClient;
use spicedb::consistency;
use spicedb::types::Filter;
use spicedb::SpiceDBError;
use spicedb_proto::authzed::api::v1 as proto;

use support::{
    spawn_permissions_server, spawn_watch_server, MockPermissionsService, MockWatchService,
};

fn object(object_type: &str, id: &str) -> proto::ObjectReference {
    proto::ObjectReference {
        object_type: object_type.into(),
        object_id: id.into(),
    }
}

fn relationship() -> proto::Relationship {
    proto::Relationship {
        resource: Some(object("document", "a")),
        relation: "viewer".into(),
        subject: Some(proto::SubjectReference {
            object: Some(object("user", "alice")),
            optional_relation: String::new(),
        }),
        optional_caveat: None,
        optional_expires_at: None,
    }
}

/// The ways a response relationship can be missing a required part.
fn broken_relationships() -> Vec<proto::Relationship> {
    vec![
        proto::Relationship {
            resource: None,
            ..relationship()
        },
        proto::Relationship {
            subject: None,
            ..relationship()
        },
        proto::Relationship {
            subject: Some(proto::SubjectReference {
                object: None,
                optional_relation: String::new(),
            }),
            ..relationship()
        },
    ]
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
    stream: impl futures::Stream<Item = Result<T, SpiceDBError>>,
) -> (usize, SpiceDBError) {
    tokio::pin!(stream);
    let mut items = 0;
    while let Some(result) = stream.next().await {
        match result {
            Ok(_) => items += 1,
            Err(err) => return (items, err),
        }
    }
    panic!("stream ended after {items} item(s) without failing on the malformed response");
}

/// A malformed response is INTERNAL, and says the client detected it so it is
/// not mistaken for an INTERNAL status the server sent.
fn assert_internal(err: &SpiceDBError) {
    assert_eq!(err.code(), tonic::Code::Internal, "{err}");
    assert!(err.is_client_detected(), "{err:?}");
    assert!(err.status().is_none(), "{err:?}");
    assert!(
        err.to_string()
            .contains("client detected malformed response from server: "),
        "{err}"
    );
    assert!(!err.to_string().contains("grpc status"), "{err}");
}

#[test]
fn a_client_detected_error_says_so_and_a_server_internal_does_not() {
    let local = spicedb::error::internal("missing relationship".into());
    assert_eq!(
        local.to_string(),
        "client detected malformed response from server: missing relationship"
    );
    assert!(local.is_client_detected());
    assert_eq!(local.code(), tonic::Code::Internal);
    assert_eq!(local.message(), "missing relationship");
    let forwarded = local.to_status();
    assert_eq!(forwarded.code(), tonic::Code::Internal);
    assert_eq!(
        forwarded.message(),
        "client detected malformed response from server: missing relationship"
    );

    let server = SpiceDBError::new(tonic::Code::Internal, "boom");
    assert!(!server.is_client_detected());
    assert_eq!(server.to_string(), "grpc status 13: boom");

    let wrapped = SpiceDBError::BulkItem {
        index: 2,
        source: Box::new(spicedb::error::internal("x".into())),
    };
    assert!(wrapped.is_client_detected());
}

fn check_question() -> spicedb::PermissionCheck {
    spicedb::PermissionCheck::new(
        spicedb::ObjectRef::new("document", "a").unwrap(),
        "view",
        spicedb::SubjectRef::direct(spicedb::ObjectRef::new("user", "alice").unwrap()),
        consistency::full(),
    )
}

#[tokio::test]
async fn check_fails_on_an_unset_permissionship() {
    let mock = MockPermissionsService::new();
    mock.push_check_permission_response(proto::CheckPermissionResponse {
        checked_at: Some(proto::ZedToken { token: "t".into() }),
        permissionship: proto::check_permission_response::Permissionship::Unspecified as i32,
        ..Default::default()
    });
    let client = connect(mock).await;
    let err = client
        .check(check_question())
        .await
        .expect_err("an unset permissionship is not an answer");
    assert_internal(&err);
    assert!(
        err.to_string().contains("permissionship is unset"),
        "{err}"
    );
}

#[tokio::test]
async fn check_bulk_fails_on_an_item_with_an_unset_permissionship() {
    let item = |permissionship: proto::check_permission_response::Permissionship| {
        proto::CheckBulkPermissionsPair {
            request: None,
            response: Some(proto::check_bulk_permissions_pair::Response::Item(
                proto::CheckBulkPermissionsResponseItem {
                    permissionship: permissionship as i32,
                    partial_caveat_info: None,
                    debug_trace: None,
                },
            )),
        }
    };
    let mock = MockPermissionsService::new();
    mock.push_check_bulk_permissions_response(proto::CheckBulkPermissionsResponse {
        checked_at: Some(proto::ZedToken { token: "t".into() }),
        pairs: vec![
            item(proto::check_permission_response::Permissionship::HasPermission),
            item(proto::check_permission_response::Permissionship::Unspecified),
        ],
    });
    let calls = mock.check_bulk_permissions_calls();
    let client = connect(mock).await;
    let err = client
        .check_bulk(spicedb::BulkCheckRequest::new(vec![
            check_question(),
            check_question(),
        ]))
        .await
        .expect_err("an unset item is not an answer");
    assert_internal(&err);
    assert!(
        err.to_string()
            .contains("check item 1: permissionship is unset"),
        "{err}"
    );
    assert_eq!(calls.load(Ordering::SeqCst), 1, "INTERNAL is not retried");
}

#[tokio::test]
async fn lookups_fail_on_a_result_with_an_unspecified_permissionship() {
    for filter in [
        spicedb::LookupPermissionshipFilter::All,
        spicedb::LookupPermissionshipFilter::HasPermissionOnly,
    ] {
        let mock = MockPermissionsService::new();
        let row = |id: &str, permissionship: proto::LookupPermissionship| {
            proto::LookupResourcesResponse {
                resource_object_id: id.into(),
                permissionship: permissionship as i32,
                ..Default::default()
            }
        };
        mock.push_lookup_resources_page(vec![
            row("a", proto::LookupPermissionship::HasPermission),
            row("b", proto::LookupPermissionship::Unspecified),
        ]);
        let client = connect(mock).await;
        let (items, err) = drain_error(
            client.lookup_resources(
                spicedb::LookupResourcesRequest::new(
                    "document",
                    "view",
                    spicedb::SubjectRef::direct(spicedb::ObjectRef::new("user", "alice").unwrap()),
                    consistency::full(),
                )
                .with_options(
                    spicedb::LookupResourcesOptions::new().with_permissionship_filter(filter),
                ),
            ),
        )
        .await;
        assert_eq!(items, 1, "{filter:?}");
        assert_internal(&err);
    }

    let mock = MockPermissionsService::new();
    let subject =
        |id: &str, permissionship: proto::LookupPermissionship| proto::LookupSubjectsResponse {
            subject: Some(proto::ResolvedSubject {
                subject_object_id: id.into(),
                permissionship: permissionship as i32,
                partial_caveat_info: None,
            }),
            ..Default::default()
        };
    mock.set_lookup_subjects_responses(vec![
        subject("alice", proto::LookupPermissionship::HasPermission),
        subject("bob", proto::LookupPermissionship::Unspecified),
    ]);
    let client = connect(mock).await;
    let (items, err) = drain_error(client.lookup_subjects(spicedb::LookupSubjectsRequest::new(
        spicedb::ObjectRef::new("document", "a").unwrap(),
        "view",
        "user",
        consistency::full(),
    )))
    .await;
    assert_eq!(items, 1);
    assert_internal(&err);
}

#[tokio::test]
async fn read_fails_on_a_response_without_its_relationship_instead_of_skipping_it() {
    let mut cases: Vec<Option<proto::Relationship>> = vec![None];
    cases.extend(broken_relationships().into_iter().map(Some));
    for broken in cases {
        let mock = MockPermissionsService::new();
        let row = |relationship| proto::ReadRelationshipsResponse {
            read_at: None,
            relationship,
            after_result_cursor: Some(proto::Cursor { token: "c".into() }),
        };
        mock.push_read_relationships_page(vec![
            row(Some(relationship())),
            row(broken.clone()),
            row(Some(relationship())),
        ]);
        let calls = mock.read_relationships_calls();
        let client = connect(mock).await;
        let (items, err) = drain_error(client.read_relationships(
            spicedb::ReadRelationshipsRequest::new(Filter::new("document"), consistency::full()),
        ))
        .await;
        assert_eq!(items, 1, "{broken:?}");
        assert_internal(&err);
        assert_eq!(calls.load(Ordering::SeqCst), 1, "INTERNAL is not retried");
    }
}

#[tokio::test]
async fn export_fails_on_a_relationship_missing_a_required_part() {
    for broken in broken_relationships() {
        let mock = MockPermissionsService::new();
        mock.push_export_relationships_page(vec![proto::ExportBulkRelationshipsResponse {
            after_result_cursor: None,
            relationships: vec![relationship(), broken.clone()],
        }]);
        let client = connect(mock).await;
        let (items, err) = drain_error(
            client.export_relationships(spicedb::ExportRelationshipsRequest::new(
                consistency::full(),
            )),
        )
        .await;
        assert_eq!(items, 1, "{broken:?}");
        assert_internal(&err);
    }
}

#[tokio::test]
async fn lookup_resources_fails_on_a_response_without_a_resource_id() {
    let mock = MockPermissionsService::new();
    mock.push_lookup_resources_page(vec![
        proto::LookupResourcesResponse {
            resource_object_id: "a".into(),
            permissionship: proto::LookupPermissionship::HasPermission as i32,
            ..Default::default()
        },
        proto::LookupResourcesResponse {
            resource_object_id: String::new(),
            permissionship: proto::LookupPermissionship::HasPermission as i32,
            ..Default::default()
        },
    ]);
    let client = connect(mock).await;
    let (items, err) = drain_error(
        client.lookup_resources(spicedb::LookupResourcesRequest::new(
            "document",
            "view",
            spicedb::SubjectRef::direct(spicedb::ObjectRef::new("user", "alice").unwrap()),
            consistency::full(),
        )),
    )
    .await;
    assert_eq!(items, 1);
    assert_internal(&err);
}

#[tokio::test]
async fn lookup_subjects_fails_on_a_response_without_a_subject() {
    let mock = MockPermissionsService::new();
    #[allow(deprecated)]
    mock.set_lookup_subjects_responses(vec![
        proto::LookupSubjectsResponse {
            subject: Some(proto::ResolvedSubject {
                subject_object_id: "alice".into(),
                permissionship: proto::LookupPermissionship::HasPermission as i32,
                partial_caveat_info: None,
            }),
            ..Default::default()
        },
        // Neither the subject nor the deprecated response-level ID.
        proto::LookupSubjectsResponse::default(),
    ]);
    let client = connect(mock).await;
    let (items, err) = drain_error(client.lookup_subjects(spicedb::LookupSubjectsRequest::new(
        spicedb::ObjectRef::new("document", "a").unwrap(),
        "view",
        "user",
        consistency::full(),
    )))
    .await;
    assert_eq!(items, 1);
    assert_internal(&err);
}

#[tokio::test]
async fn expand_fails_on_a_missing_tree_root_or_expanded_object() {
    let leaf = proto::PermissionRelationshipTree {
        expanded_object: Some(object("document", "a")),
        expanded_relation: "viewer".into(),
        tree_type: Some(proto::permission_relationship_tree::TreeType::Leaf(
            proto::DirectSubjectSet {
                subjects: vec![proto::SubjectReference {
                    object: None,
                    optional_relation: String::new(),
                }],
            },
        )),
    };
    let cases = [
        None,
        Some(proto::PermissionRelationshipTree {
            expanded_object: None,
            ..leaf.clone()
        }),
        Some(leaf),
    ];
    for tree_root in cases {
        let mock = MockPermissionsService::new();
        mock.set_expand_response(proto::ExpandPermissionTreeResponse {
            expanded_at: None,
            tree_root: tree_root.clone(),
        });
        let client = connect(mock).await;
        let err = client
            .expand_permission_tree(spicedb::ExpandPermissionTreeRequest::new(
                spicedb::ObjectRef::new("document", "a").unwrap(),
                "view",
                consistency::full(),
            ))
            .await
            .expect_err("a malformed tree must fail");
        assert_internal(&err);
    }
}

#[tokio::test]
async fn watch_fails_on_an_update_without_its_relationship_instead_of_dropping_it() {
    let mut cases: Vec<Option<proto::Relationship>> = vec![None];
    cases.extend(broken_relationships().into_iter().map(Some));
    for broken in cases {
        let responses = vec![proto::WatchResponse {
            updates: vec![
                proto::RelationshipUpdate {
                    operation: proto::relationship_update::Operation::Touch as i32,
                    relationship: Some(relationship()),
                },
                proto::RelationshipUpdate {
                    operation: proto::relationship_update::Operation::Touch as i32,
                    relationship: broken.clone(),
                },
            ],
            changes_through: Some(proto::ZedToken {
                token: "rev".into(),
            }),
            ..Default::default()
        }];
        let addr = spawn_watch_server(MockWatchService::new(responses, false)).await;
        let client = SpiceDBClient::new_plaintext(
            addr.to_string(),
            "token",
            spicedb::RemoteCredentialsPolicy::LoopbackOnly,
        )
        .await
        .unwrap();
        let (items, err) =
            drain_error(client.updates(spicedb::UpdatesRequest::new(vec!["document".into()])))
                .await;
        assert_eq!(items, 0, "{broken:?}");
        assert_internal(&err);
    }
}
