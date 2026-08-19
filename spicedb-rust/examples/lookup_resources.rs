//! Demonstrates finding resources a subject can access.
//!
//! Run with: `cargo run --example lookup_resources`

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
    let stream = client.lookup_resources(&consistency::full(), "document", "view", "user", "alice");
    tokio::pin!(stream);

    let mut resource_ids = Vec::new();
    while let Some(result) = stream.next().await {
        let result = result.expect("lookup failed");
        println!(
            "alice can view document:{} ({:?})",
            result.resource_id, result.permissionship
        );
        resource_ids.push(result.resource_id);
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
