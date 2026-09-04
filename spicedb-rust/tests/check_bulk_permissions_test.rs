//! Behavioral tests for `check_permissions`' per-item error handling and
//! `CheckResult` mapping (permissionship, missing_context, checked_at).
//!
//! The primary purpose of this file is to prove the fix for a latent bug: a
//! per-item `CheckBulkPermissions` error used to be surfaced to the caller as
//! a hardcoded `SpiceDBError::InvalidArgument`, regardless of the item's
//! actual gRPC status code (`err_resp.code`). That meant a per-item
//! `PERMISSION_DENIED` was reported to the caller as `InvalidArgument` —
//! worse than falling back to a base error type, since it actively lies
//! about the failure mode. `check_permissions` now routes per-item errors
//! through `error::from_grpc_status`, the same mapper used by every other
//! error path in this client.

mod support;

use spicedb::client::SpiceDBClient;
use spicedb::consistency;
use spicedb::error::SpiceDBError;
use spicedb::types::{Permissionship, Relationship};
use spicedb_proto::authzed::api::v1 as proto;
use spicedb_proto::google::rpc::Status as RpcStatus;
use tonic_types::pb::ErrorInfo;

use support::{spawn_permissions_server, MockPermissionsService};

fn item_pair(
    permissionship: proto::check_permission_response::Permissionship,
    partial_caveat_info: Option<proto::PartialCaveatInfo>,
) -> proto::CheckBulkPermissionsPair {
    proto::CheckBulkPermissionsPair {
        request: None,
        response: Some(proto::check_bulk_permissions_pair::Response::Item(
            proto::CheckBulkPermissionsResponseItem {
                permissionship: permissionship as i32,
                partial_caveat_info,
                debug_trace: None,
            },
        )),
    }
}

fn error_pair(code: i32, message: &str) -> proto::CheckBulkPermissionsPair {
    proto::CheckBulkPermissionsPair {
        request: None,
        response: Some(proto::check_bulk_permissions_pair::Response::Error(
            RpcStatus {
                code,
                message: message.to_string(),
                details: Vec::new(),
            },
        )),
    }
}

/// A per-item error carrying a `google.rpc.ErrorInfo` detail, the shape SpiceDB
/// uses to explain a failure. The detail rides on the item's own status, not on
/// the RPC's, so it only reaches the caller if the per-item path preserves it.
fn error_pair_with_reason(
    code: i32,
    message: &str,
    reason: &str,
    metadata: &[(&str, &str)],
) -> proto::CheckBulkPermissionsPair {
    let info = ErrorInfo {
        reason: reason.to_string(),
        domain: "authzed.com".to_string(),
        metadata: metadata
            .iter()
            .map(|(k, v)| (k.to_string(), v.to_string()))
            .collect(),
    };
    proto::CheckBulkPermissionsPair {
        request: None,
        response: Some(proto::check_bulk_permissions_pair::Response::Error(
            RpcStatus {
                code,
                message: message.to_string(),
                details: vec![prost_types::Any {
                    type_url: "type.googleapis.com/google.rpc.ErrorInfo".to_string(),
                    value: prost::Message::encode_to_vec(&info),
                }],
            },
        )),
    }
}

/// A pair whose `response` oneof is unset — neither `Item` nor `Error`.
/// The proto schema guarantees a well-behaved server never sends this, but
/// nothing on the wire prevents it, and `Option<Response>` is exactly the
/// shape a malformed/adversarial/future-incompatible response would take.
fn malformed_pair() -> proto::CheckBulkPermissionsPair {
    proto::CheckBulkPermissionsPair {
        request: None,
        response: None,
    }
}

// ---------------------------------------------------------------------------
// HARD REQUIREMENT: per-item errors must route through the real error
// mapper, not a hardcoded InvalidArgument.
// ---------------------------------------------------------------------------

#[tokio::test]
async fn check_permissions_maps_per_item_permission_denied_not_invalid_argument() {
    let mock = MockPermissionsService::new();
    // gRPC code 7 == PERMISSION_DENIED.
    mock.push_check_bulk_permissions_response(proto::CheckBulkPermissionsResponse {
        checked_at: Some(proto::ZedToken {
            token: "rev-1".to_string(),
        }),
        pairs: vec![error_pair(7, "alice may not view document:doc1")],
    });

    let addr = spawn_permissions_server(mock).await;
    let client = SpiceDBClient::new_plaintext(addr.to_string(), "token")
        .await
        .expect("client should connect to mock server");

    let rel = Relationship::new("document", "doc1", "view", "user", "alice", "")
        .expect("valid relationship");
    let err = client
        .check_permissions(&consistency::full(), "view", &[rel])
        .await
        .expect_err("a per-item error must surface as an Err");

    assert!(
        matches!(err, SpiceDBError::PermissionDenied(_)),
        "expected SpiceDBError::PermissionDenied for a per-item PERMISSION_DENIED (code 7), \
         got {err:?}. Before the fix, this hardcoded InvalidArgument regardless of err_resp.code."
    );
}

#[tokio::test]
async fn check_permissions_maps_per_item_not_found_via_real_mapper() {
    let mock = MockPermissionsService::new();
    // gRPC code 5 == NOT_FOUND. A second, independent code proves this isn't
    // a one-off special case for PERMISSION_DENIED — every code routes
    // through from_grpc_status.
    mock.push_check_bulk_permissions_response(proto::CheckBulkPermissionsResponse {
        checked_at: Some(proto::ZedToken {
            token: "rev-1".to_string(),
        }),
        pairs: vec![error_pair(5, "document:doc1 does not exist")],
    });

    let addr = spawn_permissions_server(mock).await;
    let client = SpiceDBClient::new_plaintext(addr.to_string(), "token")
        .await
        .expect("client should connect to mock server");

    let rel = Relationship::new("document", "doc1", "view", "user", "alice", "")
        .expect("valid relationship");
    let err = client
        .check_permissions(&consistency::full(), "view", &[rel])
        .await
        .expect_err("a per-item error must surface as an Err");

    assert!(
        matches!(err, SpiceDBError::NotFound(_)),
        "expected SpiceDBError::NotFound for a per-item NOT_FOUND (code 5), got {err:?}"
    );
}

// ---------------------------------------------------------------------------
// CheckResult mapping and checked_at propagation, exercised through the real
// client (not just the mapper function directly).
// ---------------------------------------------------------------------------

#[tokio::test]
async fn check_permissions_maps_items_and_propagates_response_level_checked_at() {
    let mock = MockPermissionsService::new();
    mock.push_check_bulk_permissions_response(proto::CheckBulkPermissionsResponse {
        checked_at: Some(proto::ZedToken {
            token: "rev-42".to_string(),
        }),
        pairs: vec![
            item_pair(
                proto::check_permission_response::Permissionship::HasPermission,
                None,
            ),
            item_pair(
                proto::check_permission_response::Permissionship::ConditionalPermission,
                Some(proto::PartialCaveatInfo {
                    missing_required_context: vec!["now".to_string()],
                }),
            ),
        ],
    });

    let addr = spawn_permissions_server(mock).await;
    let client = SpiceDBClient::new_plaintext(addr.to_string(), "token")
        .await
        .expect("client should connect to mock server");

    let rel1 =
        Relationship::new("document", "doc1", "view", "user", "alice", "").expect("valid rel");
    let rel2 =
        Relationship::new("document", "doc2", "view", "user", "alice", "").expect("valid rel");
    let results = client
        .check_permissions(&consistency::full(), "view", &[rel1, rel2])
        .await
        .expect("check should succeed");

    assert_eq!(results.len(), 2);

    assert_eq!(results[0].permissionship, Permissionship::HasPermission);
    assert!(results[0].has_permission());
    assert_eq!(results[0].checked_at, "rev-42");
    assert!(results[0].missing_context.is_empty());

    assert_eq!(
        results[1].permissionship,
        Permissionship::ConditionalPermission
    );
    assert!(!results[1].has_permission());
    assert_eq!(
        results[1].checked_at, "rev-42",
        "checked_at is response-level in CheckBulkPermissionsResponse — every \
         item in the batch must get the same token"
    );
    assert_eq!(results[1].missing_context, vec!["now".to_string()]);
}

// ---------------------------------------------------------------------------
// check_any / check_all: a Conditional result must not count as granted.
// ---------------------------------------------------------------------------

fn conditional_response() -> proto::CheckBulkPermissionsResponse {
    proto::CheckBulkPermissionsResponse {
        checked_at: Some(proto::ZedToken {
            token: "rev-1".to_string(),
        }),
        pairs: vec![item_pair(
            proto::check_permission_response::Permissionship::ConditionalPermission,
            Some(proto::PartialCaveatInfo {
                missing_required_context: vec!["now".to_string()],
            }),
        )],
    }
}

#[tokio::test]
async fn check_any_does_not_count_conditional_as_granted() {
    let mock = MockPermissionsService::new();
    mock.push_check_bulk_permissions_response(conditional_response());

    let addr = spawn_permissions_server(mock).await;
    let client = SpiceDBClient::new_plaintext(addr.to_string(), "token")
        .await
        .expect("client should connect to mock server");

    let rel =
        Relationship::new("document", "doc1", "view", "user", "alice", "").expect("valid rel");
    let any = client
        .check_any(&consistency::full(), "view", &[rel])
        .await
        .expect("check_any should succeed");

    assert!(
        !any,
        "a Conditional result must not count as granted for check_any"
    );
}

#[tokio::test]
async fn check_all_does_not_count_conditional_as_granted() {
    let mock = MockPermissionsService::new();
    mock.push_check_bulk_permissions_response(conditional_response());

    let addr = spawn_permissions_server(mock).await;
    let client = SpiceDBClient::new_plaintext(addr.to_string(), "token")
        .await
        .expect("client should connect to mock server");

    let rel =
        Relationship::new("document", "doc1", "view", "user", "alice", "").expect("valid rel");
    let all = client
        .check_all(&consistency::full(), "view", &[rel])
        .await
        .expect("check_all should succeed");

    assert!(
        !all,
        "a Conditional result must not count as granted for check_all"
    );
}

// ---------------------------------------------------------------------------
// check_all must not be vacuously true on zero relationships:
// Iterator::all is vacuously true over an empty sequence, so a caller
// gating on check_all(cs, "edit", &docs_to_rels(&docs)) would have been
// silently granted whenever the derived relationships slice came up empty
// -- a filter that matched nothing, an upstream returning an empty Vec.
// Root DESIGN.md: "An aggregate over zero checks is not a grant."
// check_any is deliberately left alone -- Iterator::any is already
// correctly false on an empty sequence.
// ---------------------------------------------------------------------------

#[tokio::test]
async fn check_all_returns_false_for_zero_relationships() {
    let mock = MockPermissionsService::new();
    let calls = mock.check_bulk_permissions_calls();

    let addr = spawn_permissions_server(mock).await;
    let client = SpiceDBClient::new_plaintext(addr.to_string(), "token")
        .await
        .expect("client should connect to mock server");

    let all = client
        .check_all(&consistency::full(), "view", &[])
        .await
        .expect("check_all should succeed");

    assert!(
        !all,
        "check_all must return false, not vacuously true, for zero relationships"
    );
    assert_eq!(
        calls.load(std::sync::atomic::Ordering::SeqCst),
        0,
        "check_all must not consult the server for zero relationships"
    );
}

// ---------------------------------------------------------------------------
// HARD REQUIREMENT: a malformed pair (response oneof unset — neither Item
// nor Error) must fail loudly, not silently vanish from the results.
//
// Before the fix, `None => {}` in the pair-handling match simply skipped the
// index, so `Vec<CheckResult>` came back shorter than the input slice and
// every subsequent `results[i]` was misaligned with `relationships[i]` — a
// caller zipping results against inputs would attribute an answer to the
// wrong resource.
// ---------------------------------------------------------------------------

#[tokio::test]
async fn check_permissions_errors_on_malformed_pair_instead_of_shrinking_results() {
    let mock = MockPermissionsService::new();
    mock.push_check_bulk_permissions_response(proto::CheckBulkPermissionsResponse {
        checked_at: Some(proto::ZedToken {
            token: "rev-1".to_string(),
        }),
        pairs: vec![malformed_pair()],
    });

    let addr = spawn_permissions_server(mock).await;
    let client = SpiceDBClient::new_plaintext(addr.to_string(), "token")
        .await
        .expect("client should connect to mock server");

    let rel =
        Relationship::new("document", "doc1", "view", "user", "alice", "").expect("valid rel");
    let err = client
        .check_permissions(&consistency::full(), "view", &[rel])
        .await
        .expect_err(
            "a malformed pair (neither Item nor Error) must surface as an Err, not silently \
             produce a short results vector",
        );

    assert!(
        matches!(err, SpiceDBError::Status { code: 13, .. }),
        "expected SpiceDBError::Status{{code: 13 (INTERNAL), ..}} for a malformed pair, got {err:?}"
    );
}

#[tokio::test]
async fn check_permissions_malformed_pair_does_not_desync_results_from_inputs() {
    // Three relationships; the MIDDLE pair is malformed. Before the fix this
    // returned Ok(vec![result_for_rel1, result_for_rel3]) — a 2-length
    // vector for a 3-length input, silently attributing rel3's answer to
    // index 1. Proves the fix rejects the whole batch instead of returning
    // a misaligned-but-successful result.
    let mock = MockPermissionsService::new();
    mock.push_check_bulk_permissions_response(proto::CheckBulkPermissionsResponse {
        checked_at: Some(proto::ZedToken {
            token: "rev-1".to_string(),
        }),
        pairs: vec![
            item_pair(
                proto::check_permission_response::Permissionship::HasPermission,
                None,
            ),
            malformed_pair(),
            item_pair(
                proto::check_permission_response::Permissionship::HasPermission,
                None,
            ),
        ],
    });

    let addr = spawn_permissions_server(mock).await;
    let client = SpiceDBClient::new_plaintext(addr.to_string(), "token")
        .await
        .expect("client should connect to mock server");

    let rel1 =
        Relationship::new("document", "doc1", "view", "user", "alice", "").expect("valid rel");
    let rel2 =
        Relationship::new("document", "doc2", "view", "user", "alice", "").expect("valid rel");
    let rel3 =
        Relationship::new("document", "doc3", "view", "user", "alice", "").expect("valid rel");

    let result = client
        .check_permissions(&consistency::full(), "view", &[rel1, rel2, rel3])
        .await;

    assert!(
        result.is_err(),
        "a batch containing a malformed pair must fail entirely, not return a \
         shorter-than-input Vec<CheckResult> that desyncs results[i] from relationships[i]"
    );
}

// ---------------------------------------------------------------------------
// HARD REQUIREMENT: a response with fewer pairs than request items must fail
// loudly, not silently return a short-but-"successful" Vec<CheckResult>.
//
// The proto guarantees pairs are returned in request order but says nothing
// about count. Without an explicit length check, a short response would
// silently desync results[i] from relationships[i] for every item after the
// gap -- one resource's answer attributed to another. This is distinct from
// the malformed-pair case above: every pair present here is well-formed, the
// response is just short.
// ---------------------------------------------------------------------------

#[tokio::test]
async fn check_permissions_errors_when_response_has_fewer_pairs_than_request_items() {
    let mock = MockPermissionsService::new();
    mock.push_check_bulk_permissions_response(proto::CheckBulkPermissionsResponse {
        checked_at: Some(proto::ZedToken {
            token: "rev-1".to_string(),
        }),
        // Two relationships requested, only one pair returned.
        pairs: vec![item_pair(
            proto::check_permission_response::Permissionship::HasPermission,
            None,
        )],
    });

    let addr = spawn_permissions_server(mock).await;
    let client = SpiceDBClient::new_plaintext(addr.to_string(), "token")
        .await
        .expect("client should connect to mock server");

    let rel1 =
        Relationship::new("document", "doc1", "view", "user", "alice", "").expect("valid rel");
    let rel2 =
        Relationship::new("document", "doc2", "view", "user", "alice", "").expect("valid rel");

    let err = client
        .check_permissions(&consistency::full(), "view", &[rel1, rel2])
        .await
        .expect_err(
            "a response with fewer pairs than request items must surface as an Err, not \
             silently produce a short results vector",
        );

    assert!(
        matches!(err, SpiceDBError::Status { code: 13, .. }),
        "expected SpiceDBError::Status{{code: 13 (INTERNAL), ..}} for a pairs/items length \
         mismatch, got {err:?}"
    );
}

// ---------------------------------------------------------------------------
// Regression test for spicedb-rust CHANGELOG's documented "known residual
// gap": the singular check_permission called `results.remove(0)`
// unconditionally, which panics ("removal index (is 0) should be < len (is
// 0)") -- inside the caller's task, on the authorization hot path -- when
// the server returns zero pairs for a one-item request. The length guard
// added to check_permissions_with_options above (proven by the previous
// test) now rejects a zero-pair response before check_permission ever
// reaches `results.remove(0)`, making the panic unreachable. This test
// proves that path returns a typed error instead of panicking the caller's
// task.
// ---------------------------------------------------------------------------

#[tokio::test]
async fn check_permission_errors_instead_of_panicking_on_zero_pairs() {
    let mock = MockPermissionsService::new();
    mock.push_check_bulk_permissions_response(proto::CheckBulkPermissionsResponse {
        checked_at: Some(proto::ZedToken {
            token: "rev-1".to_string(),
        }),
        pairs: vec![],
    });

    let addr = spawn_permissions_server(mock).await;
    let client = SpiceDBClient::new_plaintext(addr.to_string(), "token")
        .await
        .expect("client should connect to mock server");

    let rel =
        Relationship::new("document", "doc1", "view", "user", "alice", "").expect("valid rel");

    let err = client
        .check_permission(&consistency::full(), "view", &rel)
        .await
        .expect_err(
            "check_permission must return a typed error, not panic, when the server returns \
             zero pairs for a one-item request",
        );

    assert!(
        matches!(err, SpiceDBError::Status { code: 13, .. }),
        "expected SpiceDBError::Status{{code: 13 (INTERNAL), ..}} for a pairs/items length \
         mismatch, got {err:?}"
    );
}

/// A per-item failure must reach the caller carrying the same structured reason
/// an RPC-level failure does. The per-item `google.rpc.Status` used to be
/// reduced to a code and a message before mapping, which silently dropped the
/// item's own `ErrorInfo` -- and dropping it fails quietly, with an empty
/// reason and nothing red. See root DESIGN.md, "RULE: Error mapping must not
/// lose the server's detail".
#[tokio::test]
async fn per_item_error_carries_its_own_error_reason_and_metadata() {
    let mock = MockPermissionsService::new();
    // gRPC code 8 == RESOURCE_EXHAUSTED.
    mock.push_check_bulk_permissions_response(proto::CheckBulkPermissionsResponse {
        checked_at: Some(proto::ZedToken {
            token: "rev-1".to_string(),
        }),
        pairs: vec![error_pair_with_reason(
            8,
            "max depth exceeded",
            "ERROR_REASON_MAXIMUM_DEPTH_EXCEEDED",
            &[("maximum_depth_allowed", "50")],
        )],
    });

    let addr = spawn_permissions_server(mock).await;
    let client = SpiceDBClient::new_plaintext(addr.to_string(), "token")
        .await
        .expect("client should connect to mock server");

    let rel = Relationship::new("document", "doc1", "view", "user", "alice", "")
        .expect("valid relationship");
    let err = client
        .check_permissions(&consistency::full(), "view", &[rel])
        .await
        .expect_err("a per-item error must surface as an Err");

    assert!(
        matches!(err, SpiceDBError::ResourceExhausted(_)),
        "expected ResourceExhausted for a per-item RESOURCE_EXHAUSTED, got {err:?}"
    );
    assert_eq!(err.reason(), Some("ERROR_REASON_MAXIMUM_DEPTH_EXCEEDED"));
    assert_eq!(err.reason_domain(), Some("authzed.com"));
    assert_eq!(
        err.reason_metadata()
            .get("maximum_depth_allowed")
            .map(String::as_str),
        Some("50")
    );
}

/// The chunk size `check_permissions` splits at. Mirrors the crate-private
/// `DEFAULT_CHECK_BATCH_SIZE`.
const CHECK_BATCH_SIZE: usize = 1_000;

fn numbered_rels(n: usize) -> Vec<Relationship> {
    (0..n)
        .map(|i| {
            Relationship::new("document", format!("{i:05}"), "view", "user", "alice", "")
                .expect("valid relationship")
        })
        .collect()
}

fn ok_pair() -> proto::CheckBulkPermissionsPair {
    item_pair(
        proto::check_permission_response::Permissionship::HasPermission,
        None,
    )
}

/// Every "check item N" message must name the caller's own index, not the
/// index within whichever chunk carried the failure.
///
/// `check_permissions` splits at `DEFAULT_CHECK_BATCH_SIZE` and mapped each
/// response with a chunk-local `enumerate()`, so a failure at the caller's
/// relationship 1003 reported `check item 3` -- one resource's answer
/// attributed to another, which is exactly the misattribution the pair-count
/// guard exists to prevent, relocated into the diagnostic. This client chunked
/// before the other six did, so it carried the defect longest.
#[tokio::test]
async fn per_item_error_reports_the_callers_absolute_index() {
    let failing = CHECK_BATCH_SIZE + 3;

    let mock = MockPermissionsService::new();
    // First chunk: 1,000 successful items.
    mock.push_check_bulk_permissions_response(proto::CheckBulkPermissionsResponse {
        checked_at: None,
        pairs: (0..CHECK_BATCH_SIZE).map(|_| ok_pair()).collect(),
    });
    // Second chunk: the failure sits at local index 3, absolute index 1003.
    mock.push_check_bulk_permissions_response(proto::CheckBulkPermissionsResponse {
        checked_at: None,
        pairs: (0..CHECK_BATCH_SIZE)
            .map(|i| {
                if i == 3 {
                    error_pair(7, "nope")
                } else {
                    ok_pair()
                }
            })
            .collect(),
    });

    let addr = spawn_permissions_server(mock).await;
    let client = SpiceDBClient::new_plaintext(addr.to_string(), "token")
        .await
        .expect("client should connect to mock server");

    let err = client
        .check_permissions(
            &consistency::full(),
            "view",
            &numbered_rels(CHECK_BATCH_SIZE * 2),
        )
        .await
        .expect_err("a per-item error must surface as an Err");

    let message = err.to_string();
    assert!(
        message.contains(&format!("check item {failing}:")),
        "must name the caller's index ({failing}), not the chunk-relative 3: {message}"
    );
    assert!(
        !message.contains("check item 3:"),
        "must not report the chunk-relative index: {message}"
    );
}

/// The same requirement on the malformed-oneof guard, which builds its own
/// message rather than routing through the error mapper.
#[tokio::test]
async fn malformed_pair_reports_the_callers_absolute_index() {
    let failing = CHECK_BATCH_SIZE + 5;

    let mock = MockPermissionsService::new();
    mock.push_check_bulk_permissions_response(proto::CheckBulkPermissionsResponse {
        checked_at: None,
        pairs: (0..CHECK_BATCH_SIZE).map(|_| ok_pair()).collect(),
    });
    mock.push_check_bulk_permissions_response(proto::CheckBulkPermissionsResponse {
        checked_at: None,
        pairs: (0..CHECK_BATCH_SIZE)
            .map(|i| {
                if i == 5 {
                    // Neither Item nor Error set -- the oneof left empty.
                    proto::CheckBulkPermissionsPair {
                        request: None,
                        response: None,
                    }
                } else {
                    ok_pair()
                }
            })
            .collect(),
    });

    let addr = spawn_permissions_server(mock).await;
    let client = SpiceDBClient::new_plaintext(addr.to_string(), "token")
        .await
        .expect("client should connect to mock server");

    let err = client
        .check_permissions(
            &consistency::full(),
            "view",
            &numbered_rels(CHECK_BATCH_SIZE * 2),
        )
        .await
        .expect_err("a malformed pair must surface as an Err");

    let message = err.to_string();
    assert!(
        message.contains("malformed CheckBulkPermissionsPair"),
        "{message}"
    );
    assert!(
        message.contains(&format!("check item {failing}:")),
        "must name the caller's index ({failing}), not the chunk-relative 5: {message}"
    );
    assert!(
        !message.contains("check item 5:"),
        "must not report the chunk-relative index: {message}"
    );
}
