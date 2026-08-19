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

    // Write schema and test data
    client
        .write_schema(SCHEMA)
        .await
        .expect("write schema failed");

    let alice = Relationship::new("document", "firstdoc", "viewer", "user", "alice", "")
        .expect("invalid relationship");
    let bob = Relationship::new("document", "seconddoc", "viewer", "user", "bob", "")
        .expect("invalid relationship");

    let mut txn = Transaction::new();
    txn.touch(&alice);
    txn.touch(&bob);
    client
        .write(&txn)
        .await
        .expect("write relationships failed");

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
