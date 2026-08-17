//! Tests for caveat context on the check surface (spec D3b): a call-level
//! default applied to every item in a bulk request, and a per-item override
//! that merges with (not replaces) that default.
//!
//! Proto ground truth: `CheckBulkPermissionsRequestItem.context` (field 4) is
//! the *only* wire location for check-time caveat context —
//! `CheckBulkPermissionsRequest` itself has no context field — so a
//! call-level default must be fanned out onto every item at request-build
//! time. These tests assert on the exact `CheckBulkPermissionsRequest` the
//! mock server captured, by value, so they pin the real wire shape rather
//! than an intermediate representation.

mod support;

use std::collections::HashMap;

use spicedb::client::SpiceDBClient;
use spicedb::consistency;
use spicedb::types::Relationship;
use spicedb_proto::authzed::api::v1 as proto;

use support::{spawn_permissions_server, MockPermissionsService};

/// Decodes a captured wire-level `context` Struct back into a plain map, so
/// tests can assert on exactly what was sent, by value. `None` means "no
/// context field set on the wire" — distinct from `Some(empty map)`.
fn context_map(s: Option<&prost_types::Struct>) -> Option<HashMap<String, serde_json::Value>> {
    s.map(|s| {
        s.fields
            .iter()
            .map(|(k, v)| (k.clone(), prost_value_to_json(v)))
            .collect()
    })
}

fn prost_value_to_json(v: &prost_types::Value) -> serde_json::Value {
    use prost_types::value::Kind;
    match &v.kind {
        Some(Kind::NullValue(_)) => serde_json::Value::Null,
        Some(Kind::BoolValue(b)) => serde_json::Value::Bool(*b),
        Some(Kind::NumberValue(n)) => serde_json::Number::from_f64(*n)
            .map(serde_json::Value::Number)
            .unwrap_or(serde_json::Value::Null),
        Some(Kind::StringValue(s)) => serde_json::Value::String(s.clone()),
        Some(Kind::ListValue(list)) => {
            serde_json::Value::Array(list.values.iter().map(prost_value_to_json).collect())
        }
        Some(Kind::StructValue(s)) => serde_json::Value::Object(
            s.fields
                .iter()
                .map(|(k, v)| (k.clone(), prost_value_to_json(v)))
                .collect(),
        ),
        None => serde_json::Value::Null,
    }
}

/// A `CheckBulkPermissionsResponse` with `n` `HasPermission` pairs, one per
/// expected request item. The tests here are about request-building, not
/// response-mapping, so the response content itself doesn't matter beyond
/// having the right shape to let the call complete successfully.
fn has_permission_response(n: usize) -> proto::CheckBulkPermissionsResponse {
    proto::CheckBulkPermissionsResponse {
        checked_at: Some(proto::ZedToken {
            token: "rev-1".to_string(),
        }),
        pairs: (0..n)
            .map(|_| proto::CheckBulkPermissionsPair {
                request: None,
                response: Some(proto::check_bulk_permissions_pair::Response::Item(
                    proto::CheckBulkPermissionsResponseItem {
                        permissionship:
                            proto::check_permission_response::Permissionship::HasPermission as i32,
                        partial_caveat_info: None,
                        debug_trace: None,
                    },
                )),
            })
            .collect(),
    }
}

async fn client_with_mock(mock: MockPermissionsService) -> (SpiceDBClient, std::net::SocketAddr) {
    let addr = spawn_permissions_server(mock).await;
    let client = SpiceDBClient::new_plaintext(addr.to_string(), "token")
        .await
        .expect("client should connect to mock server");
    (client, addr)
}

// ---------------------------------------------------------------------------
// C1: call-level context alone reaches every item in a bulk request.
// ---------------------------------------------------------------------------

#[tokio::test]
async fn c1_call_level_context_reaches_every_item() {
    let mock = MockPermissionsService::new();
    mock.push_check_bulk_permissions_response(has_permission_response(2));
    let requests = mock.check_bulk_permissions_requests();
    let (client, _addr) = client_with_mock(mock).await;

    let r1 = Relationship::new("document", "1", "view", "user", "alice", "").unwrap();
    let r2 = Relationship::new("document", "2", "view", "user", "bob", "").unwrap();

    let mut context = HashMap::new();
    context.insert("now".to_string(), serde_json::json!(42.0));

    client
        .check_permissions_with_context(&consistency::full(), "view", &[r1, r2], Some(&context))
        .await
        .expect("check should succeed");

    let reqs = requests.lock().unwrap();
    assert_eq!(reqs.len(), 1);
    let items = &reqs[0].items;
    assert_eq!(items.len(), 2);

    let want = Some(context.clone());
    assert_eq!(
        context_map(items[0].context.as_ref()),
        want,
        "item 0 should carry the call-level context"
    );
    assert_eq!(
        context_map(items[1].context.as_ref()),
        want,
        "item 1 should carry the call-level context"
    );
}

// ---------------------------------------------------------------------------
// C2: per-item context alone reaches only that item.
// ---------------------------------------------------------------------------

#[tokio::test]
async fn c2_per_item_context_reaches_only_that_item() {
    let mock = MockPermissionsService::new();
    mock.push_check_bulk_permissions_response(has_permission_response(2));
    let requests = mock.check_bulk_permissions_requests();
    let (client, _addr) = client_with_mock(mock).await;

    let r1 = Relationship::new("document", "1", "view", "user", "alice", "").unwrap();

    let mut item_ctx = HashMap::new();
    item_ctx.insert("now".to_string(), serde_json::json!(42.0));
    let r2 = Relationship::new("document", "2", "view", "user", "bob", "")
        .unwrap()
        .with_check_context(item_ctx.clone());

    // Plain check_permissions — no call-level context argument at all — must
    // still honor r2's per-item context through the delegating,
    // backward-compatible entry point.
    client
        .check_permissions(&consistency::full(), "view", &[r1, r2])
        .await
        .expect("check should succeed");

    let reqs = requests.lock().unwrap();
    let items = &reqs[0].items;
    assert_eq!(
        context_map(items[0].context.as_ref()),
        None,
        "item 0 has no per-item context and no call-level default, so no context field should be set"
    );
    assert_eq!(
        context_map(items[1].context.as_ref()),
        Some(item_ctx),
        "item 1 should carry only its own per-item context"
    );
}

// ---------------------------------------------------------------------------
// C3: the merge rule. Call-level {now: 42, region: "us"} + item-level
// {region: "eu"} produces {now: 42, region: "eu"} for that item, and
// {now: 42, region: "us"} for a sibling item that supplied none. Both items
// are asserted — asserting only the overriding item also passes under
// wholesale-replacement semantics, so a single-item assertion would not pin
// the rule.
// ---------------------------------------------------------------------------

#[tokio::test]
async fn c3_merges_call_level_and_per_item_context() {
    let mock = MockPermissionsService::new();
    mock.push_check_bulk_permissions_response(has_permission_response(2));
    let requests = mock.check_bulk_permissions_requests();
    let (client, _addr) = client_with_mock(mock).await;

    let sibling = Relationship::new("document", "1", "view", "user", "alice", "").unwrap();

    let mut item_ctx = HashMap::new();
    item_ctx.insert("region".to_string(), serde_json::json!("eu"));
    let overridden = Relationship::new("document", "2", "view", "user", "bob", "")
        .unwrap()
        .with_check_context(item_ctx);

    let mut call_ctx = HashMap::new();
    call_ctx.insert("now".to_string(), serde_json::json!(42.0));
    call_ctx.insert("region".to_string(), serde_json::json!("us"));

    client
        .check_permissions_with_context(
            &consistency::full(),
            "view",
            &[sibling, overridden],
            Some(&call_ctx),
        )
        .await
        .expect("check should succeed");

    let reqs = requests.lock().unwrap();
    let items = &reqs[0].items;

    let mut want_sibling = HashMap::new();
    want_sibling.insert("now".to_string(), serde_json::json!(42.0));
    want_sibling.insert("region".to_string(), serde_json::json!("us"));
    assert_eq!(
        context_map(items[0].context.as_ref()),
        Some(want_sibling),
        "sibling item supplied no per-item context, so it must retain the call-level default unchanged"
    );

    let mut want_overridden = HashMap::new();
    want_overridden.insert("now".to_string(), serde_json::json!(42.0));
    want_overridden.insert("region".to_string(), serde_json::json!("eu"));
    assert_eq!(
        context_map(items[1].context.as_ref()),
        Some(want_overridden),
        "overridden item's region key must win, but the call-level now key (absent from the item) must be retained"
    );
}

// ---------------------------------------------------------------------------
// C4: neither call-level nor per-item context supplied => no context field
// set on the wire (None, not an empty Struct).
// ---------------------------------------------------------------------------

#[tokio::test]
async fn c4_no_context_supplied_sets_no_context_field() {
    let mock = MockPermissionsService::new();
    mock.push_check_bulk_permissions_response(has_permission_response(2));
    let requests = mock.check_bulk_permissions_requests();
    let (client, _addr) = client_with_mock(mock).await;

    let r1 = Relationship::new("document", "1", "view", "user", "alice", "").unwrap();
    let r2 = Relationship::new("document", "2", "view", "user", "bob", "").unwrap();

    client
        .check_permissions_with_context(&consistency::full(), "view", &[r1, r2], None)
        .await
        .expect("check should succeed");

    let reqs = requests.lock().unwrap();
    let items = &reqs[0].items;
    assert!(items[0].context.is_none());
    assert!(items[1].context.is_none());
}

// ---------------------------------------------------------------------------
// Regression guard: the non-context methods (check_permission,
// check_permissions, check_any, check_all) must keep their exact
// pre-existing call shape — no new required parameter — and must still set
// no context field on the wire when nothing supplies one.
// ---------------------------------------------------------------------------

#[tokio::test]
async fn existing_check_methods_stay_unchanged_and_set_no_context() {
    let r1 = || Relationship::new("document", "1", "view", "user", "alice", "").unwrap();
    let r2 = || Relationship::new("document", "2", "view", "user", "bob", "").unwrap();

    {
        let mock = MockPermissionsService::new();
        mock.push_check_bulk_permissions_response(has_permission_response(1));
        let requests = mock.check_bulk_permissions_requests();
        let (client, _addr) = client_with_mock(mock).await;
        let _ = client
            .check_permission(&consistency::full(), "view", &r1())
            .await
            .expect("check should succeed");
        let reqs = requests.lock().unwrap();
        assert!(reqs[0].items[0].context.is_none());
    }

    {
        let mock = MockPermissionsService::new();
        mock.push_check_bulk_permissions_response(has_permission_response(2));
        let requests = mock.check_bulk_permissions_requests();
        let (client, _addr) = client_with_mock(mock).await;
        let _ = client
            .check_permissions(&consistency::full(), "view", &[r1(), r2()])
            .await
            .expect("check should succeed");
        let reqs = requests.lock().unwrap();
        for item in &reqs[0].items {
            assert!(item.context.is_none());
        }
    }

    {
        let mock = MockPermissionsService::new();
        mock.push_check_bulk_permissions_response(has_permission_response(2));
        let requests = mock.check_bulk_permissions_requests();
        let (client, _addr) = client_with_mock(mock).await;
        let _ = client
            .check_any(&consistency::full(), "view", &[r1(), r2()])
            .await
            .expect("check should succeed");
        let reqs = requests.lock().unwrap();
        for item in &reqs[0].items {
            assert!(item.context.is_none());
        }
    }

    {
        let mock = MockPermissionsService::new();
        mock.push_check_bulk_permissions_response(has_permission_response(2));
        let requests = mock.check_bulk_permissions_requests();
        let (client, _addr) = client_with_mock(mock).await;
        let _ = client
            .check_all(&consistency::full(), "view", &[r1(), r2()])
            .await
            .expect("check should succeed");
        let reqs = requests.lock().unwrap();
        for item in &reqs[0].items {
            assert!(item.context.is_none());
        }
    }
}

// ---------------------------------------------------------------------------
// Delegation sanity: the _with_context variants of check_permission/
// check_any/check_all must actually forward call-level context to the wire,
// not just accept the parameter and drop it.
// ---------------------------------------------------------------------------

#[tokio::test]
async fn with_context_variants_forward_call_level_context_to_wire() {
    let mut want = HashMap::new();
    want.insert("now".to_string(), serde_json::json!(7.0));

    {
        let mock = MockPermissionsService::new();
        mock.push_check_bulk_permissions_response(has_permission_response(1));
        let requests = mock.check_bulk_permissions_requests();
        let (client, _addr) = client_with_mock(mock).await;
        let r = Relationship::new("document", "1", "view", "user", "alice", "").unwrap();
        let _ = client
            .check_permission_with_context(&consistency::full(), "view", &r, Some(&want))
            .await
            .expect("check should succeed");
        let reqs = requests.lock().unwrap();
        assert_eq!(
            context_map(reqs[0].items[0].context.as_ref()),
            Some(want.clone())
        );
    }

    {
        let mock = MockPermissionsService::new();
        mock.push_check_bulk_permissions_response(has_permission_response(2));
        let requests = mock.check_bulk_permissions_requests();
        let (client, _addr) = client_with_mock(mock).await;
        let r1 = Relationship::new("document", "1", "view", "user", "alice", "").unwrap();
        let r2 = Relationship::new("document", "2", "view", "user", "bob", "").unwrap();
        let _ = client
            .check_any_with_context(&consistency::full(), "view", &[r1, r2], Some(&want))
            .await
            .expect("check should succeed");
        let reqs = requests.lock().unwrap();
        for item in &reqs[0].items {
            assert_eq!(context_map(item.context.as_ref()), Some(want.clone()));
        }
    }

    {
        let mock = MockPermissionsService::new();
        mock.push_check_bulk_permissions_response(has_permission_response(2));
        let requests = mock.check_bulk_permissions_requests();
        let (client, _addr) = client_with_mock(mock).await;
        let r1 = Relationship::new("document", "1", "view", "user", "alice", "").unwrap();
        let r2 = Relationship::new("document", "2", "view", "user", "bob", "").unwrap();
        let _ = client
            .check_all_with_context(&consistency::full(), "view", &[r1, r2], Some(&want))
            .await
            .expect("check should succeed");
        let reqs = requests.lock().unwrap();
        for item in &reqs[0].items {
            assert_eq!(context_map(item.context.as_ref()), Some(want.clone()));
        }
    }
}
