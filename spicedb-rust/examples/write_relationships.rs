//! Demonstrates writing relationships with a transaction builder.
//!
//! Run with: `cargo run --example write_relationships`

use spicedb::client::SpiceDBClient;
use spicedb::error::SpiceDBError;
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

    // A failed precondition arrives with SpiceDB's structured explanation
    // attached, not just a message: the typed error carries the
    // authzed.api.v1.ErrorReason name and the metadata naming which
    // precondition did not hold, so recovery can be written against data
    // rather than against a parsed string.
    let seconddoc_viewer =
        Relationship::new("document", "seconddoc", "viewer", "user", "alice", "")
            .expect("invalid relationship");
    let mut doomed = Transaction::new();
    doomed.touch(&seconddoc_viewer);
    doomed.must_match(
        Filter::new("document")
            .with_resource_id("firstdoc")
            .with_relation("owner")
            .with_subject_type("user")
            .with_subject_id("nobody"),
    );

    let precondition_err = client
        .write(&doomed)
        .await
        .expect_err("expected the unsatisfiable precondition to fail the write");

    println!(
        "precondition failed: reason={:?} domain={:?} metadata={:?}",
        precondition_err.reason(),
        precondition_err.reason_domain(),
        precondition_err.reason_metadata()
    );
    assert!(
        matches!(precondition_err, SpiceDBError::FailedPrecondition(_)),
        "expected FailedPrecondition, got {precondition_err:?}"
    );
    assert_eq!(
        precondition_err.reason(),
        Some("ERROR_REASON_WRITE_OR_DELETE_PRECONDITION_FAILURE")
    );
    assert!(
        !precondition_err.reason_metadata().is_empty(),
        "expected the reason metadata to name the failing precondition"
    );

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
