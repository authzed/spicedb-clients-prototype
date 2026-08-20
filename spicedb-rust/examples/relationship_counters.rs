//! Demonstrates registering, reading, and unregistering relationship counters.
//!
//! Run with: `cargo run --example relationship_counters`

use std::time::{Duration, Instant};

use spicedb::client::SpiceDBClient;
use spicedb::error::SpiceDBError;
use spicedb::types::{CountResult, Filter, Relationship, Transaction};

/// Bounds how long the counter may stay "still calculating" before this example
/// fails. Expiry is a failure, deliberately, and not a way out of asserting.
const COUNTER_TIMEOUT: Duration = Duration::from_secs(30);
const COUNTER_POLL_INTERVAL: Duration = Duration::from_millis(100);

/// Polls until the named counter settles.
///
/// The alternative -- sleep two seconds and then guard every assertion with
/// `Some(count) =>` -- asserts nothing at all on a slow run, and nothing on ANY
/// run if the still-calculating mapping is inverted, which is the likeliest bug
/// on that exact field. Coverage that comes and goes between runs, both of them
/// green, is not coverage.
async fn settled_counter(client: &SpiceDBClient, name: &str) -> CountResult {
    let deadline = Instant::now() + COUNTER_TIMEOUT;
    loop {
        let result = client
            .experimental_count_relationships(name)
            .await
            .expect("count relationships failed");
        if let Some(count) = result {
            return count;
        }
        println!("counter is still being calculated...");
        assert!(
            Instant::now() < deadline,
            "counter {name} never settled within {COUNTER_TIMEOUT:?}"
        );
        tokio::time::sleep(COUNTER_POLL_INTERVAL).await;
    }
}

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

    // Write schema and test data
    client
        .write_schema(SCHEMA)
        .await
        .expect("write schema failed");

    let alice = Relationship::new("document", "firstdoc", "viewer", "user", "alice", "")
        .expect("invalid relationship");
    let bob = Relationship::new("document", "seconddoc", "viewer", "user", "bob", "")
        .expect("invalid relationship");

    // An `editor` the counter's filter must NOT count. Without a relationship
    // the filter has to exclude, a counter that ignored the relation filter
    // entirely would still report the expected number.
    let carol = Relationship::new("document", "firstdoc", "editor", "user", "carol", "")
        .expect("invalid relationship");

    let mut txn = Transaction::new();
    txn.touch(&alice);
    txn.touch(&bob);
    txn.touch(&carol);
    client
        .write(&txn)
        .await
        .expect("write relationships failed");

    // Unregister any existing counter from a prior run (ignore errors)
    let _ = client
        .experimental_unregister_relationship_counter("document_viewers")
        .await;

    // Register a counter for all document viewer relationships
    let filter = Filter::new("document").with_relation("viewer");
    client
        .experimental_register_relationship_counter("document_viewers", &filter)
        .await
        .expect("register counter failed");

    println!("registered counter: document_viewers");

    let count = settled_counter(&client, "document_viewers").await;

    println!(
        "document viewer count: {} (revision: {})",
        count.relationship_count, count.revision,
    );

    // Exactly the two viewer relationships written above, and not the editor.
    // A count of zero -- registration silently no-op'ing, or the value never
    // being read off the response -- fails here, and so does a count of three,
    // which is what ignoring the relation filter would produce. This example
    // clears `document` before writing, so the number is its own writes and not
    // an earlier example's leftovers.
    assert_eq!(
        count.relationship_count, 2,
        "expected the counter to report exactly 2 document viewers"
    );
    assert!(
        !count.revision.is_empty(),
        "expected a non-empty revision on the settled counter"
    );

    // Unregister the counter when done
    client
        .experimental_unregister_relationship_counter("document_viewers")
        .await
        .expect("unregister counter failed");

    println!("unregistered counter: document_viewers");

    // Unregistering has to actually remove it: reading a counter that is not
    // registered is an error, so a no-op unregister would leave this call
    // succeeding.
    match client
        .experimental_count_relationships("document_viewers")
        .await
    {
        Err(SpiceDBError::FailedPrecondition(_)) => {}
        other => panic!("expected FailedPrecondition after unregistering, got {other:?}"),
    }

    // Clean up so later examples that write a narrower schema aren't blocked by
    // leftover relationships.
    client
        .delete_relationships(&Filter::new("document"))
        .await
        .expect("cleanup failed");
}
