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

    // Single-request mode sends exactly one request, as the raw API does:
    // without a limit it is unbounded. An unbounded request is not a
    // resumable one: the server may issue no cursor for it at all, so set a
    // limit when a resume point is needed.
    let single = client.lookup_resources(
        spicedb::LookupResourcesRequest::new(
            "document",
            "view",
            spicedb::SubjectRef::new(spicedb::ObjectRef::new("user", "alice").unwrap(), None)
                .unwrap(),
            consistency::full(),
        )
        .with_options(
            spicedb::LookupResourcesOptions::new().with_paging(spicedb::Paging::SingleRequest),
        ),
    );
    tokio::pin!(single);
    let mut single_ids = Vec::new();
    while let Some(result) = single.next().await {
        let result = result.expect("single-request lookup failed");
        single_ids.push(result.resource_id);
    }
    single_ids.sort();
    assert_eq!(single_ids, vec!["firstdoc", "seconddoc"]);
    println!("single request: {single_ids:?}");

    // A limited call always ends on a resume point: repeat the request with
    // the last item's cursor to get the rest.
    let request = spicedb::LookupResourcesRequest::new(
        "document",
        "view",
        spicedb::SubjectRef::new(spicedb::ObjectRef::new("user", "alice").unwrap(), None).unwrap(),
        consistency::full(),
    );
    let first: Vec<_> = client
        .lookup_resources(
            request
                .clone()
                .with_options(spicedb::LookupResourcesOptions::new().with_limit(1)),
        )
        .map(|r| r.expect("limited lookup failed"))
        .collect()
        .await;
    assert_eq!(first.len(), 1);
    let cursor = first[0]
        .after_result_cursor
        .clone()
        .expect("the last item of a limited lookup carries a cursor");
    let rest: Vec<_> = client
        .lookup_resources(
            request
                .clone()
                .with_options(spicedb::LookupResourcesOptions::new().with_cursor(cursor)),
        )
        .map(|r| r.expect("resumed lookup failed").resource_id)
        .collect()
        .await;
    assert_eq!(rest.len(), 1, "resume returns exactly the rest");
    assert_ne!(rest[0], first[0].resource_id);
    println!("limited: {}, resumed: {rest:?}", first[0].resource_id);

    // Batching can be turned off (one resource per response message), which
    // puts a cursor on every item; a batch size can be set instead with
    // `with_response_batch_size`.
    let unbatched: Vec<_> = client
        .lookup_resources(request.with_options(
            spicedb::LookupResourcesOptions::new().with_batching(spicedb::LookupBatching::Disabled),
        ))
        .map(|r| r.expect("unbatched lookup failed"))
        .collect()
        .await;
    assert_eq!(unbatched.len(), 2);
    assert!(
        unbatched.iter().all(|r| r.after_result_cursor.is_some()),
        "without batching each item carries its cursor"
    );
    println!("unbatched: every item carries a cursor");
}
