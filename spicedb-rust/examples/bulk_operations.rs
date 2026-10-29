//! Demonstrates bulk checks, batch writes, and import/export operations.
//!
//! Run with: `cargo run --example bulk_operations`

use futures::StreamExt;
use spicedb::client::SpiceDBClient;
use spicedb::consistency;
use spicedb::error::SpiceDBError;
use spicedb::types::{DeleteAllOptions, Filter, Relationship, Transaction};

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

    // Bulk write relationships
    let users = ["alice", "bob", "charlie"];
    let mut txn = Transaction::new();
    let rels: Vec<Relationship> = users
        .iter()
        .map(|user| {
            (|| -> Result<spicedb::Relationship, spicedb::SpiceDBError> {
                spicedb::Relationship::new(
                    spicedb::ObjectRef::new("document", "report")?,
                    "viewer",
                    spicedb::SubjectRef::new(spicedb::ObjectRef::new("user", *user)?, None)?,
                )
            })()
            .expect("invalid relationship")
        })
        .collect();

    for rel in &rels {
        txn.touch(rel);
    }
    let revision = client
        .write(spicedb::WriteRequest::new((txn).clone()))
        .await
        .expect("bulk write failed");

    println!(
        "wrote {} relationships at revision: {revision:?}",
        users.len()
    );
    assert!(
        !revision.revision.as_ref().is_empty(),
        "expected non-empty revision"
    );

    // Bulk check permissions
    let check_rels: Vec<Relationship> = users
        .iter()
        .map(|user| {
            (|| -> Result<spicedb::Relationship, spicedb::SpiceDBError> {
                spicedb::Relationship::new(
                    spicedb::ObjectRef::new("document", "report")?,
                    "view",
                    spicedb::SubjectRef::new(spicedb::ObjectRef::new("user", *user)?, None)?,
                )
            })()
            .expect("invalid relationship")
        })
        .collect();

    let results = client
        .check_bulk(spicedb::BulkCheckRequest::new(
            check_rels
                .iter()
                .map(|r| {
                    spicedb::PermissionCheck::new(
                        (r).resource().clone(),
                        "view",
                        (r).subject().clone(),
                        (consistency::at_least((&revision.revision).into())).clone(),
                    )
                })
                .collect(),
        ))
        .await
        .expect("bulk check failed");

    for (i, user) in users.iter().enumerate() {
        println!(
            "user:{user} can view document:report: {} (permissionship: {:?})",
            results[i].has_permission(),
            results[i].permissionship
        );
        assert!(
            results[i].has_permission(),
            "expected user:{user} to have view permission"
        );
    }

    // Check all
    let all_allowed = client
        .check_bulk(spicedb::BulkCheckRequest::new(
            check_rels
                .iter()
                .map(|r| {
                    spicedb::PermissionCheck::new(
                        (r).resource().clone(),
                        "view",
                        (r).subject().clone(),
                        (consistency::at_least((&revision.revision).into())).clone(),
                    )
                })
                .collect(),
        ))
        .await
        .map(|results| {
            !results.is_empty() && results.iter().all(spicedb::CheckResult::has_permission)
        })
        .expect("check all failed");

    println!("all users can view: {all_allowed}");
    assert!(all_allowed, "expected all users to have view permission");

    // Check any
    let any_allowed = client
        .check_bulk(spicedb::BulkCheckRequest::new(
            check_rels
                .iter()
                .map(|r| {
                    spicedb::PermissionCheck::new(
                        (r).resource().clone(),
                        "view",
                        (r).subject().clone(),
                        (consistency::at_least((&revision.revision).into())).clone(),
                    )
                })
                .collect(),
        ))
        .await
        .map(|results| {
            !results.is_empty() && results.iter().any(spicedb::CheckResult::has_permission)
        })
        .expect("check any failed");

    println!("any user can view: {any_allowed}");
    assert!(
        any_allowed,
        "expected at least one user to have view permission"
    );

    // Bulk import relationships. import_relationships accepts anything
    // IntoIterator<Item = Relationship> -- a Vec, or (as here) a plain
    // iterator/generator passed directly, with no .collect() needed. The
    // source is consumed lazily, one batch at a time, so a caller streaming
    // in millions of relationships from a generator or a DB cursor is never
    // forced to materialize the whole thing into a Vec first.
    let import_rels = (0..100).map(|i| {
        (|| -> Result<spicedb::Relationship, spicedb::SpiceDBError> {
            spicedb::Relationship::new(
                spicedb::ObjectRef::new("document", format!("bulk-doc-{i}"))?,
                "viewer",
                spicedb::SubjectRef::new(spicedb::ObjectRef::new("user", "alice")?, None)?,
            )
        })()
        .expect("invalid relationship")
    });

    let count = client
        .import_relationships(spicedb::ImportRelationshipsRequest::new(
            futures::stream::iter((import_rels).into_iter().map(Ok)),
        ))
        .await
        .expect("import failed");

    println!("imported {count:?} relationships");
    assert_eq!(
        count.imported_count, 100,
        "expected all 100 relationships to be imported"
    );

    // Bulk export relationships. This example cleared `document` before
    // writing, so the whole store is exactly what it wrote: three viewers of
    // document:report plus the hundred imported above. Asserting the number
    // means an export that stopped after the first page, or dropped a page's
    // results, fails here -- the count used to be printed and nothing more.
    let stream = client.export_relationships(spicedb::ExportRelationshipsRequest::new(
        (consistency::full()).clone(),
    ));
    tokio::pin!(stream);

    let mut exported = Vec::new();
    while let Some(rel) = stream.next().await {
        exported.push(rel.expect("export failed"));
    }

    println!("exported {} relationships", exported.len());
    assert_eq!(
        exported.len(),
        users.len() + 100,
        "expected the export to return every relationship this example wrote"
    );
    // And they are the relationships that were written, not placeholders: a
    // conversion that lost the subject would still produce the right count.
    assert!(
        exported
            .iter()
            .any(|r| r.relationship.resource_id() == "report"
                && r.relationship.subject_id() == "alice"),
        "expected the export to include document:report#viewer@user:alice"
    );
    assert!(
        exported
            .iter()
            .any(|r| r.relationship.resource_id() == "bulk-doc-99"
                && r.relationship.subject_id() == "alice"),
        "expected the export to include the last imported relationship"
    );

    // Clean up so later examples that write a narrower schema aren't blocked by
    // leftover relationships. This store has 103 matching relationships
    // (three viewers of document:report plus the hundred imported above), so
    // a page size of 20 forces `delete_relationships_with`'s auto-paging loop
    // to repeat several times rather than complete in one call -- exercising
    // the same loop that now threads each response's `after_result_cursor`
    // into the next page's `optional_cursor` (mirroring
    // `read_relationships`'s transparent-continuation pattern, see
    // `DeleteAllOptions` docs). Asserting nothing is left afterward means a
    // loop that stopped early -- because paging broke, or because it choked
    // on the cursor field this datastore never actually populates -- fails
    // here instead of silently leaving relationships behind.
    client
        .delete_all_relationships_non_atomic(
            spicedb::DeleteAllRequest::new((Filter::new("document")).clone()).with_options(
                (DeleteAllOptions::new().with_page_size(std::num::NonZeroU32::new(20).unwrap()))
                    .clone(),
            ),
        )
        .await
        .expect("paged cleanup failed");

    let stream = client.export_relationships(spicedb::ExportRelationshipsRequest::new(
        (consistency::full()).clone(),
    ));
    tokio::pin!(stream);
    let mut remaining = Vec::new();
    while let Some(rel) = stream.next().await {
        remaining.push(rel.expect("export after cleanup failed"));
    }
    assert!(
        remaining.is_empty(),
        "expected no document relationships to remain after paged cleanup, found {}",
        remaining.len()
    );
}
