//! Tooling-grade features a CLI like zed needs, without `raw_proto()`:
//! a client over a caller-owned channel with its own retry/deadline policy,
//! per-call metadata capture, paged reads that resume from a cursor, import
//! batches and the server's abort status, per-item bulk errors, delete-all
//! progress, full tuple syntax and protocol conversions for JSON output.
//!
//! Run with: `cargo run --example tooling_escape_hatches`

use std::num::NonZeroU32;
use std::sync::{Arc, Mutex};
use std::time::Duration;

use futures::StreamExt;
use spicedb::{
    consistency, protoconv, BulkCheckRequest, BulkItemErrors, CallOptions, CheckOptions,
    DefaultDeadline, DeleteAllOptions, DeleteAllRequest, Filter, ImportRelationshipsOptions,
    ImportRelationshipsRequest, PermissionCheck, ReadRelationshipsOptions,
    ReadRelationshipsRequest, Relationship, ResponseMetadata, RetryPolicy, SpiceDBClient,
    SpiceDBError, StreamRetryMode,
};

const SCHEMA: &str = r#"use expiration

definition user {}

caveat on_network(cidr string) {
    cidr == "10.0.0.0/8"
}

definition document {
    relation viewer: user | user with on_network | user with expiration
    permission view = viewer
}"#;

#[tokio::main]
async fn main() {
    let endpoint =
        std::env::var("SPICEDB_ENDPOINT").unwrap_or_else(|_| "localhost:50051".to_string());
    let token = std::env::var("SPICEDB_TOKEN").unwrap_or_else(|_| "testtoken".to_string());

    // A channel the tool owns (in zed: the WASM or embedded-server connection).
    let channel = tonic::transport::Endpoint::from_shared(format!("http://{endpoint}"))
        .expect("endpoint")
        .connect_lazy();
    let retries = Arc::new(Mutex::new(0u32));
    let counted = retries.clone();
    let client = SpiceDBClient::from_channel(channel.clone())
        .token(token.clone())
        // Long imports and schema writes: no client default deadline.
        .default_deadline(DefaultDeadline::Disabled)
        // The tool's own policy, logged on every retry.
        .retry_policy(
            RetryPolicy::new()
                .with_max_retries(2)
                .with_initial_backoff(Duration::from_millis(50))
                .with_on_retry(move |event| {
                    *counted.lock().unwrap() += 1;
                    eprintln!("retrying in {:?}: {}", event.delay, event.error);
                }),
        )
        // A supervised watch is never reopened behind the tool's back.
        .stream_retries(StreamRetryMode::Disabled)
        .build()
        .expect("client over a caller-owned channel");

    // Cleanup first (see examples/README.md); a fresh server has no
    // `document` definition yet.
    match client
        .delete_all_relationships_non_atomic(DeleteAllRequest::new(Filter::new("document")))
        .await
    {
        Ok(_) | Err(SpiceDBError::FailedPrecondition(_)) => {}
        Err(e) => panic!("cleanup failed: {e:?}"),
    }

    // Response metadata, as `zed version` reads it.
    let captured = ResponseMetadata::new();
    client
        .write_schema(
            spicedb::WriteSchemaRequest::new(SCHEMA)
                .with_options(spicedb::WriteSchemaOptions::new().with_call_options(
                    CallOptions::new().with_response_metadata(captured.clone()),
                )),
        )
        .await
        .expect("write schema");
    assert!(
        captured.headers().get("content-type").is_some(),
        "captured response headers: {:?}",
        captured.headers()
    );

    // Full tuple syntax in, the same canonical text out.
    let tuples = [
        "document:readme#viewer@user:alice",
        r#"document:readme#viewer@user:bob[on_network:{"cidr":"10.0.0.0/8"}]"#,
        "document:readme#viewer@user:carol[expiration:2300-01-01T00:00:00Z]",
    ];
    let mut relationships: Vec<Relationship> =
        tuples.iter().map(|t| t.parse().expect("tuple")).collect();
    for i in 0..9 {
        relationships.push(
            format!("document:bulk{i}#viewer@user:alice")
                .parse()
                .expect("tuple"),
        );
    }

    // Import in messages of 4; one call is one transaction.
    let import = |rels: Vec<Relationship>| {
        ImportRelationshipsRequest::new(futures::stream::iter(rels.into_iter().map(Ok)))
            .with_options(
                ImportRelationshipsOptions::new().with_batch_size(NonZeroU32::new(4).unwrap()),
            )
    };
    let imported = client
        .import_relationships(import(relationships.clone()))
        .await
        .expect("import");
    assert_eq!(imported.imported_count, 12);
    // Re-importing a relationship reports the server's ALREADY_EXISTS.
    let err = client
        .import_relationships(import(vec![relationships[0].clone()]))
        .await
        .expect_err("duplicate import");
    assert!(matches!(err, SpiceDBError::AlreadyExists(_)), "{err:?}");
    println!(
        "duplicate import: {}",
        err.server_message().unwrap_or_default()
    );

    // Paged read: stop after 5, then resume from the fifth item's cursor with
    // the same request and page size.
    let request = ReadRelationshipsRequest::new(Filter::new("document"), consistency::full());
    let page = NonZeroU32::new(4).unwrap();
    let first: Vec<_> = client
        .read_relationships(
            request.clone().with_options(
                ReadRelationshipsOptions::new()
                    .with_page_size(page)
                    .with_limit(5),
            ),
        )
        .map(|r| r.expect("read"))
        .collect()
        .await;
    let cursor = first[4].after_result_cursor.clone().expect("cursor");
    let rest: Vec<_> = client
        .read_relationships(
            request.with_options(
                ReadRelationshipsOptions::new()
                    .with_page_size(page)
                    .with_cursor(cursor),
            ),
        )
        .map(|r| r.expect("resumed read"))
        .collect()
        .await;
    assert_eq!(
        first.len() + rest.len(),
        12,
        "resume picks up exactly where it stopped"
    );
    let mut printed: Vec<String> = first
        .iter()
        .chain(&rest)
        .map(|r| r.relationship.to_string())
        .collect();
    printed.sort();
    for tuple in tuples {
        assert!(printed.contains(&tuple.to_string()), "{tuple} round-trips");
    }
    // Protocol-shaped output for --json, using the client's own conversions.
    println!("{:?}", protoconv::read_relationship_to_proto(&first[0]));

    // Bulk check with per-item errors next to the other items' answers.
    let question = |resource: &str| {
        PermissionCheck::new(
            resource.parse().unwrap(),
            "view",
            "user:alice".parse().unwrap(),
            consistency::full(),
        )
    };
    let results = client
        .check_bulk(
            BulkCheckRequest::new(vec![
                question("document:bulk0"),
                question("nosuchtype:x"),
                question("document:none"),
            ])
            .with_options(CheckOptions::new().with_bulk_item_errors(BulkItemErrors::Returned)),
        )
        .await
        .expect("bulk check");
    assert!(results[0].has_permission());
    let item_error = results[1].error.as_ref().expect("item 1 failed");
    println!("item 1: {item_error}");
    assert!(!results[1].has_permission());
    assert!(results[2].error.is_none() && !results[2].has_permission());

    // Delete everything in batches of 5, reporting progress per batch.
    let batches = Arc::new(Mutex::new(Vec::new()));
    let seen = batches.clone();
    let deleted = client
        .delete_all_relationships_non_atomic(
            DeleteAllRequest::new(Filter::new("document")).with_options(
                DeleteAllOptions::new()
                    .with_page_size(NonZeroU32::new(5).unwrap())
                    .with_progress(move |p| seen.lock().unwrap().push(p.deleted_count)),
            ),
        )
        .await
        .expect("delete all");
    assert_eq!(deleted.deleted_count, 12);
    let batches = batches.lock().unwrap().clone();
    println!("delete-all progress: {batches:?}");
    assert_eq!(batches.last(), Some(&12));
    assert!(batches.len() >= 3, "pages of 5 over 12 relationships");

    // The caller still owns the channel after the client is gone.
    drop(client);
    let again = SpiceDBClient::from_channel(channel)
        .token(token)
        .build()
        .expect("second client on the same channel");
    again
        .read_schema(spicedb::ReadSchemaRequest::new())
        .await
        .expect("channel still usable");
    println!("retries observed: {}", retries.lock().unwrap());
}
