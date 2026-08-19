//! Demonstrates schema reflection APIs: inspecting definitions, computing
//! permissions, finding dependent relations, and diffing schemas.
//!
//! Run with: `cargo run --example schema_reflection`

use spicedb::client::SpiceDBClient;
use spicedb::consistency;

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

    // Reflect the current schema
    let result = client
        .reflect_schema(&consistency::full())
        .await
        .expect("reflect schema failed");

    println!(
        "schema has {} definitions and {} caveats (revision: {})",
        result.definitions.len(),
        result.caveats.len(),
        result.revision,
    );
    assert!(
        !result.definitions.is_empty(),
        "expected at least one definition"
    );

    for def in &result.definitions {
        println!(
            "  definition {}: {} relations, {} permissions",
            def.name,
            def.relations.len(),
            def.permissions.len(),
        );
    }

    // Find computable permissions for a relation
    let (perms, revision) = client
        .computable_permissions(&consistency::full(), "document", "viewer")
        .await
        .expect("computable permissions failed");

    println!("\ncomputable permissions for document#viewer (revision: {revision}):");
    assert!(
        !perms.is_empty(),
        "expected at least one computable permission"
    );
    for p in &perms {
        println!(
            "  {}#{} (is_permission={})",
            p.definition_name, p.relation_name, p.is_permission,
        );
    }

    // Find dependent relations for a permission
    let (deps, revision) = client
        .dependent_relations(&consistency::full(), "document", "view")
        .await
        .expect("dependent relations failed");

    println!("\ndependent relations for document#view (revision: {revision}):");
    assert!(!deps.is_empty(), "expected at least one dependent relation");
    for d in &deps {
        println!("  {}#{}", d.definition_name, d.relation_name);
    }

    // Diff against a modified schema
    let new_schema = r#"definition user {}

definition document {
    relation viewer: user
    relation editor: user
    relation owner: user
    relation admin: user
    permission view = viewer + editor + owner + admin
    permission edit = editor + owner + admin
    permission delete = owner + admin
    permission manage = admin
}"#;

    let (diffs, revision) = client
        .diff_schema(&consistency::full(), new_schema)
        .await
        .expect("diff schema failed");

    println!("\nschema diffs (revision: {revision}):");
    assert!(!diffs.is_empty(), "expected at least one diff");
    for d in &diffs {
        print!("  {}", d.kind);
        if !d.definition_name.is_empty() {
            print!(" on {}", d.definition_name);
        }
        if !d.relation_name.is_empty() {
            print!("#{}", d.relation_name);
        }
        if !d.permission_name.is_empty() {
            print!("#{}", d.permission_name);
        }
        println!();
    }
}
