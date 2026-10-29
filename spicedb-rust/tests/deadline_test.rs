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
use spicedb::types::CheckOptions;
use spicedb::types::Filter;
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

    let rel = spicedb::Relationship::from_tuple("document:doc1#view@user:alice")
        .expect("valid relationship");

    let start = std::time::Instant::now();
    let result = tokio::time::timeout(
        WATCHDOG,
        client.check_bulk(spicedb::BulkCheckRequest::new(
            [rel]
                .iter()
                .map(|r| {
                    spicedb::PermissionCheck::new(
                        (r).resource().clone(),
                        "view",
                        (r).subject().clone(),
                        (consistency::full()).clone(),
                    )
                })
                .collect(),
        )),
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

    let rel = spicedb::Relationship::from_tuple("document:doc1#view@user:alice")
        .expect("valid relationship");

    let start = std::time::Instant::now();
    let result = tokio::time::timeout(
        WATCHDOG,
        client.check_bulk(
            spicedb::BulkCheckRequest::new(
                [rel]
                    .iter()
                    .map(|r| {
                        spicedb::PermissionCheck::new(
                            (r).resource().clone(),
                            "view",
                            (r).subject().clone(),
                            (consistency::full()).clone(),
                        )
                    })
                    .collect(),
            )
            .with_options((CheckOptions::new().with_timeout(Duration::from_millis(200))).clone()),
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

fn read_row(id: &str, cursor: Option<&str>) -> proto::ReadRelationshipsResponse {
    proto::ReadRelationshipsResponse {
        read_at: None,
        relationship: Some(proto::Relationship {
            resource: Some(proto::ObjectReference {
                object_type: "document".to_string(),
                object_id: id.to_string(),
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
        after_result_cursor: cursor.map(|token| proto::Cursor {
            token: token.to_string(),
        }),
    }
}

fn lookup_row(id: &str) -> proto::LookupResourcesResponse {
    proto::LookupResourcesResponse {
        resource_object_id: id.into(),
        permissionship: proto::LookupPermissionship::HasPermission as i32,
        after_result_cursor: Some(proto::Cursor {
            token: format!("after-{id}"),
        }),
        ..Default::default()
    }
}

fn lookup_request() -> spicedb::LookupResourcesRequest {
    spicedb::LookupResourcesRequest::new(
        "document",
        "view",
        spicedb::SubjectRef::direct(spicedb::ObjectRef::new("user", "alice").unwrap()),
        consistency::full(),
    )
}

/// Three full pages of two items, then an empty one.
fn mock_with_lookup_pages() -> MockPermissionsService {
    let mock = MockPermissionsService::new();
    mock.push_lookup_resources_page(vec![lookup_row("a"), lookup_row("b")]);
    mock.push_lookup_resources_page(vec![lookup_row("c"), lookup_row("d")]);
    mock.push_lookup_resources_page(vec![lookup_row("e"), lookup_row("f")]);
    mock
}

/// Drains a stream, returning the items yielded before its error.
async fn drain_until_error<T>(
    stream: impl futures::Stream<Item = Result<T, SpiceDBError>>,
) -> (usize, SpiceDBError) {
    tokio::pin!(stream);
    let mut items = 0;
    while let Some(result) = stream.next().await {
        match result {
            Ok(_) => items += 1,
            Err(e) => return (items, e),
        }
    }
    panic!("stream ended without an error after {items} item(s)");
}

#[tokio::test]
async fn lookup_stalling_mid_page_fails_after_the_per_page_default_with_no_caller_option() {
    let mock = mock_with_lookup_pages();
    // Page 3 streams its first item, then stalls far past the default.
    mock.stall_lookup_resources_page(3, STALL);
    let calls = mock.lookup_resources_calls();
    let addr = spawn_permissions_server(mock).await;
    let client = SpiceDBClient::builder(addr.to_string(), "token")
        .plaintext()
        .default_timeout(Duration::from_millis(300))
        .build()
        .await
        .expect("client should connect to mock server");

    let start = std::time::Instant::now();
    let request = lookup_request().with_options(
        spicedb::LookupResourcesOptions::new()
            .with_page_size(std::num::NonZeroU32::new(2).unwrap()),
    );
    let (items, err) = tokio::time::timeout(
        WATCHDOG,
        drain_until_error(client.lookup_resources(request)),
    )
    .await
    .expect("a page stalling mid-read must not hang the lookup");
    let elapsed = start.elapsed();

    assert!(
        matches!(err, SpiceDBError::DeadlineExceeded(_)),
        "expected DeadlineExceeded, got {err:?}"
    );
    assert!(err.to_string().contains("page deadline"), "{err}");
    assert_eq!(
        items, 5,
        "pages 1 and 2 and page 3's first item are delivered"
    );
    assert_eq!(calls.load(std::sync::atomic::Ordering::SeqCst), 3);
    assert!(
        elapsed < STALL,
        "the per-page default must fail the stalled page (elapsed={elapsed:?})"
    );
}

#[tokio::test]
async fn the_per_page_default_bounds_each_page_not_the_whole_read() {
    // Every page stalls 250 ms before streaming; three pages take ~750 ms,
    // longer than the 400 ms default, but each page finishes within it.
    let mock = MockPermissionsService::new();
    mock.stall_read_relationships(Duration::from_millis(250));
    mock.push_read_relationships_page(vec![read_row("a", Some("c1")), read_row("b", Some("c2"))]);
    mock.push_read_relationships_page(vec![read_row("c", Some("c3")), read_row("d", Some("c4"))]);
    mock.push_read_relationships_page(vec![read_row("e", Some("c5"))]);
    let addr = spawn_permissions_server(mock).await;
    let client = SpiceDBClient::builder(addr.to_string(), "token")
        .plaintext()
        .default_timeout(Duration::from_millis(400))
        .build()
        .await
        .expect("client should connect to mock server");

    let start = std::time::Instant::now();
    let request =
        spicedb::ReadRelationshipsRequest::new(Filter::new("document"), consistency::full())
            .with_options(
                spicedb::ReadRelationshipsOptions::new()
                    .with_page_size(std::num::NonZeroU32::new(2).unwrap()),
            );
    let items: Vec<_> = tokio::time::timeout(
        WATCHDOG,
        client.read_relationships(request).collect::<Vec<_>>(),
    )
    .await
    .expect("watchdog");
    let elapsed = start.elapsed();
    let ids: Vec<String> = items
        .into_iter()
        .map(|r| {
            r.expect("no page exceeded the default")
                .relationship
                .resource()
                .object_id()
                .to_owned()
        })
        .collect();
    assert_eq!(ids, ["a", "b", "c", "d", "e"]);
    assert!(
        elapsed > Duration::from_millis(400),
        "the whole read outlived one default deadline (elapsed={elapsed:?})"
    );
}

#[tokio::test]
async fn an_earlier_caller_total_deadline_wins_over_the_per_page_default() {
    let mock = mock_with_lookup_pages();
    mock.stall_lookup_resources_page(3, STALL);
    let addr = spawn_permissions_server(mock).await;
    let client = SpiceDBClient::builder(addr.to_string(), "token")
        .plaintext()
        .default_timeout(Duration::from_secs(60))
        .build()
        .await
        .expect("client should connect to mock server");

    let start = std::time::Instant::now();
    let request = lookup_request().with_options(
        spicedb::LookupResourcesOptions::new()
            .with_page_size(std::num::NonZeroU32::new(2).unwrap())
            .with_timeout(Duration::from_millis(300)),
    );
    let (_, err) = tokio::time::timeout(
        WATCHDOG,
        drain_until_error(client.lookup_resources(request)),
    )
    .await
    .expect("watchdog");
    assert!(
        matches!(err, SpiceDBError::DeadlineExceeded(_)),
        "expected DeadlineExceeded, got {err:?}"
    );
    assert!(
        err.to_string().contains("total operation deadline"),
        "{err}"
    );
    assert!(start.elapsed() < STALL);
}

#[tokio::test]
async fn disabling_the_default_deadline_lets_a_slow_page_finish() {
    let mock = MockPermissionsService::new();
    mock.stall_read_relationships(STALL);
    mock.push_read_relationships_page(vec![read_row("a", None)]);
    let addr = spawn_permissions_server(mock).await;
    let client = SpiceDBClient::builder(addr.to_string(), "token")
        .plaintext()
        .default_timeout(Duration::from_millis(100))
        .default_deadline(spicedb::DefaultDeadline::Disabled)
        .build()
        .await
        .expect("client should connect to mock server");

    let start = std::time::Instant::now();
    let items: Vec<_> = tokio::time::timeout(
        WATCHDOG,
        client
            .read_relationships(spicedb::ReadRelationshipsRequest::new(
                Filter::new("document"),
                consistency::full(),
            ))
            .collect::<Vec<_>>(),
    )
    .await
    .expect("watchdog");
    assert_eq!(items.len(), 1);
    assert!(items[0].is_ok(), "{:?}", items[0]);
    assert!(start.elapsed() >= STALL);
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

    let rel = spicedb::Relationship::from_tuple("document:doc1#viewer@user:alice")
        .expect("valid relationship");

    let start = std::time::Instant::now();
    let result = tokio::time::timeout(
        WATCHDOG,
        client.import_relationships(spicedb::ImportRelationshipsRequest::new(
            futures::stream::iter((vec![rel]).into_iter().map(Ok)),
        )),
    )
    .await
    .expect(
        "call did not return within the watchdog -- import_relationships must not inherit \
             the unary default",
    );
    let elapsed = start.elapsed();

    let num_loaded = result.expect("a stalling server must still eventually respond");
    assert_eq!(num_loaded.imported_count, 1);
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

    let rel = spicedb::Relationship::from_tuple("document:doc1#viewer@user:alice")
        .expect("valid relationship");

    let start = std::time::Instant::now();
    let result = tokio::time::timeout(
        WATCHDOG,
        client.import_relationships(
            spicedb::ImportRelationshipsRequest::new(futures::stream::iter(
                (vec![rel]).into_iter().map(Ok),
            ))
            .with_options(
                spicedb::ImportRelationshipsOptions::new().with_timeout(Duration::from_millis(200)),
            ),
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
        "an explicit 200ms timeout on import_relationships_with_timeout must still fire \
         (elapsed={elapsed:?})"
    );
}
