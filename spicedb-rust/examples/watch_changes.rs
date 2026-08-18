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
    let object_types = vec!["document".to_string()];
    let stream = client.updates(&object_types, None);
    tokio::pin!(stream);

    // Watch is an open-ended stream: updates arrive incrementally as they
    // occur, so we consume them one at a time until the stream ends (e.g. the
    // connection closes) or an error is yielded.
    while let Some(update) = stream.next().await {
        let update = update.expect("watch stream error");
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
