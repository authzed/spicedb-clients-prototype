//! Live-SpiceDB integration tests. These require a real SpiceDB server and
//! are `#[ignore]`d by default so a plain `cargo test` never needs one.
//!
//! Run via `mage -d spicedb-rust integrationTest` (starts SpiceDB from
//! `docker-compose.test.yml` and runs `cargo test -- --ignored`), or
//! manually:
//!
//! ```sh
//! docker compose -f docker-compose.test.yml up -d
//! cargo test --test live_integration_test -- --ignored
//! docker compose -f docker-compose.test.yml down
//! ```

use spicedb::client::SpiceDBClient;
use spicedb::consistency;
use spicedb::types::{Permissionship, Relationship, Transaction};

const CAVEATED_SCHEMA: &str = r#"definition user {}

caveat active(now int) {
    now < 100
}

definition doc {
    relation viewer: user with active
    permission view = viewer
}"#;

/// The live server these tests connect to. `mage integrationTest` starts it and
/// exports `SPICEDB_ENDPOINT`/`SPICEDB_TOKEN`, the same two variables every
/// example reads; the defaults match `docker-compose.test.yml`.
fn live_endpoint() -> String {
    std::env::var("SPICEDB_ENDPOINT").unwrap_or_else(|_| "localhost:50051".to_string())
}

fn live_token() -> String {
    std::env::var("SPICEDB_TOKEN").unwrap_or_else(|_| "testtoken".to_string())
}

/// T4: writes a caveated relationship, checks the permission with no caveat
/// context supplied, and asserts the server reports a `ConditionalPermission`
/// result rather than a grant — the server needed `now` to evaluate `active`
/// and did not receive it.
#[tokio::test]
#[ignore = "requires a live SpiceDB server (see module docs)"]
async fn check_permission_with_missing_caveat_context_is_conditional_not_granted() {
    let client = SpiceDBClient::new_plaintext(live_endpoint(), live_token())
        .await
        .expect("connect to live SpiceDB");

    client
        .write_schema(CAVEATED_SCHEMA)
        .await
        .expect("write schema");

    // Write the relationship with the `active` caveat attached but no
    // context supplied.
    let rel = Relationship::new("doc", "conditionaldoc", "viewer", "user", "alice", "")
        .expect("valid relationship")
        .with_caveat("active", None);
    let mut txn = Transaction::new();
    txn.touch(&rel);
    let revision = client.write(&txn).await.expect("write relationship");

    // Check with no context — the server cannot evaluate `now < 100`.
    let check_rel = Relationship::new("doc", "conditionaldoc", "view", "user", "alice", "")
        .expect("valid relationship");
    let result = client
        .check_permission(&consistency::at_least(&revision), "view", &check_rel)
        .await
        .expect("check should succeed (the server answers Conditional, not an error)");

    assert_eq!(
        result.permissionship,
        Permissionship::ConditionalPermission,
        "expected a Conditional result when caveat context is missing, got {:?}",
        result.permissionship
    );
    assert!(
        !result.has_permission(),
        "a Conditional result must not report has_permission() == true"
    );
    assert_eq!(
        result.missing_context,
        vec!["now".to_string()],
        "expected the server to report `now` as the missing caveat parameter"
    );
    assert!(
        !result.checked_at.is_empty(),
        "checked_at should be populated from the response"
    );
}

/// C5: the payoff test for spec D3b. Same caveat schema and relationship as
/// the test above (server needs `now` and doesn't get it => Conditional),
/// but this time the caller supplies the missing context via
/// `check_permission_with_context` — proving `missing_context` is
/// actionable: a caller can resolve a Conditional into a real grant.
#[tokio::test]
#[ignore = "requires a live SpiceDB server (see module docs)"]
async fn check_permission_with_context_resolves_conditional_to_grant() {
    let client = SpiceDBClient::new_plaintext(live_endpoint(), live_token())
        .await
        .expect("connect to live SpiceDB");

    client
        .write_schema(CAVEATED_SCHEMA)
        .await
        .expect("write schema");

    let rel = Relationship::new("doc", "contextresolveddoc", "viewer", "user", "alice", "")
        .expect("valid relationship")
        .with_caveat("active", None);
    let mut txn = Transaction::new();
    txn.touch(&rel);
    let revision = client.write(&txn).await.expect("write relationship");

    let check_rel = Relationship::new("doc", "contextresolveddoc", "view", "user", "alice", "")
        .expect("valid relationship");

    // Without context: Conditional, per the test above.
    let conditional = client
        .check_permission(&consistency::at_least(&revision), "view", &check_rel)
        .await
        .expect("check should succeed");
    assert_eq!(
        conditional.permissionship,
        Permissionship::ConditionalPermission,
        "sanity check: without context the server must still answer Conditional"
    );

    // With context supplying `now`: `now < 100` evaluates true, so the
    // caveat passes and the check resolves to an outright grant.
    let mut context = std::collections::HashMap::new();
    context.insert("now".to_string(), serde_json::json!(42));
    let granted = client
        .check_permission_with_context(
            &consistency::at_least(&revision),
            "view",
            &check_rel,
            Some(&context),
        )
        .await
        .expect("check with context should succeed");

    assert_eq!(
        granted.permissionship,
        Permissionship::HasPermission,
        "expected supplying the missing `now` context to resolve the caveat to a grant, got {:?}",
        granted.permissionship
    );
    assert!(
        granted.has_permission(),
        "has_permission() must be true once the missing caveat context is supplied"
    );
    assert!(
        granted.missing_context.is_empty(),
        "a HasPermission result must not report missing context"
    );
}
