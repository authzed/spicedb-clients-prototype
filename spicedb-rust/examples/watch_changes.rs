//! Demonstrates watching for relationship changes with a bounded consumer.
//!
//! Watch is an open-ended server stream: it never completes on its own. A
//! consumer that only prints what arrives cannot fail, and one that never stops
//! cannot be run by CI at all. So this example:
//!
//!   1. subscribes from a known revision,
//!   2. makes a write that must produce a specific update,
//!   3. consumes until it has observed exactly that update (and a checkpoint),
//!   4. drops the stream -- which is how a caller abandons it in Rust, and what
//!      tells tonic to cancel the underlying HTTP/2 stream, and
//!   5. resumes from the same revision on a *fresh* stream and requires the
//!      same update to arrive again.
//!
//! Step 5 is what makes the abandonment more than a comment: a first stream
//! that was never released, or a `changes_through`/start-cursor that does not
//! round-trip, shows up as the second subscription failing or delivering
//! nothing. The server-side view of a released stream is not observable from a
//! client, so that half is covered by the unit tier; see root DESIGN.md,
//! "RULE: Abandoning a stream must release it".
//!
//! Run with: `cargo run --example watch_changes`

use std::time::Duration;

use futures::StreamExt;
use spicedb::client::SpiceDBClient;
use spicedb::error::SpiceDBError;
use spicedb::types::{Filter, Relationship, Transaction, UpdateOperation, WatchOptions};

const SCHEMA: &str = r#"definition user {}

definition document {
    relation viewer: user
    relation editor: user
    relation owner: user
    permission view = viewer + editor + owner
    permission edit = editor + owner
    permission delete = owner
}"#;

/// Bounds the wait for the update this example wrote to come back out of the
/// stream. Generous for a local SpiceDB -- the point is that the example fails,
/// with a message, instead of hanging forever.
const UPDATE_TIMEOUT: Duration = Duration::from_secs(30);

/// What was observed on one bounded pass over the stream.
#[derive(Default)]
struct Observed {
    update: Option<spicedb::types::Update>,
    checkpoint_revision: Option<String>,
}

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
    // first. Clearing also makes the write below a real change: a TOUCH of an
    // already-identical relationship is not a change, and SpiceDB emits no
    // watch event for it.
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

    // A seed write fixes the revision to watch from, so the stream cannot
    // replay what earlier examples left behind and cannot miss the write made
    // below.
    let seed = Relationship::new("document", "watched", "viewer", "user", "seed", "")
        .expect("invalid relationship");
    let mut seed_txn = Transaction::new();
    seed_txn.touch(&seed);
    let start_revision = client.write(&seed_txn).await.expect("seed write failed");

    // The update this example is waiting for. Written after the subscription
    // revision is fixed, so the stream is guaranteed to carry it.
    let target = Relationship::new("document", "watched", "editor", "user", "bob", "")
        .expect("invalid relationship");
    let mut txn = Transaction::new();
    txn.touch(&target);
    client.write(&txn).await.expect("write failed");

    let object_types = vec!["document".to_string()];

    // ── Pass 1: consume until the expected update, then abandon ──────────
    let observed = consume_until_target(&client, &object_types, &start_revision).await;

    let update = observed
        .update
        .expect("watch stream ended without delivering document:watched#editor@user:bob");
    println!("observed the expected update: {}", update.relationship);

    // The update must be the one that was written, not merely "an update".
    assert_eq!(update.relationship.resource_type, "document");
    assert_eq!(update.relationship.resource_id, "watched");
    assert_eq!(update.relationship.resource_relation, "editor");
    assert_eq!(update.relationship.subject_type, "user");
    assert_eq!(update.relationship.subject_id, "bob");
    // TOUCH is a write, so it can only be the mapping for an explicit
    // OPERATION_TOUCH -- never a default an unrecognized operation falls into.
    assert!(
        matches!(
            update.operation,
            UpdateOperation::Create | UpdateOperation::Touch
        ),
        "expected a CREATE or TOUCH for the relationship just written, got {:?}",
        update.operation
    );

    let checkpoint_revision = observed
        .checkpoint_revision
        .expect("no checkpoint event arrived -- with_checkpoints() did not reach the server");
    println!("observed a checkpoint through revision {checkpoint_revision}");

    // ── Pass 2: the first stream was abandoned; resume on a fresh one ────
    //
    // consume_until_target dropped its stream on the way out. Resuming from
    // the same revision has to work and has to deliver the same update again:
    // a start cursor that does not round-trip, or a client left wedged by the
    // abandoned stream, fails here rather than passing quietly.
    let resumed = consume_until_target(&client, &object_types, &start_revision).await;
    let resumed_update = resumed
        .update
        .expect("resuming from the same revision on a fresh stream delivered no update");
    assert_eq!(
        resumed_update.relationship.to_string(),
        update.relationship.to_string(),
        "resuming from the same revision must replay the same update"
    );
    println!("resumed from {start_revision} and saw the same update again");

    // Clean up so later examples that write a narrower schema aren't blocked
    // by leftover relationships.
    client
        .delete_relationships(&Filter::new("document"))
        .await
        .expect("cleanup failed");
}

/// Consumes one bounded pass over the watch stream, stopping as soon as the
/// target update *and* a checkpoint have both been seen, and dropping the
/// stream on the way out.
///
/// `with_checkpoints()` asks the server for periodic checkpoint events in
/// addition to relationship updates -- recommended if this SpiceDB instance is
/// running behind a proxy that aborts idle connections, since a checkpoint
/// keeps the stream alive even when nothing has changed.
async fn consume_until_target(
    client: &SpiceDBClient,
    object_types: &[String],
    start_revision: &str,
) -> Observed {
    let stream = client.updates_with(
        object_types,
        &WatchOptions::new()
            .with_start_revision(start_revision)
            .with_checkpoints(),
    );
    tokio::pin!(stream);

    let mut observed = Observed::default();

    let outcome = tokio::time::timeout(UPDATE_TIMEOUT, async {
        while let Some(event) = stream.next().await {
            let event = event.expect("watch stream error");

            // event.changes_through is a resume point: pass it as
            // start_revision on a later `updates_with()` call to pick back up
            // after a dropped stream, instead of reprocessing everything since
            // the original start_revision or silently losing changes by
            // restarting from head.
            if event.is_checkpoint {
                // A checkpoint carries no updates -- it exists to advertise a
                // fresh resume point and keep the stream alive.
                assert!(
                    event.updates.is_empty(),
                    "a checkpoint carries no updates, but this one had {}",
                    event.updates.len()
                );
                println!("CHECKPOINT: revision={}", event.changes_through);
                if observed.checkpoint_revision.is_none() {
                    observed.checkpoint_revision = Some(event.changes_through.clone());
                }
            }

            for update in &event.updates {
                let op_name = match update.operation {
                    UpdateOperation::Create => "CREATE",
                    UpdateOperation::Touch => "TOUCH",
                    UpdateOperation::Delete => "DELETE",
                    // The server sent an operation this client does not recognize. A
                    // real consumer must NOT treat this as a write -- re-read the
                    // relationship, or fail the mirror closed. The update is still
                    // delivered rather than dropped, so you can see it happened.
                    UpdateOperation::Unspecified => "UNSPECIFIED (unrecognized by this client)",
                };
                println!("{}: {}", op_name, update.relationship);

                if update.relationship.resource_type == "document"
                    && update.relationship.resource_id == "watched"
                    && update.relationship.resource_relation == "editor"
                    && update.relationship.subject_id == "bob"
                    && observed.update.is_none()
                {
                    observed.update = Some(update.clone());
                }
            }

            if observed.update.is_some() && observed.checkpoint_revision.is_some() {
                // Abandon the stream. Dropping it -- which happens when this
                // function returns -- is what tells tonic to cancel the
                // underlying HTTP/2 stream.
                break;
            }
        }
    })
    .await;

    assert!(
        outcome.is_ok(),
        "did not observe document:watched#editor@user:bob and a checkpoint within {UPDATE_TIMEOUT:?}"
    );

    observed
}
