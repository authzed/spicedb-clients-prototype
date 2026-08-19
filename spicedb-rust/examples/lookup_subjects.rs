//! Demonstrates finding subjects with access to a resource.
//!
//! Run with: `cargo run --example lookup_subjects`

use futures::StreamExt;
use spicedb::client::SpiceDBClient;
use spicedb::consistency;
use spicedb::types::{Filter, Relationship, Transaction};

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
    // Endpoint and token come from the environment so the example runs against
    // whichever SpiceDB the caller started; the defaults match
    // docker-compose.test.yml.
    let endpoint =
        std::env::var("SPICEDB_ENDPOINT").unwrap_or_else(|_| "localhost:50051".to_string());
    let token = std::env::var("SPICEDB_TOKEN").unwrap_or_else(|_| "testtoken".to_string());

    let client = SpiceDBClient::new_plaintext(endpoint, token)
        .await
        .expect("failed to create client");

    // Clear before writing the schema, not after using it. Every example runs
    // against one SpiceDB and writes a whole schema, and SpiceDB refuses a
    // WriteSchema that drops a relation while a relationship still exists
    // under it -- so what a *previous* example left behind is this example's
    // problem, and a cleanup at exit does not help if this example fails
    // first. The error is ignored on purpose: on a fresh server there is no
    // `document` definition yet, which is not a failure.
    let _ = client.delete_relationships(&Filter::new("document")).await;

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
