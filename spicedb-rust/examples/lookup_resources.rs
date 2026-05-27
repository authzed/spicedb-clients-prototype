//! Demonstrates finding resources a subject can access.
//!
//! Run with: `cargo run --example lookup_resources`

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
    let client = SpiceDBClient::new_plaintext("localhost:50051", "somerandomkeyhere")
        .await
        .expect("failed to create client");

    // Write schema
    client
        .write_schema(SCHEMA)
        .await
        .expect("write schema failed");

    // Write test data: alice can view firstdoc, alice can edit seconddoc
    let alice_viewer = Relationship::new("document", "firstdoc", "viewer", "user", "alice", "")
        .expect("invalid relationship");
    let alice_editor = Relationship::new("document", "seconddoc", "editor", "user", "alice", "")
        .expect("invalid relationship");

    let mut txn = Transaction::new();
    txn.touch(&alice_viewer);
    txn.touch(&alice_editor);
    client
        .write(&txn)
        .await
        .expect("write relationships failed");

    // Lookup all documents alice can view
    let resource_ids = client
        .lookup_resources(&consistency::full(), "document", "view", "user", "alice")
        .await
        .expect("lookup failed");

    for id in &resource_ids {
        println!("alice can view document:{id}");
    }

    assert!(
        resource_ids.contains(&"firstdoc".to_string()),
        "expected firstdoc in results"
    );
    assert!(
        resource_ids.contains(&"seconddoc".to_string()),
        "expected seconddoc in results"
    );
}
