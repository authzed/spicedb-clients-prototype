//! Demonstrates the client-level `default_timeout` builder option, a
//! per-call `_with_timeout` override, and that bulk import
//! (`import_relationships`) is a client-streaming call that is NOT bounded
//! by `default_timeout` -- see root DESIGN.md, "RULE: A unary call must have
//! a deadline".
//!
//! Run with: `cargo run --example call_deadlines`

use std::time::Duration;

use spicedb::client::SpiceDBClient;
use spicedb::consistency;
use spicedb::error::SpiceDBError;
use spicedb::types::{Filter, Relationship, Transaction};

const SCHEMA: &str = r#"definition user {}

definition document {
    relation viewer: user
    permission view = viewer
}"#;

#[tokio::main]
async fn main() {
    // default_timeout applies to every unary call that doesn't use a
    // `_with_timeout` variant. This is the documented, real construction
    // path -- not a mock -- so a signature drift here (e.g. the builder
    // method silently disappearing) would fail this example, not just a
    // unit test against a stalling stub.
    // Endpoint and token come from the environment so the example runs against
    // whichever SpiceDB the caller started; the defaults match
    // docker-compose.test.yml.
    let endpoint =
        std::env::var("SPICEDB_ENDPOINT").unwrap_or_else(|_| "localhost:50051".to_string());
    let token = std::env::var("SPICEDB_TOKEN").unwrap_or_else(|_| "testtoken".to_string());

    let client = SpiceDBClient::builder(endpoint, token)
        .plaintext()
        .default_timeout(Duration::from_secs(5))
        .build()
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

    client
        .write_schema(SCHEMA)
        .await
        .expect("write schema failed");

    let rel = Relationship::new("document", "readme", "viewer", "user", "alice", "")
        .expect("invalid relationship");
    let mut txn = Transaction::new();
    txn.touch(&rel);
    let revision = client.write(&txn).await.expect("write failed");

    // Bound by the 5s default set above.
    let check_rel = Relationship::new("document", "readme", "view", "user", "alice", "")
        .expect("invalid relationship");
    let result = client
        .check_permission(&consistency::at_least(&revision), "view", &check_rel)
        .await
        .expect("check failed");
    println!(
        "alice can view document:readme (default timeout): {}",
        result.has_permission()
    );
    assert!(result.has_permission());

    // A per-call `_with_timeout` overrides the client default for this one
    // call. 2 seconds is generous for a real call against a local SpiceDB --
    // this exercises the real timeout parameter end-to-end, not testing how
    // small a timeout can be.
    let result = client
        .check_permission_with_timeout(
            &consistency::at_least(&revision),
            "view",
            &check_rel,
            Duration::from_secs(2),
        )
        .await
        .expect("check with timeout failed");
    println!(
        "alice can view document:readme (2s per-call timeout): {}",
        result.has_permission()
    );
    assert!(result.has_permission());

    // import_relationships (ImportBulkRelationships) is client-streaming:
    // its duration scales with the size of the caller's dataset, not with
    // server latency, so it is explicitly excluded from default_timeout.
    // Calling it with no timeout bound at all -- as below -- must still
    // succeed; if a future change accidentally routed the unary default
    // into this call, a large enough import would start failing with
    // DeadlineExceeded well before it finished.
    let import_rels: Vec<Relationship> = (0..50)
        .map(|i| {
            Relationship::new(
                "document",
                format!("bulk-{i}"),
                "viewer",
                "user",
                "alice",
                "",
            )
            .expect("invalid relationship")
        })
        .collect();
    let num_loaded = client
        .import_relationships(import_rels)
        .await
        .expect("import failed");
    println!("imported {num_loaded} relationships with no timeout bound");
    assert_eq!(num_loaded, 50);

    // A caller-supplied timeout on the same client-streaming call must
    // still be honored -- the exclusion is from the *default*, not from the
    // ability to bound the call at all.
    let more_import_rels: Vec<Relationship> = (0..50)
        .map(|i| {
            Relationship::new(
                "document",
                format!("bulk2-{i}"),
                "viewer",
                "user",
                "alice",
                "",
            )
            .expect("invalid relationship")
        })
        .collect();
    let num_loaded_bounded = client
        .import_relationships_with_timeout(more_import_rels, Duration::from_secs(30))
        .await
        .expect("import with timeout failed");
    println!("imported {num_loaded_bounded} relationships with an explicit 30s timeout");
    assert_eq!(num_loaded_bounded, 50);
}
