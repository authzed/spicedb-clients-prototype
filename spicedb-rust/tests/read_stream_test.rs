//! Behavioral tests for the streaming read/lookup/export methods:
//! `read_relationships`, `lookup_resources`, `lookup_subjects`, and
//! `export_relationships`.
//!
//! Each of these methods now returns a real `impl Stream` instead of
//! buffering into a `Vec`. For the three that auto-paginate
//! (`read_relationships`, `lookup_resources`, `export_relationships`), these
//! tests prove pagination is genuinely *lazy*: the mock server is configured
//! with a full page (512 items) followed by a partial page, and the test
//! asserts the server has been called only ONCE while the client is still
//! delivering items from the first page — the second page is only fetched
//! once the first is fully drained. `lookup_subjects` has no server-side
//! pagination, so its test simply proves incremental, in-order delivery over
//! a single server-streaming call.

mod support;

use std::sync::atomic::Ordering;

use futures::StreamExt;
use spicedb::client::SpiceDBClient;
use spicedb::consistency;
use spicedb::types::{Filter, Permissionship};
use spicedb_proto::authzed::api::v1 as proto;

use support::{spawn_permissions_server, MockPermissionsService};

/// Page size used by all auto-paginating streaming methods (mirrors the
/// client's internal `DEFAULT_*_PAGE_SIZE` constants).
const PAGE_SIZE: usize = 512;

fn object_ref(object_type: &str, object_id: &str) -> proto::ObjectReference {
    proto::ObjectReference {
        object_type: object_type.to_string(),
        object_id: object_id.to_string(),
    }
}

fn subject_ref(subject_type: &str, subject_id: &str) -> proto::SubjectReference {
    proto::SubjectReference {
        object: Some(object_ref(subject_type, subject_id)),
        optional_relation: String::new(),
    }
}

fn rel_proto(resource_id: &str, relation: &str, subject_id: &str) -> proto::Relationship {
    proto::Relationship {
        resource: Some(object_ref("document", resource_id)),
        relation: relation.to_string(),
        subject: Some(subject_ref("user", subject_id)),
        optional_caveat: None,
        optional_expires_at: None,
    }
}

fn cursor(token: &str) -> proto::Cursor {
    proto::Cursor {
        token: token.to_string(),
    }
}

// ---------------------------------------------------------------------------
// read_relationships
// ---------------------------------------------------------------------------

#[tokio::test]
async fn read_relationships_yields_incrementally_and_paginates_lazily() {
    // Page 1: a FULL page (512 items) — this is what tells the client "there
    // may be more, fetch another page."
    let page1: Vec<proto::ReadRelationshipsResponse> = (0..PAGE_SIZE)
        .map(|i| {
            let is_last = i == PAGE_SIZE - 1;
            proto::ReadRelationshipsResponse {
                read_at: None,
                relationship: Some(rel_proto(&format!("doc-{i}"), "viewer", "alice")),
                after_result_cursor: is_last.then(|| cursor("page1-end")),
            }
        })
        .collect();
    // Page 2: a PARTIAL page (3 items) — tells the client "that's everything."
    let page2: Vec<proto::ReadRelationshipsResponse> = (0..3)
        .map(|i| proto::ReadRelationshipsResponse {
            read_at: None,
            relationship: Some(rel_proto(
                &format!("doc-{}", PAGE_SIZE + i),
                "viewer",
                "alice",
            )),
            after_result_cursor: None,
        })
        .collect();

    let mock = MockPermissionsService::new();
    mock.push_read_relationships_page(page1);
    mock.push_read_relationships_page(page2);
    let calls = mock.read_relationships_calls();
    let cursors = mock.read_relationships_cursors();

    let addr = spawn_permissions_server(mock).await;
    let client = SpiceDBClient::new_plaintext(
        addr.to_string(),
        "token",
        spicedb::RemoteCredentialsPolicy::LoopbackOnly,
    )
    .await
    .expect("client should connect to mock server");

    let filter = Filter::new("document");
    let stream = client.read_relationships(spicedb::ReadRelationshipsRequest::new(
        (filter).clone(),
        (consistency::full()).clone(),
    ));
    tokio::pin!(stream);

    let first = stream
        .next()
        .await
        .expect("stream should yield a first item")
        .expect("first item should be Ok(Relationship)");
    assert_eq!(first.relationship.resource_id(), "doc-0");

    // Only the FIRST page's RPC should have been issued so far — the second
    // page must not be fetched until the first is fully drained.
    assert_eq!(
        calls.load(Ordering::SeqCst),
        1,
        "second page must not be fetched while the first is still being drained"
    );

    let mut all = vec![first];
    while let Some(item) = stream.next().await {
        all.push(item.expect("item should be Ok(Relationship)"));
    }

    assert_eq!(
        all.len(),
        PAGE_SIZE + 3,
        "expected all items across both pages"
    );
    for (i, rel) in all.iter().enumerate() {
        assert_eq!(
            rel.relationship.resource_id(),
            format!("doc-{i}"),
            "items must arrive in order"
        );
    }

    assert_eq!(
        calls.load(Ordering::SeqCst),
        2,
        "pagination should have spanned exactly 2 server calls"
    );

    let seen_cursors = cursors.lock().unwrap().clone();
    assert_eq!(seen_cursors, vec![None, Some(cursor("page1-end"))]);
}

// ---------------------------------------------------------------------------
// lookup_resources
// ---------------------------------------------------------------------------

#[tokio::test]
async fn lookup_resources_yields_incrementally_and_paginates_lazily() {
    let page1: Vec<proto::LookupResourcesResponse> = (0..PAGE_SIZE)
        .map(|i| {
            let is_last = i == PAGE_SIZE - 1;
            proto::LookupResourcesResponse {
                looked_up_at: None,
                resource_object_id: format!("doc-{i}"),
                permissionship: proto::LookupPermissionship::HasPermission as i32,
                partial_caveat_info: None,
                after_result_cursor: is_last.then(|| cursor("page1-end")),
            }
        })
        .collect();
    let page2: Vec<proto::LookupResourcesResponse> = (0..4)
        .map(|i| proto::LookupResourcesResponse {
            looked_up_at: None,
            resource_object_id: format!("doc-{}", PAGE_SIZE + i),
            permissionship: proto::LookupPermissionship::HasPermission as i32,
            partial_caveat_info: None,
            after_result_cursor: None,
        })
        .collect();

    let mock = MockPermissionsService::new();
    mock.push_lookup_resources_page(page1);
    mock.push_lookup_resources_page(page2);
    let calls = mock.lookup_resources_calls();
    let cursors = mock.lookup_resources_cursors();

    let addr = spawn_permissions_server(mock).await;
    let client = SpiceDBClient::new_plaintext(
        addr.to_string(),
        "token",
        spicedb::RemoteCredentialsPolicy::LoopbackOnly,
    )
    .await
    .expect("client should connect to mock server");

    let stream = client.lookup_resources(spicedb::LookupResourcesRequest::new(
        "document",
        "view",
        spicedb::SubjectRef::new(spicedb::ObjectRef::new("user", "alice").unwrap(), None).unwrap(),
        (consistency::full()).clone(),
    ));
    tokio::pin!(stream);

    let first = stream
        .next()
        .await
        .expect("stream should yield a first item")
        .expect("first item should be Ok(LookupResource)");
    assert_eq!(first.resource_id, "doc-0");
    assert_eq!(first.permissionship, Permissionship::HasPermission);
    assert_eq!(first.partial_caveat, None);
    assert_eq!(
        calls.load(Ordering::SeqCst),
        1,
        "second page must not be fetched while the first is still being drained"
    );

    let mut all = vec![first];
    while let Some(item) = stream.next().await {
        all.push(item.expect("item should be Ok(LookupResource)"));
    }

    assert_eq!(all.len(), PAGE_SIZE + 4);
    for (i, result) in all.iter().enumerate() {
        assert_eq!(
            result.resource_id,
            format!("doc-{i}"),
            "items must arrive in order"
        );
        assert_eq!(result.permissionship, Permissionship::HasPermission);
    }

    assert_eq!(
        calls.load(Ordering::SeqCst),
        2,
        "pagination should have spanned exactly 2 server calls"
    );

    let seen_cursors = cursors.lock().unwrap().clone();
    assert_eq!(seen_cursors, vec![None, Some(cursor("page1-end"))]);
}

// ---------------------------------------------------------------------------
// lookup_subjects
// ---------------------------------------------------------------------------

#[tokio::test]
async fn lookup_subjects_yields_incrementally_over_a_single_call() {
    let responses = vec![
        proto::LookupSubjectsResponse {
            looked_up_at: None,
            #[allow(deprecated)]
            subject_object_id: String::new(),
            #[allow(deprecated)]
            excluded_subject_ids: Vec::new(),
            #[allow(deprecated)]
            permissionship: 0,
            #[allow(deprecated)]
            partial_caveat_info: None,
            subject: Some(proto::ResolvedSubject {
                subject_object_id: "alice".to_string(),
                permissionship: proto::LookupPermissionship::HasPermission as i32,
                partial_caveat_info: None,
            }),
            excluded_subjects: Vec::new(),
            after_result_cursor: None,
        },
        proto::LookupSubjectsResponse {
            looked_up_at: None,
            #[allow(deprecated)]
            subject_object_id: String::new(),
            #[allow(deprecated)]
            excluded_subject_ids: Vec::new(),
            #[allow(deprecated)]
            permissionship: 0,
            #[allow(deprecated)]
            partial_caveat_info: None,
            subject: Some(proto::ResolvedSubject {
                subject_object_id: "bob".to_string(),
                permissionship: proto::LookupPermissionship::HasPermission as i32,
                partial_caveat_info: None,
            }),
            excluded_subjects: Vec::new(),
            after_result_cursor: None,
        },
    ];

    let mock = MockPermissionsService::new();
    mock.set_lookup_subjects_responses(responses);
    let calls = mock.lookup_subjects_calls();

    let addr = spawn_permissions_server(mock).await;
    let client = SpiceDBClient::new_plaintext(
        addr.to_string(),
        "token",
        spicedb::RemoteCredentialsPolicy::LoopbackOnly,
    )
    .await
    .expect("client should connect to mock server");

    let stream = client.lookup_subjects(spicedb::LookupSubjectsRequest::new(
        spicedb::ObjectRef::new("document", "firstdoc").unwrap(),
        "view",
        "user",
        (consistency::full()).clone(),
    ));
    tokio::pin!(stream);

    let first = stream
        .next()
        .await
        .expect("stream should yield a first item")
        .expect("first item should be Ok(LookupSubject)");
    assert_eq!(first.subject.subject_id, "alice");
    assert_eq!(first.subject.permissionship, Permissionship::HasPermission);
    assert!(first.excluded_subjects.is_empty());
    assert_eq!(calls.load(Ordering::SeqCst), 1);

    let second = stream
        .next()
        .await
        .expect("stream should yield a second item")
        .expect("second item should be Ok(LookupSubject)");
    assert_eq!(second.subject.subject_id, "bob");

    assert!(
        stream.next().await.is_none(),
        "stream should end after 2 items"
    );
    assert_eq!(
        calls.load(Ordering::SeqCst),
        1,
        "lookup_subjects has no pagination — exactly one server call"
    );
}

// ---------------------------------------------------------------------------
// lookup_subjects — wildcard excluded subjects (security-critical)
// ---------------------------------------------------------------------------

/// Proves that when the server resolves a wildcard ("*") subject with
/// exclusions, the client surfaces `excluded_subjects` to the caller instead
/// of dropping them. A caller that ignores this list would incorrectly treat
/// the wildcard as a blanket grant to every subject, including ones the
/// server explicitly excluded — the over-grant risk this task fixes.
#[tokio::test]
async fn lookup_subjects_surfaces_wildcard_excluded_subjects() {
    let responses = vec![proto::LookupSubjectsResponse {
        looked_up_at: None,
        #[allow(deprecated)]
        subject_object_id: String::new(),
        #[allow(deprecated)]
        excluded_subject_ids: Vec::new(),
        #[allow(deprecated)]
        permissionship: 0,
        #[allow(deprecated)]
        partial_caveat_info: None,
        subject: Some(proto::ResolvedSubject {
            subject_object_id: "*".to_string(),
            permissionship: proto::LookupPermissionship::HasPermission as i32,
            partial_caveat_info: None,
        }),
        excluded_subjects: vec![
            proto::ResolvedSubject {
                subject_object_id: "banned1".to_string(),
                permissionship: proto::LookupPermissionship::HasPermission as i32,
                partial_caveat_info: None,
            },
            proto::ResolvedSubject {
                subject_object_id: "banned2".to_string(),
                permissionship: proto::LookupPermissionship::HasPermission as i32,
                partial_caveat_info: None,
            },
        ],
        after_result_cursor: None,
    }];

    let mock = MockPermissionsService::new();
    mock.set_lookup_subjects_responses(responses);

    let addr = spawn_permissions_server(mock).await;
    let client = SpiceDBClient::new_plaintext(
        addr.to_string(),
        "token",
        spicedb::RemoteCredentialsPolicy::LoopbackOnly,
    )
    .await
    .expect("client should connect to mock server");

    let stream = client.lookup_subjects(spicedb::LookupSubjectsRequest::new(
        spicedb::ObjectRef::new("document", "firstdoc").unwrap(),
        "view",
        "user",
        (consistency::full()).clone(),
    ));
    tokio::pin!(stream);

    let result = stream
        .next()
        .await
        .expect("stream should yield a result")
        .expect("result should be Ok(LookupSubject)");

    assert_eq!(
        result.subject.subject_id, "*",
        "subject should be the wildcard"
    );
    assert_eq!(result.subject.permissionship, Permissionship::HasPermission);

    let excluded_ids: Vec<&str> = result
        .excluded_subjects
        .iter()
        .map(|s| s.subject_id.as_str())
        .collect();
    assert_eq!(
        excluded_ids,
        vec!["banned1", "banned2"],
        "excluded_subjects must be surfaced to the caller so wildcard exclusions aren't silently dropped"
    );
    for excluded in &result.excluded_subjects {
        assert_eq!(excluded.permissionship, Permissionship::HasPermission);
    }

    assert!(stream.next().await.is_none());
}

/// Proves that when a server only populates the deprecated
/// `excluded_subject_ids` field (no `excluded_subjects`) and the deprecated
/// top-level `subject_object_id`/`permissionship` fields (no nested
/// `subject`), the client still reconstructs equivalent native results
/// instead of yielding empty/missing data.
#[tokio::test]
async fn lookup_subjects_falls_back_to_deprecated_fields() {
    let responses = vec![proto::LookupSubjectsResponse {
        looked_up_at: None,
        #[allow(deprecated)]
        subject_object_id: "*".to_string(),
        #[allow(deprecated)]
        excluded_subject_ids: vec!["banned1".to_string(), "banned2".to_string()],
        #[allow(deprecated)]
        permissionship: proto::LookupPermissionship::HasPermission as i32,
        #[allow(deprecated)]
        partial_caveat_info: None,
        subject: None,
        excluded_subjects: Vec::new(),
        after_result_cursor: None,
    }];

    let mock = MockPermissionsService::new();
    mock.set_lookup_subjects_responses(responses);

    let addr = spawn_permissions_server(mock).await;
    let client = SpiceDBClient::new_plaintext(
        addr.to_string(),
        "token",
        spicedb::RemoteCredentialsPolicy::LoopbackOnly,
    )
    .await
    .expect("client should connect to mock server");

    let stream = client.lookup_subjects(spicedb::LookupSubjectsRequest::new(
        spicedb::ObjectRef::new("document", "firstdoc").unwrap(),
        "view",
        "user",
        (consistency::full()).clone(),
    ));
    tokio::pin!(stream);

    let result = stream
        .next()
        .await
        .expect("stream should yield a result")
        .expect("result should be Ok(LookupSubject)");

    assert_eq!(
        result.subject.subject_id, "*",
        "should fall back to deprecated subject_object_id when subject is unset"
    );
    assert_eq!(result.subject.permissionship, Permissionship::HasPermission);

    let excluded_ids: Vec<&str> = result
        .excluded_subjects
        .iter()
        .map(|s| s.subject_id.as_str())
        .collect();
    assert_eq!(
        excluded_ids,
        vec!["banned1", "banned2"],
        "should fall back to deprecated excluded_subject_ids when excluded_subjects is unset"
    );
    // The deprecated excluded_subject_ids field carries only IDs, so
    // permissionship falls back to Unspecified for those entries.
    for excluded in &result.excluded_subjects {
        assert_eq!(excluded.permissionship, Permissionship::Unspecified);
        assert_eq!(excluded.partial_caveat, None);
    }

    assert!(stream.next().await.is_none());
}

// ---------------------------------------------------------------------------
// export_relationships
// ---------------------------------------------------------------------------

#[tokio::test]
async fn export_relationships_yields_incrementally_and_paginates_lazily() {
    let page1_rels: Vec<proto::Relationship> = (0..PAGE_SIZE)
        .map(|i| rel_proto(&format!("doc-{i}"), "viewer", "alice"))
        .collect();
    let page1 = vec![proto::ExportBulkRelationshipsResponse {
        after_result_cursor: Some(cursor("page1-end")),
        relationships: page1_rels,
    }];

    let page2_rels: Vec<proto::Relationship> = (0..3)
        .map(|i| rel_proto(&format!("doc-{}", PAGE_SIZE + i), "viewer", "alice"))
        .collect();
    let page2 = vec![proto::ExportBulkRelationshipsResponse {
        after_result_cursor: None,
        relationships: page2_rels,
    }];

    let mock = MockPermissionsService::new();
    mock.push_export_relationships_page(page1);
    mock.push_export_relationships_page(page2);
    let calls = mock.export_relationships_calls();
    let cursors = mock.export_relationships_cursors();

    let addr = spawn_permissions_server(mock).await;
    let client = SpiceDBClient::new_plaintext(
        addr.to_string(),
        "token",
        spicedb::RemoteCredentialsPolicy::LoopbackOnly,
    )
    .await
    .expect("client should connect to mock server");

    let stream = client.export_relationships(spicedb::ExportRelationshipsRequest::new(
        (consistency::full()).clone(),
    ));
    tokio::pin!(stream);

    let first = stream
        .next()
        .await
        .expect("stream should yield a first item")
        .expect("first item should be Ok(Relationship)");
    assert_eq!(first.relationship.resource_id(), "doc-0");
    assert_eq!(
        calls.load(Ordering::SeqCst),
        1,
        "second page must not be fetched while the first is still being drained"
    );

    let mut all = vec![first];
    while let Some(item) = stream.next().await {
        all.push(item.expect("item should be Ok(Relationship)"));
    }

    assert_eq!(all.len(), PAGE_SIZE + 3);
    for (i, rel) in all.iter().enumerate() {
        assert_eq!(
            rel.relationship.resource_id(),
            format!("doc-{i}"),
            "items must arrive in order"
        );
    }

    assert_eq!(
        calls.load(Ordering::SeqCst),
        2,
        "pagination should have spanned exactly 2 server calls"
    );

    let seen_cursors = cursors.lock().unwrap().clone();
    assert_eq!(seen_cursors, vec![None, Some(cursor("page1-end"))]);
}

#[tokio::test]
async fn lookup_resources_filters_exact_grants_across_pages() {
    for filtered in [false, true] {
        let mock = MockPermissionsService::new();
        let row = |id: &str, permissionship, token: &str| proto::LookupResourcesResponse {
            resource_object_id: id.into(),
            permissionship,
            after_result_cursor: Some(cursor(token)),
            looked_up_at: Some(proto::ZedToken {
                token: "snapshot".into(),
            }),
            ..Default::default()
        };
        mock.push_lookup_resources_page(vec![row("conditional", 2, "1"), row("unknown", 99, "2")]);
        // An unset (0) permissionship is a malformed response, so the
        // filtered-out rows here are values the client does not know.
        mock.push_lookup_resources_page(vec![row("unknown2", 98, "3"), row("grant1", 1, "4")]);
        // Every page is requested with the same page size (2); a short page
        // ends the lookup, so the server fills each page it has rows for.
        mock.push_lookup_resources_page(vec![row("conditional2", 2, "5"), row("grant2", 1, "6")]);
        let cursors = mock.lookup_resources_cursors();
        let addr = spawn_permissions_server(mock).await;
        let client = SpiceDBClient::new_plaintext(
            addr.to_string(),
            "token",
            spicedb::RemoteCredentialsPolicy::LoopbackOnly,
        )
        .await
        .unwrap();
        let mut options = spicedb::LookupResourcesOptions::new()
            .with_page_size(std::num::NonZeroU32::new(2).unwrap())
            .with_limit(2);
        if filtered {
            options = options
                .with_permissionship_filter(spicedb::LookupPermissionshipFilter::HasPermissionOnly);
        }
        let request = spicedb::LookupResourcesRequest::new(
            "document",
            "view",
            spicedb::SubjectRef::new(spicedb::ObjectRef::new("user", "alice").unwrap(), None)
                .unwrap(),
            consistency::full(),
        )
        .with_options(options);
        let stream = client.lookup_resources(request);
        tokio::pin!(stream);
        let mut ids = Vec::new();
        while let Some(result) = stream.next().await {
            ids.push(result.unwrap().resource_id);
        }
        if filtered {
            assert_eq!(ids, vec!["grant1", "grant2"]);
            assert_eq!(
                *cursors.lock().unwrap(),
                vec![None, Some(cursor("2")), Some(cursor("4")),]
            );
        } else {
            assert_eq!(ids, vec!["conditional", "unknown"]);
        }
    }
}
