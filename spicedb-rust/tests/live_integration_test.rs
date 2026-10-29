//! Live-SpiceDB integration tests. These require a real SpiceDB server and
//! are `#[ignore]`d by default so a plain `cargo test` never needs one.
//!
//! Run via `mage -d spicedb-rust integrationTest` (starts SpiceDB from
//! `docker-compose.test.yml` and runs `cargo test -- --ignored`), or
//! manually:
//!
//! ```sh
//! docker compose -f docker-compose.test.yml up -d
//! cargo test --test live_integration_test -- --ignored
//! docker compose -f docker-compose.test.yml down
//! ```

use spicedb::client::SpiceDBClient;
use spicedb::consistency;
use spicedb::types::CheckOptions;
use spicedb::types::{Permissionship, Transaction};

static LIVE_LOCK: tokio::sync::Mutex<()> = tokio::sync::Mutex::const_new(());

async fn cleanup(client: &SpiceDBClient) {
    for definition in ["document", "doc"] {
        match client
            .delete_all_relationships_non_atomic(spicedb::DeleteAllRequest::new(
                spicedb::Filter::new(definition),
            ))
            .await
        {
            Ok(_) | Err(spicedb::SpiceDBError::FailedPrecondition(_)) => {}
            Err(error) => panic!("cleanup failed: {error}"),
        }
    }
}

const CAVEATED_SCHEMA: &str = r#"definition user {}

caveat active(now int) {
    now < 100
}

definition doc {
    relation viewer: user with active
    permission view = viewer
}"#;

/// The live server these tests connect to. `mage integrationTest` starts it and
/// exports `SPICEDB_ENDPOINT`/`SPICEDB_TOKEN`, the same two variables every
/// example reads; the defaults match `docker-compose.test.yml`.
fn live_endpoint() -> String {
    std::env::var("SPICEDB_ENDPOINT").unwrap_or_else(|_| "localhost:50051".to_string())
}

fn live_token() -> String {
    std::env::var("SPICEDB_TOKEN").unwrap_or_else(|_| "testtoken".to_string())
}

/// Writes a caveated relationship, checks the permission with no caveat
/// context supplied, and asserts the server reports a `ConditionalPermission`
/// result rather than a grant — the server needed `now` to evaluate `active`
/// and did not receive it.
#[tokio::test]
#[ignore = "requires a live SpiceDB server (see module docs)"]
async fn check_permission_with_missing_caveat_context_is_conditional_not_granted() {
    let _guard = LIVE_LOCK.lock().await;
    let client = SpiceDBClient::new_plaintext(
        live_endpoint(),
        live_token(),
        spicedb::RemoteCredentialsPolicy::LoopbackOnly,
    )
    .await
    .expect("connect to live SpiceDB");

    cleanup(&client).await;
    client
        .write_schema(spicedb::WriteSchemaRequest::new(CAVEATED_SCHEMA))
        .await
        .expect("write schema");

    // Write the relationship with the `active` caveat attached but no
    // context supplied.
    let rel = spicedb::Relationship::from_tuple("doc:conditionaldoc#viewer@user:alice")
        .expect("valid relationship")
        .with_caveat("active", None);
    let mut txn = Transaction::new();
    txn.touch(&rel);
    let revision = client
        .write(spicedb::WriteRequest::new((txn).clone()))
        .await
        .expect("write relationship");

    // Check with no context — the server cannot evaluate `now < 100`.
    let check_rel = spicedb::Relationship::from_tuple("doc:conditionaldoc#view@user:alice")
        .expect("valid relationship");
    let result = client
        .check(spicedb::PermissionCheck::new(
            check_rel.resource().clone(),
            "view",
            check_rel.subject().clone(),
            (consistency::at_least((&revision.revision).into())).clone(),
        ))
        .await
        .expect("check should succeed (the server answers Conditional, not an error)");

    assert_eq!(
        result.permissionship,
        Permissionship::ConditionalPermission,
        "expected a Conditional result when caveat context is missing, got {:?}",
        result.permissionship
    );
    assert!(
        !result.has_permission(),
        "a Conditional result must not report has_permission() == true"
    );
    assert_eq!(
        result.missing_context,
        vec!["now".to_string()],
        "expected the server to report `now` as the missing caveat parameter"
    );
    assert!(
        !result.checked_at.as_ref().is_empty(),
        "checked_at should be populated from the response"
    );
    cleanup(&client).await;
}

/// C5: the payoff test for spec D3b. Same caveat schema and relationship as
/// the test above (server needs `now` and doesn't get it => Conditional),
/// but this time the caller supplies the missing context via
/// `check_permission_with_options` — proving `missing_context` is
/// actionable: a caller can resolve a Conditional into a real grant.
#[tokio::test]
#[ignore = "requires a live SpiceDB server (see module docs)"]
async fn check_permission_with_options_resolves_conditional_to_grant() {
    let _guard = LIVE_LOCK.lock().await;
    let client = SpiceDBClient::new_plaintext(
        live_endpoint(),
        live_token(),
        spicedb::RemoteCredentialsPolicy::LoopbackOnly,
    )
    .await
    .expect("connect to live SpiceDB");

    cleanup(&client).await;
    client
        .write_schema(spicedb::WriteSchemaRequest::new(CAVEATED_SCHEMA))
        .await
        .expect("write schema");

    let rel = spicedb::Relationship::from_tuple("doc:contextresolveddoc#viewer@user:alice")
        .expect("valid relationship")
        .with_caveat("active", None);
    let mut txn = Transaction::new();
    txn.touch(&rel);
    let revision = client
        .write(spicedb::WriteRequest::new((txn).clone()))
        .await
        .expect("write relationship");

    let check_rel = spicedb::Relationship::from_tuple("doc:contextresolveddoc#view@user:alice")
        .expect("valid relationship");

    // Without context: Conditional, per the test above.
    let conditional = client
        .check(spicedb::PermissionCheck::new(
            check_rel.resource().clone(),
            "view",
            check_rel.subject().clone(),
            (consistency::at_least((&revision.revision).into())).clone(),
        ))
        .await
        .expect("check should succeed");
    assert_eq!(
        conditional.permissionship,
        Permissionship::ConditionalPermission,
        "sanity check: without context the server must still answer Conditional"
    );

    // With context supplying `now`: `now < 100` evaluates true, so the
    // caveat passes and the check resolves to an outright grant.
    let mut context = std::collections::HashMap::new();
    context.insert("now".to_string(), serde_json::json!(42));
    let granted = client
        .check(
            spicedb::PermissionCheck::new(
                check_rel.resource().clone(),
                "view",
                check_rel.subject().clone(),
                (consistency::at_least((&revision.revision).into())).clone(),
            )
            .with_options((CheckOptions::new().with_context(context.clone())).clone()),
        )
        .await
        .expect("check with context should succeed");

    assert_eq!(
        granted.permissionship,
        Permissionship::HasPermission,
        "expected supplying the missing `now` context to resolve the caveat to a grant, got {:?}",
        granted.permissionship
    );
    assert!(
        granted.has_permission(),
        "has_permission() must be true once the missing caveat context is supplied"
    );
    assert!(
        granted.missing_context.is_empty(),
        "a HasPermission result must not report missing context"
    );
    cleanup(&client).await;
}

const ADOPTION_SCHEMA: &str = r#"use expiration

definition user {}

caveat active(now int) {
    now < 100
}

definition doc {
    relation viewer: user | user with active | user with expiration
    permission view = viewer
}"#;

async fn adoption_client() -> SpiceDBClient {
    let client = SpiceDBClient::new_plaintext(
        live_endpoint(),
        live_token(),
        spicedb::RemoteCredentialsPolicy::LoopbackOnly,
    )
    .await
    .expect("connect to live SpiceDB");
    cleanup(&client).await;
    client
        .write_schema(spicedb::WriteSchemaRequest::new(ADOPTION_SCHEMA))
        .await
        .expect("write schema");
    client
}

/// The page-two bug: SpiceDB hashes consistency and limit into every cursor,
/// so a pager that switches page two to an exact snapshot or shrinks the last
/// page's limit is rejected ("the cursor provided does not have the same
/// arguments as the original API call"). Reads and lookups must cross several
/// pages, honour a total limit that is not a multiple of the page size, and
/// resume from an item's cursor.
#[tokio::test]
#[ignore = "requires a live SpiceDB server (see module docs)"]
async fn paged_reads_and_lookups_cross_pages_and_resume() {
    use futures::StreamExt;
    use std::num::NonZeroU32;
    let _guard = LIVE_LOCK.lock().await;
    let client = adoption_client().await;

    let mut txn = Transaction::new();
    for i in 0..7 {
        txn.touch(&format!("doc:page{i}#viewer@user:alice").parse().unwrap());
    }
    let written = client
        .write(spicedb::WriteRequest::new(txn))
        .await
        .expect("write relationships");
    let cs = consistency::at_least(written.revision.clone());
    let page = NonZeroU32::new(2).unwrap();

    let read = spicedb::ReadRelationshipsRequest::new(spicedb::Filter::new("doc"), cs.clone());
    let items: Vec<_> = client
        .read_relationships(
            read.clone().with_options(
                spicedb::ReadRelationshipsOptions::new()
                    .with_page_size(page)
                    .with_limit(5),
            ),
        )
        .map(|r| r.expect("every page must be accepted by SpiceDB"))
        .collect()
        .await;
    assert_eq!(items.len(), 5, "limit 5 over pages of 2");
    assert!(items.iter().all(|i| !i.read_at.as_ref().is_empty()));
    let resumed: Vec<_> = client
        .read_relationships(
            read.with_options(
                spicedb::ReadRelationshipsOptions::new()
                    .with_page_size(page)
                    .with_cursor(items[4].after_result_cursor.clone().expect("cursor")),
            ),
        )
        .map(|r| r.expect("resume must be accepted"))
        .collect()
        .await;
    assert_eq!(resumed.len(), 2, "the two relationships after the fifth");

    let lookup =
        spicedb::LookupResourcesRequest::new("doc", "view", "user:alice".parse().unwrap(), cs);
    let found: Vec<_> = client
        .lookup_resources(
            lookup.with_options(
                spicedb::LookupResourcesOptions::new()
                    .with_page_size(page)
                    .with_limit(5),
            ),
        )
        .map(|r| r.expect("every lookup page must be accepted by SpiceDB"))
        .collect()
        .await;
    assert_eq!(found.len(), 5);
    assert!(found.iter().all(|f| f.after_result_cursor.is_some()));
    cleanup(&client).await;
}

/// Import reports the server's abort status, and the full tuple syntax
/// (caveat and expiration) round-trips through a real server.
#[tokio::test]
#[ignore = "requires a live SpiceDB server (see module docs)"]
async fn import_abort_status_and_full_tuples_round_trip() {
    use futures::StreamExt;
    let _guard = LIVE_LOCK.lock().await;
    let client = adoption_client().await;

    let tuples = [
        "doc:t1#viewer@user:alice",
        r#"doc:t2#viewer@user:bob[active:{"now":5}]"#,
        "doc:t3#viewer@user:carol[expiration:2300-01-02T03:04:05Z]",
    ];
    let rels: Vec<spicedb::Relationship> = tuples.iter().map(|t| t.parse().unwrap()).collect();
    let import = |rels: Vec<spicedb::Relationship>| {
        spicedb::ImportRelationshipsRequest::new(futures::stream::iter(rels.into_iter().map(Ok)))
            .with_options(
                spicedb::ImportRelationshipsOptions::new()
                    .with_batch_size(std::num::NonZeroU32::new(1).unwrap()),
            )
    };
    let imported = client
        .import_relationships(import(rels.clone()))
        .await
        .expect("import");
    assert_eq!(imported.imported_count, 3);

    let err = client
        .import_relationships(import(vec![rels[0].clone()]))
        .await
        .expect_err("duplicate import must fail");
    assert!(
        matches!(err, spicedb::SpiceDBError::AlreadyExists(_)),
        "expected the server's ALREADY_EXISTS, got {err:?}"
    );
    assert_eq!(err.code(), tonic::Code::AlreadyExists);

    let mut read: Vec<String> = client
        .read_relationships(spicedb::ReadRelationshipsRequest::new(
            spicedb::Filter::new("doc"),
            consistency::full(),
        ))
        .map(|r| r.unwrap().relationship.to_string())
        .collect()
        .await;
    read.sort();
    assert_eq!(read, tuples.map(String::from).to_vec());
    cleanup(&client).await;
}

/// All-or-nothing deletion is rejected by the server instead of deleting
/// part of the match, and per-call response metadata is captured.
#[tokio::test]
#[ignore = "requires a live SpiceDB server (see module docs)"]
async fn all_or_nothing_delete_and_response_metadata() {
    let _guard = LIVE_LOCK.lock().await;
    let client = adoption_client().await;
    let mut txn = Transaction::new();
    for i in 0..3 {
        txn.touch(&format!("doc:del{i}#viewer@user:alice").parse().unwrap());
    }
    client.write(spicedb::WriteRequest::new(txn)).await.unwrap();

    let err = client
        .delete_relationships(
            spicedb::DeleteRequest::new(
                spicedb::Filter::new("doc"),
                std::num::NonZeroU32::new(2).unwrap(),
            )
            .with_options(
                spicedb::DeleteOptions::new().with_partial(spicedb::DeletePartial::Disallowed),
            ),
        )
        .await
        .expect_err("more matches than the limit must be rejected");
    assert!(
        matches!(err, spicedb::SpiceDBError::InvalidArgument(_)),
        "{err:?}"
    );
    assert_eq!(
        err.reason(),
        Some("ERROR_REASON_TOO_MANY_RELATIONSHIPS_FOR_TRANSACTIONAL_DELETE")
    );

    let captured = spicedb::ResponseMetadata::new();
    let deleted = client
        .delete_relationships(
            spicedb::DeleteRequest::server_default_limit(spicedb::Filter::new("doc")).with_options(
                spicedb::DeleteOptions::new()
                    .with_partial(spicedb::DeletePartial::Disallowed)
                    .with_call_options(
                        spicedb::CallOptions::new().with_response_metadata(captured.clone()),
                    ),
            ),
        )
        .await
        .expect("delete within the server default limit");
    assert_eq!(deleted.deleted_count, 3);
    assert!(deleted.complete);
    assert!(
        captured
            .headers()
            .get("io.spicedb.respmeta.dispatchedoperationscount")
            .is_some(),
        "SpiceDB's response metadata must be captured: {:?}",
        captured.headers()
    );
}

/// The URL form deployments configure (`http://host:port`) reaches a real
/// SpiceDB through the plain builder with an eager connect, and checks see a
/// direct subject built with `into()`; the same eager connect to a closed port
/// fails at startup.
#[tokio::test]
#[ignore = "requires a live SpiceDB server (see module docs)"]
async fn http_url_endpoint_with_eager_connect_reaches_spicedb() {
    let _guard = LIVE_LOCK.lock().await;
    adoption_client().await;
    let url = format!("http://{}", live_endpoint());
    let client = SpiceDBClient::builder(&url, live_token())
        .connect_mode(spicedb::ConnectMode::Eager)
        .build()
        .await
        .unwrap_or_else(|e| panic!("{url} must connect: {e:?}"));
    let viewer: spicedb::Relationship = "doc:url#viewer@user:alice".parse().unwrap();
    let mut txn = Transaction::new();
    txn.touch(&viewer);
    let written = client.write(spicedb::WriteRequest::new(txn)).await.unwrap();
    let check = spicedb::PermissionCheck::new(
        viewer.resource().clone(),
        "view",
        spicedb::ObjectRef::new("user", "alice").unwrap().into(),
        consistency::at_least(written.revision),
    );
    assert!(client.check(check).await.unwrap().has_permission());
    cleanup(&client).await;

    let down = SpiceDBClient::builder("http://127.0.0.1:1", live_token())
        .connect_mode(spicedb::ConnectMode::Eager)
        .build()
        .await;
    assert!(matches!(down, Err(spicedb::SpiceDBError::Transport(_))));
}
