//! Demonstrates finding subjects with access to a resource.
//!
//! Run with: `cargo run --example lookup_subjects`

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

    // Write test data: alice is a viewer, bob is an editor
    let alice_viewer =
        Relationship::new("document", "firstdoc", "viewer", "user", "alice", "")
            .expect("invalid relationship");
    let bob_editor =
        Relationship::new("document", "firstdoc", "editor", "user", "bob", "")
            .expect("invalid relationship");

    let mut txn = Transaction::new();
    txn.touch(&alice_viewer);
    txn.touch(&bob_editor);
    client.write(&txn).await.expect("write relationships failed");

    // Lookup all users who can view firstdoc
    let subject_ids = client
        .lookup_subjects(
            &consistency::full(),
            "document",
            "firstdoc",
            "view",
            "user",
        )
        .await
        .expect("lookup failed");

    for id in &subject_ids {
        println!("user:{id} can view document:firstdoc");
    }

    assert!(
        subject_ids.contains(&"alice".to_string()),
        "expected alice in results"
    );
    assert!(
        subject_ids.contains(&"bob".to_string()),
        "expected bob in results (editor implies view)"
    );
}
