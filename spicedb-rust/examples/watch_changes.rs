//! Demonstrates watching for relationship changes using the watch API.
//!
//! Run with: `cargo run --example watch_changes`

use futures::StreamExt;
use spicedb::client::SpiceDBClient;
use spicedb::types::UpdateOperation;

#[tokio::main]
async fn main() {
    let client = SpiceDBClient::new_plaintext("localhost:50051", "testtoken")
        .await
        .expect("failed to create client");

    // Watch for changes on "document" resources from the beginning.
    // `include_checkpoints: true` asks the server for periodic checkpoint
    // events in addition to relationship updates -- recommended if this
    // SpiceDB instance is running behind a proxy that aborts idle
    // connections, since a checkpoint keeps the stream alive even when
    // nothing has changed.
    let object_types = vec!["document".to_string()];
    let stream = client.updates(&object_types, None, true);
    tokio::pin!(stream);

    // Watch is an open-ended stream: events arrive incrementally as they
    // occur, so we consume them one at a time until the stream ends (e.g. the
    // connection closes) or an error is yielded.
    while let Some(event) = stream.next().await {
        let event = event.expect("watch stream error");

        // event.changes_through is a resume point: pass it as start_revision
        // on a later `updates()` call to pick back up after a dropped
        // stream, instead of reprocessing everything since the original
        // start_revision or silently losing changes by restarting from
        // head.
        if event.is_checkpoint {
            // A checkpoint carries no updates -- it exists to advertise a
            // fresh resume point and keep the stream alive.
            println!("CHECKPOINT: revision={}", event.changes_through);
            continue;
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
        }
    }
}
