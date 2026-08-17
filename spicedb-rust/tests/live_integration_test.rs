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

/// T4: writes a caveated relationship, checks the permission with no caveat
/// context supplied, and asserts the server reports a `ConditionalPermission`
/// result rather than a grant — the server needed `now` to evaluate `active`
/// and did not receive it.
#[tokio::test]
#[ignore = "requires a live SpiceDB server (see module docs)"]
async fn check_permission_with_missing_caveat_context_is_conditional_not_granted() {
    let client = SpiceDBClient::new_plaintext("localhost:50051", "testtoken")
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
