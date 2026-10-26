//! Demonstrates checking a permission using `check_permission`.
//!
//! Run with: `cargo run --example check_permission`

use spicedb::client::SpiceDBClient;
use spicedb::consistency;
use spicedb::error::SpiceDBError;
use spicedb::types::CheckOptions;
use spicedb::types::{Filter, Transaction};

const SCHEMA: &str = r#"definition user {}

caveat active(now int) {
    now < 100
}

definition document {
    relation viewer: user
    relation editor: user
    relation owner: user
    relation conditional_viewer: user with active
    permission view = viewer + editor + owner
    permission edit = editor + owner
    permission delete = owner
    permission conditional_view = conditional_viewer
}"#;

#[tokio::main]
async fn main() {
    // Create a plaintext client (testing only — no TLS)
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

    // Write test data: alice is a viewer of firstdoc
    let rel = spicedb::Relationship::from_tuple("document:firstdoc#viewer@user:alice")
        .expect("invalid relationship");
    let mut txn = Transaction::new();
    txn.touch(&rel);
    let revision = client
        .write(spicedb::WriteRequest::new((txn).clone()))
        .await
        .expect("write relationships failed");

    // Check permission: alice should be able to view firstdoc
    let check_rel = spicedb::Relationship::from_tuple("document:firstdoc#view@user:alice")
        .expect("invalid relationship");
    let result = client
        .check(
            spicedb::PermissionCheck::new(
                check_rel.resource().clone(),
                "view",
                check_rel.subject().clone(),
                (consistency::at_least((&revision.revision).into())).clone(),
            )
            .with_options(spicedb::CheckOptions::new().with_tracing(spicedb::TracingMode::Enabled)),
        )
        .await
        .expect("check failed");

    println!(
        "alice can view document:firstdoc: {} (permissionship: {:?})",
        result.has_permission(),
        result.permissionship
    );
    assert!(
        result.has_permission(),
        "expected alice to have view permission"
    );
    assert!(
        !result.checked_at.as_ref().is_empty(),
        "checked_at should be populated from the response"
    );

    let trace = result
        .debug_trace
        .as_ref()
        .expect("tracing must return native debug information");
    assert!(!trace.schema_used.is_empty());
    assert!(trace.check.is_some());

    // Conditional check: alice is a conditional_viewer of conditionaldoc via
    // the `active` caveat, but no caveat context is supplied at check time —
    // the server cannot evaluate `now < 100`, so it returns a Conditional
    // result rather than a grant.
    let conditional_rel =
        spicedb::Relationship::from_tuple("document:conditionaldoc#conditional_viewer@user:alice")
            .expect("invalid relationship")
            .with_caveat("active", None);
    let mut conditional_txn = Transaction::new();
    conditional_txn.touch(&conditional_rel);
    let conditional_revision = client
        .write(spicedb::WriteRequest::new((conditional_txn).clone()))
        .await
        .expect("write conditional relationship failed");

    let conditional_check_rel =
        spicedb::Relationship::from_tuple("document:conditionaldoc#conditional_view@user:alice")
            .expect("invalid relationship");
    let conditional_result = client
        .check(spicedb::PermissionCheck::new(
            conditional_check_rel.resource().clone(),
            "conditional_view",
            conditional_check_rel.subject().clone(),
            (consistency::at_least((&conditional_revision.revision).into())).clone(),
        ))
        .await
        .expect("conditional check failed");

    println!(
        "alice can conditionally view document:conditionaldoc: {} (permissionship: {:?}, missing context: {:?})",
        conditional_result.has_permission(),
        conditional_result.permissionship,
        conditional_result.missing_context
    );
    assert!(
        !conditional_result.has_permission(),
        "a Conditional result must not report has_permission() == true"
    );
    assert_eq!(
        conditional_result.missing_context,
        vec!["now".to_string()],
        "expected the server to report `now` as the missing caveat parameter"
    );

    // Resolve the conditional: supply the `now` context the server reported
    // as missing via `check_permission_with_options`. `now < 100` evaluates
    // true, so this time the check resolves to an outright grant.
    let mut context = std::collections::HashMap::new();
    context.insert("now".to_string(), serde_json::json!(42));
    let resolved_result = client
        .check(
            spicedb::PermissionCheck::new(
                conditional_check_rel.resource().clone(),
                "conditional_view",
                conditional_check_rel.subject().clone(),
                (consistency::at_least((&conditional_revision.revision).into())).clone(),
            )
            .with_options((CheckOptions::new().with_context(context.clone())).clone()),
        )
        .await
        .expect("check with context failed");

    println!(
        "alice can conditionally view document:conditionaldoc with context {{now: 42}}: {} (permissionship: {:?})",
        resolved_result.has_permission(),
        resolved_result.permissionship
    );
    assert!(
        resolved_result.has_permission(),
        "expected supplying the missing `now` context to resolve the caveat to a grant"
    );
}
