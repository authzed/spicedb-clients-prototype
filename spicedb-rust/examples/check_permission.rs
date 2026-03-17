//! Demonstrates checking a permission using `check_permission`.
//!
//! Run with: `cargo run --example check_permission`

use spicedb::client::SpiceDBClient;
use spicedb::consistency;
use spicedb::types::{Relationship, Transaction};

const SCHEMA: &str = r#"definition user {}

definition document {
    relation viewer: user
    relation editor: user
    relation owner: user
    permission view = viewer + editor + owner
    permission edit = editor + owner
    permission delete = owner
}"#;

#[tokio::main]
async fn main() {
    // Create a plaintext client (testing only — no TLS)
    let client = SpiceDBClient::new_plaintext("localhost:50051", "somerandomkeyhere")
        .await
        .expect("failed to create client");

    // Write schema
    client
        .write_schema(SCHEMA)
        .await
        .expect("write schema failed");

    // Write test data: alice is a viewer of firstdoc
    let rel = Relationship::new("document", "firstdoc", "viewer", "user", "alice", "")
        .expect("invalid relationship");
    let mut txn = Transaction::new();
    txn.touch(&rel);
    let revision = client.write(&txn).await.expect("write relationships failed");

    // Check permission: alice should be able to view firstdoc
    let check_rel = Relationship::new("document", "firstdoc", "view", "user", "alice", "")
        .expect("invalid relationship");
    let result = client
        .check_permission(&consistency::at_least(&revision), "view", &check_rel)
        .await
        .expect("check failed");

    println!("alice can view document:firstdoc: {}", result.has_permission);
    assert!(result.has_permission, "expected alice to have view permission");
}
