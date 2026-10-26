//! Demonstrates finding resources a subject can access.
//!
//! Run with: `cargo run --example lookup_resources`

use futures::StreamExt;
use spicedb::client::SpiceDBClient;
use spicedb::consistency;
use spicedb::error::SpiceDBError;
use spicedb::types::{Filter, Transaction};

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

    // Write test data: alice can view firstdoc, alice can edit seconddoc
    let alice_viewer = spicedb::Relationship::from_tuple("document:firstdoc#viewer@user:alice")
        .expect("invalid relationship");
    let alice_editor = spicedb::Relationship::from_tuple("document:seconddoc#editor@user:alice")
        .expect("invalid relationship");

    let mut txn = Transaction::new();
    txn.touch(&alice_viewer);
    txn.touch(&alice_editor);
    client
        .write(spicedb::WriteRequest::new((txn).clone()))
        .await
        .expect("write relationships failed");

    // Collect only unconditional grants; conditional results need more caveat context.
    let stream = client.lookup_resources(
        spicedb::LookupResourcesRequest::new(
            "document",
            "view",
            spicedb::SubjectRef::new(spicedb::ObjectRef::new("user", "alice").unwrap(), None)
                .unwrap(),
            (consistency::full()).clone(),
        )
        .with_options(
            spicedb::LookupResourcesOptions::new()
                .with_permissionship_filter(spicedb::LookupPermissionshipFilter::HasPermissionOnly),
        ),
    );
    tokio::pin!(stream);

    let mut resource_ids = Vec::new();
    while let Some(result) = stream.next().await {
        let result = result.expect("lookup failed");
        println!(
            "alice can view document:{} ({:?})",
            result.resource_id, result.permissionship
        );
        resource_ids.push(result.resource_id);
    }

    assert!(
        resource_ids.contains(&"firstdoc".to_string()),
        "expected firstdoc in results"
    );
    assert!(
        resource_ids.contains(&"seconddoc".to_string()),
        "expected seconddoc in results"
    );
}
