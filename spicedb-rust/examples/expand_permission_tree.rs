//! Demonstrates expanding a permission tree with `expand_permission_tree` and
//! walking the native `PermissionTree` result.
//!
//! Run with: `cargo run --example expand_permission_tree`

use spicedb::client::SpiceDBClient;
use spicedb::consistency;
use spicedb::types::{PermissionTree, Relationship, Transaction, TreeOperation};

const SCHEMA: &str = r#"definition user {}

definition document {
    relation viewer: user
    relation editor: user
    relation owner: user
    permission view = viewer + editor + owner
    permission edit = editor + owner
    permission delete = owner
}"#;

/// Recursively walks a `PermissionTree`, collecting the `(subject_type,
/// subject_id)` pairs found at every leaf node.
fn collect_leaf_subjects(tree: &PermissionTree, out: &mut Vec<(String, String)>) {
    if let Some(leaf) = &tree.leaf {
        for subject in &leaf.subjects {
            out.push((subject.subject_type.clone(), subject.subject_id.clone()));
        }
    }
    if let Some(intermediate) = &tree.intermediate {
        for child in &intermediate.children {
            collect_leaf_subjects(child, out);
        }
    }
}

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

    // Write test data: alice is a viewer, bob is an editor of firstdoc
    let alice_viewer = Relationship::new("document", "firstdoc", "viewer", "user", "alice", "")
        .expect("invalid relationship");
    let bob_editor = Relationship::new("document", "firstdoc", "editor", "user", "bob", "")
        .expect("invalid relationship");
    let mut txn = Transaction::new();
    txn.touch(&alice_viewer);
    txn.touch(&bob_editor);
    let revision = client
        .write(&txn)
        .await
        .expect("write relationships failed");

    // Expand the "view" permission tree for firstdoc
    let result = client
        .expand_permission_tree(
            &consistency::at_least(&revision),
            "document",
            "firstdoc",
            "view",
        )
        .await
        .expect("expand permission tree failed");

    println!(
        "expanded document:firstdoc#view at revision: {}",
        result.revision
    );
    assert!(!result.revision.is_empty(), "expected non-empty revision");

    let tree = &result.tree;

    // The root node describes the expanded object/relation.
    println!(
        "root: {}:{}#{}",
        tree.expanded_object.object_type, tree.expanded_object.object_id, tree.expanded_relation,
    );
    assert_eq!(tree.expanded_object.object_type, "document");
    assert_eq!(tree.expanded_object.object_id, "firstdoc");

    // "view" is defined as `viewer + editor + owner`, a union of three
    // relations, so the root is an intermediate node.
    let intermediate = tree
        .intermediate
        .as_ref()
        .expect("expected the root node to be an intermediate node");
    println!(
        "root operation: {:?} ({} children)",
        intermediate.operation,
        intermediate.children.len()
    );
    assert_eq!(intermediate.operation, TreeOperation::Union);
    assert!(tree.leaf.is_none(), "intermediate and leaf are exclusive");

    // Walk the full tree and collect every subject found at a leaf.
    let mut subjects = Vec::new();
    collect_leaf_subjects(tree, &mut subjects);

    println!("subjects found in tree:");
    for (subject_type, subject_id) in &subjects {
        println!("  {subject_type}:{subject_id}");
    }
    assert!(
        subjects.contains(&("user".to_string(), "alice".to_string())),
        "expected alice to appear as a subject in the expanded tree"
    );
    assert!(
        subjects.contains(&("user".to_string(), "bob".to_string())),
        "expected bob to appear as a subject in the expanded tree"
    );
}
