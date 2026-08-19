//! Demonstrates reading relationships with a filter.
//!
//! Run with: `cargo run --example read_relationships`

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

    // Write schema
    client
        .write_schema(SCHEMA)
        .await
        .expect("write schema failed");

    // Write test data
    let alice = Relationship::new("document", "firstdoc", "viewer", "user", "alice", "")
        .expect("invalid relationship");
    let bob = Relationship::new("document", "firstdoc", "viewer", "user", "bob", "")
        .expect("invalid relationship");

    let mut txn = Transaction::new();
    txn.touch(&alice);
    txn.touch(&bob);
    client
        .write(&txn)
        .await
        .expect("write relationships failed");

    // Read relationships matching a filter
    let filter = Filter::new("document")
        .with_resource_id("firstdoc")
        .with_relation("viewer");

    let stream = client.read_relationships(&consistency::full(), &filter);
    tokio::pin!(stream);

    let mut relationships = Vec::new();
    while let Some(rel) = stream.next().await {
        let rel = rel.expect("read failed");
        println!("found relationship: {rel}");
        relationships.push(rel);
    }

    assert!(
        !relationships.is_empty(),
        "expected at least one relationship"
    );
    println!("found {} relationships", relationships.len());

    // Clean up so a later example isn't blocked by leftover relationships: the
    // integration runner drives every example against one SpiceDB, and an
    // example that narrows the schema fails outright if a relationship still
    // exists under a relation it drops.
    client
        .delete_relationships(&Filter::new("document"))
        .await
        .expect("cleanup failed");
}
