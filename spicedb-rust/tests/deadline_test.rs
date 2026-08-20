//! Call-deadline enforcement. Root DESIGN.md, "RULE: A unary call must have
//! a deadline".
//!
//! Runs a real in-process gRPC server (`support::MockPermissionsService`)
//! whose handlers deliberately stall, so these tests exercise the real
//! `tonic::Request::set_timeout` plumbing end to end. A mocked/queued
//! response can't prove a deadline is actually enforced -- tonic's deadline
//! machinery lives in the transport layer, below any application-level mock.
//!
//! Every stalling handler self-bounds its stall to `STALL` rather than
//! blocking forever: long enough to dwarf the tiny per-test deadlines below
//! (proving enforcement, not luck), short enough that a test failure doesn't
//! hang the suite for long even without the watchdog. Each call is
//! additionally wrapped in `tokio::time::timeout(WATCHDOG, ...)`, which fails
//! the test (instead of hanging it, and CI along with it) if a regression
//! reintroduces an unbounded call.

mod support;

use std::time::Duration;

use futures::StreamExt;
use spicedb::client::SpiceDBClient;
use spicedb::consistency;
use spicedb::error::SpiceDBError;
use spicedb::types::{Filter, Relationship};
use spicedb_proto::authzed::api::v1 as proto;

use support::{spawn_permissions_server, MockPermissionsService};

/// How long a stalling handler stalls before finally answering. Every
/// per-test deadline below is far smaller than this, so a passing test
/// proves the client's timeout fired -- it did not just get lucky with
/// scheduling.
const STALL: Duration = Duration::from_secs(2);

/// Wall-clock budget for a single watchdog-wrapped call.
const WATCHDOG: Duration = Duration::from_secs(10);

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
async fn unary_call_against_a_stub_that_never_responds_times_out_with_deadline_exceeded() {
    let mock = MockPermissionsService::new();
    mock.stall_check_bulk_permissions(STALL);
    mock.push_check_bulk_permissions_response(has_permission_response());

    let addr = spawn_permissions_server(mock).await;
    let client = SpiceDBClient::builder(addr.to_string(), "token")
        .plaintext()
        .default_timeout(Duration::from_millis(200))
        .build()
        .await
        .expect("client should connect to mock server");

    let rel = Relationship::new("document", "doc1", "view", "user", "alice", "")
        .expect("valid relationship");

    let start = std::time::Instant::now();
    let result = tokio::time::timeout(
        WATCHDOG,
        client.check_permissions(&consistency::full(), "view", &[rel]),
    )
    .await
    .expect("call did not return within the watchdog -- deadline enforcement regressed");
    let elapsed = start.elapsed();

    let err = result.expect_err("a stalling server must not let the call succeed");
    assert!(
        matches!(err, SpiceDBError::DeadlineExceeded(_)),
        "expected SpiceDBError::DeadlineExceeded, got {err:?}"
    );
    assert!(
        elapsed < STALL,
        "the call must fail at the ~200ms client default, not wait out the server's \
         {STALL:?} stall (elapsed={elapsed:?})"
    );
}

#[tokio::test]
async fn per_call_timeout_overrides_a_much_larger_client_default() {
    let mock = MockPermissionsService::new();
    mock.stall_check_bulk_permissions(STALL);
    mock.push_check_bulk_permissions_response(has_permission_response());

    let addr = spawn_permissions_server(mock).await;
    // Client default is larger than the server's stall -- if the per-call
    // override did not take effect, this call would not fail quickly.
    let client = SpiceDBClient::builder(addr.to_string(), "token")
        .plaintext()
        .default_timeout(STALL * 10)
        .build()
        .await
        .expect("client should connect to mock server");

    let rel = Relationship::new("document", "doc1", "view", "user", "alice", "")
        .expect("valid relationship");

    let start = std::time::Instant::now();
    let result = tokio::time::timeout(
        WATCHDOG,
        client.check_permissions_with_timeout(
            &consistency::full(),
            "view",
            &[rel],
            Duration::from_millis(200),
        ),
    )
    .await
    .expect("call did not return within the watchdog -- deadline enforcement regressed");
    let elapsed = start.elapsed();

    let err = result.expect_err("a stalling server must not let the call succeed");
    assert!(
        matches!(err, SpiceDBError::DeadlineExceeded(_)),
        "expected SpiceDBError::DeadlineExceeded, got {err:?}"
    );
    assert!(
        elapsed < STALL,
        "the per-call timeout=200ms must override the large client default (elapsed={elapsed:?})"
    );
}

#[tokio::test]
async fn streaming_call_does_not_inherit_the_unary_default() {
    let mock = MockPermissionsService::new();
    mock.stall_read_relationships(STALL);
    mock.push_read_relationships_page(vec![proto::ReadRelationshipsResponse {
        read_at: None,
        relationship: Some(proto::Relationship {
            resource: Some(proto::ObjectReference {
                object_type: "document".to_string(),
                object_id: "a".to_string(),
            }),
            relation: "viewer".to_string(),
            subject: Some(proto::SubjectReference {
                object: Some(proto::ObjectReference {
                    object_type: "user".to_string(),
                    object_id: "jimmy".to_string(),
                }),
                optional_relation: String::new(),
            }),
            optional_caveat: None,
            optional_expires_at: None,
        }),
        after_result_cursor: None,
    }]);

    let addr = spawn_permissions_server(mock).await;
    // default_timeout is far smaller than the server's stall. If
    // read_relationships inherited it, this would fail with
    // DeadlineExceeded instead of yielding the item.
    let client = SpiceDBClient::builder(addr.to_string(), "token")
        .plaintext()
        .default_timeout(Duration::from_millis(100))
        .build()
        .await
        .expect("client should connect to mock server");

    let filter = Filter::new("document");
    let start = std::time::Instant::now();
    let items: Vec<_> = tokio::time::timeout(WATCHDOG, async {
        let stream = client.read_relationships(&consistency::full(), &filter);
        tokio::pin!(stream);
        stream.collect::<Vec<_>>().await
    })
    .await
    .expect("stream did not complete within the watchdog -- deadline enforcement regressed");
    let elapsed = start.elapsed();

    let resource_ids: Vec<String> = items
        .into_iter()
        .map(|r| r.expect("stream must not fail").resource_id)
        .collect();
    assert_eq!(resource_ids, vec!["a".to_string()]);
    assert!(
        elapsed >= STALL,
        "the stream must outlive the tiny unary default -- it should have waited out the \
         server's {STALL:?} stall (elapsed={elapsed:?})"
    );
}

#[tokio::test]
async fn import_relationships_does_not_inherit_the_unary_default() {
    // import_relationships (ImportBulkRelationships) is client-streaming:
    // its duration scales with the size of the caller's dataset, not with
    // server latency, so root DESIGN.md's "RULE: A unary call must have a
    // deadline" (clause 3, amended to cover client-streaming and
    // bidirectional RPCs) excludes it from `default_timeout`. default_timeout
    // here is far smaller than the server's stall -- if import_relationships
    // inherited it, this call would fail with DeadlineExceeded instead of
    // completing.
    let mock = MockPermissionsService::new();
    mock.stall_import_bulk_relationships(STALL);

    let addr = spawn_permissions_server(mock).await;
    let client = SpiceDBClient::builder(addr.to_string(), "token")
        .plaintext()
        .default_timeout(Duration::from_millis(100))
        .build()
        .await
        .expect("client should connect to mock server");

    let rel = Relationship::new("document", "doc1", "viewer", "user", "alice", "")
        .expect("valid relationship");

    let start = std::time::Instant::now();
    let result = tokio::time::timeout(WATCHDOG, client.import_relationships(vec![rel]))
        .await
        .expect(
            "call did not return within the watchdog -- import_relationships must not inherit \
             the unary default",
        );
    let elapsed = start.elapsed();

    let num_loaded = result.expect("a stalling server must still eventually respond");
    assert_eq!(num_loaded, 1);
    assert!(
        elapsed >= STALL,
        "import_relationships must outlive the tiny unary default -- it should have waited out \
         the server's {STALL:?} stall (elapsed={elapsed:?})"
    );
}

#[tokio::test]
async fn import_relationships_with_timeout_still_bounds_the_call() {
    // The exclusion above is from the *default*, not from the ability to
    // bound the call at all -- import_relationships_with_timeout must still
    // fail quickly against a stalling server.
    let mock = MockPermissionsService::new();
    mock.stall_import_bulk_relationships(STALL);

    let addr = spawn_permissions_server(mock).await;
    let client = SpiceDBClient::builder(addr.to_string(), "token")
        .plaintext()
        .build()
        .await
        .expect("client should connect to mock server");

    let rel = Relationship::new("document", "doc1", "viewer", "user", "alice", "")
        .expect("valid relationship");

    let start = std::time::Instant::now();
    let result = tokio::time::timeout(
        WATCHDOG,
        client.import_relationships_with_timeout(vec![rel], Duration::from_millis(200)),
    )
    .await
    .expect("call did not return within the watchdog -- deadline enforcement regressed");
    let elapsed = start.elapsed();

    let err = result.expect_err("a stalling server must not let the call succeed");
    assert!(
        matches!(err, SpiceDBError::DeadlineExceeded(_)),
        "expected SpiceDBError::DeadlineExceeded, got {err:?}"
    );
    assert!(
        elapsed < STALL,
        "an explicit 200ms timeout on import_relationships_with_timeout must still fire \
         (elapsed={elapsed:?})"
    );
}
