//! Demonstrates reading and writing schema.
//!
//! Run with: `cargo run --example schema_management`

use spicedb::client::SpiceDBClient;

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
