//! Demonstrates bulk checks, batch writes, and import/export operations.
//!
//! Run with: `cargo run --example bulk_operations`

use futures::StreamExt;
use spicedb::client::SpiceDBClient;
use spicedb::consistency;
use spicedb::types::{Relationship, Transaction};

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

    // Bulk write relationships
    let users = ["alice", "bob", "charlie"];
    let mut txn = Transaction::new();
    let rels: Vec<Relationship> = users
        .iter()
        .map(|user| {
            Relationship::new("document", "report", "viewer", "user", *user, "")
                .expect("invalid relationship")
        })
        .collect();

    for rel in &rels {
        txn.touch(rel);
    }
    let revision = client.write(&txn).await.expect("bulk write failed");

    println!(
        "wrote {} relationships at revision: {revision}",
        users.len()
    );
    assert!(!revision.is_empty(), "expected non-empty revision");

    // Bulk check permissions
    let check_rels: Vec<Relationship> = users
        .iter()
        .map(|user| {
            Relationship::new("document", "report", "view", "user", *user, "")
                .expect("invalid relationship")
        })
        .collect();

    let results = client
        .check_permissions(&consistency::at_least(&revision), "view", &check_rels)
        .await
        .expect("bulk check failed");

    for (i, user) in users.iter().enumerate() {
        println!("user:{user} can view document:report: {}", results[i]);
        assert!(results[i], "expected user:{user} to have view permission");
    }

    // Check all
    let all_allowed = client
        .check_all(&consistency::at_least(&revision), "view", &check_rels)
        .await
        .expect("check all failed");

    println!("all users can view: {all_allowed}");
    assert!(all_allowed, "expected all users to have view permission");

    // Check any
    let any_allowed = client
        .check_any(&consistency::at_least(&revision), "view", &check_rels)
        .await
        .expect("check any failed");

    println!("any user can view: {any_allowed}");
    assert!(
        any_allowed,
        "expected at least one user to have view permission"
    );

    // Bulk import relationships
    let import_rels: Vec<Relationship> = (0..100)
        .map(|i| {
            Relationship::new(
                "document",
                &format!("bulk-doc-{i}"),
                "viewer",
                "user",
                "alice",
                "",
            )
            .expect("invalid relationship")
        })
        .collect();

    let count = client
        .import_relationships(import_rels)
        .await
        .expect("import failed");

    println!("imported {count} relationships");
    assert!(count > 0, "expected at least one imported relationship");

    // Bulk export relationships
    let stream = client.export_relationships(&consistency::full(), None);
    tokio::pin!(stream);

    let mut exported = Vec::new();
    while let Some(rel) = stream.next().await {
        exported.push(rel.expect("export failed"));
    }

    println!("exported {} relationships", exported.len());
}
