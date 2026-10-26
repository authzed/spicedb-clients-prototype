//! Demonstrates expanding a permission tree with `expand_permission_tree` and
//! walking the native `PermissionTree` result.
//!
//! Run with: `cargo run --example expand_permission_tree`

use spicedb::client::SpiceDBClient;
use spicedb::consistency;
use spicedb::error::SpiceDBError;
use spicedb::types::{Filter, PermissionTree, Transaction, TreeOperation};

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
    match tree {
        PermissionTree::Leaf { subjects, .. } => {
            for subject in subjects {
                out.push((
                    subject.object().object_type().into(),
                    subject.object().object_id().into(),
                ));
            }
        }
        PermissionTree::Intermediate { children, .. } => {
            for child in children {
                collect_leaf_subjects(child, out);
            }
        }
        _ => {}
    }
}

#[tokio::main]
async fn main() {
    // Endpoint and token come from the environment so the example runs against
    // whichever SpiceDB the caller started; the defaults match
    // docker-compose.test.yml.
    let endpoint =
        std::env::var("SPICEDB_ENDPOINT").unwrap_or_else(|_| "localhost:50051".to_string());
    let token = std::env::var("SPICEDB_TOKEN").unwrap_or_else(|_| "testtoken".to_string());

    let client = SpiceDBClient::new_plaintext(
        endpoint,
        token,
        spicedb::RemoteCredentialsPolicy::LoopbackOnly,
    )
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
    match client
        .delete_all_relationships_non_atomic(spicedb::DeleteAllRequest::new(
            (Filter::new("document")).clone(),
        ))
        .await
    {
        Ok(_) | Err(SpiceDBError::FailedPrecondition(_)) => {}
        Err(e) => panic!("cleanup before schema write failed: {e:?}"),
    }

    // Write schema
    client
        .write_schema(spicedb::WriteSchemaRequest::new(SCHEMA))
        .await
        .expect("write schema failed");

    // Write test data: alice is a viewer, bob is an editor of firstdoc
    let alice_viewer = spicedb::Relationship::from_tuple("document:firstdoc#viewer@user:alice")
        .expect("invalid relationship");
    let bob_editor = spicedb::Relationship::from_tuple("document:firstdoc#editor@user:bob")
        .expect("invalid relationship");
    let mut txn = Transaction::new();
    txn.touch(&alice_viewer);
    txn.touch(&bob_editor);
    let revision = client
        .write(spicedb::WriteRequest::new((txn).clone()))
        .await
        .expect("write relationships failed");

    // Expand the "view" permission tree for firstdoc
    let result = client
        .expand_permission_tree(spicedb::ExpandPermissionTreeRequest::new(
            spicedb::ObjectRef::new("document", "firstdoc").unwrap(),
            "view",
            (consistency::at_least((&revision.revision).into())).clone(),
        ))
        .await
        .expect("expand permission tree failed");

    println!(
        "expanded document:firstdoc#view at revision: {}",
        result.revision
    );
    assert!(
        !result.revision.as_ref().is_empty(),
        "expected non-empty revision"
    );

    let tree = &result.tree;

    let PermissionTree::Intermediate {
        expanded_object,
        operation,
        ..
    } = tree
    else {
        panic!("expected intermediate root")
    };
    assert_eq!(expanded_object.object_type(), "document");
    assert_eq!(expanded_object.object_id(), "firstdoc");
    assert_eq!(*operation, TreeOperation::Union);

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
