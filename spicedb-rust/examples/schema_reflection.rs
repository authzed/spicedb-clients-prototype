//! Demonstrates schema reflection APIs: inspecting definitions, computing
//! permissions, finding dependent relations, and diffing schemas.
//!
//! Run with: `cargo run --example schema_reflection`

use spicedb::client::SpiceDBClient;
use spicedb::consistency;
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
    for def in &result.definitions {
        println!(
            "  definition {}: {} relations, {} permissions",
            def.name,
            def.relations.len(),
            def.permissions.len(),
        );
    }

    // Assert the shape of what came back, not merely that something did. A
    // non-empty check passes on a reflection that returned one definition with
    // no relations and no permissions -- which is what dropping the nested
    // conversion loops would produce. Relations and permissions are also
    // distinct lists: a reflection that conflated them would put `view` among
    // the relations.
    let mut definition_names: Vec<&str> =
        result.definitions.iter().map(|d| d.name.as_str()).collect();
    definition_names.sort_unstable();
    assert_eq!(
        definition_names,
        vec!["document", "user"],
        "unexpected definitions in the reflected schema"
    );

    let document = result
        .definitions
        .iter()
        .find(|d| d.name == "document")
        .expect("expected a `document` definition in the reflected schema");
    let mut relation_names: Vec<&str> =
        document.relations.iter().map(|r| r.name.as_str()).collect();
    relation_names.sort_unstable();
    assert_eq!(relation_names, vec!["editor", "owner", "viewer"]);
    let mut permission_names: Vec<&str> = document
        .permissions
        .iter()
        .map(|p| p.name.as_str())
        .collect();
    permission_names.sort_unstable();
    assert_eq!(permission_names, vec!["delete", "edit", "view"]);
    assert!(
        !result.revision.is_empty(),
        "expected a non-empty revision from reflect_schema"
    );

    // Find computable permissions for a relation
    let (perms, revision) = client
        .computable_permissions(&consistency::full(), "document", "viewer")
        .await
        .expect("computable permissions failed");

    println!("\ncomputable permissions for document#viewer (revision: {revision}):");
    for p in &perms {
        println!(
            "  {}#{} (is_permission={})",
            p.definition_name, p.relation_name, p.is_permission,
        );
    }
    // `viewer` appears in `view` and in nothing else in SCHEMA, so the answer
    // is exactly one reference, and it names the permission rather than the
    // relation it was asked about.
    let computable: Vec<String> = perms
        .iter()
        .map(|p| format!("{}#{}", p.definition_name, p.relation_name))
        .collect();
    assert_eq!(computable, vec!["document#view".to_string()]);
    assert!(
        perms[0].is_permission,
        "expected document#view to be reported as a permission, not a relation"
    );
    assert!(!revision.is_empty());

    // Find dependent relations for a permission
    let (deps, revision) = client
        .dependent_relations(&consistency::full(), "document", "view")
        .await
        .expect("dependent relations failed");

    println!("\ndependent relations for document#view (revision: {revision}):");
    for d in &deps {
        println!("  {}#{}", d.definition_name, d.relation_name);
    }
    // `view = viewer + editor + owner`, so all three relations are dependencies
    // and nothing else is.
    let mut dependents: Vec<String> = deps
        .iter()
        .map(|d| format!("{}#{}", d.definition_name, d.relation_name))
        .collect();
    dependents.sort();
    assert_eq!(
        dependents,
        vec![
            "document#editor".to_string(),
            "document#owner".to_string(),
            "document#viewer".to_string(),
        ]
    );
    assert!(!revision.is_empty());

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

    // new_schema adds one relation and one permission and changes the
    // expression of the other three, so the diff is a known set. Asserting only
    // that it is non-empty would pass on a diff that reported every kind as
    // "unknown" -- the exact outcome of the proto-to-native mapping falling
    // through to its default branch.
    let diff_tuples: Vec<(&str, &str, &str, &str)> = diffs
        .iter()
        .map(|d| {
            (
                d.kind.as_str(),
                d.definition_name.as_str(),
                d.relation_name.as_str(),
                d.permission_name.as_str(),
            )
        })
        .collect();
    assert!(
        diff_tuples.contains(&("relation_added", "document", "admin", "")),
        "expected a relation_added diff for document#admin, got {diff_tuples:?}"
    );
    assert!(
        diff_tuples.contains(&("permission_added", "document", "", "manage")),
        "expected a permission_added diff for document#manage, got {diff_tuples:?}"
    );
    assert!(
        diff_tuples.contains(&("permission_expr_changed", "document", "", "view")),
        "expected a permission_expr_changed diff for document#view, got {diff_tuples:?}"
    );
    assert!(
        !diffs.iter().any(|d| d.kind == "unknown"),
        "a diff came back as \"unknown\": the proto-to-native diff mapping does not cover \
         what the server sent ({diff_tuples:?})"
    );
    assert!(!revision.is_empty());
}
