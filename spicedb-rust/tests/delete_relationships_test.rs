//! Behavioral tests for `delete_relationships` / `delete_relationships_with`:
//! preconditions, page-size override, and the default (no-options) path.
//!
//! These tests capture the outbound `DeleteRelationshipsRequest` against a
//! mock `PermissionsService` and assert on what the client actually sent —
//! proving `DeleteOptions` preconditions and limit reach the wire, and that
//! the simple `delete_relationships(filter)` path is unaffected.

mod support;

use std::sync::atomic::Ordering;

use spicedb::client::SpiceDBClient;
use spicedb::types::{DeleteOptions, Filter};
use spicedb_proto::authzed::api::v1 as proto;

use support::{spawn_permissions_server, MockPermissionsService};

/// Default per-request page size used by `delete_relationships`'s auto-paging
/// loop (mirrors the client's internal `DEFAULT_DELETE_PAGE_SIZE`).
const DEFAULT_DELETE_PAGE_SIZE: u32 = 10_000;

fn complete_response(token: &str) -> proto::DeleteRelationshipsResponse {
    proto::DeleteRelationshipsResponse {
        deleted_at: Some(proto::ZedToken {
            token: token.to_string(),
        }),
        deletion_progress: proto::delete_relationships_response::DeletionProgress::Complete as i32,
        relationships_deleted_count: 0,
    }
}

fn partial_response(token: &str) -> proto::DeleteRelationshipsResponse {
    proto::DeleteRelationshipsResponse {
        deleted_at: Some(proto::ZedToken {
            token: token.to_string(),
        }),
        deletion_progress: proto::delete_relationships_response::DeletionProgress::Partial as i32,
        relationships_deleted_count: 0,
    }
}

/// Builds the `proto::RelationshipFilter` expected on the wire for a filter
/// narrowed to `resource_id` only (no relation/subject). `Filter::to_proto`
/// is crate-private, so integration tests assert against an independently
/// constructed expected proto rather than calling it.
fn expected_resource_only_filter(
    resource_type: &str,
    resource_id: &str,
) -> proto::RelationshipFilter {
    proto::RelationshipFilter {
        resource_type: resource_type.to_string(),
        optional_resource_id: resource_id.to_string(),
        optional_resource_id_prefix: String::new(),
        optional_relation: String::new(),
        optional_subject_filter: None,
    }
}

/// Builds the `proto::RelationshipFilter` expected on the wire for a fully
/// narrowed filter (resource id + relation + subject type/id).
fn expected_full_filter(
    resource_type: &str,
    resource_id: &str,
    relation: &str,
    subject_type: &str,
    subject_id: &str,
) -> proto::RelationshipFilter {
    proto::RelationshipFilter {
        resource_type: resource_type.to_string(),
        optional_resource_id: resource_id.to_string(),
        optional_resource_id_prefix: String::new(),
        optional_relation: relation.to_string(),
        optional_subject_filter: Some(proto::SubjectFilter {
            subject_type: subject_type.to_string(),
            optional_subject_id: subject_id.to_string(),
            optional_relation: None,
        }),
    }
}

// ---------------------------------------------------------------------------
// delete_relationships (no options) — default behavior is unchanged
// ---------------------------------------------------------------------------

#[tokio::test]
async fn delete_relationships_sends_no_preconditions_and_default_limit() {
    let mock = MockPermissionsService::new();
    mock.push_delete_relationships_response(complete_response("rev-1"));
    let calls = mock.delete_relationships_calls();
    let requests = mock.delete_relationships_requests();

    let addr = spawn_permissions_server(mock).await;
    let client = SpiceDBClient::new_plaintext(addr.to_string(), "token")
        .await
        .expect("client should connect to mock server");

    let filter = Filter::new("document").with_resource_id("doc1");
    let revision = client
        .delete_relationships(&filter)
        .await
        .expect("delete should succeed");

    assert_eq!(revision, "rev-1");
    assert_eq!(calls.load(Ordering::SeqCst), 1);

    let seen = requests.lock().unwrap().clone();
    assert_eq!(seen.len(), 1);
    let req = &seen[0];
    assert_eq!(
        req.relationship_filter,
        Some(expected_resource_only_filter("document", "doc1"))
    );
    assert!(
        req.optional_preconditions.is_empty(),
        "no-options delete must send no preconditions"
    );
    assert_eq!(req.optional_limit, DEFAULT_DELETE_PAGE_SIZE);
    assert!(req.optional_allow_partial_deletions);
}

// ---------------------------------------------------------------------------
// delete_relationships_with — preconditions
// ---------------------------------------------------------------------------

#[tokio::test]
async fn delete_relationships_with_sends_must_match_and_must_not_match_preconditions() {
    let mock = MockPermissionsService::new();
    mock.push_delete_relationships_response(complete_response("rev-2"));
    let requests = mock.delete_relationships_requests();

    let addr = spawn_permissions_server(mock).await;
    let client = SpiceDBClient::new_plaintext(addr.to_string(), "token")
        .await
        .expect("client should connect to mock server");

    let filter = Filter::new("document").with_resource_id("doc1");
    let must_match_filter = Filter::new("document")
        .with_resource_id("doc1")
        .with_relation("owner")
        .with_subject_type("user")
        .with_subject_id("alice");
    let must_not_match_filter = Filter::new("document")
        .with_resource_id("doc1")
        .with_relation("owner")
        .with_subject_type("user")
        .with_subject_id("mallory");

    let options = DeleteOptions::new()
        .with_must_match(must_match_filter.clone())
        .with_must_not_match(must_not_match_filter.clone());

    let revision = client
        .delete_relationships_with(&filter, &options)
        .await
        .expect("delete should succeed");
    assert_eq!(revision, "rev-2");

    let seen = requests.lock().unwrap().clone();
    assert_eq!(seen.len(), 1);
    let preconditions = &seen[0].optional_preconditions;
    assert_eq!(preconditions.len(), 2);

    assert_eq!(
        preconditions[0].operation,
        proto::precondition::Operation::MustMatch as i32
    );
    assert_eq!(
        preconditions[0].filter,
        Some(expected_full_filter(
            "document", "doc1", "owner", "user", "alice"
        ))
    );

    assert_eq!(
        preconditions[1].operation,
        proto::precondition::Operation::MustNotMatch as i32
    );
    assert_eq!(
        preconditions[1].filter,
        Some(expected_full_filter(
            "document", "doc1", "owner", "user", "mallory"
        ))
    );
}

// ---------------------------------------------------------------------------
// delete_relationships_with — limit override
// ---------------------------------------------------------------------------

#[tokio::test]
async fn delete_relationships_with_overrides_page_size() {
    let mock = MockPermissionsService::new();
    mock.push_delete_relationships_response(complete_response("rev-3"));
    let requests = mock.delete_relationships_requests();

    let addr = spawn_permissions_server(mock).await;
    let client = SpiceDBClient::new_plaintext(addr.to_string(), "token")
        .await
        .expect("client should connect to mock server");

    let filter = Filter::new("document");
    let options = DeleteOptions::new().with_limit(25);

    client
        .delete_relationships_with(&filter, &options)
        .await
        .expect("delete should succeed");

    let seen = requests.lock().unwrap().clone();
    assert_eq!(seen[0].optional_limit, 25);
    assert!(seen[0].optional_preconditions.is_empty());
}

// ---------------------------------------------------------------------------
// delete_relationships_with — auto-paging across multiple calls
// ---------------------------------------------------------------------------

#[tokio::test]
async fn delete_relationships_with_pages_until_complete() {
    let mock = MockPermissionsService::new();
    mock.push_delete_relationships_response(partial_response("rev-page1"));
    mock.push_delete_relationships_response(partial_response("rev-page2"));
    mock.push_delete_relationships_response(complete_response("rev-final"));
    let calls = mock.delete_relationships_calls();
    let requests = mock.delete_relationships_requests();

    let addr = spawn_permissions_server(mock).await;
    let client = SpiceDBClient::new_plaintext(addr.to_string(), "token")
        .await
        .expect("client should connect to mock server");

    let filter = Filter::new("document");
    let options = DeleteOptions::new().with_limit(10);

    let revision = client
        .delete_relationships_with(&filter, &options)
        .await
        .expect("delete should succeed");

    assert_eq!(
        revision, "rev-final",
        "should return the revision of the final (complete) page"
    );
    assert_eq!(
        calls.load(Ordering::SeqCst),
        3,
        "should repeat until DELETION_PROGRESS_COMPLETE"
    );

    // Every page in the loop must carry the same limit and (empty) preconditions.
    let seen = requests.lock().unwrap().clone();
    assert_eq!(seen.len(), 3);
    for req in &seen {
        assert_eq!(req.optional_limit, 10);
        assert!(req.optional_preconditions.is_empty());
        assert!(req.optional_allow_partial_deletions);
    }
}
