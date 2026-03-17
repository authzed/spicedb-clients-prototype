//! Demonstrates reading relationships with a filter.
//!
//! Run with: `cargo run --example read_relationships`

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
    let client = SpiceDBClient::new_plaintext("localhost:50051", "somerandomkeyhere")
        .await
        .expect("failed to create client");

    // Write schema
    client
        .write_schema(SCHEMA)
        .await
        .expect("write schema failed");

    // Write test data
    let alice =
        Relationship::new("document", "firstdoc", "viewer", "user", "alice", "")
            .expect("invalid relationship");
    let bob =
        Relationship::new("document", "firstdoc", "viewer", "user", "bob", "")
            .expect("invalid relationship");

    let mut txn = Transaction::new();
    txn.touch(&alice);
    txn.touch(&bob);
    client.write(&txn).await.expect("write relationships failed");

    // Read relationships matching a filter
    let filter = Filter::new("document")
        .with_resource_id("firstdoc")
        .with_relation("viewer");

    let relationships = client
        .read_relationships(&consistency::full(), &filter)
        .await
        .expect("read failed");

    for rel in &relationships {
        println!("found relationship: {rel}");
    }

    assert!(
        !relationships.is_empty(),
        "expected at least one relationship"
    );
    println!("found {} relationships", relationships.len());
}
