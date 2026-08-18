//! Retry safety, per root DESIGN.md "RULE: Automatic retry is for
//! idempotent operations only":
//!
//! - Reads (e.g. `check_permissions`/`CheckBulkPermissions`) retry on a
//!   transient error.
//! - Mutations (e.g. `write`/`WriteRelationships`) are attempted exactly
//!   once, even on a retryable error -- a `WriteRelationships` carrying
//!   `OPERATION_CREATE` or preconditions is not idempotent, and retrying a
//!   lost response would surface `ALREADY_EXISTS`/`FAILED_PRECONDITION` for
//!   a write that in fact succeeded.
//! - `RESOURCE_EXHAUSTED` is never retried, on either a read or a mutation.
//!
//! See `src/error.rs`'s and `tests/error_test.rs`'s inverted
//! `is_transient`/`RESOURCE_EXHAUSTED` unit tests for the error-mapping half
//! of this guarantee, and `src/client.rs`'s `jittered_backoff_ms` unit test
//! for the backoff-jitter half.

mod support;

use std::sync::atomic::Ordering;

use spicedb::client::SpiceDBClient;
use spicedb::consistency;
use spicedb::error::SpiceDBError;
use spicedb::types::{Relationship, Transaction};
use spicedb_proto::authzed::api::v1 as proto;
use tonic::Status;

use support::{spawn_permissions_server, MockPermissionsService};

/// A single successful `CheckBulkPermissions` pair -- the response the mock
/// falls through to once its failure queue is exhausted. Without this, the
/// mock's zero-pairs default response would itself trip the client's
/// pair-count guard (`check_bulk_permissions returned 0 pair(s) for 1
/// request item(s)`) and mask what this test is actually checking.
fn has_permission_response() -> proto::CheckBulkPermissionsResponse {
    proto::CheckBulkPermissionsResponse {
        checked_at: Some(proto::ZedToken {
            token: "rev-1".to_string(),
        }),
        pairs: vec![proto::CheckBulkPermissionsPair {
            request: None,
            response: Some(proto::check_bulk_permissions_pair::Response::Item(
                proto::CheckBulkPermissionsResponseItem {
                    permissionship: proto::check_permission_response::Permissionship::HasPermission
                        as i32,
                    partial_caveat_info: None,
                    debug_trace: None,
                },
            )),
        }],
    }
}

#[tokio::test]
async fn read_is_retried_on_a_transient_error() {
    let mock = MockPermissionsService::new();
    mock.queue_check_bulk_permissions_failure(Status::unavailable("down"));
    mock.push_check_bulk_permissions_response(has_permission_response());
    let calls = mock.check_bulk_permissions_calls();

    let addr = spawn_permissions_server(mock).await;
    let client = SpiceDBClient::new_plaintext(addr.to_string(), "token")
        .await
        .expect("client should connect to mock server");

    let rel = Relationship::new("document", "doc1", "view", "user", "alice", "")
        .expect("valid relationship");
    let result = client
        .check_permissions(&consistency::full(), "view", &[rel])
        .await;

    assert!(
        result.is_ok(),
        "a transient error on a read must be retried and eventually succeed, got {result:?}"
    );
    assert_eq!(
        calls.load(Ordering::SeqCst),
        2,
        "expected exactly one retry (initial attempt + 1 retry)"
    );
}

#[tokio::test]
async fn mutation_is_attempted_exactly_once_on_a_retryable_error() {
    let mock = MockPermissionsService::new();
    // UNAVAILABLE is retryable for reads -- but write() must not retry it.
    mock.queue_write_relationships_failure(Status::unavailable("down"));
    let calls = mock.write_relationships_calls();

    let addr = spawn_permissions_server(mock).await;
    let client = SpiceDBClient::new_plaintext(addr.to_string(), "token")
        .await
        .expect("client should connect to mock server");

    let rel = Relationship::new("document", "doc1", "viewer", "user", "alice", "")
        .expect("valid relationship");
    let mut txn = Transaction::new();
    txn.create(&rel);

    let err = client
        .write(&txn)
        .await
        .expect_err("the mock's only queued response is a failure");

    assert!(
        matches!(err, SpiceDBError::Unavailable(_)),
        "expected SpiceDBError::Unavailable, got {err:?}"
    );
    assert_eq!(
        calls.load(Ordering::SeqCst),
        1,
        "a mutation must be attempted exactly once, even on a retryable error"
    );
}

#[tokio::test]
async fn resource_exhausted_is_never_retried_on_a_read() {
    let mock = MockPermissionsService::new();
    // gRPC code 8 == RESOURCE_EXHAUSTED. Queue more than MAX_RETRIES of
    // these -- if this were ever retried, the call would still fail, but
    // the call count would be > 1.
    mock.queue_check_bulk_permissions_failure(Status::resource_exhausted("quota"));
    let calls = mock.check_bulk_permissions_calls();

    let addr = spawn_permissions_server(mock).await;
    let client = SpiceDBClient::new_plaintext(addr.to_string(), "token")
        .await
        .expect("client should connect to mock server");

    let rel = Relationship::new("document", "doc1", "view", "user", "alice", "")
        .expect("valid relationship");
    let err = client
        .check_permissions(&consistency::full(), "view", &[rel])
        .await
        .expect_err("the mock's only queued response is a failure");

    assert!(
        matches!(err, SpiceDBError::ResourceExhausted(_)),
        "expected SpiceDBError::ResourceExhausted, got {err:?}"
    );
    assert_eq!(
        calls.load(Ordering::SeqCst),
        1,
        "RESOURCE_EXHAUSTED must never be retried, even on a read"
    );
}

#[tokio::test]
async fn resource_exhausted_is_never_retried_on_a_mutation() {
    let mock = MockPermissionsService::new();
    mock.queue_write_relationships_failure(Status::resource_exhausted("quota"));
    let calls = mock.write_relationships_calls();

    let addr = spawn_permissions_server(mock).await;
    let client = SpiceDBClient::new_plaintext(addr.to_string(), "token")
        .await
        .expect("client should connect to mock server");

    let rel = Relationship::new("document", "doc1", "viewer", "user", "alice", "")
        .expect("valid relationship");
    let mut txn = Transaction::new();
    txn.create(&rel);

    let err = client
        .write(&txn)
        .await
        .expect_err("the mock's only queued response is a failure");

    assert!(matches!(err, SpiceDBError::ResourceExhausted(_)));
    assert_eq!(calls.load(Ordering::SeqCst), 1);
}

/// A non-transient error (e.g. INVALID_ARGUMENT) on a mutation must not be
/// retried either -- this was already true before this change (mutations
/// only ever got a "free" retry via the same transient check reads use), but
/// it's worth pinning down explicitly now that mutations have their own
/// code path (`call_once`).
#[tokio::test]
async fn non_transient_mutation_error_is_attempted_once() {
    let mock = MockPermissionsService::new();
    mock.queue_write_relationships_failure(Status::invalid_argument("bad relationship"));
    let calls = mock.write_relationships_calls();

    let addr = spawn_permissions_server(mock).await;
    let client = SpiceDBClient::new_plaintext(addr.to_string(), "token")
        .await
        .expect("client should connect to mock server");

    let rel = Relationship::new("document", "doc1", "viewer", "user", "alice", "")
        .expect("valid relationship");
    let mut txn = Transaction::new();
    txn.create(&rel);

    let err = client
        .write(&txn)
        .await
        .expect_err("the mock's only queued response is a failure");

    assert!(matches!(err, SpiceDBError::InvalidArgument(_)));
    assert_eq!(calls.load(Ordering::SeqCst), 1);
}
