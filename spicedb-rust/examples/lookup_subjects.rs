//! Demonstrates finding subjects with access to a resource.
//!
//! Run with: `cargo run --example lookup_subjects`

use futures::StreamExt;
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
    let client = SpiceDBClient::new_plaintext("localhost:50051", "testtoken")
        .await
        .expect("failed to create client");

    // Write schema
    client
        .write_schema(SCHEMA)
        .await
        .expect("write schema failed");

    // Write test data: alice is a viewer, bob is an editor
    let alice_viewer = Relationship::new("document", "firstdoc", "viewer", "user", "alice", "")
        .expect("invalid relationship");
    let bob_editor = Relationship::new("document", "firstdoc", "editor", "user", "bob", "")
        .expect("invalid relationship");

    let mut txn = Transaction::new();
    txn.touch(&alice_viewer);
    txn.touch(&bob_editor);
    client
        .write(&txn)
        .await
        .expect("write relationships failed");

    // Lookup all users who can view firstdoc
    let stream =
        client.lookup_subjects(&consistency::full(), "document", "firstdoc", "view", "user");
    tokio::pin!(stream);

    let mut subject_ids = Vec::new();
    while let Some(result) = stream.next().await {
        let result = result.expect("lookup failed");
        let subject_id = result.subject.subject_id;

        // If the subject is the wildcard "*", the permission was granted to
        // every subject of the requested type EXCEPT those explicitly listed
        // in `excluded_subjects` — callers MUST check this list before
        // treating a wildcard match as a blanket grant.
        if subject_id == "*" {
            for excluded in &result.excluded_subjects {
                println!(
                    "user:{} is explicitly excluded from the wildcard grant",
                    excluded.subject_id
                );
            }
        }

        println!(
            "user:{subject_id} can view document:firstdoc ({:?})",
            result.subject.permissionship
        );
        subject_ids.push(subject_id);
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
