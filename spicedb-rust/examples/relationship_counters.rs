//! Demonstrates registering, reading, and unregistering relationship counters.
//!
//! Run with: `cargo run --example relationship_counters`

use spicedb::client::SpiceDBClient;
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
    let client = SpiceDBClient::new_plaintext("localhost:50051", "somerandomkeyhere")
        .await
        .expect("failed to create client");

    // Write schema and test data
    client
        .write_schema(SCHEMA)
        .await
        .expect("write schema failed");

    let alice =
        Relationship::new("document", "firstdoc", "viewer", "user", "alice", "")
            .expect("invalid relationship");
    let bob =
        Relationship::new("document", "seconddoc", "viewer", "user", "bob", "")
            .expect("invalid relationship");

    let mut txn = Transaction::new();
    txn.touch(&alice);
    txn.touch(&bob);
    client.write(&txn).await.expect("write relationships failed");

    // Unregister any existing counter from a prior run (ignore errors)
    let _ = client
        .experimental_unregister_relationship_counter("document_viewers")
        .await;

    // Register a counter for all document viewer relationships
    let filter = Filter::new("document").with_relation("viewer");
    client
        .experimental_register_relationship_counter("document_viewers", &filter)
        .await
        .expect("register counter failed");

    println!("registered counter: document_viewers");

    // Wait a moment for the counter to be computed
    tokio::time::sleep(std::time::Duration::from_secs(2)).await;

    // Read the counter value
    let result = client
        .experimental_count_relationships("document_viewers")
        .await
        .expect("count relationships failed");

    match result {
        None => {
            println!("counter is still being calculated...");
        }
        Some(count) => {
            println!(
                "document viewer count: {} (revision: {})",
                count.relationship_count, count.revision,
            );
            assert!(
                count.relationship_count > 0,
                "expected non-zero relationship count"
            );
        }
    }

    // Unregister the counter when done
    client
        .experimental_unregister_relationship_counter("document_viewers")
        .await
        .expect("unregister counter failed");

    println!("unregistered counter: document_viewers");
}
