//! Demonstrates reading and writing schema.
//!
//! Run with: `cargo run --example schema_management`

use spicedb::client::SpiceDBClient;
use spicedb::error::SpiceDBError;
use spicedb::types::Filter;

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

    // Write a schema
    let revision = client
        .write_schema(SCHEMA)
        .await
        .expect("write schema failed");

    println!("wrote schema at revision: {revision}");
    assert!(!revision.is_empty(), "expected non-empty revision");

    // Read the schema back
    let (read_schema, read_revision) = client.read_schema().await.expect("read schema failed");

    println!("read schema at revision {read_revision}:\n{read_schema}");

    assert!(
        read_schema.contains("definition user"),
        "expected schema to contain 'definition user'"
    );
    assert!(
        read_schema.contains("definition document"),
        "expected schema to contain 'definition document'"
    );
}
