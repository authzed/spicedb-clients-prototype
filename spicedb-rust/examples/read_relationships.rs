//! Demonstrates reading relationships with a filter.
//!
//! Run with: `cargo run --example read_relationships`

use futures::StreamExt;
use spicedb::client::SpiceDBClient;
use spicedb::consistency;
use spicedb::error::SpiceDBError;
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
    // first.
    //
    // Exactly one error is tolerated: on a fresh server there is no `document`
    // definition yet, which SpiceDB reports as FailedPrecondition
    // (ERROR_REASON_UNKNOWN_DEFINITION). Anything else -- an unreachable
    // server, a bad token -- must still fail the example.
    match client.delete_relationships(&Filter::new("document")).await {
        Ok(_) | Err(SpiceDBError::FailedPrecondition(_)) => {}
        Err(e) => panic!("cleanup before schema write failed: {e:?}"),
    }

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
}
