//! Demonstrates watching for relationship changes using the watch API.
//!
//! Run with: `cargo run --example watch_changes`

use spicedb::client::SpiceDBClient;
use spicedb::types::UpdateOperation;

#[tokio::main]
async fn main() {
    let client = SpiceDBClient::new_plaintext("localhost:50051", "somerandomkeyhere")
        .await
        .expect("failed to create client");

    // Watch for changes on "document" resources from the beginning
    let object_types = vec!["document".to_string()];
    let updates = client
        .updates(&object_types, None)
        .await
        .expect("watch failed");

    for update in &updates {
        let op_name = match update.operation {
            UpdateOperation::Create => "CREATE",
            UpdateOperation::Touch => "TOUCH",
            UpdateOperation::Delete => "DELETE",
        };
        println!("{}: {}", op_name, update.relationship);
    }
}
