//! Demonstrates writing relationships with a transaction builder.
//!
//! Run with: `cargo run --example write_relationships`

use spicedb::client::SpiceDBClient;
use spicedb::types::{DeleteOptions, Filter, Relationship, Transaction};

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

    // Build a transaction with multiple operations and a precondition
    let alice_viewer = Relationship::new("document", "firstdoc", "viewer", "user", "alice", "")
        .expect("invalid relationship");
    let bob_editor = Relationship::new("document", "firstdoc", "editor", "user", "bob", "")
        .expect("invalid relationship");

    let mut txn = Transaction::new();
    txn.touch(&alice_viewer);
    txn.touch(&bob_editor);
    txn.must_not_match(
        Filter::new("document")
            .with_resource_id("firstdoc")
            .with_relation("owner")
            .with_subject_type("user")
            .with_subject_id("mallory"),
    );

    let revision = client.write(&txn).await.expect("write failed");

    println!("wrote relationships at revision: {revision}");
    assert!(!revision.is_empty(), "expected non-empty revision");

    // Delete relationships, guarded by a precondition and a page-size limit.
    // `delete_relationships` (used above implicitly via `write`'s Transaction)
    // has a simple filter-only sibling for unguarded deletes; here we use
    // `delete_relationships_with` to require that alice is still a viewer
    // before deleting bob's editor grant.
    let delete_options = DeleteOptions::new()
        .with_must_match(
            Filter::new("document")
                .with_resource_id("firstdoc")
                .with_relation("viewer")
                .with_subject_type("user")
                .with_subject_id("alice"),
        )
        .with_limit(1_000);

    let delete_revision = client
        .delete_relationships_with(
            &Filter::new("document")
                .with_resource_id("firstdoc")
                .with_relation("editor")
                .with_subject_type("user")
                .with_subject_id("bob"),
            &delete_options,
        )
        .await
        .expect("guarded delete failed");

    println!("deleted relationships at revision: {delete_revision}");
    assert!(!delete_revision.is_empty(), "expected non-empty revision");
}
